// Command sat-import converts a Digital SAT (Bluebook) practice PDF set into
// OmniScore JSON. It is a separate binary from `omniscore` so that Poppler /
// vision-LLM dependencies don't leak into the runtime artifact.
//
// Typical use (input dir contains the Bluebook + Scoring PDFs):
//
//	sat-import \
//	  -in    data/raw/sat/digital-sat-practice-1 \
//	  -slug  digital-sat-practice-1 \
//	  -title "Digital SAT Practice #1" \
//	  -model anthropic/claude-sonnet-4-6
//
// Or pass each PDF explicitly:
//
//	sat-import \
//	  -pdf-test       data/raw/sat/.../Digital SAT Practice#1_Bluebook.pdf \
//	  -pdf-scoring    data/raw/sat/.../Digital SAT Practice#1_Bluebook_Scoring.pdf \
//	  -pdf-explanation data/raw/sat/.../Digital SAT Practice#1_Bluebook_explanation.pdf \
//	  -slug  digital-sat-practice-1 ...
//
// Output: data/omni-data/sat/<slug>/{test.json,curve.json,figures/*.png}
//
// Internally it delegates to internal/importer/pipeline.Run with profile.SAT().
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
		inDir       = flag.String("in", "", "input directory containing the test PDFs (slug-named subdir under data/raw/sat/)")
		pdfTest     = flag.String("pdf-test", "", "test PDF path (Bluebook). If empty, -in is scanned for *_Bluebook.pdf")
		pdfScoring  = flag.String("pdf-scoring", "", "optional scoring PDF (answer key + curve). If empty, -in is scanned for *_Bluebook_Scoring.pdf")
		pdfExpl     = flag.String("pdf-explanation", "", "optional explanation PDF (rationales). If empty, -in is scanned for *_Bluebook_explanation.pdf")
		slug        = flag.String("slug", "", "output slug; defaults to the basename of -in")
		title       = flag.String("title", "", "test title shown to students; defaults to a Title-Case version of -slug")
		modelSpec   = flag.String("model", "", "vision model spec as vendor/model, e.g. anthropic/claude-sonnet-4-6. Falls back to $OMNI_MODEL.")
		ollamaHost  = flag.String("host", "http://localhost:11434", "Ollama server URL (only used when -model starts with ollama/)")
		outRoot     = flag.String("out", "data/omni-data", "content root: writes <out>/sat/<slug>/{test.json,curve.json,figures/}")
		workdir     = flag.String("workdir", "", "scratch dir for page rasters + LLM cache + review log (default: .import-cache/<slug>)")
		consistency = flag.Int("consistency", 3, "self-consistency rounds per MCQ page (>=1)")
		dpiClassify = flag.Int("dpi-classify", 200, "DPI for the page-classifier rasterization pass")
		dpiExtract  = flag.Int("dpi-extract", 300, "DPI for the question-extraction rasterization pass")
		force       = flag.Bool("force", false, "bypass the on-disk import cache and re-run even if inputs are unchanged")
		validateIn  = flag.String("validate-only", "", "if set, parse this test JSON and run validation; no LLM calls")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	prof := profile.SAT()

	if *validateIn != "" {
		if err := validateOnly(*validateIn, prof, logger); err != nil {
			logger.Error("validate", "err", err)
			os.Exit(1)
		}
		return
	}

	if *inDir == "" && *pdfTest == "" {
		fmt.Fprintln(os.Stderr, "usage: sat-import -in <dir> [-slug <slug>] -model <vendor/model> [flags]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	resolved, err := resolvePDFs(*inDir, *pdfTest, *pdfScoring, *pdfExpl)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sat-import:", err)
		os.Exit(2)
	}
	if *slug == "" {
		if *inDir != "" {
			*slug = filepath.Base(filepath.Clean(*inDir))
		} else {
			*slug = strings.TrimSuffix(filepath.Base(resolved.test), filepath.Ext(resolved.test))
		}
	}
	if *title == "" {
		*title = titleCase(*slug)
	}

	spec := strings.TrimSpace(*modelSpec)
	if spec == "" {
		spec = os.Getenv("OMNI_MODEL")
	}
	if spec == "" {
		fmt.Fprintln(os.Stderr, "sat-import: -model not set and $OMNI_MODEL is empty (e.g. -model anthropic/claude-sonnet-4-6)")
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
	logger.Info("provider ready", "name", prov.Name(),
		"pdf_test", resolved.test, "pdf_scoring", resolved.scoring, "pdf_explanation", resolved.explanation)

	in := pipeline.Input{
		Profile:        prof,
		Slug:           *slug,
		Title:          *title,
		PDFTest:        resolved.test,
		PDFScoring:     resolved.scoring,
		PDFExplanation: resolved.explanation,
		OutRoot:        *outRoot,
		Workdir:        *workdir,
		Provider:       prov,
		ConsistencyN:   *consistency,
		DPIClassify:    *dpiClassify,
		DPIExtract:     *dpiExtract,
		Fingerprint:    spec,
		Force:          *force,
	}
	if _, err := pipeline.Run(ctx, logger, in); err != nil {
		logger.Error("import", "err", err)
		os.Exit(1)
	}
}

type resolvedPDFs struct {
	test, scoring, explanation string
}

// resolvePDFs picks the three PDF paths from explicit flags or, failing that,
// scans inDir for filename patterns matching the Bluebook naming convention.
// PDFTest is required; the other two are optional.
func resolvePDFs(inDir, test, scoring, explanation string) (resolvedPDFs, error) {
	out := resolvedPDFs{test: test, scoring: scoring, explanation: explanation}

	if inDir != "" {
		entries, err := os.ReadDir(inDir)
		if err != nil {
			return out, fmt.Errorf("read -in %s: %w", inDir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".pdf") {
				continue
			}
			name := e.Name()
			lower := strings.ToLower(name)
			full := filepath.Join(inDir, name)
			switch {
			case strings.Contains(lower, "_scoring"):
				if out.scoring == "" {
					out.scoring = full
				}
			case strings.Contains(lower, "_explanation"), strings.Contains(lower, "_explanantions"):
				if out.explanation == "" {
					out.explanation = full
				}
			default:
				if out.test == "" {
					out.test = full
				}
			}
		}
	}

	if out.test == "" {
		return out, fmt.Errorf("-pdf-test not set and no Bluebook PDF found in -in %s", inDir)
	}
	return out, nil
}

// titleCase converts a kebab-case slug into a friendly title:
// "digital-sat-practice-1" → "Digital Sat Practice 1". Caller is welcome
// to pass -title to override.
func titleCase(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func buildProvider(spec, ollamaHost, workdir string) (vision.Provider, error) {
	inner, err := importer.FromString(spec, ollamaHost)
	if err != nil {
		return nil, err
	}
	return vision.Cached(inner, filepath.Join(workdir, "llm-cache"))
}

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
				Type:          q.Type,
				StemMD:        q.StemMD,
				HasStemFigure: q.StemFigure != nil,
				PassageMD:     q.PassageMD,
				AnswerLabel:   q.AnswerLabel,
				AnswerValues:  q.AnswerValues,
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
