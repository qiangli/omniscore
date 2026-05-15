package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type llmQ struct {
	ID           string      `json:"id"`
	Type         string      `json:"type,omitempty"`
	PassageMD    string      `json:"passage_md,omitempty"`
	StemMD       string      `json:"stem_md"`
	AnswerLabel  string      `json:"answer_label,omitempty"`
	AnswerValues []string    `json:"answer_values,omitempty"`
	Choices      []llmChoice `json:"choices,omitempty"`
}
type llmChoice struct {
	Label  string `json:"label"`
	TextMD string `json:"text_md"`
}
type llmMod struct {
	ID        string `json:"id"`
	Questions []llmQ `json:"questions"`
}
type llmTest struct {
	Slug    string   `json:"slug"`
	Modules []llmMod `json:"modules"`
}

func loadLLMTest(llmRoot, slug string) (*llmTest, error) {
	b, err := os.ReadFile(filepath.Join(llmRoot, slug, "test.json"))
	if err != nil {
		return nil, err
	}
	var t llmTest
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

var (
	wsRE2    = regexp.MustCompile(`\s+`)
	mdRE     = regexp.MustCompile("[\\*_`\\\\$]")
	dashRE   = regexp.MustCompile(`[\x{2013}\x{2014}\x{2010}\x{2011}\x{2212}-]+`)
	quoteRE  = regexp.MustCompile(`[\x{2018}\x{2019}\x{201C}\x{201D}'"]+`)
	spaceRE  = regexp.MustCompile(`[\x{00A0}\x{2009}\x{200A}\x{202F}]`)
)

func norm(s string) string {
	s = strings.ToLower(s)
	s = mdRE.ReplaceAllString(s, "")
	s = spaceRE.ReplaceAllString(s, " ")
	s = dashRE.ReplaceAllString(s, "-")
	s = quoteRE.ReplaceAllString(s, "'")
	s = wsRE2.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// shingleSim returns Jaccard similarity over word-3-grams.
func shingleSim(a, b string) float64 {
	A := shingles(a, 3)
	B := shingles(b, 3)
	if len(A) == 0 || len(B) == 0 {
		return 0
	}
	inter := 0
	for k := range A {
		if _, ok := B[k]; ok {
			inter++
		}
	}
	return float64(inter) / float64(len(A)+len(B)-inter)
}
func shingles(s string, n int) map[string]struct{} {
	ws := strings.Fields(s)
	if len(ws) < n {
		return map[string]struct{}{strings.Join(ws, " "): {}}
	}
	out := map[string]struct{}{}
	for i := 0; i+n <= len(ws); i++ {
		out[strings.Join(ws[i:i+n], " ")] = struct{}{}
	}
	return out
}

// Diff result for one test (one PDF).
type DiffRow struct {
	Module               string
	OurCount             int
	LLMCount             int
	MatchedStems         int // stems with similarity >= 0.5
	MatchedChoiceQs      int // questions whose 4 choices all match
	AvgStemSim           float64
}

// Compare a parsed Test against an LLM-extracted llmTest.
func DiffAgainstLLM(parsed *Test, llm *llmTest) []DiffRow {
	if parsed == nil || llm == nil {
		return nil
	}
	var rows []DiffRow
	for _, lm := range llm.Modules {
		ours := parsed.Modules[lm.ID]
		row := DiffRow{
			Module:   lm.ID,
			OurCount: len(ours),
			LLMCount: len(lm.Questions),
		}
		// Match by question number when available; otherwise by best-similarity.
		byNum := map[int]Question{}
		for _, q := range ours {
			if q.Number > 0 {
				byNum[q.Number] = q
			}
		}
		var totalSim float64
		var matched int
		for i, lq := range lm.Questions {
			ourQ, ok := byNum[i+1]
			if !ok {
				continue
			}
			// Compare full pre-choice text: LLM splits passage/stem; we may
			// have split too, but a concat of both sides is order-independent.
			llmFull := norm(strings.TrimSpace(lq.PassageMD + " " + lq.StemMD))
			ourFull := norm(strings.TrimSpace(ourQ.PassageMD + " " + ourQ.StemMD))
			sim := shingleSim(llmFull, ourFull)
			totalSim += sim
			if sim >= 0.5 {
				matched++
			}
			// choice match: by label, normalized text similarity >= 0.6.
			ourChoiceByLabel := map[string]Choice{}
			for _, c := range ourQ.Choices {
				ourChoiceByLabel[c.Label] = c
			}
			allMatch := len(lq.Choices) > 0 && len(ourQ.Choices) == len(lq.Choices)
			for _, lc := range lq.Choices {
				oc, ok := ourChoiceByLabel[lc.Label]
				if !ok {
					allMatch = false
					break
				}
				if shingleSim(norm(oc.TextMD), norm(lc.TextMD)) < 0.6 {
					allMatch = false
					break
				}
			}
			if allMatch {
				row.MatchedChoiceQs++
			}
		}
		row.MatchedStems = matched
		if len(lm.Questions) > 0 {
			row.AvgStemSim = totalSim / float64(len(lm.Questions))
		}
		rows = append(rows, row)
	}
	return rows
}
