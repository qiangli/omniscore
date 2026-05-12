// Command ap-import converts a College Board AP Calculus BC released-exam
// PDF into OmniScore JSON. It is a separate binary from `omniscore` so that
// Poppler / Ollama dependencies don't leak into the runtime artifact.
//
// Typical use:
//
//	ap-import \
//	  -pdf "data/AP Calc BC/AP Calc BC 2014.pdf" \
//	  -slug ap-calc-bc-2014 \
//	  -title "AP Calculus BC — 2014 Released Exam" \
//	  -provider ollama \
//	  -ollama-model llama3.2-vision:90b \
//	  -out data/omni-data/ap \
//	  -workdir .import-cache/ap-calc-bc-2014 \
//	  -consistency 3
//
// The output:
//
//	data/omni-data/ap/tests/<slug>.json
//	data/omni-data/ap/curves/<slug>.json
//	data/omni-data/ap/figures/<slug>/qNN-stem.png
//	.import-cache/<slug>/.review/<slug>.md
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
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/importer"
	"github.com/qiangli/omniscore/internal/importer/render"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

func main() {
	var (
		pdfPath     = flag.String("pdf", "", "input PDF path (College Board AP Calc BC released exam)")
		slug        = flag.String("slug", "", "output slug, e.g. ap-calc-bc-2014")
		title       = flag.String("title", "", "test title shown to students")
		modelSpec   = flag.String("model", "", "vision model spec as vendor/model, e.g. ollama/llama3.2-vision:90b, anthropic/claude-sonnet-4-6, openai/gpt-4o, gemini/gemini-2.5-pro. Falls back to $OMNI_MODEL.")
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

	if *validateIn != "" {
		if err := validateOnly(*validateIn, logger); err != nil {
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

	if err := runImport(ctx, logger, prov, *pdfPath, *slug, *title, *outRoot, *workdir, *consistency, *dpiClassify, *dpiExtract); err != nil {
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

func runImport(ctx context.Context, logger *slog.Logger, p vision.Provider,
	pdfPath, slug, title, outRoot, workdir string,
	consistency, dpiClassify, dpiExtract int) error {

	start := time.Now()

	// 1. Rasterize at low DPI for the classifier pass.
	classifyDir := filepath.Join(workdir, "pages-classify")
	logger.Info("rasterize", "purpose", "classify", "dpi", dpiClassify, "out", classifyDir)
	classifyPages, err := render.Rasterize(ctx, pdfPath, dpiClassify, classifyDir)
	if err != nil {
		return err
	}
	logger.Info("rasterize done", "pages", len(classifyPages))

	// 2. Classify every page.
	logger.Info("classify pages")
	classes, err := importer.ClassifyPages(ctx, p, classifyPages)
	if err != nil {
		return err
	}
	classCount := map[importer.PageClass]int{}
	for _, c := range classes {
		classCount[c]++
	}
	logger.Info("classify done", "by_class", classCount)

	// 3. Rasterize at high DPI for the extraction pass.
	extractDir := filepath.Join(workdir, "pages-extract")
	logger.Info("rasterize", "purpose", "extract", "dpi", dpiExtract, "out", extractDir)
	extractPages, err := render.Rasterize(ctx, pdfPath, dpiExtract, extractDir)
	if err != nil {
		return err
	}
	if len(extractPages) != len(classifyPages) {
		return fmt.Errorf("page count mismatch: classify=%d extract=%d",
			len(classifyPages), len(extractPages))
	}
	// Map class onto the high-DPI page list (same order, same indices).
	highClass := make(map[string]importer.PageClass, len(extractPages))
	for i, low := range classifyPages {
		highClass[extractPages[i]] = classes[low]
	}

	// 4. Extract MCQs from pages classified mcq_*.
	var mcqNoCalc, mcqCalc, answerKeyPages, curvePages []string
	for _, page := range extractPages {
		switch highClass[page] {
		case importer.ClassMCQNoCalc:
			mcqNoCalc = append(mcqNoCalc, page)
		case importer.ClassMCQCalc:
			mcqCalc = append(mcqCalc, page)
		case importer.ClassAnswerKey:
			answerKeyPages = append(answerKeyPages, page)
		case importer.ClassScoringCurve:
			curvePages = append(curvePages, page)
		}
	}
	logger.Info("classified content pages",
		"mcq_no_calc", len(mcqNoCalc), "mcq_calc", len(mcqCalc),
		"answer_key", len(answerKeyPages), "scoring_curve", len(curvePages))

	noCalcQs, err := extractAll(ctx, p, mcqNoCalc, consistency, logger, "mcq_no_calc")
	if err != nil {
		return err
	}
	calcQs, err := extractAll(ctx, p, mcqCalc, consistency, logger, "mcq_calc")
	if err != nil {
		return err
	}

	// 5. Reconcile answers.
	logger.Info("extract answer key", "pages", len(answerKeyPages))
	key, err := importer.ExtractAnswerKey(ctx, p, answerKeyPages)
	if err != nil {
		return err
	}
	logger.Info("answer key extracted", "answers", len(key))
	noCalcQs = importer.ReconcileAnswers(noCalcQs, key)
	calcQs = importer.ReconcileAnswers(calcQs, key)

	// 6. Curve.
	logger.Info("extract curve", "pages", len(curvePages))
	curve, err := importer.ExtractCurve(ctx, p, slug, curvePages)
	if err != nil {
		logger.Warn("curve extraction failed; emitting empty curve", "err", err)
		curve = content.Curve{TestSlug: slug, Sections: map[string][]content.CurvePoint{}}
	}

	// 7. Emit.
	in := importer.EmitInput{
		Slug: slug, Title: title, OutRoot: outRoot, Workdir: workdir,
		Curve: curve,
		Modules: []importer.EmitModule{
			{ID: "mcq-no-calc", Section: "mcq_no_calc",
				Title: "Section I, Part A — No calculator", TimeLimitS: 3300, Questions: noCalcQs},
			{ID: "mcq-calc", Section: "mcq_calc",
				Title: "Section I, Part B — Calculator", TimeLimitS: 3000, Questions: calcQs},
		},
	}
	written, flagged, err := importer.Emit(in)
	if err != nil {
		return err
	}
	logger.Info("emit done", "written", written, "flagged", flagged,
		"out", filepath.Join(outRoot, "tests", slug+".json"),
		"review", filepath.Join(workdir, ".review", slug+".md"),
		"elapsed", time.Since(start).String())
	return nil
}

func extractAll(ctx context.Context, p vision.Provider, pages []string, n int, logger *slog.Logger, section string) ([]importer.ExtractedQuestion, error) {
	var out []importer.ExtractedQuestion
	for i, page := range pages {
		logger.Info("extract page", "section", section, "i", i+1, "of", len(pages), "page", filepath.Base(page))
		qs, err := importer.ExtractPage(ctx, p, page, n)
		if err != nil {
			return nil, err
		}
		out = append(out, qs...)
	}
	// Stable order by question number across pages.
	sort.Slice(out, func(i, j int) bool { return out[i].QuestionNumber < out[j].QuestionNumber })
	return out, nil
}

// validateOnly reads an existing tests/<slug>.json and runs ValidateQuestion
// against every MCQ in it. Useful after a human edits a flagged question to
// confirm the change still matches the schema.
func validateOnly(path string, logger *slog.Logger) error {
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
			eq := importer.ExtractedQuestion{
				QuestionNumber: 0, // unused for validate-only
				StemMD:         q.StemMD,
				HasStemFigure:  q.StemFigure != nil,
				AnswerLabel:    q.AnswerLabel,
			}
			for _, c := range q.Choices {
				eq.Choices = append(eq.Choices, importer.ExtractedChoice{
					Label: c.Label, TextMD: c.TextMD, HasFigure: c.Figure != nil,
				})
			}
			issues := importer.ValidateQuestion(eq)
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
