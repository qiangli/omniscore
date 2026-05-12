package importer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qiangli/omniscore/internal/content"
)

// EmitInput bundles everything needed to assemble a published Test JSON +
// curve + figures + review log. modulePages tells us which extracted
// questions belong to which module (mcq_no_calc vs mcq_calc).
type EmitInput struct {
	Slug    string
	Title   string
	OutRoot string // e.g. data/omni-data/ap
	Workdir string // e.g. .import-cache/<slug>; review log lives here

	// Per-module: section code → time limit (seconds) → ordered ExtractedQuestions.
	Modules []EmitModule

	Curve content.Curve
}

// EmitModule is one timed section's worth of questions plus its presentation
// metadata. Caller groups questions by classified PageClass before calling.
type EmitModule struct {
	ID         string
	Section    string // e.g. "mcq_no_calc"
	Title      string // e.g. "Section I, Part A — No calculator"
	TimeLimitS int
	Questions  []ExtractedQuestion
}

// Emit writes the test JSON, copies figure PNGs, writes the curve JSON, and
// emits a review-log markdown summarizing every flagged question.
//
// Output layout:
//
//	<OutRoot>/tests/<slug>.json
//	<OutRoot>/curves/<slug>.json
//	<OutRoot>/figures/<slug>/qNN-stem.png   (whole-page fallback)
//	<Workdir>/.review/<slug>.md
//
// Returns the count of questions written and the count flagged for review.
func Emit(in EmitInput) (written, flagged int, err error) {
	for _, dir := range []string{
		filepath.Join(in.OutRoot, "tests"),
		filepath.Join(in.OutRoot, "curves"),
		filepath.Join(in.OutRoot, "figures", in.Slug),
		filepath.Join(in.Workdir, ".review"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, 0, fmt.Errorf("emit: mkdir %s: %w", dir, err)
		}
	}

	t := content.Test{
		Slug:     in.Slug,
		Title:    in.Title,
		ExamType: "ap",
		Subject:  "calc_bc",
	}

	type flaggedEntry struct {
		Section        string
		QuestionNumber int
		Issues         []string
		ReviewNotes    []string
	}
	var review []flaggedEntry

	for _, m := range in.Modules {
		mod := content.Module{
			ID:         m.ID,
			Section:    m.Section,
			Title:      m.Title,
			TimeLimitS: m.TimeLimitS,
		}
		for _, q := range m.Questions {
			cq := content.Question{
				ID:          fmt.Sprintf("%s-q%d", m.Section, q.QuestionNumber),
				StemMD:      q.StemMD,
				AnswerLabel: q.AnswerLabel,
			}
			if q.HasStemFigure {
				figName := fmt.Sprintf("q%d-stem.png", q.QuestionNumber)
				if err := copyFigure(q.PageSource,
					filepath.Join(in.OutRoot, "figures", in.Slug, figName)); err != nil {
					return written, flagged, fmt.Errorf("emit: copy figure for q%d: %w", q.QuestionNumber, err)
				}
				cq.StemFigure = &content.Figure{
					Src: "figures/" + figName,
					Alt: fmt.Sprintf("Figure for question %d", q.QuestionNumber),
				}
			}
			for _, c := range q.Choices {
				cc := content.Choice{Label: c.Label, TextMD: c.TextMD}
				if c.HasFigure {
					// Choice-level figures: stash a per-choice page-source PNG. The
					// importer emits one figure per (question, choice-letter) so the
					// human reviewer can later re-crop with bbox precision.
					figName := fmt.Sprintf("q%d-%s.png", q.QuestionNumber, strings.ToLower(c.Label))
					if err := copyFigure(q.PageSource,
						filepath.Join(in.OutRoot, "figures", in.Slug, figName)); err != nil {
						return written, flagged, fmt.Errorf("emit: copy figure for q%d %s: %w",
							q.QuestionNumber, c.Label, err)
					}
					cc.Figure = &content.Figure{
						Src: "figures/" + figName,
						Alt: fmt.Sprintf("Figure for question %d choice %s", q.QuestionNumber, c.Label),
					}
				}
				cq.Choices = append(cq.Choices, cc)
			}
			mod.Questions = append(mod.Questions, cq)
			written++

			issues := ValidateQuestion(q)
			if len(issues) > 0 || q.NeedsReview {
				flagged++
				review = append(review, flaggedEntry{
					Section:        m.Section,
					QuestionNumber: q.QuestionNumber,
					Issues:         issues,
					ReviewNotes:    q.ReviewNotes,
				})
			}
		}
		t.Modules = append(t.Modules, mod)
	}

	if err := writeIndentedJSON(filepath.Join(in.OutRoot, "tests", in.Slug+".json"), t); err != nil {
		return written, flagged, err
	}
	if err := writeIndentedJSON(filepath.Join(in.OutRoot, "curves", in.Slug+".json"), in.Curve); err != nil {
		return written, flagged, err
	}

	// Stable order for the review log.
	sort.Slice(review, func(i, j int) bool {
		if review[i].Section != review[j].Section {
			return review[i].Section < review[j].Section
		}
		return review[i].QuestionNumber < review[j].QuestionNumber
	})

	rfile, err := os.Create(filepath.Join(in.Workdir, ".review", in.Slug+".md"))
	if err != nil {
		return written, flagged, err
	}
	defer rfile.Close()
	fmt.Fprintf(rfile, "# %s — extraction review (%s)\n\n", in.Slug, time.Now().Format(time.RFC3339))
	fmt.Fprintf(rfile, "Written: %d questions  ·  Flagged for review: %d\n\n", written, flagged)
	if len(review) == 0 {
		fmt.Fprintln(rfile, "No questions flagged. Spot-check a random sample anyway.")
	}
	currentSection := ""
	for _, r := range review {
		if r.Section != currentSection {
			fmt.Fprintf(rfile, "\n## %s\n\n", r.Section)
			currentSection = r.Section
		}
		fmt.Fprintf(rfile, "- **q%d**\n", r.QuestionNumber)
		for _, iss := range r.Issues {
			fmt.Fprintf(rfile, "  - issue: %s\n", iss)
		}
		for _, note := range r.ReviewNotes {
			fmt.Fprintf(rfile, "  - note: %s\n", note)
		}
	}

	return written, flagged, nil
}

func copyFigure(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func writeIndentedJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
