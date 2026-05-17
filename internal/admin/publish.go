package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/store"
)

// ErrPublishNotConfigured is returned when an admin tries to publish but
// the server was booted without a -published root.
var ErrPublishNotConfigured = errors.New("admin: publish not configured; restart with -published <dir>")

// Publisher emits the approved-only subset of a reviewed test into a
// sibling content tree that the runtime can boot from directly. Pending
// and flagged questions are dropped; question IDs are preserved so a
// future re-import keeps the existing question_review keys aligned.
type Publisher struct {
	IO            *JSONIO
	Store         *store.Store
	PublishedRoot string
}

// ModuleStat is one line of the publish summary returned to the admin UI.
type ModuleStat struct {
	Section   string `json:"section"`
	Title     string `json:"title"`
	Questions int    `json:"questions"`
	Dropped   int    `json:"dropped"`
}

// PublishResponse is the result of POST /api/admin/tests/{slug}/publish.
type PublishResponse struct {
	Path          string       `json:"path"`
	QuestionsIn   int          `json:"questions_in"`
	QuestionsOut  int          `json:"questions_out"`
	FiguresCopied int          `json:"figures_copied"`
	Modules       []ModuleStat `json:"modules"`
}

// Publish handles POST /api/admin/tests/{slug}/publish.
func (p *Publisher) Publish(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	if p.PublishedRoot == "" {
		writeError(w, http.StatusServiceUnavailable, ErrPublishNotConfigured.Error())
		return
	}

	srcPath, err := p.IO.LocateTest(slug)
	if errors.Is(err, ErrFlatLayout) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, ErrTestNotOnDisk) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	raw, err := os.ReadFile(srcPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var t content.Test
	if err := json.Unmarshal(raw, &t); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("parse %s: %w", srcPath, err).Error())
		return
	}
	if t.ExamType == "" {
		writeError(w, http.StatusUnprocessableEntity, "test.json missing exam_type; cannot route publish destination")
		return
	}

	approved, err := loadApprovedSet(r.Context(), p.Store, slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	stats, emptyModules, questionsIn, questionsOut := filterModules(&t, approved)
	if len(emptyModules) > 0 {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("modules with zero approved questions: %v", emptyModules))
		return
	}
	if questionsOut == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no approved questions; nothing to publish")
		return
	}

	destDir := filepath.Join(p.PublishedRoot, t.ExamType, slug)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := atomicWrite(filepath.Join(destDir, "test.json"), out); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	srcDir := filepath.Dir(srcPath)
	if err := copyFileIfExists(filepath.Join(srcDir, "curve.json"), filepath.Join(destDir, "curve.json")); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("copy curve.json: %w", err).Error())
		return
	}

	figureFiles := collectFigureFilenames(t)
	figuresCopied := 0
	if len(figureFiles) > 0 {
		destFigs := filepath.Join(destDir, "figures")
		if err := os.MkdirAll(destFigs, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		srcFigs := filepath.Join(srcDir, "figures")
		for _, name := range figureFiles {
			n, err := copyFigureFile(filepath.Join(srcFigs, name), filepath.Join(destFigs, name))
			if err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Errorf("copy figure %s: %w", name, err).Error())
				return
			}
			figuresCopied += n
		}
	}

	absDest, err := filepath.Abs(destDir)
	if err != nil {
		absDest = destDir
	}
	writeJSON(w, http.StatusOK, PublishResponse{
		Path:          absDest,
		QuestionsIn:   questionsIn,
		QuestionsOut:  questionsOut,
		FiguresCopied: figuresCopied,
		Modules:       stats,
	})
}

func filterModules(t *content.Test, approved map[string]bool) (stats []ModuleStat, empty []string, in, out int) {
	stats = make([]ModuleStat, 0, len(t.Modules))
	for i := range t.Modules {
		m := &t.Modules[i]
		before := len(m.Questions)
		in += before
		kept := m.Questions[:0]
		for _, q := range m.Questions {
			if approved[q.ID] {
				kept = append(kept, q)
			}
		}
		m.Questions = kept
		out += len(kept)
		stats = append(stats, ModuleStat{
			Section:   m.Section,
			Title:     m.Title,
			Questions: len(kept),
			Dropped:   before - len(kept),
		})
		if len(kept) == 0 {
			empty = append(empty, m.Section)
		}
	}
	return stats, empty, in, out
}

func loadApprovedSet(ctx context.Context, s *store.Store, slug string) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT question_id FROM question_review WHERE test_slug = ? AND status = 'approved'`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// collectFigureFilenames returns the deduplicated, sorted list of figure
// basenames referenced by surviving questions. filepath.Base strips the
// "figures/" prefix the JSON carries, since the destination already has
// its own figures/ directory.
func collectFigureFilenames(t content.Test) []string {
	seen := map[string]bool{}
	add := func(f *content.Figure) {
		if f == nil || f.Src == "" {
			return
		}
		seen[filepath.Base(f.Src)] = true
	}
	for _, m := range t.Modules {
		for _, q := range m.Questions {
			add(q.PassageFigure)
			add(q.StemFigure)
			for _, c := range q.Choices {
				add(c.Figure)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// copyFileIfExists is a curve.json-shaped helper: missing source is not
// an error (legacy AP tests have no curve), but any other read failure is.
func copyFileIfExists(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer in.Close()
	return writeViaTemp(dst, in)
}

// copyFigureFile returns (1, nil) on success or (0, err) on failure.
// Unlike curve.json a missing figure is an error — surviving JSON
// references would 404 in the published instance.
func copyFigureFile(src, dst string) (int, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	if err := writeViaTemp(dst, in); err != nil {
		return 0, err
	}
	return 1, nil
}

func writeViaTemp(dst string, r io.Reader) error {
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
