package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	importer "github.com/qiangli/omniscore/internal/importer"
	"github.com/qiangli/omniscore/internal/importer/profile"
	"github.com/qiangli/omniscore/internal/importer/render"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

// FallbackConfig wires up the LLM-fallback path. Two modes:
//   - Mode="text"   : feed the parser's per-page text to a text-only LLM
//                     (Ollama) and ask it to structure questions as JSON.
//                     Fastest, no rasterization, works with the largest
//                     models that fit (e.g. qwen3.5:35b).
//   - Mode="vision" : rasterize the page and call a vision LLM via the
//                     existing internal/importer/vision provider stack.
type FallbackConfig struct {
	Mode     string // "text" (default) | "vision"
	Model    string // "vendor/model" — vision: "ollama/qwen2.5vl:32b"; text: "ollama/qwen3.5:35b"
	Host     string // ollama base URL; ignored for cloud vision vendors
	Workdir  string // scratch for rasterized PNGs + LLM cache
	DPI      int    // rasterization DPI for vision mode; default 300
	MaxPages int    // cap on total LLM page-queries (0 = unlimited)
}

// runLLMFallback fills gaps in a parsed Test by identifying pages with
// missing question numbers, then asking the LLM to extract those pages.
// Updates `t` in place. Returns counts {pages_queried, questions_filled}.
func runLLMFallback(ctx context.Context, pdfPath string, parsedPages []Page, t *Test, cfg FallbackConfig) (queried, filled int, err error) {
	if cfg.Mode == "" {
		cfg.Mode = "text"
	}
	if cfg.DPI == 0 {
		cfg.DPI = 300
	}
	if cfg.Workdir == "" {
		cfg.Workdir = filepath.Join(os.TempDir(), "sat-pdf-eval-fallback")
	}

	var (
		visionProv vision.Provider
		pngs       []string
		textCli    *ollamaTextClient
	)
	switch cfg.Mode {
	case "vision":
		visionProv, err = buildVisionProvider(cfg)
		if err != nil {
			return 0, 0, err
		}
		pageDir := filepath.Join(cfg.Workdir, "pages", strings.TrimSuffix(filepath.Base(pdfPath), ".pdf"))
		pngs, err = render.Rasterize(ctx, pdfPath, cfg.DPI, pageDir)
		if err != nil {
			return 0, 0, fmt.Errorf("rasterize: %w", err)
		}
	case "text":
		textCli, err = newOllamaTextClient(cfg.Model, cfg.Host, filepath.Join(cfg.Workdir, "text-cache"))
		if err != nil {
			return 0, 0, err
		}
	case "agent":
		// Agent mode has its own driver; short-circuit out of this function.
		return runLLMAgentFallback(ctx, pdfPath, parsedPages, t, cfg, os.Stderr)
	default:
		return 0, 0, fmt.Errorf("unknown llm-mode %q (want text, vision, or agent)", cfg.Mode)
	}

	sat := profile.SAT()
	moduleByID := map[string]profile.ModuleSpec{}
	for _, m := range sat.Modules {
		moduleByID[m.ID] = m
	}
	caps := map[string]int{"rw-1": 33, "rw-2": 33, "math-1": 27, "math-2": 27}

	for _, modID := range []string{"rw-1", "rw-2", "math-1", "math-2"} {
		modSpec, ok := moduleByID[modID]
		if !ok {
			continue
		}
		got := t.Modules[modID]
		missing := missingNumbers(got, caps[modID])
		if len(missing) == 0 {
			continue
		}
		totalPages := len(pngs)
		if totalPages == 0 {
			totalPages = len(parsedPages)
		}
		targetPages := candidatePages(got, missing, totalPages)
		if len(targetPages) == 0 {
			continue
		}
		prompt := sat.Prompts.Extract(sat, modSpec)

		for _, pi := range targetPages {
			if cfg.MaxPages > 0 && queried >= cfg.MaxPages {
				break
			}
			missSet := map[int]bool{}
			for _, n := range missing {
				missSet[n] = true
			}

			var resp string
			var gerr error
			switch cfg.Mode {
			case "vision":
				pngPath := pageByIndex(pngs, pi)
				if pngPath == "" {
					continue
				}
				img, rerr := os.ReadFile(pngPath)
				if rerr != nil {
					continue
				}
				resp, gerr = visionProv.Generate(ctx, vision.Request{
					Image: img, Prompt: prompt, Temperature: 0,
				})
			case "text":
				pageText := pageTextFor(parsedPages, pi)
				if pageText == "" {
					continue
				}
				resp, gerr = textCli.Generate(ctx, prompt+"\n\nPAGE TEXT (extracted from the PDF; may be imperfect for math equations):\n```\n"+pageText+"\n```")
			}
			queried++
			if gerr != nil {
				fmt.Fprintf(os.Stderr, "[llm-fallback] %s mod=%s page=%d err=%v\n",
					filepath.Base(pdfPath), modID, pi, gerr)
				continue
			}
			for _, lq := range parseExtractResponse(resp) {
				if !missSet[lq.Number] {
					continue
				}
				if hasQuestionNumber(t.Modules[modID], lq.Number) {
					continue
				}
				lq.Module = modID
				lq.PageIndex = pi
				lq.Source = "llm-fallback-" + cfg.Mode
				t.Modules[modID] = append(t.Modules[modID], lq)
				filled++
				delete(missSet, lq.Number)
			}
		}
		qs := t.Modules[modID]
		sort.Slice(qs, func(i, j int) bool { return qs[i].Number < qs[j].Number })
		t.Modules[modID] = qs
	}
	return queried, filled, nil
}

func buildVisionProvider(cfg FallbackConfig) (vision.Provider, error) {
	provider, err := importer.FromString(cfg.Model, cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("build vision provider %q: %w", cfg.Model, err)
	}
	cacheDir := filepath.Join(cfg.Workdir, "llm-cache")
	_ = os.MkdirAll(cacheDir, 0o755)
	return vision.Cached(provider, cacheDir)
}

func pageTextFor(pages []Page, i int) string {
	for _, p := range pages {
		if p.Index == i {
			return strings.Join(p.Cols, "\n--- column break ---\n")
		}
	}
	return ""
}

// --- text-only Ollama client ---------------------------------------------

type ollamaTextClient struct {
	model    string
	baseURL  string
	cacheDir string
	hc       *http.Client
}

func newOllamaTextClient(modelSpec, host, cacheDir string) (*ollamaTextClient, error) {
	parts := strings.SplitN(modelSpec, "/", 2)
	if len(parts) != 2 || parts[0] != "ollama" {
		return nil, fmt.Errorf("text mode requires ollama/<model> spec, got %q", modelSpec)
	}
	_ = os.MkdirAll(cacheDir, 0o755)
	if host == "" {
		host = "http://localhost:11434"
	}
	return &ollamaTextClient{
		model:    parts[1],
		baseURL:  strings.TrimRight(host, "/"),
		cacheDir: cacheDir,
		hc:       &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

func (o *ollamaTextClient) Generate(ctx context.Context, prompt string) (string, error) {
	// Content-addressed cache: sha256(model | prompt).
	h := sha256.New()
	h.Write([]byte(o.model))
	h.Write([]byte{0})
	h.Write([]byte(prompt))
	key := hex.EncodeToString(h.Sum(nil))
	cachePath := filepath.Join(o.cacheDir, key)
	if b, err := os.ReadFile(cachePath); err == nil {
		return string(b), nil
	}

	body, _ := json.Marshal(map[string]any{
		"model":  o.model,
		"prompt": prompt,
		"stream": false,
		"format": "json",
		"options": map[string]any{
			"temperature": 0.0,
			"num_ctx":     8192,
		},
	})
	req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ollama %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var wire struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return "", fmt.Errorf("decode ollama response: %w (raw=%s)", err, string(raw))
	}
	_ = os.WriteFile(cachePath, []byte(wire.Response), 0o644)
	return wire.Response, nil
}

// --- helpers (unchanged from earlier draft) ------------------------------

func missingNumbers(qs []Question, cap int) []int {
	have := map[int]bool{}
	for _, q := range qs {
		have[q.Number] = true
	}
	var out []int
	for n := 1; n <= cap; n++ {
		if !have[n] {
			out = append(out, n)
		}
	}
	return out
}

func candidatePages(have []Question, missing []int, totalPages int) []int {
	numToPage := map[int]int{}
	for _, q := range have {
		if q.PageIndex > 0 {
			numToPage[q.Number] = q.PageIndex
		}
	}
	seen := map[int]bool{}
	var out []int
	for _, n := range missing {
		var pages []int
		if p, ok := numToPage[n-1]; ok {
			pages = append(pages, p, p+1)
		}
		if p, ok := numToPage[n+1]; ok {
			pages = append(pages, p-1, p)
		}
		if len(pages) == 0 {
			closestN, closestP := 0, 0
			for k, p := range numToPage {
				if closestN == 0 || abs(k-n) < abs(closestN-n) {
					closestN, closestP = k, p
				}
			}
			if closestN > 0 {
				delta := (n - closestN) / 2
				pages = append(pages, closestP+delta-1, closestP+delta, closestP+delta+1)
			}
		}
		for _, p := range pages {
			if p < 1 || (totalPages > 0 && p > totalPages) {
				continue
			}
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func pageByIndex(pngs []string, i int) string {
	want := fmt.Sprintf("page-%d.png", i)
	wantPadded2 := fmt.Sprintf("page-%02d.png", i)
	wantPadded3 := fmt.Sprintf("page-%03d.png", i)
	for _, p := range pngs {
		base := filepath.Base(p)
		if base == want || base == wantPadded2 || base == wantPadded3 {
			return p
		}
	}
	if i >= 1 && i <= len(pngs) {
		return pngs[i-1]
	}
	return ""
}

func parseExtractResponse(s string) []Question {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
	}
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if i := strings.LastIndex(s, "}"); i >= 0 && i < len(s)-1 {
		s = s[:i+1]
	}
	var wire struct {
		Questions []struct {
			QuestionNumber int    `json:"question_number"`
			Type           string `json:"type"`
			PassageMD      string `json:"passage_md"`
			StemMD         string `json:"stem_md"`
			Choices        []struct {
				Label  string `json:"label"`
				TextMD string `json:"text_md"`
			} `json:"choices"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(s), &wire); err != nil {
		return nil
	}
	out := make([]Question, 0, len(wire.Questions))
	for _, q := range wire.Questions {
		nq := Question{
			Number:    q.QuestionNumber,
			PassageMD: q.PassageMD,
			StemMD:    q.StemMD,
		}
		for _, c := range q.Choices {
			nq.Choices = append(nq.Choices, Choice{Label: c.Label, TextMD: c.TextMD})
		}
		out = append(out, nq)
	}
	return out
}
