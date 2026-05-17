package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/store"
)

// writeJSONFile is a small helper that mkdir-p's the parent and writes v
// as indented JSON. Used to lay out test fixtures on disk.
func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeBytes(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupPublishFixture writes a per-test subfolder layout with two
// questions per module, plus a curve.json and two figures, loads it
// into SQLite, and returns roots + publisher.
func setupPublishFixture(t *testing.T) (contentRoot, publishedRoot string, s *store.Store, pub *Publisher) {
	t.Helper()
	ctx := context.Background()
	s = openTestStore(t)
	contentRoot = t.TempDir()
	publishedRoot = t.TempDir()

	test := content.Test{
		Slug: "sat-1", Title: "SAT 1", ExamType: "sat",
		Modules: []content.Module{
			{
				ID: "rw", Section: "rw", Title: "Reading & Writing", TimeLimitS: 600,
				Questions: []content.Question{
					{ID: "q1", StemMD: "stem 1", Choices: []content.Choice{{Label: "A", TextMD: "a"}, {Label: "B", TextMD: "b"}}, AnswerLabel: "A",
						StemFigure: &content.Figure{Src: "figures/q1.png"}},
					{ID: "q2", StemMD: "stem 2", Choices: []content.Choice{{Label: "A", TextMD: "a"}, {Label: "B", TextMD: "b"}}, AnswerLabel: "B",
						StemFigure: &content.Figure{Src: "figures/q2.png"}},
				},
			},
			{
				ID: "math", Section: "math", Title: "Math", TimeLimitS: 600,
				Questions: []content.Question{
					{ID: "q3", StemMD: "stem 3", Choices: []content.Choice{{Label: "A", TextMD: "a"}, {Label: "B", TextMD: "b"}}, AnswerLabel: "A"},
					{ID: "q4", StemMD: "stem 4", Choices: []content.Choice{{Label: "A", TextMD: "a"}, {Label: "B", TextMD: "b"}}, AnswerLabel: "B"},
				},
			},
		},
	}
	writeJSONFile(t, filepath.Join(contentRoot, "sat", "sat-1", "test.json"), test)
	writeJSONFile(t, filepath.Join(contentRoot, "sat", "sat-1", "curve.json"), content.Curve{
		TestSlug: "sat-1",
		Sections: map[string][]content.CurvePoint{
			"rw":   {{Raw: 0, Scaled: 200}, {Raw: 2, Scaled: 800}},
			"math": {{Raw: 0, Scaled: 200}, {Raw: 2, Scaled: 800}},
		},
	})
	writeBytes(t, filepath.Join(contentRoot, "sat", "sat-1", "figures", "q1.png"), []byte("PNGq1"))
	writeBytes(t, filepath.Join(contentRoot, "sat", "sat-1", "figures", "q2.png"), []byte("PNGq2"))

	if err := content.LoadFromDisk(ctx, s, contentRoot); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	pub = &Publisher{IO: NewJSONIO(contentRoot, s), Store: s, PublishedRoot: publishedRoot}
	return
}

func callPublish(t *testing.T, pub *Publisher, slug string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/admin/tests/"+slug+"/publish", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", slug)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	pub.Publish(w, req)
	return w
}

func setReview(t *testing.T, s *store.Store, slug, qid, status string) {
	t.Helper()
	if err := upsertReview(context.Background(), s, slug, qid, status, ""); err != nil {
		t.Fatal(err)
	}
}

func TestPublishApprovedOnly(t *testing.T) {
	contentRoot, publishedRoot, s, pub := setupPublishFixture(t)
	_ = contentRoot

	// Approve q1, q3; leave q2 pending; flag q4.
	setReview(t, s, "sat-1", "q1", StatusApproved)
	setReview(t, s, "sat-1", "q3", StatusApproved)
	setReview(t, s, "sat-1", "q4", StatusFlagged)

	w := callPublish(t, pub, "sat-1")
	if w.Code != http.StatusOK {
		t.Fatalf("publish: want 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp PublishResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.QuestionsIn != 4 || resp.QuestionsOut != 2 {
		t.Fatalf("counts: want in=4 out=2, got in=%d out=%d", resp.QuestionsIn, resp.QuestionsOut)
	}
	if resp.FiguresCopied != 1 {
		t.Fatalf("figures: want 1 (only q1.png is referenced by an approved question), got %d", resp.FiguresCopied)
	}

	// Verify the disk artifact.
	destTest := filepath.Join(publishedRoot, "sat", "sat-1", "test.json")
	raw, err := os.ReadFile(destTest)
	if err != nil {
		t.Fatalf("read published test.json: %v", err)
	}
	var got content.Test
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Modules) != 2 {
		t.Fatalf("modules: want 2, got %d", len(got.Modules))
	}
	if len(got.Modules[0].Questions) != 1 || got.Modules[0].Questions[0].ID != "q1" {
		t.Fatalf("rw module: want [q1], got %+v", got.Modules[0].Questions)
	}
	if len(got.Modules[1].Questions) != 1 || got.Modules[1].Questions[0].ID != "q3" {
		t.Fatalf("math module: want [q3], got %+v", got.Modules[1].Questions)
	}

	// Figure src must round-trip as relative (so the published instance's
	// loader rewrites it with its own root).
	if got.Modules[0].Questions[0].StemFigure.Src != "figures/q1.png" {
		t.Fatalf("figure src should be relative, got %q", got.Modules[0].Questions[0].StemFigure.Src)
	}

	// Only the referenced figure was copied.
	if _, err := os.Stat(filepath.Join(publishedRoot, "sat", "sat-1", "figures", "q1.png")); err != nil {
		t.Fatalf("q1.png missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(publishedRoot, "sat", "sat-1", "figures", "q2.png")); !os.IsNotExist(err) {
		t.Fatalf("q2.png should not have been copied (q2 was not approved); err=%v", err)
	}

	// curve.json copied verbatim.
	if _, err := os.Stat(filepath.Join(publishedRoot, "sat", "sat-1", "curve.json")); err != nil {
		t.Fatalf("curve.json missing: %v", err)
	}
}

func TestPublishRejectsEmptyModule(t *testing.T) {
	_, _, s, pub := setupPublishFixture(t)
	// Approve only questions in the rw module; math will be empty.
	setReview(t, s, "sat-1", "q1", StatusApproved)
	setReview(t, s, "sat-1", "q2", StatusApproved)

	w := callPublish(t, pub, "sat-1")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for empty math module, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPublishRejectsNoApprovedQuestions(t *testing.T) {
	_, _, _, pub := setupPublishFixture(t)
	// No reviews set → nothing approved.
	w := callPublish(t, pub, "sat-1")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 when nothing is approved, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPublishMissingTest(t *testing.T) {
	_, _, _, pub := setupPublishFixture(t)
	w := callPublish(t, pub, "nope")
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 for missing test, got %d", w.Code)
	}
}

func TestPublishNotConfigured(t *testing.T) {
	_, _, s, pub := setupPublishFixture(t)
	pub.PublishedRoot = "" // simulate boot without -published

	setReview(t, s, "sat-1", "q1", StatusApproved)
	setReview(t, s, "sat-1", "q3", StatusApproved)

	w := callPublish(t, pub, "sat-1")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 without published root, got %d", w.Code)
	}
}

func TestPublishIsIdempotent(t *testing.T) {
	_, publishedRoot, s, pub := setupPublishFixture(t)
	setReview(t, s, "sat-1", "q1", StatusApproved)
	setReview(t, s, "sat-1", "q3", StatusApproved)

	for i := 0; i < 2; i++ {
		w := callPublish(t, pub, "sat-1")
		if w.Code != http.StatusOK {
			t.Fatalf("publish #%d: got %d body=%s", i+1, w.Code, w.Body.String())
		}
	}
	// Single test.json (atomic rename overwrites), no stray .tmp.
	entries, _ := os.ReadDir(filepath.Join(publishedRoot, "sat", "sat-1"))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("stray temp file left behind: %s", e.Name())
		}
	}
}
