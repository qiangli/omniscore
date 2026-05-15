// pdfcompare — evaluate PDF text extraction backends against a directory of
// SAT PDFs, parse each result into structured questions, and diff each
// backend's output against the existing vision-LLM JSON.
//
//   go run . -in <pdfdir> -llm <llmroot> -out ./out
//
// Adding a new backend: drop a file in this directory with init() {
// Register(Registration{Name, Kind, New}) }. No other changes required.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var practiceNumRE = regexp.MustCompile(`Practice (?:Test )?#?(\d)`)

func pdfNameToSlug(name string) string {
	m := practiceNumRE.FindStringSubmatch(name)
	if len(m) != 2 {
		return ""
	}
	return "sat-linear-practice-" + m[1]
}

type runRow struct {
	Source     string
	Kind       string
	PDF        string
	Slug       string
	Pages      int
	ExtractMS  int64
	ParseMS    int64
	Modules    int
	Questions  int
	Choices    int
	DiffRows   []DiffRow
	Err        string
}

func main() {
	in := flag.String("in", "", "directory of PDFs")
	llmRoot := flag.String("llm", "", "root of LLM-extracted test.json files")
	outDir := flag.String("out", "./out", "directory for per-source text dumps")
	only := flag.String("only", "", "comma-separated source names to include (default: all registered)")
	skipParse := flag.Bool("skip-parse", false, "skip question parser, only benchmark text extraction")
	llmFallbackModel := flag.String("llm-fallback", "", "vendor/model for LLM fallback on parser misses (e.g. ollama/qwen3.5:35b for text or ollama/qwen2.5vl:32b for vision). Empty disables fallback.")
	llmMode := flag.String("llm-mode", "agent", "fallback mode: agent (tool-calling loop, recommended), text (one-shot page text), or vision (rasterize + vision LLM)")
	llmHost := flag.String("llm-host", "http://localhost:11434", "Ollama base URL; ignored for cloud vendors")
	llmWorkdir := flag.String("llm-workdir", "/tmp/sat-pdf-eval-fallback", "scratch directory for rasterized PNGs + LLM cache")
	llmDPI := flag.Int("llm-dpi", 300, "rasterization DPI for LLM fallback (vision mode)")
	llmMaxPages := flag.Int("llm-max-pages", 0, "cap on total LLM page-queries across all PDFs (0=unlimited)")
	flag.Parse()

	if *in == "" {
		fmt.Fprintln(os.Stderr, "missing -in")
		os.Exit(2)
	}
	var sources []Registration
	if *only != "" {
		for _, n := range strings.Split(*only, ",") {
			n = strings.TrimSpace(n)
			r := Lookup(n)
			if r == nil {
				fmt.Fprintf(os.Stderr, "unknown source %q (registered: %v)\n", n, names(Sources()))
				os.Exit(2)
			}
			sources = append(sources, *r)
		}
	} else {
		sources = Sources()
	}
	if len(sources) == 0 {
		fmt.Fprintln(os.Stderr, "no sources registered")
		os.Exit(2)
	}

	entries, err := os.ReadDir(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read dir: %v\n", err)
		os.Exit(1)
	}
	var pdfs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".pdf") {
			continue
		}
		pdfs = append(pdfs, filepath.Join(*in, e.Name()))
	}
	sort.Strings(pdfs)

	_ = os.MkdirAll(*outDir, 0o755)
	for _, s := range sources {
		_ = os.MkdirAll(filepath.Join(*outDir, s.Name), 0o755)
	}

	var rows []runRow
	for _, pdf := range pdfs {
		base := filepath.Base(pdf)
		slug := pdfNameToSlug(base)
		isQuestionPDF := slug != "" && !strings.Contains(strings.ToLower(base), "answer") && !strings.Contains(strings.ToLower(base), "scoring")

		var llm *llmTest
		if isQuestionPDF && *llmRoot != "" {
			llm, _ = loadLLMTest(*llmRoot, slug)
		}

		for _, s := range sources {
			src := s.New()
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			start := time.Now()
			pages, err := src.Pages(ctx, pdf)
			cancel()
			extractMS := time.Since(start).Milliseconds()
			row := runRow{Source: s.Name, Kind: s.Kind, PDF: base, Slug: slug, Pages: len(pages), ExtractMS: extractMS}
			if err != nil {
				row.Err = err.Error()
				if errors.Is(err, context.DeadlineExceeded) {
					row.Err = "timeout"
				}
				rows = append(rows, row)
				fmt.Printf("[%s/%s] %s ERR %v\n", s.Kind, s.Name, base, row.Err)
				continue
			}
			// Dump text for visual inspection.
			dump := filepath.Join(*outDir, s.Name, strings.TrimSuffix(base, ".pdf")+".txt")
			f, _ := os.Create(dump)
			for _, p := range pages {
				fmt.Fprintf(f, "\n=== page %d ===\n", p.Index)
				for ci, c := range p.Cols {
					fmt.Fprintf(f, "[col %d]\n%s\n", ci, c)
				}
			}
			_ = f.Close()
			if *skipParse || !isQuestionPDF {
				rows = append(rows, row)
				fmt.Printf("[%s/%s] %s  pages=%d extract=%dms\n", s.Kind, s.Name, base, len(pages), extractMS)
				continue
			}
			pstart := time.Now()
			parsed := ParseTest(pages)
			row.ParseMS = time.Since(pstart).Milliseconds()
			row.Modules = len(parsed.Modules)
			for _, qs := range parsed.Modules {
				row.Questions += len(qs)
				for _, q := range qs {
					row.Choices += len(q.Choices)
				}
			}
			// Snapshot pre-fallback diff for comparison.
			var preFallback []DiffRow
			if llm != nil {
				preFallback = DiffAgainstLLM(parsed, llm)
				row.DiffRows = preFallback
			}

			// LLM fallback: fill question-number gaps using a vision provider.
			fallbackQueried, fallbackFilled := 0, 0
			if *llmFallbackModel != "" {
				fbCfg := FallbackConfig{
					Mode:     *llmMode,
					Model:    *llmFallbackModel,
					Host:     *llmHost,
					Workdir:  *llmWorkdir,
					DPI:      *llmDPI,
					MaxPages: *llmMaxPages,
				}
				fbCtx, fbCancel := context.WithTimeout(context.Background(), 30*time.Minute)
				q, fl, err := runLLMFallback(fbCtx, pdf, pages, parsed, fbCfg)
				fbCancel()
				fallbackQueried, fallbackFilled = q, fl
				if err != nil {
					fmt.Fprintf(os.Stderr, "[llm-fallback] %s: %v\n", base, err)
				}
				// Refresh totals & diff after merge.
				row.Questions, row.Choices = 0, 0
				for _, qs := range parsed.Modules {
					row.Questions += len(qs)
					for _, q := range qs {
						row.Choices += len(q.Choices)
					}
				}
				if llm != nil {
					row.DiffRows = DiffAgainstLLM(parsed, llm)
				}
			}
			rows = append(rows, row)

			// Per-PDF live line.
			diffSum := ""
			if len(row.DiffRows) > 0 {
				var s2 strings.Builder
				for _, d := range row.DiffRows {
					fmt.Fprintf(&s2, " %s=%d/%d(stem=%.2f,choices=%d)", d.Module, d.OurCount, d.LLMCount, d.AvgStemSim, d.MatchedChoiceQs)
				}
				diffSum = s2.String()
			}
			fbInfo := ""
			if *llmFallbackModel != "" {
				fbInfo = fmt.Sprintf(" fb_q=%d fb_filled=%d", fallbackQueried, fallbackFilled)
			}
			fmt.Printf("[%s/%s] %s pages=%d extract=%dms parse=%dms qs=%d chs=%d%s%s\n",
				s.Kind, s.Name, base, len(pages), extractMS, row.ParseMS, row.Questions, row.Choices, diffSum, fbInfo)
		}
	}

	// Print 3-way summary table.
	fmt.Println()
	fmt.Println("=== Per-source aggregate (question PDFs only) ===")
	aggTime := map[string]int64{}
	aggQs := map[string]int{}
	aggChs := map[string]int{}
	aggStemMatched := map[string]int{}
	aggStemTotal := map[string]int{}
	aggChoiceMatchedQs := map[string]int{}
	aggChoiceTotal := map[string]int{}
	aggStemSimSum := map[string]float64{}
	aggStemSimN := map[string]int{}
	for _, r := range rows {
		if r.Slug == "" || r.Err != "" {
			continue
		}
		if strings.Contains(strings.ToLower(r.PDF), "answer") || strings.Contains(strings.ToLower(r.PDF), "scoring") {
			continue
		}
		aggTime[r.Source] += r.ExtractMS + r.ParseMS
		aggQs[r.Source] += r.Questions
		aggChs[r.Source] += r.Choices
		for _, d := range r.DiffRows {
			aggStemMatched[r.Source] += d.MatchedStems
			aggStemTotal[r.Source] += d.LLMCount
			aggChoiceMatchedQs[r.Source] += d.MatchedChoiceQs
			aggChoiceTotal[r.Source] += d.LLMCount
			aggStemSimSum[r.Source] += d.AvgStemSim * float64(d.LLMCount)
			aggStemSimN[r.Source] += d.LLMCount
		}
	}
	fmt.Printf("%-12s %-7s %8s %5s %5s %10s %10s %8s\n",
		"SOURCE", "KIND", "TIME_MS", "QS", "CHS", "STEMS≥.5", "CHCS_OK", "MEAN_SIM")
	fmt.Println(strings.Repeat("-", 80))
	for _, s := range sources {
		name := s.Name
		mean := 0.0
		if aggStemSimN[name] > 0 {
			mean = aggStemSimSum[name] / float64(aggStemSimN[name])
		}
		fmt.Printf("%-12s %-7s %8d %5d %5d %10s %10s %8.3f\n",
			name, s.Kind, aggTime[name], aggQs[name], aggChs[name],
			pct(aggStemMatched[name], aggStemTotal[name]),
			pct(aggChoiceMatchedQs[name], aggChoiceTotal[name]),
			mean)
	}
	// LLM ground-truth row.
	llmQs, llmChs := 0, 0
	for _, pdf := range pdfs {
		base := filepath.Base(pdf)
		slug := pdfNameToSlug(base)
		if slug == "" || strings.Contains(strings.ToLower(base), "answer") || strings.Contains(strings.ToLower(base), "scoring") {
			continue
		}
		t, _ := loadLLMTest(*llmRoot, slug)
		if t == nil {
			continue
		}
		for _, m := range t.Modules {
			llmQs += len(m.Questions)
			for _, q := range m.Questions {
				llmChs += len(q.Choices)
			}
		}
	}
	fmt.Printf("%-12s %-7s %8s %5d %5d %10s %10s %8s\n",
		"llm", "ref", "-", llmQs, llmChs, "ref", "ref", "1.000")

	// Per-module breakdown for the source with best mean-sim.
	bestSrc := ""
	bestSim := -1.0
	for _, s := range sources {
		mean := 0.0
		if aggStemSimN[s.Name] > 0 {
			mean = aggStemSimSum[s.Name] / float64(aggStemSimN[s.Name])
		}
		if mean > bestSim {
			bestSim = mean
			bestSrc = s.Name
		}
	}
	if bestSrc != "" {
		fmt.Printf("\n=== Per-module breakdown for best source: %s ===\n", bestSrc)
		fmt.Printf("%-32s %-8s %5s %5s %8s %8s %8s\n", "PDF", "MODULE", "OUR", "LLM", "STEMS≥.5", "CHCS_OK", "MEAN_SIM")
		for _, r := range rows {
			if r.Source != bestSrc {
				continue
			}
			for _, d := range r.DiffRows {
				fmt.Printf("%-32s %-8s %5d %5d %8d %8d %8.3f\n",
					truncate(r.PDF, 32), d.Module, d.OurCount, d.LLMCount,
					d.MatchedStems, d.MatchedChoiceQs, d.AvgStemSim)
			}
		}
	}
}

func names(rs []Registration) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func pct(a, b int) string {
	if b == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d(%.0f%%)", a, b, 100*float64(a)/float64(b))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
