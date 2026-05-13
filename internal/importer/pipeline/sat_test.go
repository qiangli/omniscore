package pipeline_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qiangli/omniscore/internal/importer/pipeline"
	"github.com/qiangli/omniscore/internal/importer/profile"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

func TestSATProfile_FourChoicesAcceptedFourRejected(t *testing.T) {
	// SAT MCQs are exactly 4 choices (A–D). The validator accepts 4, and
	// (one less than expected = 3) — though 3 isn't valid SAT, validating it
	// is the profile's policy concern, not the test's.
	q := pipeline.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Reading comprehension question.",
		Choices: []pipeline.ExtractedChoice{
			{Label: "A", TextMD: "x"}, {Label: "B", TextMD: "y"},
			{Label: "C", TextMD: "z"}, {Label: "D", TextMD: "w"},
		},
		AnswerLabel: "B",
	}
	issues := pipeline.ValidateQuestion(profile.SAT(), q)
	if len(issues) != 0 {
		t.Fatalf("4-choice SAT should be valid; got: %v", issues)
	}

	// Five choices is wrong for SAT (it'd be valid for AP).
	q.Choices = append(q.Choices, pipeline.ExtractedChoice{Label: "E", TextMD: "v"})
	issues = pipeline.ValidateQuestion(profile.SAT(), q)
	if !containsSubstr(issues, "expected 4 choices") {
		t.Fatalf("5-choice SAT should be flagged; got: %v", issues)
	}
}

func TestSATAnswerKey_NumericFallback(t *testing.T) {
	// Some SAT answer-key tables print numerics (1–4) instead of letters.
	// ExtractAnswerKey should normalize them to A–D using a scripted provider
	// that emits a numeric-keyed JSON payload.
	dir := t.TempDir()
	page := filepath.Join(dir, "ak.png")
	if err := os.WriteFile(page, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := `{"answers":[
		{"question_number":1,"label":"3"},
		{"question_number":2,"label":"A"},
		{"question_number":3,"label":"4"}
	]}`
	p := &scriptedProvider{responses: []string{resp}}

	prof := profile.SAT()
	mod := prof.Modules[0] // rw-1
	key, err := pipeline.ExtractAnswerKey(context.Background(), p, prof, mod, []string{page})
	if err != nil {
		t.Fatal(err)
	}
	if key[1] != "C" {
		t.Errorf("q1: numeric 3 should map to C, got %q", key[1])
	}
	if key[2] != "A" {
		t.Errorf("q2: letter A should round-trip, got %q", key[2])
	}
	if key[3] != "D" {
		t.Errorf("q3: numeric 4 should map to D, got %q", key[3])
	}
}

func TestSATProfile_PageClassesIncludeFourModules(t *testing.T) {
	prof := profile.SAT()
	classes := prof.PageClasses()
	// Expected: instructions, rw_module_1, rw_module_2, math_module_1,
	// math_module_2, answer_key, scoring_curve, skip.
	want := []string{
		"instructions",
		"rw_module_1", "rw_module_2",
		"math_module_1", "math_module_2",
		"answer_key", "scoring_curve", "skip",
	}
	if len(classes) != len(want) {
		t.Fatalf("PageClasses count: want %d, got %d (%v)", len(want), len(classes), classes)
	}
	for i, w := range want {
		if classes[i] != w {
			t.Errorf("PageClasses[%d]: want %q, got %q", i, w, classes[i])
		}
	}
}

func TestSATProfile_RWPromptMentionsPassage(t *testing.T) {
	// The R&W modules should ask the LLM for passage_md; Math modules
	// should not.
	prof := profile.SAT()
	var rw, math profile.ModuleSpec
	for _, m := range prof.Modules {
		if m.Section == "rw" && rw.ID == "" {
			rw = m
		}
		if m.Section == "math" && math.ID == "" {
			math = m
		}
	}
	rwPrompt := prof.Prompts.Extract(prof, rw)
	mathPrompt := prof.Prompts.Extract(prof, math)
	if !strings.Contains(rwPrompt, "passage_md") {
		t.Errorf("R&W prompt should mention passage_md, got: %s", rwPrompt)
	}
	if !strings.Contains(rwPrompt, "left-pane reading passage") {
		t.Errorf("R&W prompt should describe passage extraction, got: %s", rwPrompt)
	}
	if strings.Contains(mathPrompt, "left-pane reading passage") {
		t.Errorf("Math prompt should not describe passage extraction, got: %s", mathPrompt)
	}
}

// Unused-import shield so this file pulls in the vision package for type
// checking of scriptedProvider.
var _ vision.Provider = (*scriptedProvider)(nil)
