package server

import (
	"io/fs"
	"net/http"
	"strings"
)

// spaHandler serves a Vite-built SPA from an embedded FS. Any non-asset path
// falls back to index.html so client-side routes work on hard refresh.
func spaHandler(distFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(distFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try the file directly first; if it exists, serve it.
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if f, err := distFS.Open(clean); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for unknown routes (but not API).
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		idx, err := distFS.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_ = idx.Close()
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
