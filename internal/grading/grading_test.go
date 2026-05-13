package grading_test

import (
	"testing"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/grading"
)

func TestMCQ(t *testing.T) {
	q := content.Question{Type: "mcq", AnswerLabel: "C"}
	cases := map[string]bool{
		"C": true, "c": false, " C ": true, "A": false, "": false,
	}
	for sub, want := range cases {
		if got := grading.IsCorrect(q, sub); got != want {
			t.Errorf("IsCorrect(%q) = %v, want %v", sub, got, want)
		}
	}
	// Empty Type defaults to mcq.
	q2 := content.Question{AnswerLabel: "B"}
	if !grading.IsCorrect(q2, "B") {
		t.Error("empty type should grade as mcq")
	}
}

func TestSPR_Equivalences(t *testing.T) {
	q := content.Question{Type: "spr", AnswerValues: []string{"0.5"}}
	correct := []string{"0.5", ".5", "0.50", "1/2", "2/4", "+0.5"}
	for _, sub := range correct {
		if !grading.IsCorrect(q, sub) {
			t.Errorf("expected %q to grade as correct against 0.5", sub)
		}
	}
	wrong := []string{"5", "0.6", "1/3", "", " ", "abc"}
	for _, sub := range wrong {
		if grading.IsCorrect(q, sub) {
			t.Errorf("expected %q to grade as wrong against 0.5", sub)
		}
	}
}

func TestSPR_MultipleAcceptedAnswers(t *testing.T) {
	q := content.Question{Type: "spr", AnswerValues: []string{"3/4", "0.75"}}
	for _, sub := range []string{"0.75", "3/4", ".75", "6/8"} {
		if !grading.IsCorrect(q, sub) {
			t.Errorf("expected %q correct under either 3/4 or 0.75", sub)
		}
	}
}

func TestSPR_MultiPart(t *testing.T) {
	// Real Digital SAT example: "2 or -12" question accepts either order.
	q := content.Question{Type: "spr", AnswerValues: []string{"2; -12"}}
	good := []string{"2; -12", "-12; 2", "2, -12", "-12,2", "  2 ; -12  ", "2;-12.0"}
	for _, sub := range good {
		if !grading.IsCorrect(q, sub) {
			t.Errorf("expected %q correct for two-solution question", sub)
		}
	}
	bad := []string{"2", "-12", "2; 12", "2; -12; 0"}
	for _, sub := range bad {
		if grading.IsCorrect(q, sub) {
			t.Errorf("expected %q wrong for two-solution question", sub)
		}
	}
}

func TestSPR_Negatives(t *testing.T) {
	q := content.Question{Type: "spr", AnswerValues: []string{"-12"}}
	for _, sub := range []string{"-12", "-12.0", "-24/2"} {
		if !grading.IsCorrect(q, sub) {
			t.Errorf("expected %q correct", sub)
		}
	}
}

func TestSPR_Integer(t *testing.T) {
	q := content.Question{Type: "spr", AnswerValues: []string{"2520"}}
	for _, sub := range []string{"2520", "2520.0", "2520/1", "+2520"} {
		if !grading.IsCorrect(q, sub) {
			t.Errorf("expected %q correct", sub)
		}
	}
}

func TestCorrectDisplay(t *testing.T) {
	tests := []struct {
		name string
		q    content.Question
		want string
	}{
		{"mcq", content.Question{Type: "mcq", AnswerLabel: "C"}, "C"},
		{"spr_single", content.Question{Type: "spr", AnswerValues: []string{"0.5"}}, "0.5"},
		{"spr_multi", content.Question{Type: "spr", AnswerValues: []string{"0.5", "1/2"}}, "0.5 or 1/2"},
		{"unknown", content.Question{Type: "essay"}, ""},
	}
	for _, tc := range tests {
		if got := grading.CorrectDisplay(tc.q); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUnknownType_FailsClosed(t *testing.T) {
	q := content.Question{Type: "essay", AnswerLabel: "anything"}
	if grading.IsCorrect(q, "anything") {
		t.Error("unknown type should fail closed (return false)")
	}
}
