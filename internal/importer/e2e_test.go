package importer_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/importer"
	"github.com/qiangli/omniscore/internal/importer/render"
	"github.com/qiangli/omniscore/internal/importer/vision"
	"github.com/qiangli/omniscore/internal/store"
)

// TestE2E_MockProvider_2014PDF runs the full importer pipeline against the
// real 2014 AP Calc BC PDF using a deterministic mock vision Provider that
// dispatches by prompt content. It exercises render → classify → extract →
// reconcile → curve → emit, then loads the emitted JSON back through the
// runtime content loader to confirm round-trip compatibility.
//
// Skips when the PDF or pdftoppm is missing (CI / contributor without the
// private content repo). The PDF lives in data/, which is gitignored.
func TestE2E_MockProvider_2014PDF(t *testing.T) {
	pdfPath, _ := filepath.Abs(filepath.Join("..", "..", "data", "AP Calc BC", "AP Calc BC 2014.pdf"))
	if _, err := os.Stat(pdfPath); err != nil {
		t.Skipf("PDF not present (%s); this test exercises the real pipeline only when the PDF is on disk", pdfPath)
	}

	workdir := t.TempDir()
	outRoot := filepath.Join(workdir, "ap")
	ctx := context.Background()

	// 1. Rasterize. Use one DPI for both classify + extract to halve the work.
	pageDir := filepath.Join(workdir, "pages")
	pages, err := render.Rasterize(ctx, pdfPath, 100, pageDir)
	if err != nil {
		t.Fatalf("rasterize: %v", err)
	}
	t.Logf("rasterized %d pages", len(pages))
	if len(pages) < 50 {
		t.Fatalf("expected ~80 pages from the 2014 PDF, got %d", len(pages))
	}

	// 2. Mock provider that dispatches by prompt content and is deterministic
	// per (image, prompt-stage). Self-consistency rounds therefore agree.
	mp := newMockProvider(len(pages))

	classes, err := importer.ClassifyPages(ctx, mp, pages)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}

	var mcqNoCalc, mcqCalc, ansKey, curve []string
	for _, p := range pages {
		switch classes[p] {
		case importer.ClassMCQNoCalc:
			mcqNoCalc = append(mcqNoCalc, p)
		case importer.ClassMCQCalc:
			mcqCalc = append(mcqCalc, p)
		case importer.ClassAnswerKey:
			ansKey = append(ansKey, p)
		case importer.ClassScoringCurve:
			curve = append(curve, p)
		}
	}
	t.Logf("classified: mcq_no_calc=%d mcq_calc=%d answer_key=%d scoring_curve=%d (other=%d)",
		len(mcqNoCalc), len(mcqCalc), len(ansKey), len(curve),
		len(pages)-len(mcqNoCalc)-len(mcqCalc)-len(ansKey)-len(curve))
	if len(mcqNoCalc) == 0 || len(mcqCalc) == 0 || len(ansKey) == 0 || len(curve) == 0 {
		t.Fatalf("mock distribution did not yield at least one of each class")
	}

	// 3. Per-page MCQ extraction with N=3 self-consistency.
	var noCalcQs []importer.ExtractedQuestion
	for _, page := range mcqNoCalc {
		qs, err := importer.ExtractPage(ctx, mp, page, 3)
		if err != nil {
			t.Fatalf("extract %s: %v", page, err)
		}
		noCalcQs = append(noCalcQs, qs...)
	}
	var calcQs []importer.ExtractedQuestion
	for _, page := range mcqCalc {
		qs, err := importer.ExtractPage(ctx, mp, page, 3)
		if err != nil {
			t.Fatalf("extract %s: %v", page, err)
		}
		calcQs = append(calcQs, qs...)
	}
	sort.Slice(noCalcQs, func(i, j int) bool { return noCalcQs[i].QuestionNumber < noCalcQs[j].QuestionNumber })
	sort.Slice(calcQs, func(i, j int) bool { return calcQs[i].QuestionNumber < calcQs[j].QuestionNumber })

	// 4. Answer-key reconciliation.
	key, err := importer.ExtractAnswerKey(ctx, mp, ansKey)
	if err != nil {
		t.Fatalf("answer key: %v", err)
	}
	noCalcQs = importer.ReconcileAnswers(noCalcQs, key)
	calcQs = importer.ReconcileAnswers(calcQs, key)

	// 5. Curve.
	const slug = "ap-calc-bc-2014-mock"
	crv, err := importer.ExtractCurve(ctx, mp, slug, curve)
	if err != nil {
		t.Fatalf("curve: %v", err)
	}

	// 6. Emit.
	in := importer.EmitInput{
		Slug: slug, Title: "AP Calc BC 2014 (mock e2e)", OutRoot: outRoot, Workdir: workdir,
		Curve: crv,
		Modules: []importer.EmitModule{
			{ID: "mcq-no-calc", Section: "mcq_no_calc",
				Title: "Section I, Part A — No calculator", TimeLimitS: 3300, Questions: noCalcQs},
			{ID: "mcq-calc", Section: "mcq_calc",
				Title: "Section I, Part B — Calculator", TimeLimitS: 3000, Questions: calcQs},
		},
	}
	written, flagged, err := importer.Emit(in)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	t.Logf("emit: written=%d flagged=%d (review log at %s)",
		written, flagged, filepath.Join(workdir, ".review", slug+".md"))

	if written != len(mcqNoCalc)+len(mcqCalc) {
		t.Errorf("written count mismatch: want %d, got %d", len(mcqNoCalc)+len(mcqCalc), written)
	}

	// 7. Load through the runtime loader. workdir/ap is the per-exam-type
	// subdir, so workdir is the contentRoot for the loader.
	s, err := store.Open(ctx, filepath.Join(workdir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := content.LoadFromDisk(ctx, s, workdir); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	got, err := content.Get(ctx, s, slug)
	if err != nil {
		t.Fatalf("Get %s: %v", slug, err)
	}
	if got.ExamType != "ap" || got.Subject != "calc_bc" {
		t.Errorf("metadata: %+v", got)
	}
	if len(got.Modules) != 2 {
		t.Fatalf("want 2 modules, got %d", len(got.Modules))
	}
	if n := len(got.Modules[0].Questions); n != len(mcqNoCalc) {
		t.Errorf("module 0 questions: want %d, got %d", len(mcqNoCalc), n)
	}
	if n := len(got.Modules[1].Questions); n != len(mcqCalc) {
		t.Errorf("module 1 questions: want %d, got %d", len(mcqCalc), n)
	}
	for _, m := range got.Modules {
		for _, q := range m.Questions {
			if q.AnswerLabel == "" || !strings.Contains("ABCDE", q.AnswerLabel) {
				t.Errorf("q %s answer_label %q invalid", q.ID, q.AnswerLabel)
			}
			if len(q.Choices) != 5 {
				t.Errorf("q %s choices=%d (want 5)", q.ID, len(q.Choices))
			}
		}
	}
	t.Logf("round-trip OK: loaded %s with %d modules, %d total Qs",
		got.Slug, len(got.Modules),
		len(got.Modules[0].Questions)+len(got.Modules[1].Questions))
}

// --- mock provider ----------------------------------------------------------

type mockProvider struct {
	totalPages int

	mu             sync.Mutex
	classify       map[string]importer.PageClass
	extract        map[string]string
	classifyOrder  int // increments first time we see a new image during classify
}

func newMockProvider(totalPages int) *mockProvider {
	return &mockProvider{
		totalPages: totalPages,
		classify:   map[string]importer.PageClass{},
		extract:    map[string]string{},
	}
}

func (m *mockProvider) Name() string { return "mock-e2e" }

func (m *mockProvider) Generate(_ context.Context, req vision.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case strings.Contains(req.Prompt, "EXACTLY ONE token"):
		return string(m.classifyFor(req.Image)), nil
	case strings.Contains(req.Prompt, "extracting AP Calculus BC multiple-choice questions"):
		return m.extractFor(req.Image), nil
	case strings.Contains(req.Prompt, "multiple-choice answer key page"):
		return m.answerKeyJSON(), nil
	case strings.Contains(req.Prompt, "scoring worksheet"):
		return m.curveJSON(), nil
	}
	return "", errors.New("mockProvider: unrecognized prompt")
}

// classifyFor returns the same class for the same image (so a re-run yields
// stable classifications). The class distribution rolls over the page count
// to guarantee at least one of each MCQ section + answer key + curve.
func (m *mockProvider) classifyFor(image []byte) importer.PageClass {
	key := hash(image)
	if c, ok := m.classify[key]; ok {
		return c
	}
	idx := m.classifyOrder
	m.classifyOrder++
	c := classifyByIndex(idx, m.totalPages)
	m.classify[key] = c
	return c
}

// classifyByIndex returns a synthetic class based on a page's position in the
// document. Tuned so the 2014 PDF (~80 pages) yields a realistic mix.
func classifyByIndex(idx, total int) importer.PageClass {
	switch {
	case idx < 20:
		return importer.ClassInstructions
	case idx < 45:
		return importer.ClassMCQNoCalc
	case idx < 60:
		return importer.ClassMCQCalc
	case idx < 65:
		return importer.ClassAnswerKey
	case idx < 70:
		return importer.ClassScoringCurve
	default:
		return importer.ClassSkip
	}
}

// extractFor returns one synthetic MCQ per unique image. The question_number
// equals the order in which we first saw this image during extraction. The
// 3 self-consistency rounds for the same page get the same JSON, so the
// vote is unanimous and questions don't get spuriously flagged.
func (m *mockProvider) extractFor(image []byte) string {
	key := hash(image)
	if s, ok := m.extract[key]; ok {
		return s
	}
	qNum := len(m.extract) + 1
	answer := []string{"A", "B", "C", "D", "E"}[qNum%5]
	js := fmt.Sprintf(`{"questions":[{
		"question_number": %d,
		"stem_md": "Synthetic stem for question %d.",
		"has_stem_figure": false,
		"choices": [
			{"label":"A","text_md":"choice A","has_figure":false},
			{"label":"B","text_md":"choice B","has_figure":false},
			{"label":"C","text_md":"choice C","has_figure":false},
			{"label":"D","text_md":"choice D","has_figure":false},
			{"label":"E","text_md":"choice E","has_figure":false}
		]
	}]}`, qNum, qNum)
	_ = answer // answer is set later by the answer-key reconciliation pass
	m.extract[key] = js
	return js
}

// answerKeyJSON returns answers for question_numbers 1..40 (covers any
// realistic mock distribution from classifyByIndex). Each answer cycles
// through A..E so the test exercises all five labels.
func (m *mockProvider) answerKeyJSON() string {
	var b strings.Builder
	b.WriteString(`{"answers":[`)
	for n := 1; n <= 40; n++ {
		if n > 1 {
			b.WriteByte(',')
		}
		label := []string{"A", "B", "C", "D", "E"}[n%5]
		fmt.Fprintf(&b, `{"question_number":%d,"label":%q}`, n, label)
	}
	b.WriteString(`]}`)
	return b.String()
}

// curveJSON returns a synthetic AP curve mapping raw 0..40 to AP grades 1..5.
func (m *mockProvider) curveJSON() string {
	return `{"ranges":[
		{"raw_min":0,"raw_max":7,"scaled":1},
		{"raw_min":8,"raw_max":15,"scaled":2},
		{"raw_min":16,"raw_max":23,"scaled":3},
		{"raw_min":24,"raw_max":31,"scaled":4},
		{"raw_min":32,"raw_max":40,"scaled":5}
	]}`
}

func hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
