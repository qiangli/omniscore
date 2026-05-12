// Package importer drives the AP PDF → OmniScore JSON pipeline.
//
// Stages in order: Rasterize (render package) → Classify (this file) →
// Extract MCQs → Self-consistency vote → Validate → Reconcile against answer
// key → Emit JSON. The cmd/ap-import binary wires them together.
package importer

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/qiangli/omniscore/internal/importer/vision"
)

// PageClass tags each rasterized PDF page so later stages know what to do
// with it. Values match the section codes used in OmniScore content JSON.
type PageClass string

const (
	ClassInstructions PageClass = "instructions"
	ClassMCQNoCalc    PageClass = "mcq_no_calc"
	ClassMCQCalc      PageClass = "mcq_calc"
	ClassFRQNoCalc    PageClass = "frq_no_calc"
	ClassFRQCalc      PageClass = "frq_calc"
	ClassAnswerKey    PageClass = "answer_key"
	ClassScoringCurve PageClass = "scoring_curve"
	ClassSkip         PageClass = "skip"
)

var allClasses = []PageClass{
	ClassInstructions, ClassMCQNoCalc, ClassMCQCalc, ClassFRQNoCalc,
	ClassFRQCalc, ClassAnswerKey, ClassScoringCurve, ClassSkip,
}

// classifyPrompt is the strict-output prompt for the page classifier.
const classifyPrompt = `You are looking at a single page from a College Board AP Calculus BC released practice exam PDF.

Reply with EXACTLY ONE token from this list, lowercase, no punctuation, no explanation:

  instructions    — exam instructions, cover, copyright, table of contents
  mcq_no_calc     — Section I, Part A multiple-choice questions (calculator NOT permitted)
  mcq_calc        — Section I, Part B multiple-choice questions (calculator permitted)
  frq_no_calc     — Section II, Part B free-response questions (calculator NOT permitted)
  frq_calc        — Section II, Part A free-response questions (calculator permitted)
  answer_key      — multiple-choice answer key page
  scoring_curve   — scoring worksheet / raw-to-scaled conversion table
  skip            — anything else (blank pages, bubble answer sheet, demographic form)

Reply with only one token. No prose.`

// ClassifyPages runs the page classifier on every page image and returns a
// map[pagePath]PageClass. Pages whose response is unrecognized are tagged
// ClassSkip; that's safer than failing the whole import.
func ClassifyPages(ctx context.Context, p vision.Provider, pagePaths []string) (map[string]PageClass, error) {
	out := make(map[string]PageClass, len(pagePaths))
	for _, page := range pagePaths {
		img, err := os.ReadFile(page)
		if err != nil {
			return nil, fmt.Errorf("classify: read %s: %w", page, err)
		}
		raw, err := p.Generate(ctx, vision.Request{
			Image:       img,
			Prompt:      classifyPrompt,
			Temperature: 0.0,
		})
		if err != nil {
			return nil, fmt.Errorf("classify: %s: %w", page, err)
		}
		out[page] = parseClass(raw)
	}
	return out, nil
}

// parseClass extracts a PageClass token from a free-form LLM response. It
// looks for the first known token; if nothing matches, returns ClassSkip
// (the safe default — an unclassified page just won't be processed).
func parseClass(raw string) PageClass {
	s := strings.ToLower(strings.TrimSpace(raw))
	// Prefer exact match first.
	for _, c := range allClasses {
		if s == string(c) {
			return c
		}
	}
	// Fall back to substring match (handles models that wrap the answer in prose).
	for _, c := range allClasses {
		if strings.Contains(s, string(c)) {
			return c
		}
	}
	return ClassSkip
}
