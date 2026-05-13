package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/importer/pipeline"
	"github.com/qiangli/omniscore/internal/importer/profile"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

// scriptedProvider returns a queue of canned responses, one per Generate call,
// so we can drive ExtractPage through deterministic self-consistency rounds.
type scriptedProvider struct {
	responses []string
	idx       int
}

func (s *scriptedProvider) Name() string { return "scripted" }
func (s *scriptedProvider) Generate(_ context.Context, _ vision.Request) (string, error) {
	if s.idx >= len(s.responses) {
		return "", errors.New("scriptedProvider: ran out of responses")
	}
	out := s.responses[s.idx]
	s.idx++
	return out, nil
}

func writePagePNG(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func apMCQModule() profile.ModuleSpec {
	for _, m := range profile.AP().Modules {
		return m
	}
	panic("AP profile has no modules")
}

func TestExtractPage_HappyPath(t *testing.T) {
	dir := t.TempDir()
	page := writePagePNG(t, dir, "page-001.png")

	canned := `{"questions":[{
		"question_number": 1,
		"stem_md": "What is $f'(2)$?",
		"has_stem_figure": false,
		"choices": [
			{"label":"A","text_md":"$3$","has_figure":false},
			{"label":"B","text_md":"$6$","has_figure":false},
			{"label":"C","text_md":"$9$","has_figure":false},
			{"label":"D","text_md":"$12$","has_figure":false},
			{"label":"E","text_md":"$15$","has_figure":false}
		]
	}]}`
	p := &scriptedProvider{responses: []string{canned, canned, canned}}

	qs, err := pipeline.ExtractPage(context.Background(), p, profile.AP(), apMCQModule(), page, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 {
		t.Fatalf("want 1 question, got %d", len(qs))
	}
	if qs[0].QuestionNumber != 1 || qs[0].StemMD != "What is $f'(2)$?" {
		t.Fatalf("merged question wrong: %+v", qs[0])
	}
	if len(qs[0].Choices) != 5 || qs[0].Choices[2].Label != "C" {
		t.Fatalf("choices wrong: %+v", qs[0].Choices)
	}
	if qs[0].NeedsReview {
		t.Fatalf("unanimous responses should not flag for review: %+v", qs[0].ReviewNotes)
	}
}

func TestExtractPage_DisagreementFlagsForReview(t *testing.T) {
	dir := t.TempDir()
	page := writePagePNG(t, dir, "page-001.png")

	five := `{"questions":[{"question_number":1,"stem_md":"x","has_stem_figure":false,"choices":[
		{"label":"A","text_md":"a","has_figure":false},
		{"label":"B","text_md":"b","has_figure":false},
		{"label":"C","text_md":"c","has_figure":false},
		{"label":"D","text_md":"d","has_figure":false},
		{"label":"E","text_md":"e","has_figure":false}
	]}]}`
	four := `{"questions":[{"question_number":1,"stem_md":"x","has_stem_figure":false,"choices":[
		{"label":"A","text_md":"a","has_figure":false},
		{"label":"B","text_md":"b","has_figure":false},
		{"label":"C","text_md":"c","has_figure":false},
		{"label":"D","text_md":"d","has_figure":false}
	]}]}`
	p := &scriptedProvider{responses: []string{five, five, four}}

	qs, err := pipeline.ExtractPage(context.Background(), p, profile.AP(), apMCQModule(), page, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 || !qs[0].NeedsReview {
		t.Fatalf("disagreement should flag for review: %+v", qs)
	}
	if len(qs[0].Choices) != 5 {
		t.Fatalf("majority count (5) should win: got %d", len(qs[0].Choices))
	}
}

func TestExtractPage_AllUnparsedReturnsReviewEntry(t *testing.T) {
	dir := t.TempDir()
	page := writePagePNG(t, dir, "page-001.png")

	garbage := "I'm sorry, I can't extract that page."
	p := &scriptedProvider{responses: []string{garbage, garbage, garbage}}

	qs, err := pipeline.ExtractPage(context.Background(), p, profile.AP(), apMCQModule(), page, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 || !qs[0].NeedsReview {
		t.Fatalf("all-unparsed should yield one review-flagged entry: %+v", qs)
	}
}

func TestReconcileAnswers(t *testing.T) {
	qs := []pipeline.ExtractedQuestion{
		{QuestionNumber: 1, AnswerLabel: ""},
		{QuestionNumber: 2, AnswerLabel: "A"},
		{QuestionNumber: 3, AnswerLabel: "B"},
		{QuestionNumber: 4, AnswerLabel: ""},
	}
	key := map[int]string{1: "C", 2: "A", 3: "D"}
	out := pipeline.ReconcileAnswers(qs, key)

	if out[0].AnswerLabel != "C" {
		t.Errorf("q1: want C from key, got %q", out[0].AnswerLabel)
	}
	if out[1].AnswerLabel != "A" || out[1].NeedsReview {
		t.Errorf("q2 should be clean: %+v", out[1])
	}
	if out[2].AnswerLabel != "D" || !containsSubstr(out[2].ReviewNotes, "answer mismatch") {
		t.Errorf("q3 should override + flag: %+v", out[2])
	}
	if !out[3].NeedsReview {
		t.Errorf("q4 should be flagged (not in key): %+v", out[3])
	}
}

func TestEmit_RoundTripWithRuntimeLoader(t *testing.T) {
	// Emit a minimal AP test with the importer, then load it back through the
	// runtime content loader.
	dir := t.TempDir()
	outRoot := dir
	workdir := filepath.Join(dir, "workdir")

	pageDir := filepath.Join(workdir, "pages")
	if err := os.MkdirAll(pageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pagePNG := filepath.Join(pageDir, "page-001.png")
	if err := os.WriteFile(pagePNG, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}

	in := pipeline.EmitInput{
		Profile: profile.AP(),
		Slug:    "ap-emit-test",
		Title:   "Emitter Round-Trip Test",
		OutRoot: outRoot,
		Workdir: workdir,
		Curve: content.Curve{
			TestSlug: "ap-emit-test",
			Sections: map[string][]content.CurvePoint{
				"mcq_total": {{Raw: 0, Scaled: 1}, {Raw: 30, Scaled: 5}},
			},
		},
		Modules: []pipeline.EmitModule{
			{
				ID: "mcq-no-calc", Section: "mcq_no_calc",
				Title: "Section I, Part A — No calculator", TimeLimitS: 600,
				Questions: []pipeline.ExtractedQuestion{
					{
						PageSource: pagePNG, QuestionNumber: 1,
						StemMD: "$f'(2)$ if $f(x)=x^3-3x$?",
						Choices: []pipeline.ExtractedChoice{
							{Label: "A", TextMD: "$3$"}, {Label: "B", TextMD: "$6$"},
							{Label: "C", TextMD: "$9$"}, {Label: "D", TextMD: "$12$"},
							{Label: "E", TextMD: "$15$"},
						},
						AnswerLabel: "C",
					},
					{
						PageSource: pagePNG, QuestionNumber: 2,
						StemMD: "Pick the slope field for $dy/dx = y$",
						HasStemFigure: true,
						Choices: []pipeline.ExtractedChoice{
							{Label: "A", TextMD: "", HasFigure: true},
							{Label: "B", TextMD: "", HasFigure: true},
							{Label: "C", TextMD: "", HasFigure: true},
							{Label: "D", TextMD: "", HasFigure: true},
							{Label: "E", TextMD: "", HasFigure: true},
						},
						AnswerLabel: "B",
					},
				},
			},
		},
	}
	written, flagged, err := pipeline.Emit(in)
	if err != nil {
		t.Fatal(err)
	}
	if written != 2 {
		t.Errorf("written: want 2, got %d", written)
	}
	if flagged != 0 {
		t.Errorf("flagged: want 0, got %d", flagged)
	}

	raw, err := os.ReadFile(filepath.Join(outRoot, "ap", "ap-emit-test", "test.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got content.Test
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("emitted JSON does not parse as content.Test: %v", err)
	}
	if got.ExamType != "ap" || got.Subject != "calc_bc" {
		t.Errorf("metadata: %+v", got)
	}
	if len(got.Modules) != 1 || len(got.Modules[0].Questions) != 2 {
		t.Fatalf("module shape: %+v", got.Modules)
	}
	q2 := got.Modules[0].Questions[1]
	if q2.StemFigure == nil || !strings.HasSuffix(q2.StemFigure.Src, "q2-stem.png") {
		t.Errorf("q2 stem figure: %+v", q2.StemFigure)
	}
	for _, c := range q2.Choices {
		if c.Figure == nil {
			t.Errorf("q2 choice %s missing figure", c.Label)
		}
	}

	figDir := filepath.Join(outRoot, "ap", "ap-emit-test", "figures")
	for _, name := range []string{"q2-stem.png", "q2-a.png", "q2-b.png", "q2-c.png", "q2-d.png", "q2-e.png"} {
		if _, err := os.Stat(filepath.Join(figDir, name)); err != nil {
			t.Errorf("figure %s missing: %v", name, err)
		}
	}

	if _, err := os.Stat(filepath.Join(workdir, ".review", "ap-emit-test.md")); err != nil {
		t.Errorf("review log missing: %v", err)
	}
}
