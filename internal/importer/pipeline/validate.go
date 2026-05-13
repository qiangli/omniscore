package pipeline

import (
	"fmt"
	"strings"

	"github.com/qiangli/omniscore/internal/importer/profile"
)

// ValidateQuestion runs structural checks on one ExtractedQuestion, scoped
// to the profile's choice set. Returns the set of issues found; an empty
// slice means the question is publishable. Caller decides whether to treat
// any issue as fatal (block emit) or as a review-log entry (proceed but flag).
func ValidateQuestion(prof profile.Profile, q ExtractedQuestion) []string {
	var issues []string

	if strings.TrimSpace(q.StemMD) == "" && !q.HasStemFigure {
		issues = append(issues, "empty stem and no stem figure")
	}

	expectedLabels := prof.ChoiceLabels
	expectedMax := len(expectedLabels)

	// Profiles that allow a smaller legacy choice count (AP Calc BC's 2014–2017
	// 4-choice exams) flag this on Features. For now we accept exactly the
	// profile's expected count, or one less (legacy fallback).
	if n := len(q.Choices); n != expectedMax && n != expectedMax-1 {
		issues = append(issues, fmt.Sprintf("expected %d choices, got %d", expectedMax, n))
	}

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
// fragment. Does NOT invoke a real KaTeX parser. Catches:
//   - unbalanced $...$ delimiters
//   - unbalanced { } or [ ] inside math regions
//
// Returns "" when nothing suspicious is detected.
func katexSanity(s string) string {
	dollars := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && (i == 0 || s[i-1] != '\\') {
			dollars++
		}
	}
	if dollars%2 != 0 {
		return fmt.Sprintf("unbalanced $ delimiters (%d found)", dollars)
	}

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
