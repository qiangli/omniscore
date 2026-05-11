// Package content defines the OmniScore test-content schema and loader.
package content

// Test is the on-disk representation of a published practice test.
type Test struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	ExamType string   `json:"exam_type"` // "sat" | "ap"
	Modules  []Module `json:"modules"`
}

// Module is one timed section of a Test.
type Module struct {
	ID         string     `json:"id"`
	Section    string     `json:"section"` // "rw" | "math"
	Title      string     `json:"title"`
	TimeLimitS int        `json:"time_limit_s"`
	Questions  []Question `json:"questions"`
}

// Question is one stem with optional passage and 2-4 lettered choices.
type Question struct {
	ID          string   `json:"id"`
	PassageMD   string   `json:"passage_md,omitempty"`
	StemMD      string   `json:"stem_md"`
	Choices     []Choice `json:"choices"`
	AnswerLabel string   `json:"answer_label,omitempty"`
	RationaleMD string   `json:"rationale_md,omitempty"`
}

// Choice is one lettered answer option.
type Choice struct {
	Label  string `json:"label"`
	TextMD string `json:"text_md"`
}

// Curve is the on-disk raw->scaled score lookup for a Test.
type Curve struct {
	TestSlug string                  `json:"test_slug"`
	Sections map[string][]CurvePoint `json:"sections"`
}

// CurvePoint maps one raw score to one scaled score for a given section.
type CurvePoint struct {
	Raw    int `json:"raw"`
	Scaled int `json:"scaled"`
}

// StripAnswers returns a deep copy of t with answer keys + rationales removed.
// Use this before sending a test to a student mid-session.
func StripAnswers(t Test) Test {
	out := t
	out.Modules = make([]Module, len(t.Modules))
	for i, m := range t.Modules {
		out.Modules[i] = m
		out.Modules[i].Questions = make([]Question, len(m.Questions))
		for j, q := range m.Questions {
			qq := q
			qq.AnswerLabel = ""
			qq.RationaleMD = ""
			out.Modules[i].Questions[j] = qq
		}
	}
	return out
}
