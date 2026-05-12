// Package gemini implements vision.Provider against Google's Gemini API
// (generativelanguage.googleapis.com). Reads GEMINI_API_KEY from env at
// construction (also accepts GOOGLE_API_KEY as an alias). The key is sent
// as a query parameter, never logged.
package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/qiangli/omniscore/internal/importer/vision"
)

const (
	defaultBase   = "https://generativelanguage.googleapis.com/v1beta"
	defaultMaxOut = 4096
)

// Provider talks to Google's Gemini for one specific model.
type Provider struct {
	Model         string // e.g. "gemini-2.5-pro", "gemini-2.5-flash"
	APIKey        string // not logged
	Base          string // override only for testing
	MaxOutputToks int
	hc            *http.Client
}

func New(model, apiKey string) *Provider {
	return &Provider{
		Model:         model,
		APIKey:        apiKey,
		Base:          defaultBase,
		MaxOutputToks: defaultMaxOut,
		hc:            &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *Provider) Name() string { return "gemini:" + p.Model }

type generateReq struct {
	Contents         []geminiContent  `json:"contents"`
	GenerationConfig generationConfig `json:"generationConfig"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string         `json:"text,omitempty"`
	InlineData *inlineDataObj `json:"inline_data,omitempty"`
}

type inlineDataObj struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type generationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type generateResp struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *Provider) Generate(ctx context.Context, req vision.Request) (string, error) {
	if p.APIKey == "" {
		return "", fmt.Errorf("gemini: GEMINI_API_KEY (or GOOGLE_API_KEY) not set")
	}
	body := generateReq{
		Contents: []geminiContent{{
			Parts: []geminiPart{
				{InlineData: &inlineDataObj{
					MimeType: "image/png",
					Data:     base64.StdEncoding.EncodeToString(req.Image),
				}},
				{Text: req.Prompt},
			},
		}},
		GenerationConfig: generationConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: p.MaxOutputToks,
		},
	}
	bb, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("gemini: marshal: %w", err)
	}
	endpoint := fmt.Sprintf("%s/models/%s:generateContent?key=%s",
		p.Base, p.Model, url.QueryEscape(p.APIKey))
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bb))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.hc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("gemini: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var out generateResp
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return "", fmt.Errorf("gemini: parse response: %w (body=%q)", err, snip(respBytes))
	}
	if out.Error != nil {
		return "", fmt.Errorf("gemini: %s (%d): %s", out.Error.Status, out.Error.Code, out.Error.Message)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("gemini: HTTP %d: %s", resp.StatusCode, snip(respBytes))
	}
	if len(out.Candidates) == 0 {
		return "", fmt.Errorf("gemini: no candidates in response")
	}
	var sb bytes.Buffer
	for _, part := range out.Candidates[0].Content.Parts {
		sb.WriteString(part.Text)
	}
	return sb.String(), nil
}

func snip(b []byte) string {
	if len(b) > 256 {
		return string(b[:256]) + "...(truncated)"
	}
	return string(b)
}
