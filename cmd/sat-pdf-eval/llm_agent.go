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
	"regexp"
	"sort"
	"strings"
	"time"
)

// Agentic LLM fallback: instead of stuffing whole pages into one prompt,
// give the model a small tool surface (read_pdf_page, list_questions,
// add_question, mark_module_done) and loop until it fills the module or
// hits the iteration cap. Per-module loop, mutating tools.

// --- Ollama /api/chat client with tool calling ---------------------------

type chatMessage struct {
	Role      string     `json:"role"`              // system | user | assistant | tool
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"-"` // used when serializing role=tool
}

type toolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type olToolParam struct {
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	Enum        []string          `json:"enum,omitempty"`
	Items       *olToolParam      `json:"items,omitempty"`
	Properties  map[string]any    `json:"properties,omitempty"`
	Required    []string          `json:"required,omitempty"`
	AdditionalP bool              `json:"additionalProperties,omitempty"`
}

type olToolSchema struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type ollamaChatClient struct {
	model    string
	baseURL  string
	cacheDir string
	hc       *http.Client
}

func newOllamaChatClient(modelSpec, host, cacheDir string) (*ollamaChatClient, error) {
	parts := strings.SplitN(modelSpec, "/", 2)
	if len(parts) != 2 || parts[0] != "ollama" {
		return nil, fmt.Errorf("agent mode requires ollama/<model> spec, got %q", modelSpec)
	}
	_ = os.MkdirAll(cacheDir, 0o755)
	if host == "" {
		host = "http://localhost:11434"
	}
	return &ollamaChatClient{
		model:    parts[1],
		baseURL:  strings.TrimRight(host, "/"),
		cacheDir: cacheDir,
		hc:       &http.Client{Timeout: 10 * time.Minute},
	}, nil
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Tools    []olToolSchema `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

type chatResponse struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
}

func (o *ollamaChatClient) Chat(ctx context.Context, msgs []chatMessage, tools []olToolSchema) (chatMessage, error) {
	// Build wire messages: when role=tool, Ollama wants {role:"tool", content:"..."}
	// (and modern variants also accept tool_call_id; we omit since qwen2.5 doesn't track it).
	wireMsgs := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		w := map[string]any{"role": m.Role, "content": m.Content}
		if len(m.ToolCalls) > 0 {
			w["tool_calls"] = m.ToolCalls
		}
		if m.Role == "tool" && m.ToolName != "" {
			w["name"] = m.ToolName
		}
		wireMsgs = append(wireMsgs, w)
	}
	reqBody := map[string]any{
		"model":    o.model,
		"messages": wireMsgs,
		"stream":   false,
		"options":  map[string]any{"temperature": 0.0, "num_ctx": 16384},
	}
	if len(tools) > 0 {
		reqBody["tools"] = tools
	}

	body, _ := json.Marshal(reqBody)
	// content-addressed cache so re-runs are free.
	h := sha256.New()
	h.Write([]byte(o.model))
	h.Write(body)
	key := hex.EncodeToString(h.Sum(nil))
	cachePath := filepath.Join(o.cacheDir, key)
	if b, err := os.ReadFile(cachePath); err == nil {
		var cm chatMessage
		if err := json.Unmarshal(b, &cm); err == nil {
			return cm, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return chatMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.hc.Do(req)
	if err != nil {
		return chatMessage{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return chatMessage{}, err
	}
	if resp.StatusCode != 200 {
		return chatMessage{}, fmt.Errorf("ollama %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return chatMessage{}, fmt.Errorf("decode chat resp: %w (raw=%s)", err, truncStr(string(raw), 400))
	}
	out, _ := json.Marshal(cr.Message)
	_ = os.WriteFile(cachePath, out, 0o644)
	return cr.Message, nil
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// --- Agent state + tool dispatch -----------------------------------------

type agentState struct {
	pdfPath   string
	pages     []Page
	test      *Test
	modID     string
	cap       int
	allowSPR  bool
	logf      func(format string, a ...any)
	startCnt  int

	// Verbatim grounding: every read_pdf_page result is appended to
	// readHaystack (normalized). add_question rejects any stem that doesn't
	// have a contiguous N-gram in the haystack — forces the model to copy
	// from the source instead of paraphrasing/hallucinating.
	readHaystack    string
	readHaystackTok []string // pre-tokenized for grounding checks
	rejectedGround  int
}

var groundingTokenRE = regexp.MustCompile(`[a-z0-9]+`)

func groundingTokens(s string) []string {
	return groundingTokenRE.FindAllString(strings.ToLower(s), -1)
}

func (s *agentState) appendHaystack(text string) {
	s.readHaystack += "\n" + text
	s.readHaystackTok = groundingTokens(s.readHaystack)
}

// checkGrounding returns "" if grounded, or a short reason if not.
//
// Rule: the stem must share at least one contiguous 6-token run with the
// haystack of pages-the-model-has-read. For MCQ choices, each choice's
// text must share a 3-token run (choices are short).
//
// SPR stems are exempt (often "{equation} What is x?" is too short).
func (s *agentState) checkGrounding(stem string, choices []struct {
	Label  string `json:"label"`
	TextMD string `json:"text_md"`
}, qtype string) string {
	if len(s.readHaystackTok) == 0 {
		return "no pages have been read yet — call read_pdf_page first"
	}
	stemToks := groundingTokens(stem)
	if qtype != "spr" && len(stemToks) >= 6 {
		if !hasNgramOverlap(stemToks, s.readHaystackTok, 6) {
			return "stem text does not match any page you have read (need ≥6 consecutive tokens to match)"
		}
	}
	if qtype == "mcq" {
		for _, c := range choices {
			ct := groundingTokens(c.TextMD)
			if len(ct) < 3 {
				continue
			}
			if !hasNgramOverlap(ct, s.readHaystackTok, 3) {
				return fmt.Sprintf("choice %s text does not match any page you have read", c.Label)
			}
		}
	}
	return ""
}

// hasNgramOverlap returns true if any contiguous n-token slice of needle
// appears as a contiguous slice in haystack.
func hasNgramOverlap(needle, haystack []string, n int) bool {
	if len(needle) < n || len(haystack) < n {
		return false
	}
	// Build a set of n-grams from needle, then scan haystack once.
	needleNgrams := make(map[string]struct{}, len(needle)-n+1)
	for i := 0; i+n <= len(needle); i++ {
		needleNgrams[strings.Join(needle[i:i+n], " ")] = struct{}{}
	}
	for i := 0; i+n <= len(haystack); i++ {
		if _, ok := needleNgrams[strings.Join(haystack[i:i+n], " ")]; ok {
			return true
		}
	}
	return false
}

func (s *agentState) tools() []olToolSchema {
	mk := func(name, desc string, params map[string]any) olToolSchema {
		var t olToolSchema
		t.Type = "function"
		t.Function.Name = name
		t.Function.Description = desc
		t.Function.Parameters = params
		return t
	}
	pageEnum := []any{}
	for _, p := range s.pages {
		pageEnum = append(pageEnum, p.Index)
	}
	return []olToolSchema{
		mk("list_questions", "Return the captured question numbers and the still-missing ones for the current module. Always call this first.", map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		}),
		mk("read_pdf_page", "Return the text content of one page of the SAT question PDF (extracted via a pure-Go PDF parser; preserves visual columns).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"page": map[string]any{
					"type":        "integer",
					"description": "1-based page index; SAT practice tests have ~48 pages.",
				},
			},
			"required":             []string{"page"},
			"additionalProperties": false,
		}),
		mk("add_question", "Add ONE extracted question to the current module. Validates that the question number is in range and not already present.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"number": map[string]any{
					"type":        "integer",
					"description": "1-based question number within the module.",
				},
				"type": map[string]any{
					"type":        "string",
					"enum":        []string{"mcq", "spr"},
					"description": "mcq (4 choices A-D) or spr (no choices, typed answer).",
				},
				"passage_md": map[string]any{
					"type":        "string",
					"description": "Reading passage / problem setup, in Markdown. Empty string if none.",
				},
				"stem_md": map[string]any{
					"type":        "string",
					"description": "The question prompt itself (e.g. \"Which choice completes the text...\"). Math expressions in $...$ KaTeX.",
				},
				"choices": map[string]any{
					"type":        "array",
					"description": "4 choices for mcq, empty array for spr.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"label":   map[string]any{"type": "string", "enum": []string{"A", "B", "C", "D"}},
							"text_md": map[string]any{"type": "string"},
						},
						"required": []string{"label", "text_md"},
					},
				},
			},
			"required":             []string{"number", "type", "stem_md", "choices"},
			"additionalProperties": false,
		}),
		mk("mark_module_done", "Signal that you have extracted every question for this module. Call once at the end.", map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		}),
	}
}

type dispatchResult struct {
	Content string
	Done    bool
}

func (s *agentState) dispatch(name string, args json.RawMessage) dispatchResult {
	switch name {
	case "list_questions":
		got := s.test.Modules[s.modID]
		var captured []int
		for _, q := range got {
			captured = append(captured, q.Number)
		}
		sort.Ints(captured)
		miss := missingNumbers(got, s.cap)
		next := 0
		if len(miss) > 0 {
			next = miss[0]
		}
		b, _ := json.Marshal(map[string]any{
			"module_id":   s.modID,
			"cap":         s.cap,
			"captured":    captured,
			"missing":     miss,
			"next_to_add": next,
		})
		return dispatchResult{Content: string(b)}
	case "read_pdf_page":
		var p struct {
			Page int `json:"page"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return dispatchResult{Content: fmt.Sprintf(`{"error":"bad args: %v"}`, err)}
		}
		text := pageTextFor(s.pages, p.Page)
		if text == "" {
			return dispatchResult{Content: fmt.Sprintf(`{"error":"page %d not found"}`, p.Page)}
		}
		// Append to grounding haystack (use the FULL text, not the truncated
		// version sent to the model — model might quote anything it saw).
		s.appendHaystack(text)
		if len(text) > 8000 {
			text = text[:8000] + "\n…[truncated]"
		}
		b, _ := json.Marshal(map[string]any{"page": p.Page, "text": text})
		return dispatchResult{Content: string(b)}
	case "add_question":
		var p struct {
			Number    int    `json:"number"`
			Type      string `json:"type"`
			PassageMD string `json:"passage_md"`
			StemMD    string `json:"stem_md"`
			Choices   []struct {
				Label  string `json:"label"`
				TextMD string `json:"text_md"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return dispatchResult{Content: fmt.Sprintf(`{"error":"bad args: %v"}`, err)}
		}
		if p.Number < 1 || p.Number > s.cap {
			return dispatchResult{Content: fmt.Sprintf(`{"error":"number %d out of range [1,%d]"}`, p.Number, s.cap)}
		}
		if hasQuestionNumber(s.test.Modules[s.modID], p.Number) {
			return dispatchResult{Content: fmt.Sprintf(`{"error":"question %d already present"}`, p.Number)}
		}
		if p.Type == "" {
			p.Type = "mcq"
		}
		if p.Type == "mcq" && len(p.Choices) != 4 {
			return dispatchResult{Content: fmt.Sprintf(`{"error":"mcq requires exactly 4 choices, got %d"}`, len(p.Choices))}
		}
		if p.Type == "spr" && !s.allowSPR {
			return dispatchResult{Content: `{"error":"spr not allowed in this module"}`}
		}
		// Verbatim grounding: at least one 6-token contiguous substring of
		// the stem (and, for mcq, each choice) must appear in pages we've
		// actually read. Skips trivial stems (< 6 tokens) and SPR.
		if reason := s.checkGrounding(p.StemMD, p.Choices, p.Type); reason != "" {
			s.rejectedGround++
			s.logf("[agent %s] reject q=%d ungrounded: %s\n", s.modID, p.Number, reason)
			return dispatchResult{Content: fmt.Sprintf(
				`{"error":"%s","hint":"Copy the question text VERBATIM from a read_pdf_page result. Do not paraphrase. If the text differs, re-read the page and use the exact wording."}`,
				reason)}
		}
		nq := Question{
			Module:    s.modID,
			Number:    p.Number,
			PassageMD: p.PassageMD,
			StemMD:    p.StemMD,
			Source:    "llm-agent",
		}
		for _, c := range p.Choices {
			nq.Choices = append(nq.Choices, Choice{Label: c.Label, TextMD: c.TextMD})
		}
		s.test.Modules[s.modID] = append(s.test.Modules[s.modID], nq)
		s.logf("[agent %s] add q=%d (now %d/%d)\n", s.modID, p.Number, len(s.test.Modules[s.modID]), s.cap)
		b, _ := json.Marshal(map[string]any{"ok": true, "captured_count": len(s.test.Modules[s.modID])})
		return dispatchResult{Content: string(b)}
	case "mark_module_done":
		return dispatchResult{Content: `{"ok":true}`, Done: true}
	default:
		return dispatchResult{Content: fmt.Sprintf(`{"error":"unknown tool %q"}`, name)}
	}
}

// --- Loop driver ---------------------------------------------------------

// runLLMAgentFallback runs a per-module agentic loop with mutating tools.
// Returns counts of (chat iterations, questions added).
func runLLMAgentFallback(ctx context.Context, pdfPath string, pages []Page, t *Test, cfg FallbackConfig, log io.Writer) (iters, filled int, err error) {
	if log == nil {
		log = io.Discard
	}
	logf := func(format string, a ...any) { fmt.Fprintf(log, format, a...) }

	client, err := newOllamaChatClient(cfg.Model, cfg.Host, filepath.Join(cfg.Workdir, "agent-cache"))
	if err != nil {
		return 0, 0, err
	}

	caps := map[string]int{"rw-1": 33, "rw-2": 33, "math-1": 27, "math-2": 27}
	allowSPR := map[string]bool{"rw-1": false, "rw-2": false, "math-1": true, "math-2": true}

	for _, modID := range []string{"rw-1", "rw-2", "math-1", "math-2"} {
		got := t.Modules[modID]
		if len(got) >= caps[modID] {
			continue
		}
		state := &agentState{
			pdfPath:  pdfPath,
			pages:    pages,
			test:     t,
			modID:    modID,
			cap:      caps[modID],
			allowSPR: allowSPR[modID],
			logf:     logf,
			startCnt: len(got),
		}
		sysPrompt := buildAgentSystemPrompt(modID, caps[modID], allowSPR[modID], pages)
		userPrompt := fmt.Sprintf(`Extract all questions for module %s of this SAT practice test. There should be exactly %d questions. The parser has already captured %d of them; you only need to fill the gaps. Always call list_questions first to see what's missing.`,
			modID, caps[modID], len(got))

		msgs := []chatMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userPrompt},
		}
		tools := state.tools()

		maxIters := 30
		if cfg.MaxPages > 0 {
			maxIters = cfg.MaxPages
		}
		startCount := len(t.Modules[modID])
		for i := 0; i < maxIters; i++ {
			iters++
			if len(t.Modules[modID]) >= caps[modID] {
				break
			}
			resp, cerr := client.Chat(ctx, msgs, tools)
			if cerr != nil {
				logf("[agent %s] chat err: %v\n", modID, cerr)
				break
			}
			msgs = append(msgs, resp)
			if len(resp.ToolCalls) == 0 {
				// model emitted a text reply; check if it's a "done" signal,
				// otherwise nudge it to call a tool.
				logf("[agent %s] no tool call, content=%q\n", modID, truncStr(resp.Content, 200))
				if strings.Contains(strings.ToLower(resp.Content), "done") {
					break
				}
				msgs = append(msgs, chatMessage{Role: "user", Content: "Please call a tool. Use list_questions to see progress."})
				continue
			}
			done := false
			for _, tc := range resp.ToolCalls {
				res := state.dispatch(tc.Function.Name, tc.Function.Arguments)
				msgs = append(msgs, chatMessage{
					Role: "tool", Content: res.Content, ToolName: tc.Function.Name,
				})
				if res.Done {
					done = true
				}
			}
			if done {
				break
			}
			// Token-budget guard: keep system+user+last 20 msgs.
			if len(msgs) > 24 {
				msgs = append(msgs[:2], msgs[len(msgs)-20:]...)
			}
		}
		added := len(t.Modules[modID]) - startCount
		filled += added
		qs := t.Modules[modID]
		sort.Slice(qs, func(i, j int) bool { return qs[i].Number < qs[j].Number })
		t.Modules[modID] = qs
		logf("[agent %s] done: %d -> %d (added %d) iters=%d rejected_ungrounded=%d\n",
			modID, startCount, len(t.Modules[modID]), added, iters, state.rejectedGround)
	}
	return iters, filled, nil
}

func buildAgentSystemPrompt(modID string, cap int, allowSPR bool, pages []Page) string {
	section := "Reading and Writing"
	if strings.HasPrefix(modID, "math") {
		section = "Math"
	}
	sprBlurb := ""
	if allowSPR {
		sprBlurb = `
- Some math questions are student-produced response ("spr") — they have no A-D choices. For those, set type="spr" and choices=[].`
	}
	pageHint := fmt.Sprintf("The PDF has %d pages total.", len(pages))
	// Find pages most likely to contain this module's content (rough hint).
	modulePageHint := ""
	switch modID {
	case "rw-1":
		modulePageHint = "Module rw-1 questions are roughly on pages 2–14."
	case "rw-2":
		modulePageHint = "Module rw-2 questions are roughly on pages 16–28."
	case "math-1":
		modulePageHint = "Module math-1 questions are roughly on pages 30–38."
	case "math-2":
		modulePageHint = "Module math-2 questions are roughly on pages 40–48."
	}
	return fmt.Sprintf(`You are extracting Digital SAT questions from one module of a practice test PDF.
Module: %s (section "%s", %d questions total).%s

%s
%s

You have tools available. You MUST use tools — do NOT respond with free-form JSON or prose summaries.

Workflow:
1. ALWAYS start by calling list_questions to see which question numbers are missing.
2. Call read_pdf_page with a page number where you expect to find a missing question. Use the page hint above.
3. From the page text, identify questions by their leading number (1, 2, 3, ...) and choice anchors (A), B), C), D)).
4. For each question you can fully read on that page, call add_question with its fields:
   - number: the visible question number within the module
   - type: "mcq" (default) or "spr"
   - passage_md: the reading passage (R&W) or problem setup (Math), in Markdown. Empty for math MCQs that have no setup beyond the prompt.
   - stem_md: the prompt sentence (e.g. "Which choice completes the text..."). Math in $...$ KaTeX.
   - choices: array of 4 {label, text_md} for mcq; empty for spr.
5. Repeat: list_questions to check progress, read more pages, add more questions.
6. When all %d questions are added, call mark_module_done.

Rules:
- Preserve original wording verbatim — do not paraphrase or summarize.
- All math expressions MUST use $...$ KaTeX delimiters.
- Do not invent questions. If the page does not contain the question number you're looking for, read a different page.
- Output ONLY tool calls. No prose between tool calls.`,
		modID, section, cap, sprBlurb, pageHint, modulePageHint, cap)
}

// regex used by read_pdf_region tool (not currently exposed in the tool list,
// kept for future expansion).
var _ = regexp.MustCompile
