package main

import (
	"regexp"
	"strings"
)

// Question is the minimal parsed shape. Maps directly onto content.Question
// in the omniscore repo.
type Question struct {
	Module      string   // "rw-1" | "rw-2" | "math-1" | "math-2"
	Number      int      // 1-based within module
	PageIndex   int      // 1-based source page in the PDF
	PassageMD   string   // body text (R&W) / problem setup (Math)
	StemMD      string   // the prompt (e.g., "Which choice...")
	Choices     []Choice // empty for SPR
	AnswerLabel string
	Source      string   // "parser" | "llm-fallback"
}

type Choice struct {
	Label  string
	TextMD string
}

// Test is the per-PDF parsed result.
type Test struct {
	Slug    string
	Modules map[string][]Question
}

var (
	rwHeaderRE   = regexp.MustCompile(`(?i)Reading\s+and\s+Writing`)
	mathHeaderRE = regexp.MustCompile(`(?im)^\s*Math\s*$`)
	module12RE   = regexp.MustCompile(`Module\s*\n?\s*([12])`)
	qCountRE     = regexp.MustCompile(`(\d+)\s*Q\s*U\s*E\s*S\s*T\s*I\s*O\s*N\s*S`)
	stopRE       = regexp.MustCompile(`(?m)^\s*STOP\s*$`)
	continueRE   = regexp.MustCompile(`CO\s*NTI\s*N\s*U\s*E|\bCONTINUE\b`)
	footerRE     = regexp.MustCompile(`Unauthorized copying or reuse of any part of this page is illegal`)

	qNumLineRE = regexp.MustCompile(`^\s*(\d{1,2})\s*$`)
	choiceRE   = regexp.MustCompile(`^\s*([A-D])\)\s*(.*)$`)

	// Stem-prompt patterns. When the parser sees one of these as a (mostly-)
	// standalone line near the end of a question region, everything before it
	// is the passage and the prompt itself is the stem.
	stemPromptRE = regexp.MustCompile(`(?i)^(Which (choice|finding|quotation|statement)|According to the text|The student wants|Mainly,|Based on the (?:text|passage|table|graph))`)
)

// ParseTest walks the per-page columns of a question PDF and extracts
// per-module question lists. Phase 1 handles MCQ items (R&W + Math MCQ);
// SPR items get an empty Choices slice with stem captured.
func ParseTest(pages []Page) *Test {
	t := &Test{Modules: map[string][]Question{}}

	state := ""
	// Module capacity caps — used both to reject bogus accumulation and
	// to detect "module is done" before the next header is seen.
	cap := map[string]int{"rw-1": 33, "rw-2": 33, "math-1": 27, "math-2": 27}

	for _, pg := range pages {
		header := strings.Join(pg.Cols, "\n")

		// New-module detection. Reading & Writing wins over Math when both
		// patterns appear (the math reference page has "Math" referenced in
		// formulas; rw header is the only Reading marker).
		nextState := ""
		if rwHeaderRE.MatchString(header) && qCountRE.MatchString(header) {
			if m := module12RE.FindStringSubmatch(header); len(m) == 2 {
				nextState = "rw-" + m[1]
			}
		} else if mathHeaderRE.MatchString(header) && qCountRE.MatchString(header) {
			if m := module12RE.FindStringSubmatch(header); len(m) == 2 {
				nextState = "math-" + m[1]
			}
		}
		if nextState != "" {
			state = nextState
			// fall through: some PDFs put q1 of the new module on the header page
		}

		if strings.Contains(header, "No Test Material On This Page") {
			state = ""
			continue
		}
		if state == "" {
			continue
		}
		// Math directions/reference pages: skip when no anchored question numbers.
		if strings.Contains(header, "DIRECTIONS") && !hasQuestionAnchors(pg) {
			continue
		}
		// If we've already filled this module, stop accumulating.
		if len(t.Modules[state]) >= cap[state] {
			continue
		}

		for _, col := range pg.Cols {
			qs := parseColumnQuestions(col)
			for _, q := range qs {
				if q.Number < 1 || q.Number > cap[state] {
					continue
				}
				// Numbers are sequential within a module; reject duplicates.
				if hasQuestionNumber(t.Modules[state], q.Number) {
					continue
				}
				q.Module = state
				q.PageIndex = pg.Index
				q.Source = "parser"
				splitPassageStem(&q)
				t.Modules[state] = append(t.Modules[state], q)
			}
		}

		// End-of-module STOP marker — defensive, clear state if we hit cap.
		if stopRE.MatchString(header) && len(t.Modules[state]) >= cap[state] {
			state = ""
		}
	}
	return t
}

func hasQuestionNumber(qs []Question, n int) bool {
	for _, q := range qs {
		if q.Number == n {
			return true
		}
	}
	return false
}

// splitPassageStem extracts the prompt sentence from the end of StemMD into
// the Stem field; everything before becomes Passage. If the StemMD doesn't
// contain a recognized prompt, it stays as Stem (caller can decide).
func splitPassageStem(q *Question) {
	full := q.StemMD
	// Walk sentences right-to-left looking for the first one that matches a
	// recognized prompt opener.
	// Cheap segmentation: split on ". " and "? " and "! ".
	sentences := splitSentences(full)
	for i := len(sentences) - 1; i >= 0; i-- {
		s := strings.TrimSpace(sentences[i])
		if stemPromptRE.MatchString(s) {
			q.PassageMD = strings.TrimSpace(strings.Join(sentences[:i], " "))
			q.StemMD = s
			return
		}
	}
	// Fallback: leave whole thing as stem (the diff handles passage+stem concat).
}

func splitSentences(s string) []string {
	var out []string
	cur := strings.Builder{}
	for i := 0; i < len(s); i++ {
		cur.WriteByte(s[i])
		if (s[i] == '.' || s[i] == '?' || s[i] == '!') && i+1 < len(s) && s[i+1] == ' ' {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func hasQuestionAnchors(p Page) bool {
	for _, col := range p.Cols {
		for _, line := range strings.Split(col, "\n") {
			if qNumLineRE.MatchString(line) {
				return true
			}
		}
	}
	return false
}

// parseColumnQuestions walks one column's lines, anchoring on bare-integer
// lines (question numbers) and choice prefixes.
func parseColumnQuestions(col string) []Question {
	var qs []Question
	lines := strings.Split(col, "\n")
	var cur *Question
	var stem []string
	var curChoice *Choice
	var choiceLines []string

	flushChoice := func() {
		if curChoice == nil {
			return
		}
		curChoice.TextMD = strings.TrimSpace(strings.Join(choiceLines, " "))
		cur.Choices = append(cur.Choices, *curChoice)
		curChoice = nil
		choiceLines = nil
	}
	flushQuestion := func() {
		if cur == nil {
			return
		}
		flushChoice()
		if len(cur.Choices) == 0 {
			// Stem-only (SPR) — keep stem content.
			cur.StemMD = strings.TrimSpace(strings.Join(stem, " "))
		} else {
			cur.StemMD = strings.TrimSpace(strings.Join(stem, " "))
		}
		qs = append(qs, *cur)
		cur = nil
		stem = nil
	}

	for _, line := range lines {
		l := strings.TrimRight(line, " \t\r")
		if footerRE.MatchString(l) || continueRE.MatchString(l) || stopRE.MatchString(l) {
			continue
		}
		if m := qNumLineRE.FindStringSubmatch(l); len(m) == 2 {
			// Start of new question.
			flushQuestion()
			n := atoi(m[1])
			if n < 1 || n > 33 {
				continue // page-number footer or noise
			}
			cur = &Question{Number: n}
			continue
		}
		if cur == nil {
			continue
		}
		if m := choiceRE.FindStringSubmatch(l); len(m) == 3 {
			flushChoice()
			curChoice = &Choice{Label: m[1]}
			choiceLines = []string{m[2]}
			continue
		}
		if curChoice != nil {
			if strings.TrimSpace(l) == "" {
				continue
			}
			choiceLines = append(choiceLines, strings.TrimSpace(l))
		} else {
			if strings.TrimSpace(l) == "" {
				continue
			}
			stem = append(stem, strings.TrimSpace(l))
		}
	}
	flushQuestion()
	return qs
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}
