// Package content defines the OmniScore test-content schema and loader.
package content

// Test is the on-disk representation of a published practice test.
type Test struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	ExamType string   `json:"exam_type"`         // "sat" | "ap" | future: "act", "psat", "gre", ...
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

// QuestionType selects how a question is rendered, answered, and graded.
// New types are added by registering a grader in internal/grading and a
// renderer in the frontend; the schema itself stays unchanged.
//
// Conventions:
//   - "" (empty) is treated as "mcq" for back-compat with existing AP/SAT JSON.
//   - "mcq"  — single-select multiple choice. Choices is populated. AnswerLabel
//     names the correct choice (e.g. "C"). AnswerValues is unused.
//   - "spr"  — student-produced response (SAT Math fill-in / future short-answer).
//     Choices is empty. AnswerValues is the list of accepted canonical forms
//     (e.g. ["0.5", "1/2", ".5"] or ["2; -12"] for ordered pairs). The grader
//     normalizes the submitted string before membership comparison.
//
// Future types (multi-select, matching, essay, etc.) plug in by reusing
// AnswerValues with type-specific semantics.
const (
	QuestionTypeMCQ = "mcq"
	QuestionTypeSPR = "spr"
)

// Question is one prompt (with optional passage and figures) and a typed
// answer specification. Type drives both the renderer (frontend) and the
// grader (internal/grading); see QuestionType* constants.
type Question struct {
	ID            string   `json:"id"`
	Type          string   `json:"type,omitempty"` // "" → "mcq"; see QuestionType* constants
	PassageMD     string   `json:"passage_md,omitempty"`
	PassageFigure *Figure  `json:"passage_figure,omitempty"`
	StemMD        string   `json:"stem_md"`
	StemFigure    *Figure  `json:"stem_figure,omitempty"`
	Choices       []Choice `json:"choices,omitempty"`       // populated for mcq; empty for spr
	AnswerLabel   string   `json:"answer_label,omitempty"`  // mcq: correct choice label
	AnswerValues  []string `json:"answer_values,omitempty"` // spr: accepted answer forms (and future types)
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

// CurvePoint maps one raw score to a scaled score for a given section.
//
// Two encodings are supported:
//   - Single point (AP exams, legacy SAT demo): set Scaled only.
//   - Range (real SAT / PSAT, where the official scoring guide prints a
//     [lower, upper] band per raw score): set ScaledLow and ScaledHigh.
//     Scaled, if also set, should equal the midpoint; if absent the loader
//     fills it from (ScaledLow+ScaledHigh)/2 for back-compat.
type CurvePoint struct {
	Raw        int `json:"raw"`
	Scaled     int `json:"scaled,omitempty"`
	ScaledLow  int `json:"scaled_low,omitempty"`
	ScaledHigh int `json:"scaled_high,omitempty"`
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
			qq.AnswerValues = nil
			qq.RationaleMD = ""
			out.Modules[i].Questions[j] = qq
		}
	}
	return out
}
