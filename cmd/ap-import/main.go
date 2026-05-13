// Command ap-import converts a College Board AP released-exam PDF into
// OmniScore JSON. It is a separate binary from `omniscore` so that Poppler /
// vision-LLM dependencies don't leak into the runtime artifact.
//
// Typical use:
//
//	ap-import \
//	  -pdf "data/AP Calc BC/AP Calc BC 2014.pdf" \
//	  -slug ap-calc-bc-2014 \
//	  -title "AP Calculus BC — 2014 Released Exam" \
//	  -model anthropic/claude-sonnet-4-6 \
//	  -out data/omni-data/ap
//
// Internally it delegates to internal/importer/pipeline.Run with profile.AP();
// every exam-specific knob (choice count, sections, time limits, curve scale,
// prompts) lives in internal/importer/profile/ap.go.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/importer"
	"github.com/qiangli/omniscore/internal/importer/pipeline"
	"github.com/qiangli/omniscore/internal/importer/profile"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

func main() {
	var (
		pdfPath     = flag.String("pdf", "", "input PDF path (College Board AP released exam)")
		slug        = flag.String("slug", "", "output slug, e.g. ap-calc-bc-2014")
		title       = flag.String("title", "", "test title shown to students")
		modelSpec   = flag.String("model", "", "vision model spec as vendor/model, e.g. anthropic/claude-sonnet-4-6. Falls back to $OMNI_MODEL.")
		ollamaHost  = flag.String("host", "http://localhost:11434", "Ollama server URL (only used when -model starts with ollama/)")
		outRoot     = flag.String("out", "data/omni-data/ap", "output root: writes <out>/tests, <out>/curves, <out>/figures/<slug>")
		workdir     = flag.String("workdir", "", "scratch dir for page rasters + LLM cache + review log (default: .import-cache/<slug>)")
		consistency = flag.Int("consistency", 3, "self-consistency rounds per MCQ page (>=1)")
		dpiClassify = flag.Int("dpi-classify", 200, "DPI for the page-classifier rasterization pass")
		dpiExtract  = flag.Int("dpi-extract", 300, "DPI for the question-extraction rasterization pass")
		validateIn  = flag.String("validate-only", "", "if set, parse this test JSON and run validation; no LLM calls")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	prof := profile.AP()

	if *validateIn != "" {
		if err := validateOnly(*validateIn, prof, logger); err != nil {
			logger.Error("validate", "err", err)
			os.Exit(1)
		}
		return
	}

	if *pdfPath == "" || *slug == "" || *title == "" {
		fmt.Fprintln(os.Stderr, "usage: ap-import -pdf <path> -slug <slug> -title <title> -model <vendor/model> [flags]")
		flag.PrintDefaults()
		os.Exit(2)
	}
	spec := strings.TrimSpace(*modelSpec)
	if spec == "" {
		spec = os.Getenv("OMNI_MODEL")
	}
	if spec == "" {
		fmt.Fprintln(os.Stderr, "ap-import: -model not set and $OMNI_MODEL is empty (e.g. -model anthropic/claude-sonnet-4-6)")
		os.Exit(2)
	}
	if *workdir == "" {
		*workdir = filepath.Join(".import-cache", *slug)
	}

	prov, err := buildProvider(spec, *ollamaHost, *workdir)
	if err != nil {
		logger.Error("provider", "err", err)
		os.Exit(1)
	}
	logger.Info("provider ready", "name", prov.Name())

	in := pipeline.Input{
		Profile:      prof,
		Slug:         *slug,
		Title:        *title,
		PDFTest:      *pdfPath,
		OutRoot:      *outRoot,
		Workdir:      *workdir,
		Provider:     prov,
		ConsistencyN: *consistency,
		DPIClassify:  *dpiClassify,
		DPIExtract:   *dpiExtract,
	}
	if _, err := pipeline.Run(ctx, logger, in); err != nil {
		logger.Error("import", "err", err)
		os.Exit(1)
	}
}

func buildProvider(spec, ollamaHost, workdir string) (vision.Provider, error) {
	inner, err := importer.FromString(spec, ollamaHost)
	if err != nil {
		return nil, err
	}
	return vision.Cached(inner, filepath.Join(workdir, "llm-cache"))
}

// validateOnly reads an existing tests/<slug>.json and runs ValidateQuestion
// against every MCQ in it. Useful after a human edits a flagged question to
// confirm the change still matches the schema.
func validateOnly(path string, prof profile.Profile, logger *slog.Logger) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var t content.Test
	if err := json.Unmarshal(raw, &t); err != nil {
		return err
	}
	totalQs, totalIssues := 0, 0
	for _, m := range t.Modules {
		for _, q := range m.Questions {
			eq := pipeline.ExtractedQuestion{
				StemMD:        q.StemMD,
				HasStemFigure: q.StemFigure != nil,
				AnswerLabel:   q.AnswerLabel,
			}
			for _, c := range q.Choices {
				eq.Choices = append(eq.Choices, pipeline.ExtractedChoice{
					Label: c.Label, TextMD: c.TextMD, HasFigure: c.Figure != nil,
				})
			}
			issues := pipeline.ValidateQuestion(prof, eq)
			totalQs++
			if len(issues) > 0 {
				totalIssues++
				logger.Warn("issue", "module", m.ID, "question", q.ID, "issues", issues)
			}
		}
	}
	logger.Info("validate-only done", "questions", totalQs, "with_issues", totalIssues, "file", path)
	if totalIssues > 0 {
		return fmt.Errorf("%d question(s) failed validation", totalIssues)
	}
	return nil
}
