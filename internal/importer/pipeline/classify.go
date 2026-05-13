// Package pipeline runs the shared PDF → omniscore content JSON pipeline.
// Per-exam differences live in profile.Profile; everything in this package is
// exam-agnostic and operates against a profile passed in by the caller.
//
// Stages in order: Rasterize (internal/importer/render) → Classify pages →
// Extract MCQs (with self-consistency) → Reconcile answers → Extract curve →
// Validate → Emit. The orchestrator is Run in pipeline.go.
package pipeline

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/qiangli/omniscore/internal/importer/profile"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

// PageClass tags one rasterized PDF page with a classifier label drawn from
// Profile.PageClasses(). Stored as a string so it can be compared directly
// against ModuleSpec.ClassifierLabel.
type PageClass string

const (
	ClassInstructions PageClass = "instructions"
	ClassAnswerKey    PageClass = "answer_key"
	ClassScoringCurve PageClass = "scoring_curve"
	ClassSkip         PageClass = "skip"
)

// ClassifyPages runs the profile's classify prompt against every page image
// and returns a map[pagePath]PageClass. Pages whose response doesn't match
// any valid label for this profile are tagged ClassSkip — that's safer than
// failing the whole import.
func ClassifyPages(ctx context.Context, p vision.Provider, prof profile.Profile, pagePaths []string) (map[string]PageClass, error) {
	prompt := prof.Prompts.Classify(prof)
	valid := prof.PageClasses()

	out := make(map[string]PageClass, len(pagePaths))
	for _, page := range pagePaths {
		img, err := os.ReadFile(page)
		if err != nil {
			return nil, fmt.Errorf("classify: read %s: %w", page, err)
		}
		raw, err := p.Generate(ctx, vision.Request{
			Image:       img,
			Prompt:      prompt,
			Temperature: 0.0,
		})
		if err != nil {
			return nil, fmt.Errorf("classify: %s: %w", page, err)
		}
		out[page] = parseClass(raw, valid)
	}
	return out, nil
}

// parseClass extracts a PageClass token from a free-form LLM response,
// matching against the per-profile valid label set. Falls back to
// ClassSkip when nothing matches.
func parseClass(raw string, valid []string) PageClass {
	s := strings.ToLower(strings.TrimSpace(raw))
	// Exact match preferred.
	for _, c := range valid {
		if s == c {
			return PageClass(c)
		}
	}
	// Fall back to substring (handles models that wrap the answer in prose).
	for _, c := range valid {
		if strings.Contains(s, c) {
			return PageClass(c)
		}
	}
	return ClassSkip
}
