// Package anthropic implements vision.Provider against Anthropic's Messages
// API. Reads ANTHROPIC_API_KEY from env at construction; the key is never
// logged or returned in error strings.
package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qiangli/omniscore/internal/importer/vision"
)

const (
	defaultEndpoint = "https://api.anthropic.com/v1/messages"
	defaultVersion  = "2023-06-01"
	defaultMaxToks  = 4096
)

// Provider talks to Anthropic's Messages API for one specific model.
type Provider struct {
	Model     string // e.g. "claude-sonnet-4-6", "claude-opus-4-7"
	APIKey    string // not logged
	Endpoint  string // override only for testing
	Version   string // anthropic-version header
	MaxTokens int    // upper bound on response length
	hc        *http.Client
}

// New constructs a Provider. Empty apiKey is permitted (Generate will return
// a clear error) so the factory can construct without crashing on missing env.
func New(model, apiKey string) *Provider {
	return &Provider{
		Model:     model,
		APIKey:    apiKey,
		Endpoint:  defaultEndpoint,
		Version:   defaultVersion,
		MaxTokens: defaultMaxToks,
		hc:        &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *Provider) Name() string { return "anthropic:" + p.Model }

type messagesReq struct {
	Model       string         `json:"model"`
	MaxTokens   int            `json:"max_tokens"`
	Temperature float64        `json:"temperature"`
	Messages    []messageEntry `json:"messages"`
}

type messageEntry struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type   string         `json:"type"`
	Text   string         `json:"text,omitempty"`
	Source *imageSource   `json:"source,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type messagesResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *Provider) Generate(ctx context.Context, req vision.Request) (string, error) {
	if p.APIKey == "" {
		return "", fmt.Errorf("anthropic: ANTHROPIC_API_KEY not set")
	}
	body := messagesReq{
		Model:       p.Model,
		MaxTokens:   p.MaxTokens,
		Temperature: req.Temperature,
		Messages: []messageEntry{{
			Role: "user",
			Content: []contentBlock{
				{
					Type: "image",
					Source: &imageSource{
						Type: "base64", MediaType: "image/png",
						Data: base64.StdEncoding.EncodeToString(req.Image),
					},
				},
				{Type: "text", Text: req.Prompt},
			},
		}},
	}
	bb, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("anthropic: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.Endpoint, bytes.NewReader(bb))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.APIKey)
	httpReq.Header.Set("anthropic-version", p.Version)

	resp, err := p.hc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var out messagesResp
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return "", fmt.Errorf("anthropic: parse response: %w (body=%q)", err, snip(respBytes))
	}
	if out.Error != nil {
		return "", fmt.Errorf("anthropic: %s: %s", out.Error.Type, out.Error.Message)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, snip(respBytes))
	}
	var sb strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

func snip(b []byte) string {
	if len(b) > 256 {
		return string(b[:256]) + "...(truncated)"
	}
	return string(b)
}
