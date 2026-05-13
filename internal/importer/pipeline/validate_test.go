package pipeline_test

import (
	"strings"
	"testing"

	"github.com/qiangli/omniscore/internal/importer/pipeline"
	"github.com/qiangli/omniscore/internal/importer/profile"
)

func TestValidateQuestion_HappyPath(t *testing.T) {
	q := pipeline.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "What is $f'(2)$ if $f(x)=x^3-3x$?",
		Choices: []pipeline.ExtractedChoice{
			{Label: "A", TextMD: "$3$"},
			{Label: "B", TextMD: "$6$"},
			{Label: "C", TextMD: "$9$"},
			{Label: "D", TextMD: "$12$"},
			{Label: "E", TextMD: "$15$"},
		},
		AnswerLabel: "C",
	}
	issues := pipeline.ValidateQuestion(profile.AP(), q)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got: %v", issues)
	}
}

func TestValidateQuestion_BadAnswer(t *testing.T) {
	q := pipeline.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Pick one",
		Choices: []pipeline.ExtractedChoice{
			{Label: "A", TextMD: "x"}, {Label: "B", TextMD: "y"},
			{Label: "C", TextMD: "z"}, {Label: "D", TextMD: "w"},
			{Label: "E", TextMD: "v"},
		},
		AnswerLabel: "Q",
	}
	issues := pipeline.ValidateQuestion(profile.AP(), q)
	if !containsSubstr(issues, "answer_label") {
		t.Fatalf("expected answer_label issue, got: %v", issues)
	}
}

func TestValidateQuestion_UnbalancedDollars(t *testing.T) {
	q := pipeline.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Evaluate $\\int_0^1 x^2 \\, dx",
		Choices: []pipeline.ExtractedChoice{
			{Label: "A", TextMD: "$1$"}, {Label: "B", TextMD: "$2$"},
			{Label: "C", TextMD: "$3$"}, {Label: "D", TextMD: "$4$"},
			{Label: "E", TextMD: "$5$"},
		},
		AnswerLabel: "A",
	}
	issues := pipeline.ValidateQuestion(profile.AP(), q)
	if !containsSubstr(issues, "$ delimiters") {
		t.Fatalf("expected dollar-delimiter issue, got: %v", issues)
	}
}

func TestValidateQuestion_UnbalancedBracesInsideMath(t *testing.T) {
	q := pipeline.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Eval $\\dfrac{1{2}$",
		Choices: []pipeline.ExtractedChoice{
			{Label: "A", TextMD: "$1$"}, {Label: "B", TextMD: "$2$"},
			{Label: "C", TextMD: "$3$"}, {Label: "D", TextMD: "$4$"},
			{Label: "E", TextMD: "$5$"},
		},
		AnswerLabel: "A",
	}
	issues := pipeline.ValidateQuestion(profile.AP(), q)
	if !containsSubstr(issues, "unbalanced braces") {
		t.Fatalf("expected braces issue, got: %v", issues)
	}
}

func TestValidateQuestion_FourChoicesAcceptedForLegacyExams(t *testing.T) {
	// AP Calc BC 2014–2017 used 4-choice MCQs. Validator accepts profile.AP()
	// with either 4 or 5 choices (one less than the canonical 5 is allowed).
	q := pipeline.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Question text",
		Choices: []pipeline.ExtractedChoice{
			{Label: "A", TextMD: "x"}, {Label: "B", TextMD: "y"},
			{Label: "C", TextMD: "z"}, {Label: "D", TextMD: "w"},
		},
		AnswerLabel: "B",
	}
	issues := pipeline.ValidateQuestion(profile.AP(), q)
	if len(issues) != 0 {
		t.Fatalf("4-choice should be valid for AP; got: %v", issues)
	}
}

func TestValidateQuestion_FigureSubstitutesForEmptyText(t *testing.T) {
	q := pipeline.ExtractedQuestion{
		QuestionNumber: 1,
		StemMD:         "Pick the graph that shows $f'$",
		HasStemFigure:  false,
		Choices: []pipeline.ExtractedChoice{
			{Label: "A", TextMD: "", HasFigure: true},
			{Label: "B", TextMD: "", HasFigure: true},
			{Label: "C", TextMD: "", HasFigure: true},
			{Label: "D", TextMD: "", HasFigure: true},
			{Label: "E", TextMD: "", HasFigure: true},
		},
		AnswerLabel: "A",
	}
	issues := pipeline.ValidateQuestion(profile.AP(), q)
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
