package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/importer/profile"
	"github.com/qiangli/omniscore/internal/importer/render"
	"github.com/qiangli/omniscore/internal/importer/vision"
)

// Input bundles every knob Run needs. PDFTest is required; PDFScoring and
// PDFExplanation are optional (empty string ⇒ skip that PDF). All paths are
// absolute or relative to the caller's working directory.
type Input struct {
	Profile profile.Profile

	Slug  string
	Title string

	PDFTest        string
	PDFScoring     string // optional; if empty, pipeline scans PDFTest for answer-key/curve pages
	PDFExplanation string // optional; currently not consumed (reserved for rationale ingestion)

	OutRoot      string
	Workdir      string
	Provider     vision.Provider
	ConsistencyN int
	DPIClassify  int
	DPIExtract   int

	// Fingerprint identifies the run environment for the on-disk import
	// cache. Callers typically populate this with the vision-model spec
	// ("anthropic/claude-sonnet-4-6"). When it changes, the cache misses
	// and the pipeline re-runs even if the input PDFs are byte-identical.
	Fingerprint string

	// Force, when true, bypasses the on-disk cache check and always
	// re-runs the full pipeline.
	Force bool
}

// Result reports what Run produced.
type Result struct {
	Test    content.Test
	Curve   content.Curve
	Written int
	Flagged int
	Elapsed time.Duration
}

// Run executes the full pipeline: rasterize → classify → extract MCQs →
// reconcile answers → extract curve → validate → emit. Returns once Emit
// has written the test JSON + curve + figures + review log.
//
// Run is idempotent: if a prior run produced .import-manifest.json under the
// output slug folder and the inputs (PDF hashes) + parameters (DPI,
// consistency, fingerprint) all match, Run returns the prior Result without
// touching the LLM. Set Input.Force=true to bypass the cache.
func Run(ctx context.Context, log *slog.Logger, in Input) (Result, error) {
	start := time.Now()

	if in.ConsistencyN < 1 {
		in.ConsistencyN = 1
	}

	// Pre-flight: compute the manifest we'd write now, and compare against
	// any prior manifest on disk. Cache hit means we skip every LLM call.
	expected, err := computeManifest(in)
	if err != nil {
		return Result{}, fmt.Errorf("compute import manifest: %w", err)
	}
	manifestPath := ManifestPath(in.OutRoot, in.Profile.ExamType, in.Slug)
	if !in.Force {
		if prior, ok, perr := LoadManifest(manifestPath); perr == nil && ok && manifestMatches(prior, expected) {
			cached, cerr := readCachedResult(in, expected, log)
			if cerr == nil {
				log.Info("import cache hit; skipping pipeline",
					"slug", in.Slug, "manifest", manifestPath, "elapsed", time.Since(start).String())
				return cached, nil
			}
			log.Warn("import cache hit but cached output unreadable; re-running", "err", cerr)
		}
	}

	// 1. Rasterize every input PDF at low DPI for classification.
	classifyPages, _, err := rasterizeAll(ctx, log, in, in.DPIClassify, "pages-classify")
	if err != nil {
		return Result{}, err
	}
	log.Info("rasterize done", "purpose", "classify", "pages", len(classifyPages))

	// 2. Classify every page against the profile vocabulary.
	classes, err := ClassifyPages(ctx, in.Provider, in.Profile, classifyPages)
	if err != nil {
		return Result{}, err
	}
	classCount := map[PageClass]int{}
	for _, c := range classes {
		classCount[c]++
	}
	log.Info("classify done", "by_class", stringifyClassCount(classCount))

	// 3. Rasterize again at high DPI for extraction. Page indices align with
	//    the low-DPI list — one PDF, deterministic page count.
	extractPages, _, err := rasterizeAll(ctx, log, in, in.DPIExtract, "pages-extract")
	if err != nil {
		return Result{}, err
	}
	if len(extractPages) != len(classifyPages) {
		return Result{}, fmt.Errorf("page count mismatch: classify=%d extract=%d",
			len(classifyPages), len(extractPages))
	}

	// Project classes onto the high-DPI page paths (same order across PDFs).
	highClass := make(map[string]PageClass, len(extractPages))
	for i, low := range classifyPages {
		highClass[extractPages[i]] = classes[low]
	}

	// 4. Bucket pages by class.
	type bucket struct {
		Pages []string
	}
	moduleBuckets := map[string]*bucket{}
	for _, m := range in.Profile.Modules {
		moduleBuckets[m.ClassifierLabel] = &bucket{}
	}
	var answerKeyPages, curvePages []string
	for _, page := range extractPages {
		c := string(highClass[page])
		switch c {
		case string(ClassAnswerKey):
			answerKeyPages = append(answerKeyPages, page)
		case string(ClassScoringCurve):
			curvePages = append(curvePages, page)
		default:
			if b, ok := moduleBuckets[c]; ok {
				b.Pages = append(b.Pages, page)
			}
		}
	}
	log.Info("classified content pages",
		"modules", len(moduleBuckets),
		"answer_key", len(answerKeyPages),
		"scoring_curve", len(curvePages))

	// 5. Extract MCQs per module.
	emitModules := make([]EmitModule, 0, len(in.Profile.Modules))
	allQs := make([]ExtractedQuestion, 0)
	for _, m := range in.Profile.Modules {
		pages := moduleBuckets[m.ClassifierLabel].Pages
		log.Info("extract module", "section", m.Section, "pages", len(pages))
		qs, err := extractAll(ctx, in.Provider, in.Profile, m, pages, in.ConsistencyN, log)
		if err != nil {
			return Result{}, err
		}
		emitModules = append(emitModules, EmitModule{
			ID:         m.ID,
			Section:    m.Section,
			Title:      m.Title,
			TimeLimitS: m.TimeLimitS,
			Questions:  qs,
		})
		allQs = append(allQs, qs...)
	}

	// 6. Answer-key extraction. Profiles with per-module keys (SAT) split
	//    answer-key pages between modules upstream; this pass currently
	//    extracts using each module's prompt against the full answer-key page
	//    set and merges per-module. For AP there's exactly one module-prompt
	//    pass since the prompt set is uniform.
	keyByModule := map[string]map[int]AnswerKeyEntry{}
	if in.Profile.Features.PerModuleAnswerKey {
		for _, m := range in.Profile.Modules {
			k, err := ExtractAnswerKey(ctx, in.Provider, in.Profile, m, answerKeyPages)
			if err != nil {
				return Result{}, err
			}
			keyByModule[m.ID] = k
		}
	} else if len(in.Profile.Modules) > 0 {
		k, err := ExtractAnswerKey(ctx, in.Provider, in.Profile, in.Profile.Modules[0], answerKeyPages)
		if err != nil {
			return Result{}, err
		}
		for _, m := range in.Profile.Modules {
			keyByModule[m.ID] = k
		}
	}
	log.Info("answer keys extracted",
		"modules", len(keyByModule),
		"total_answers", sumKeys(keyByModule))

	for i, em := range emitModules {
		if key, ok := keyByModule[em.ID]; ok && len(key) > 0 {
			emitModules[i].Questions = ReconcileAnswers(em.Questions, key)
		}
	}

	// 7. Curve.
	log.Info("extract curve", "pages", len(curvePages))
	curve, err := ExtractCurve(ctx, in.Provider, in.Profile, in.Slug, curvePages)
	if err != nil {
		log.Warn("curve extraction failed; emitting empty curve", "err", err)
		curve = content.Curve{TestSlug: in.Slug, Sections: map[string][]content.CurvePoint{}}
	}

	// 8. Emit.
	emitIn := EmitInput{
		Profile: in.Profile,
		Slug:    in.Slug,
		Title:   in.Title,
		OutRoot: in.OutRoot,
		Workdir: in.Workdir,
		Modules: emitModules,
		Curve:   curve,
	}
	written, flagged, err := Emit(emitIn)
	if err != nil {
		return Result{}, err
	}

	res := Result{
		Curve:   curve,
		Written: written,
		Flagged: flagged,
		Elapsed: time.Since(start),
	}
	// Rebuild Test for return value (Emit writes it; we mirror the shape so
	// callers don't have to re-read disk).
	res.Test = content.Test{
		Slug:     in.Slug,
		Title:    in.Title,
		ExamType: in.Profile.ExamType,
		Subject:  in.Profile.Subject,
	}
	for _, em := range emitModules {
		mod := content.Module{
			ID:         em.ID,
			Section:    em.Section,
			Title:      em.Title,
			TimeLimitS: em.TimeLimitS,
		}
		for _, q := range em.Questions {
			cq := content.Question{
				ID:          fmt.Sprintf("%s-q%d", em.Section, q.QuestionNumber),
				StemMD:      q.StemMD,
				PassageMD:   q.PassageMD,
				AnswerLabel: q.AnswerLabel,
			}
			for _, c := range q.Choices {
				cq.Choices = append(cq.Choices, content.Choice{Label: c.Label, TextMD: c.TextMD})
			}
			mod.Questions = append(mod.Questions, cq)
		}
		res.Test.Modules = append(res.Test.Modules, mod)
	}

	// 9. Copy the source PDFs into <slug>/raw/ so teachers/admins can open
	//    the per-test folder and verify the conversion against the originals.
	rawPaths, err := CopyRawPDFs(in.OutRoot, in.Profile.ExamType, in.Slug, map[string]string{
		"test":        in.PDFTest,
		"scoring":     in.PDFScoring,
		"explanation": in.PDFExplanation,
	})
	if err != nil {
		log.Warn("copy raw pdfs failed", "err", err)
	} else if len(rawPaths) > 0 {
		log.Info("raw pdfs copied", "count", len(rawPaths), "dir", RawDir(in.OutRoot, in.Profile.ExamType, in.Slug))
	}

	// 10. Persist the manifest so the next run with identical inputs can
	//     short-circuit. completed_at is stamped after every artifact has
	//     landed on disk; a partial run leaves a stale manifest at most.
	expected.CompletedAtMS = time.Now().UnixMilli()
	if err := WriteManifest(manifestPath, expected); err != nil {
		log.Warn("write import manifest", "err", err)
	}

	log.Info("emit done",
		"written", written,
		"flagged", flagged,
		"out", filepath.Join(in.OutRoot, in.Profile.ExamType, in.Slug, "test.json"),
		"review", filepath.Join(in.Workdir, ".review", in.Slug+".md"),
		"elapsed", res.Elapsed.String())
	return res, nil
}

// readCachedResult reconstructs a Result struct from on-disk artifacts when
// a cache hit short-circuits the pipeline. Returns an error if either
// test.json or curve.json is missing/malformed.
func readCachedResult(in Input, manifest ImportManifest, _ *slog.Logger) (Result, error) {
	slugDir := filepath.Join(in.OutRoot, in.Profile.ExamType, in.Slug)
	testPath := filepath.Join(slugDir, "test.json")
	curvePath := filepath.Join(slugDir, "curve.json")
	t, err := readTestJSON(testPath)
	if err != nil {
		return Result{}, fmt.Errorf("read cached test.json: %w", err)
	}
	c, err := readCurveJSON(curvePath)
	if err != nil {
		return Result{}, fmt.Errorf("read cached curve.json: %w", err)
	}
	written := 0
	for _, m := range t.Modules {
		written += len(m.Questions)
	}
	_ = manifest // reserved for future use (e.g. surfacing fingerprint in Result)
	return Result{Test: t, Curve: c, Written: written}, nil
}

func readTestJSON(path string) (content.Test, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return content.Test{}, err
	}
	var t content.Test
	if err := json.Unmarshal(raw, &t); err != nil {
		return content.Test{}, err
	}
	return t, nil
}

func readCurveJSON(path string) (content.Curve, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// An import that emitted an empty curve writes no file; treat
			// that as a valid cached result.
			return content.Curve{Sections: map[string][]content.CurvePoint{}}, nil
		}
		return content.Curve{}, err
	}
	var c content.Curve
	if err := json.Unmarshal(raw, &c); err != nil {
		return content.Curve{}, err
	}
	return c, nil
}

func rasterizeAll(ctx context.Context, log *slog.Logger, in Input, dpi int, subdir string) ([]string, map[string]string, error) {
	var all []string
	pdfOfPage := map[string]string{}

	pdfs := []string{}
	if in.PDFTest != "" {
		pdfs = append(pdfs, in.PDFTest)
	}
	if in.PDFScoring != "" {
		pdfs = append(pdfs, in.PDFScoring)
	}
	if in.PDFExplanation != "" {
		pdfs = append(pdfs, in.PDFExplanation)
	}
	if len(pdfs) == 0 {
		return nil, nil, fmt.Errorf("rasterize: no input PDFs (PDFTest is required)")
	}

	for i, pdf := range pdfs {
		dir := filepath.Join(in.Workdir, subdir, fmt.Sprintf("pdf-%d", i))
		log.Info("rasterize", "purpose", subdir, "dpi", dpi, "pdf", filepath.Base(pdf), "out", dir)
		pages, err := render.Rasterize(ctx, pdf, dpi, dir)
		if err != nil {
			return nil, nil, fmt.Errorf("rasterize %s: %w", pdf, err)
		}
		for _, p := range pages {
			pdfOfPage[p] = pdf
		}
		all = append(all, pages...)
	}
	return all, pdfOfPage, nil
}

func extractAll(ctx context.Context, p vision.Provider, prof profile.Profile, mod profile.ModuleSpec, pages []string, n int, log *slog.Logger) ([]ExtractedQuestion, error) {
	var out []ExtractedQuestion
	for i, page := range pages {
		log.Info("extract page", "section", mod.Section, "i", i+1, "of", len(pages), "page", filepath.Base(page))
		qs, err := ExtractPage(ctx, p, prof, mod, page, n)
		if err != nil {
			return nil, err
		}
		out = append(out, qs...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QuestionNumber < out[j].QuestionNumber })
	return out, nil
}

func stringifyClassCount(in map[PageClass]int) map[string]int {
	out := map[string]int{}
	for k, v := range in {
		out[string(k)] = v
	}
	return out
}

func sumKeys(m map[string]map[int]AnswerKeyEntry) int {
	n := 0
	for _, v := range m {
		n += len(v)
	}
	return n
}
