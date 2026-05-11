// Package frontend exposes the Vite production build (frontend/dist) as an
// io/fs.FS so the Go binary can serve the React app from memory. The dist
// directory is populated by `npm run build` before `go build`; this package
// has a single empty .gitkeep so `go build` succeeds without the JS step
// (the binary will then serve a "frontend not built" stub).
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// DistFS returns the rooted embedded dist filesystem.
func DistFS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return dist
	}
	return sub
}
