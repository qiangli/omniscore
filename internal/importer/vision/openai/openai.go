// Package openai implements vision.Provider against OpenAI's Chat Completions
// API. Reads OPENAI_API_KEY from env at construction; the key is never logged.
package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/qiangli/omniscore/internal/importer/vision"
)

const (
	defaultEndpoint = "https://api.openai.com/v1/chat/completions"
	defaultMaxToks  = 4096
)

// Provider talks to OpenAI Chat Completions for one specific model.
type Provider struct {
	Model     string // e.g. "gpt-4o", "gpt-4.1", "gpt-4o-mini"
	APIKey    string // not logged
	Endpoint  string // override only for testing
	MaxTokens int
	hc        *http.Client
}

func New(model, apiKey string) *Provider {
	return &Provider{
		Model:     model,
		APIKey:    apiKey,
		Endpoint:  defaultEndpoint,
		MaxTokens: defaultMaxToks,
		hc:        &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *Provider) Name() string { return "openai:" + p.Model }

type chatReq struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	Messages    []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *imageURLPart `json:"image_url,omitempty"`
}

type imageURLPart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "low" | "high" | "auto"
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *Provider) Generate(ctx context.Context, req vision.Request) (string, error) {
	if p.APIKey == "" {
		return "", fmt.Errorf("openai: OPENAI_API_KEY not set")
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(req.Image)
	body := chatReq{
		Model:       p.Model,
		MaxTokens:   p.MaxTokens,
		Temperature: req.Temperature,
		Messages: []chatMessage{{
			Role: "user",
			Content: []contentPart{
				{Type: "text", Text: req.Prompt},
				{Type: "image_url", ImageURL: &imageURLPart{URL: dataURL, Detail: "high"}},
			},
		}},
	}
	bb, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("openai: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.Endpoint, bytes.NewReader(bb))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)

	resp, err := p.hc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var out chatResp
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return "", fmt.Errorf("openai: parse response: %w (body=%q)", err, snip(respBytes))
	}
	if out.Error != nil {
		return "", fmt.Errorf("openai: %s: %s", out.Error.Type, out.Error.Message)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, snip(respBytes))
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices in response")
	}
	return out.Choices[0].Message.Content, nil
}

func snip(b []byte) string {
	if len(b) > 256 {
		return string(b[:256]) + "...(truncated)"
	}
	return string(b)
}
