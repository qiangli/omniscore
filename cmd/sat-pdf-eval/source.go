package main

import (
	"context"
	"sort"
)

// Page is a single PDF page rendered as reading-order columns of text.
// Columns are ordered left-to-right. Each column is plain text with
// newlines between visual lines.
type Page struct {
	Index int
	Cols  []string
}

// TextSource is the abstraction over a PDF text-extraction backend.
// Adding a new backend = one new file with one init() Register() call.
// No parser changes required.
type TextSource interface {
	Name() string
	Pages(ctx context.Context, path string) ([]Page, error)
}

// Registration is how a source plugs itself in.
type Registration struct {
	Name string
	Kind string // "purego" | "cgo" | "shell" — used to label the 3-way comparison
	New  func() TextSource
}

var registry []Registration

// Register installs a source. Call from init() in the source's own file.
func Register(r Registration) { registry = append(registry, r) }

// Sources returns all registered sources sorted by Name (stable enumeration).
func Sources() []Registration {
	out := make([]Registration, len(registry))
	copy(out, registry)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup finds a source by name. Empty string returns nil.
func Lookup(name string) *Registration {
	for i := range registry {
		if registry[i].Name == name {
			return &registry[i]
		}
	}
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
