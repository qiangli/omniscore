package pipeline_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/importer/pipeline"
	"github.com/qiangli/omniscore/internal/importer/profile"
	"github.com/qiangli/omniscore/internal/importer/vision"
	"github.com/qiangli/omniscore/internal/store"
)

// TestE2E_MockProvider_2014PDF runs the full pipeline.Run against the real
// 2014 AP Calc BC PDF using a deterministic mock vision Provider that
// dispatches by prompt content. It exercises render → classify → extract →
// reconcile → curve → emit, then loads the emitted JSON back through the
// runtime content loader to confirm round-trip compatibility.
//
// Skips when the PDF or pdftoppm is missing.
func TestE2E_MockProvider_2014PDF(t *testing.T) {
	pdfPath, _ := filepath.Abs(filepath.Join("..", "..", "..", "data", "AP Calc BC", "AP Calc BC 2014.pdf"))
	if _, err := os.Stat(pdfPath); err != nil {
		t.Skipf("PDF not present (%s); this test exercises the real pipeline only when the PDF is on disk", pdfPath)
	}

	workdir := t.TempDir()
	outRoot := workdir
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	mp := newMockProvider()

	in := pipeline.Input{
		Profile:      profile.AP(),
		Slug:         "ap-calc-bc-2014-mock",
		Title:        "AP Calc BC 2014 (mock e2e)",
		PDFTest:      pdfPath,
		OutRoot:      outRoot,
		Workdir:      workdir,
		Provider:     mp,
		ConsistencyN: 3,
		DPIClassify:  100,
		DPIExtract:   100,
	}
	res, err := pipeline.Run(ctx, logger, in)
	if err != nil {
		t.Fatalf("pipeline.Run: %v", err)
	}
	t.Logf("Run: written=%d flagged=%d elapsed=%s", res.Written, res.Flagged, res.Elapsed)
	if res.Written == 0 {
		t.Fatalf("pipeline emitted zero questions")
	}

	// Load through the runtime loader.
	s, err := store.Open(ctx, filepath.Join(workdir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := content.LoadFromDisk(ctx, s, workdir); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	got, err := content.Get(ctx, s, in.Slug)
	if err != nil {
		t.Fatalf("Get %s: %v", in.Slug, err)
	}
	if got.ExamType != "ap" || got.Subject != "calc_bc" {
		t.Errorf("metadata: %+v", got)
	}
	if len(got.Modules) != 2 {
		t.Fatalf("want 2 modules, got %d", len(got.Modules))
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
	mu            sync.Mutex
	classify      map[string]pipeline.PageClass
	extract       map[string]string
	classifyOrder int
}

func newMockProvider() *mockProvider {
	return &mockProvider{
		classify: map[string]pipeline.PageClass{},
		extract:  map[string]string{},
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

func (m *mockProvider) classifyFor(image []byte) pipeline.PageClass {
	key := hash(image)
	if c, ok := m.classify[key]; ok {
		return c
	}
	idx := m.classifyOrder
	m.classifyOrder++
	c := classifyByIndex(idx)
	m.classify[key] = c
	return c
}

// classifyByIndex returns a synthetic class based on a page's position in the
// document. Tuned so the 2014 PDF (~80 pages) yields a realistic mix.
func classifyByIndex(idx int) pipeline.PageClass {
	switch {
	case idx < 20:
		return pipeline.ClassInstructions
	case idx < 45:
		return pipeline.PageClass("mcq_no_calc")
	case idx < 60:
		return pipeline.PageClass("mcq_calc")
	case idx < 65:
		return pipeline.ClassAnswerKey
	case idx < 70:
		return pipeline.ClassScoringCurve
	default:
		return pipeline.ClassSkip
	}
}

func (m *mockProvider) extractFor(image []byte) string {
	key := hash(image)
	if s, ok := m.extract[key]; ok {
		return s
	}
	qNum := len(m.extract) + 1
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
	m.extract[key] = js
	return js
}

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
