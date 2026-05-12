// Package content defines the OmniScore test-content schema and loader.
package content

// Test is the on-disk representation of a published practice test.
type Test struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	ExamType string   `json:"exam_type"`         // "sat" | "ap"
	Subject  string   `json:"subject,omitempty"` // e.g. "calc_bc" — AP subject code
	Modules  []Module `json:"modules"`
}

// Module is one timed section of a Test.
type Module struct {
	ID         string     `json:"id"`
	Section    string     `json:"section"` // "rw" | "math" | "mcq_no_calc" | ...
	Title      string     `json:"title"`
	TimeLimitS int        `json:"time_limit_s"`
	Questions  []Question `json:"questions"`
}

// Question is one stem (with optional passage and figures) and a set of lettered choices.
type Question struct {
	ID            string   `json:"id"`
	PassageMD     string   `json:"passage_md,omitempty"`
	PassageFigure *Figure  `json:"passage_figure,omitempty"`
	StemMD        string   `json:"stem_md"`
	StemFigure    *Figure  `json:"stem_figure,omitempty"`
	Choices       []Choice `json:"choices"`
	AnswerLabel   string   `json:"answer_label,omitempty"`
	RationaleMD   string   `json:"rationale_md,omitempty"`
}

// Choice is one lettered answer option, optionally accompanied by a figure.
type Choice struct {
	Label  string  `json:"label"`
	TextMD string  `json:"text_md"`
	Figure *Figure `json:"figure,omitempty"`
}

// Figure is an inline image asset. Src is rewritten by the loader to an
// absolute /api/figures/<exam>/<slug>/<file> URL before the JSON blob is
// persisted, so the frontend renders <img src=...> verbatim.
type Figure struct {
	Src     string `json:"src"`
	Alt     string `json:"alt,omitempty"`
	WidthPx int    `json:"width_px,omitempty"`
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
