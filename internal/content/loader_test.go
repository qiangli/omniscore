package content_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/store"
)

// openTestStore opens a fresh in-memory store backed by a tempdir SQLite file.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFlatLegacy(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	root := t.TempDir()

	writeJSON(t, filepath.Join(root, "tests", "sat-flat.json"), content.Test{
		Slug: "sat-flat", Title: "Flat SAT", ExamType: "sat",
		Modules: []content.Module{{ID: "rw", Section: "rw", Title: "RW", TimeLimitS: 600,
			Questions: []content.Question{{ID: "q1", StemMD: "Q?", Choices: []content.Choice{{Label: "A"}, {Label: "B"}}, AnswerLabel: "A"}}}},
	})
	writeJSON(t, filepath.Join(root, "curves", "sat-flat.json"), content.Curve{
		TestSlug: "sat-flat",
		Sections: map[string][]content.CurvePoint{"rw": {{Raw: 0, Scaled: 200}, {Raw: 1, Scaled: 800}}},
	})

	if err := content.LoadFromDisk(ctx, s, root); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	got, err := content.Get(ctx, s, "sat-flat")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ExamType != "sat" || len(got.Modules) != 1 {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestLoadPerTestSubfolder(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	root := t.TempDir()

	writeJSON(t, filepath.Join(root, "sat", "sat-1", "test.json"), content.Test{
		Slug: "sat-1", Title: "SAT 1", ExamType: "sat",
		Modules: []content.Module{{ID: "rw", Section: "rw", Title: "RW", TimeLimitS: 600,
			Questions: []content.Question{{ID: "q1", StemMD: "Q?", Choices: []content.Choice{{Label: "A"}}, AnswerLabel: "A"}}}},
	})
	writeJSON(t, filepath.Join(root, "ap", "ap-1", "test.json"), content.Test{
		Slug: "ap-1", Title: "AP 1", ExamType: "ap", Subject: "calc_bc",
		Modules: []content.Module{{ID: "mcq", Section: "mcq_no_calc", Title: "MCQ", TimeLimitS: 600,
			Questions: []content.Question{{ID: "q1", StemMD: "Q?", Choices: []content.Choice{{Label: "A"}}, AnswerLabel: "A"}}}},
	})

	if err := content.LoadFromDisk(ctx, s, root); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	for _, slug := range []string{"sat-1", "ap-1"} {
		got, err := content.Get(ctx, s, slug)
		if err != nil {
			t.Fatalf("Get %s: %v", slug, err)
		}
		if got.Slug != slug {
			t.Errorf("slug: want %q got %q", slug, got.Slug)
		}
	}
	apTest, _ := content.Get(ctx, s, "ap-1")
	if apTest.Subject != "calc_bc" {
		t.Errorf("AP subject: want calc_bc, got %q", apTest.Subject)
	}
}

func TestLoadFigureSrcRewrite(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	root := t.TempDir()

	writeJSON(t, filepath.Join(root, "ap", "demo", "test.json"), content.Test{
		Slug: "demo", Title: "Demo", ExamType: "ap", Subject: "calc_bc",
		Modules: []content.Module{{
			ID: "mcq", Section: "mcq_calc", Title: "MCQ", TimeLimitS: 600,
			Questions: []content.Question{
				{
					ID: "q1", StemMD: "see fig",
					StemFigure: &content.Figure{Src: "figures/q1-stem.png", Alt: "graph"},
					Choices: []content.Choice{
						{Label: "A", TextMD: "yes", Figure: &content.Figure{Src: "figures/q1-A.png"}},
						{Label: "B", TextMD: "no"},
					},
					AnswerLabel: "A",
				},
			},
		}},
	})

	if err := content.LoadFromDisk(ctx, s, root); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	got, err := content.Get(ctx, s, "demo")
	if err != nil {
		t.Fatal(err)
	}
	q := got.Modules[0].Questions[0]
	if q.StemFigure == nil || q.StemFigure.Src != "/api/figures/ap/demo/q1-stem.png" {
		t.Errorf("stem figure src: got %+v", q.StemFigure)
	}
	if q.Choices[0].Figure == nil || q.Choices[0].Figure.Src != "/api/figures/ap/demo/q1-A.png" {
		t.Errorf("choice figure src: got %+v", q.Choices[0].Figure)
	}
	// Idempotence: re-loading should not double-rewrite (still absolute, not nested).
	if err := content.LoadFromDisk(ctx, s, root); err != nil {
		t.Fatal(err)
	}
	got2, _ := content.Get(ctx, s, "demo")
	if got2.Modules[0].Questions[0].StemFigure.Src != "/api/figures/ap/demo/q1-stem.png" {
		t.Errorf("rewrite not idempotent: %+v", got2.Modules[0].Questions[0].StemFigure)
	}
}

func TestLoadStrayDirIgnored(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	root := t.TempDir()

	// One legitimate AP test...
	writeJSON(t, filepath.Join(root, "ap", "ok", "test.json"), content.Test{
		Slug: "ok", Title: "OK", ExamType: "ap",
		Modules: []content.Module{{ID: "m", Section: "s", Title: "T", TimeLimitS: 60,
			Questions: []content.Question{{ID: "q1", StemMD: "?", Choices: []content.Choice{{Label: "A"}}, AnswerLabel: "A"}}}},
	})
	// ...plus a stray .git dir and an empty subdir without test.json.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ap", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := content.LoadFromDisk(ctx, s, root); err != nil {
		t.Fatalf("LoadFromDisk should ignore stray dirs, got: %v", err)
	}
	if _, err := content.Get(ctx, s, "ok"); err != nil {
		t.Fatalf("legitimate test not loaded: %v", err)
	}
}

func TestLoadMixedFlatAndPerTestSubfolder(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	root := t.TempDir()

	// Flat (legacy) layout.
	writeJSON(t, filepath.Join(root, "tests", "flat.json"), content.Test{
		Slug: "flat", Title: "Flat", ExamType: "sat",
		Modules: []content.Module{{ID: "rw", Section: "rw", Title: "RW", TimeLimitS: 60,
			Questions: []content.Question{{ID: "q1", StemMD: "?", Choices: []content.Choice{{Label: "A"}}, AnswerLabel: "A"}}}},
	})
	// Per-test subfolder alongside.
	writeJSON(t, filepath.Join(root, "ap", "ap", "test.json"), content.Test{
		Slug: "ap", Title: "AP", ExamType: "ap",
		Modules: []content.Module{{ID: "mcq", Section: "mcq", Title: "MCQ", TimeLimitS: 60,
			Questions: []content.Question{{ID: "q1", StemMD: "?", Choices: []content.Choice{{Label: "A"}}, AnswerLabel: "A"}}}},
	})

	if err := content.LoadFromDisk(ctx, s, root); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"flat", "ap"} {
		if _, err := content.Get(ctx, s, slug); err != nil {
			t.Errorf("Get %s: %v", slug, err)
		}
	}
}
