package importer

import (
	"fmt"
	"strings"
)

// ValidateQuestion runs structural checks on one ExtractedQuestion. Returns
// the set of issues found; an empty slice means the question is publishable.
// Caller decides whether to treat any issue as fatal (block emit) or as a
// review-log entry (proceed but flag).
func ValidateQuestion(q ExtractedQuestion) []string {
	var issues []string

	if strings.TrimSpace(q.StemMD) == "" && !q.HasStemFigure {
		issues = append(issues, "empty stem and no stem figure")
	}

	// Most AP MCQs use 5 choices; a few legacy years use 4. Accept either.
	if n := len(q.Choices); n != 5 && n != 4 {
		issues = append(issues, fmt.Sprintf("expected 4 or 5 choices, got %d", n))
	}

	expectedLabels := []string{"A", "B", "C", "D", "E"}
	for i, c := range q.Choices {
		if i >= len(expectedLabels) {
			break
		}
		if c.Label != expectedLabels[i] {
			issues = append(issues, fmt.Sprintf("choice %d label %q, expected %q", i, c.Label, expectedLabels[i]))
		}
		if strings.TrimSpace(c.TextMD) == "" && !c.HasFigure {
			issues = append(issues, fmt.Sprintf("choice %s has empty text and no figure", c.Label))
		}
		if iss := katexSanity(c.TextMD); iss != "" {
			issues = append(issues, fmt.Sprintf("choice %s: %s", c.Label, iss))
		}
	}

	if q.AnswerLabel != "" {
		valid := false
		for _, c := range q.Choices {
			if c.Label == q.AnswerLabel {
				valid = true
				break
			}
		}
		if !valid {
			issues = append(issues,
				fmt.Sprintf("answer_label %q is not among choice labels", q.AnswerLabel))
		}
	}

	if iss := katexSanity(q.StemMD); iss != "" {
		issues = append(issues, "stem: "+iss)
	}

	return issues
}

// katexSanity runs cheap structural checks on inline KaTeX inside a markdown
// fragment. It does NOT invoke a real KaTeX parser — that would require a
// Node helper. Catches the most common LLM errors:
//
//   - unbalanced $...$ delimiters
//   - unbalanced { } or [ ] inside math regions
//
// Returns "" when nothing suspicious is detected.
func katexSanity(s string) string {
	// Count unescaped $ characters.
	dollars := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && (i == 0 || s[i-1] != '\\') {
			dollars++
		}
	}
	if dollars%2 != 0 {
		return fmt.Sprintf("unbalanced $ delimiters (%d found)", dollars)
	}

	// Walk math regions and check brace/bracket balance.
	inMath := false
	braces, brackets := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && (i == 0 || s[i-1] != '\\') {
			if inMath {
				if braces != 0 || brackets != 0 {
					return fmt.Sprintf("unbalanced braces in math region (braces=%d brackets=%d)", braces, brackets)
				}
			}
			inMath = !inMath
			braces, brackets = 0, 0
			continue
		}
		if !inMath {
			continue
		}
		switch s[i] {
		case '{':
			if i == 0 || s[i-1] != '\\' {
				braces++
			}
		case '}':
			if i == 0 || s[i-1] != '\\' {
				braces--
			}
		case '[':
			brackets++
		case ']':
			brackets--
		}
	}
	return ""
}
