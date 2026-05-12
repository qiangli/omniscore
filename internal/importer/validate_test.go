package importer_test

import (
	"strings"
	"testing"

	"github.com/qiangli/omniscore/internal/importer"
)

func TestValidateQuestion_HappyPath(t *testing.T) {
	q := importer.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "What is $f'(2)$ if $f(x)=x^3-3x$?",
		Choices: []importer.ExtractedChoice{
			{Label: "A", TextMD: "$3$"},
			{Label: "B", TextMD: "$6$"},
			{Label: "C", TextMD: "$9$"},
			{Label: "D", TextMD: "$12$"},
			{Label: "E", TextMD: "$15$"},
		},
		AnswerLabel: "C",
	}
	issues := importer.ValidateQuestion(q)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got: %v", issues)
	}
}

func TestValidateQuestion_BadAnswer(t *testing.T) {
	q := importer.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Pick one",
		Choices: []importer.ExtractedChoice{
			{Label: "A", TextMD: "x"}, {Label: "B", TextMD: "y"},
			{Label: "C", TextMD: "z"}, {Label: "D", TextMD: "w"},
			{Label: "E", TextMD: "v"},
		},
		AnswerLabel: "Q",
	}
	issues := importer.ValidateQuestion(q)
	if !containsSubstr(issues, "answer_label") {
		t.Fatalf("expected answer_label issue, got: %v", issues)
	}
}

func TestValidateQuestion_UnbalancedDollars(t *testing.T) {
	q := importer.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Evaluate $\\int_0^1 x^2 \\, dx",
		Choices: []importer.ExtractedChoice{
			{Label: "A", TextMD: "$1$"}, {Label: "B", TextMD: "$2$"},
			{Label: "C", TextMD: "$3$"}, {Label: "D", TextMD: "$4$"},
			{Label: "E", TextMD: "$5$"},
		},
		AnswerLabel: "A",
	}
	issues := importer.ValidateQuestion(q)
	if !containsSubstr(issues, "$ delimiters") {
		t.Fatalf("expected dollar-delimiter issue, got: %v", issues)
	}
}

func TestValidateQuestion_UnbalancedBracesInsideMath(t *testing.T) {
	q := importer.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Eval $\\dfrac{1{2}$",
		Choices: []importer.ExtractedChoice{
			{Label: "A", TextMD: "$1$"}, {Label: "B", TextMD: "$2$"},
			{Label: "C", TextMD: "$3$"}, {Label: "D", TextMD: "$4$"},
			{Label: "E", TextMD: "$5$"},
		},
		AnswerLabel: "A",
	}
	issues := importer.ValidateQuestion(q)
	if !containsSubstr(issues, "unbalanced braces") {
		t.Fatalf("expected braces issue, got: %v", issues)
	}
}

func TestValidateQuestion_FourChoicesAcceptedForLegacyExams(t *testing.T) {
	// Some older AP Calc BC exams (pre-2017) used 4-choice MCQs. Validator
	// accepts both 4 and 5; the prompt pads to 5 with empty E for newer years.
	q := importer.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Question text",
		Choices: []importer.ExtractedChoice{
			{Label: "A", TextMD: "x"}, {Label: "B", TextMD: "y"},
			{Label: "C", TextMD: "z"}, {Label: "D", TextMD: "w"},
		},
		AnswerLabel: "B",
	}
	issues := importer.ValidateQuestion(q)
	if len(issues) != 0 {
		t.Fatalf("4-choice should be valid; got: %v", issues)
	}
}

func TestValidateQuestion_FigureSubstitutesForEmptyText(t *testing.T) {
	// A choice that's purely a figure (e.g. "which graph?") has empty text but
	// HasFigure=true. Should not be flagged as empty.
	q := importer.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Pick the graph that shows $f'$",
		HasStemFigure:  false,
		Choices: []importer.ExtractedChoice{
			{Label: "A", TextMD: "", HasFigure: true},
			{Label: "B", TextMD: "", HasFigure: true},
			{Label: "C", TextMD: "", HasFigure: true},
			{Label: "D", TextMD: "", HasFigure: true},
			{Label: "E", TextMD: "", HasFigure: true},
		},
		AnswerLabel: "A",
	}
	issues := importer.ValidateQuestion(q)
	if len(issues) != 0 {
		t.Fatalf("figure-only choices should be valid; got: %v", issues)
	}
}

func containsSubstr(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
