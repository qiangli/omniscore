// Package vision abstracts a vision LLM that, given a rasterized PDF page and
// a task-specific prompt, returns text (typically JSON the caller will parse).
// The interface is the seam that lets the AP importer swap between Ollama
// (local, default for v1) and a cloud provider (Anthropic/OpenAI/Gemini)
// without touching pipeline code.
package vision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Request is one vision-LLM call against one page image.
type Request struct {
	Image       []byte  // raw PNG bytes
	Prompt      string  // task instructions; provider does not transform
	Temperature float64 // 0..1
}

// Provider is the contract pipeline stages depend on. A concrete impl is
// constructed once at startup and reused for every page.
type Provider interface {
	Name() string
	Generate(ctx context.Context, req Request) (string, error)
}

// Cached wraps a Provider with a content-addressed on-disk cache so reruns of
// the importer cost zero LLM calls for unchanged inputs. Cache key is
// sha256(provider.Name | image | prompt | temperature). Cache directory must
// exist; entries are filename-safe hex.
func Cached(inner Provider, dir string) (Provider, error) {
	if dir == "" {
		return nil, errors.New("vision.Cached: empty cache dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("vision.Cached: mkdir %s: %w", dir, err)
	}
	return &cachedProvider{inner: inner, dir: dir}, nil
}

type cachedProvider struct {
	inner Provider
	dir   string
}

func (c *cachedProvider) Name() string { return c.inner.Name() + "+cache" }

func (c *cachedProvider) Generate(ctx context.Context, req Request) (string, error) {
	key := keyFor(c.inner.Name(), req)
	path := filepath.Join(c.dir, key)
	if b, err := os.ReadFile(path); err == nil {
		return string(b), nil
	}
	out, err := c.inner.Generate(ctx, req)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		// Don't fail the call on a cache-write error; the LLM result is real.
		return out, nil
	}
	return out, nil
}

func keyFor(name string, r Request) string {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write(r.Image)
	h.Write([]byte{0})
	h.Write([]byte(r.Prompt))
	h.Write([]byte{0})
	h.Write(fmt.Appendf(nil, "%.4f", r.Temperature))
	return hex.EncodeToString(h.Sum(nil)) + ".json"
}
