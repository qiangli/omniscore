// Package ollama implements vision.Provider against a local Ollama server's
// /api/generate endpoint. Tested against llama3.2-vision and qwen2-vl. Image
// is sent as base64 in the `images` array; response is read non-streaming
// (stream=false).
package ollama

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

// Provider talks to one Ollama server.
type Provider struct {
	Host    string        // e.g. "http://localhost:11434"
	Model   string        // e.g. "llama3.2-vision:90b"
	Timeout time.Duration // per-call HTTP timeout (0 = use 90s default)
	hc      *http.Client
}

// New constructs a Provider with sensible defaults.
func New(host, model string) *Provider {
	if host == "" {
		host = "http://localhost:11434"
	}
	host = strings.TrimRight(host, "/")
	return &Provider{
		Host:    host,
		Model:   model,
		Timeout: 90 * time.Second,
		hc:      &http.Client{Timeout: 90 * time.Second},
	}
}

func (p *Provider) Name() string { return "ollama:" + p.Model }

// generateReq mirrors Ollama's /api/generate body for image inputs. Stream is
// disabled so we can read one JSON response.
type generateReq struct {
	Model   string   `json:"model"`
	Prompt  string   `json:"prompt"`
	Images  []string `json:"images,omitempty"` // base64-encoded image bytes
	Stream  bool     `json:"stream"`
	Options struct {
		Temperature float64 `json:"temperature"`
	} `json:"options"`
}

type generateResp struct {
	Response string `json:"response"`
	Error    string `json:"error,omitempty"`
}

func (p *Provider) Generate(ctx context.Context, req vision.Request) (string, error) {
	body := generateReq{
		Model:  p.Model,
		Prompt: req.Prompt,
		Stream: false,
	}
	if len(req.Image) > 0 {
		body.Images = []string{base64.StdEncoding.EncodeToString(req.Image)}
	}
	body.Options.Temperature = req.Temperature

	bb, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("ollama: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.Host+"/api/generate", bytes.NewReader(bb))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.hc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama: %s: %w", p.Host, err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("ollama: %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}
	var out generateResp
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return "", fmt.Errorf("ollama: parse response: %w (body=%q)", err, respBytes)
	}
	if out.Error != "" {
		return "", fmt.Errorf("ollama: %s", out.Error)
	}
	return out.Response, nil
}
