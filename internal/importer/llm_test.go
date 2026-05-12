package importer_test

import (
	"strings"
	"testing"

	"github.com/qiangli/omniscore/internal/importer"
)

func TestParseSpec(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want importer.Spec
	}{
		{"ollama/llama3.2-vision:90b", true, importer.Spec{Vendor: "ollama", Model: "llama3.2-vision:90b"}},
		{"anthropic/claude-sonnet-4-6", true, importer.Spec{Vendor: "anthropic", Model: "claude-sonnet-4-6"}},
		{"openai/gpt-4o", true, importer.Spec{Vendor: "openai", Model: "gpt-4o"}},
		{"gemini/gemini-2.5-pro", true, importer.Spec{Vendor: "gemini", Model: "gemini-2.5-pro"}},
		{"  ollama/llama3.2  ", true, importer.Spec{Vendor: "ollama", Model: "llama3.2"}},
		{"ANTHROPIC/claude-opus-4-7", true, importer.Spec{Vendor: "anthropic", Model: "claude-opus-4-7"}},
		// Models with multiple slashes / colons preserved verbatim after first slash.
		{"ollama/registry/some-model:tag", true, importer.Spec{Vendor: "ollama", Model: "registry/some-model:tag"}},

		{"", false, importer.Spec{}},
		{"ollama", false, importer.Spec{}},
		{"ollama/", false, importer.Spec{}},
		{"/llama3.2", false, importer.Spec{}},
		{"unknown/foo", false, importer.Spec{}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := importer.ParseSpec(tc.in)
			if (err == nil) != tc.ok {
				t.Fatalf("ok=%v err=%v", tc.ok, err)
			}
			if !tc.ok {
				return
			}
			if got != tc.want {
				t.Fatalf("want %+v, got %+v", tc.want, got)
			}
		})
	}
}

func TestFromSpec_OllamaNoKeyNeeded(t *testing.T) {
	p, err := importer.FromSpec(importer.Spec{Vendor: "ollama", Model: "llama3.2"}, "http://localhost:11434")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Name(), "ollama:") {
		t.Errorf("name: %s", p.Name())
	}
}

func TestFromSpec_CloudMissingKeyError(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	for _, vendor := range []string{"anthropic", "openai", "gemini"} {
		t.Run(vendor, func(t *testing.T) {
			_, err := importer.FromSpec(importer.Spec{Vendor: vendor, Model: "x"}, "")
			if err == nil {
				t.Fatalf("expected error when %s API key is missing", vendor)
			}
			if !strings.Contains(err.Error(), "API_KEY") {
				t.Errorf("error message should mention API_KEY: %v", err)
			}
		})
	}
}

func TestFromSpec_CloudUsesKeyFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-fake")
	p, err := importer.FromSpec(importer.Spec{Vendor: "anthropic", Model: "claude-sonnet-4-6"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "anthropic:claude-sonnet-4-6" {
		t.Errorf("name: %s", p.Name())
	}
}

func TestFromSpec_GeminiAcceptsGoogleAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "AIza-fake")
	p, err := importer.FromSpec(importer.Spec{Vendor: "gemini", Model: "gemini-2.5-pro"}, "")
	if err != nil {
		t.Fatalf("expected GOOGLE_API_KEY fallback to work: %v", err)
	}
	if !strings.HasPrefix(p.Name(), "gemini:") {
		t.Errorf("name: %s", p.Name())
	}
}
