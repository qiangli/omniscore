package importer

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/qiangli/omniscore/internal/importer/vision"
	"github.com/qiangli/omniscore/internal/importer/vision/anthropic"
	"github.com/qiangli/omniscore/internal/importer/vision/gemini"
	"github.com/qiangli/omniscore/internal/importer/vision/ollama"
	"github.com/qiangli/omniscore/internal/importer/vision/openai"
)

// Spec captures the parsed result of a "vendor/model" string.
type Spec struct {
	Vendor string // "ollama" | "anthropic" | "openai" | "gemini"
	Model  string // vendor-specific model id, e.g. "claude-sonnet-4-6"
}

// ParseSpec splits "vendor/model" into a Spec. The vendor must be one of the
// supported set; any string after the first slash (including further slashes
// and colons, as Ollama tags use them) is the model id.
//
// Examples:
//
//	ollama/llama3.2-vision:90b
//	anthropic/claude-sonnet-4-6
//	openai/gpt-4o
//	gemini/gemini-2.5-pro
func ParseSpec(s string) (Spec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Spec{}, errors.New("vision: empty model spec")
	}
	i := strings.IndexByte(s, '/')
	if i <= 0 || i == len(s)-1 {
		return Spec{}, fmt.Errorf("vision: spec %q must be vendor/model", s)
	}
	vendor := strings.ToLower(s[:i])
	model := s[i+1:]
	switch vendor {
	case "ollama", "anthropic", "openai", "gemini":
		// ok
	default:
		return Spec{}, fmt.Errorf("vision: unknown vendor %q (use ollama|anthropic|openai|gemini)", vendor)
	}
	return Spec{Vendor: vendor, Model: model}, nil
}

// FromSpec constructs a Provider from a parsed Spec, reading API keys from
// the conventional env vars and falling back to the provided ollamaHost when
// vendor is ollama. Returns an error if the spec asks for a vendor whose
// API key is missing.
func FromSpec(s Spec, ollamaHost string) (vision.Provider, error) {
	switch s.Vendor {
	case "ollama":
		return ollama.New(ollamaHost, s.Model), nil
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, errors.New("vision: ANTHROPIC_API_KEY not set")
		}
		return anthropic.New(s.Model, key), nil
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, errors.New("vision: OPENAI_API_KEY not set")
		}
		return openai.New(s.Model, key), nil
	case "gemini":
		key := os.Getenv("GEMINI_API_KEY")
		if key == "" {
			key = os.Getenv("GOOGLE_API_KEY")
		}
		if key == "" {
			return nil, errors.New("vision: GEMINI_API_KEY (or GOOGLE_API_KEY) not set")
		}
		return gemini.New(s.Model, key), nil
	default:
		return nil, fmt.Errorf("vision: unknown vendor %q", s.Vendor)
	}
}

// FromString is the one-shot convenience wrapper used by callers that have
// the raw spec string and don't need to inspect the parsed Spec.
func FromString(spec, ollamaHost string) (vision.Provider, error) {
	s, err := ParseSpec(spec)
	if err != nil {
		return nil, err
	}
	return FromSpec(s, ollamaHost)
}
