package server

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// serveFigure resolves /api/figures/<exam>/<slug>/<rest> to a file under
// FiguresRoot/<exam>/figures/<slug>/<rest>. Path-traversal protected: the
// cleaned absolute path must remain inside the per-slug figures directory.
func (s *Server) serveFigure(w http.ResponseWriter, r *http.Request) {
	exam := chi.URLParam(r, "exam")
	slug := chi.URLParam(r, "slug")
	rest := chi.URLParam(r, "*")
	if exam == "" || slug == "" || rest == "" {
		writeError(w, http.StatusBadRequest, "missing path segment")
		return
	}
	base := filepath.Join(s.FiguresRoot, exam, "figures", slug)
	full := filepath.Clean(filepath.Join(base, rest))
	cleanedBase := filepath.Clean(base) + string(filepath.Separator)
	if !strings.HasPrefix(full, cleanedBase) {
		writeError(w, http.StatusBadRequest, "bad path")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	http.ServeFile(w, r, full)
}
