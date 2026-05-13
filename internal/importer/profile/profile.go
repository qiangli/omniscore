// Package profile describes how one exam type (AP, SAT, …) should be parsed
// and emitted by the shared importer pipeline. A Profile bundles the static
// metadata (choice labels, sections, time limits, curve scale) with the
// exam-specific prompt set so that the pipeline itself stays exam-agnostic.
//
// Two profile factories ship in this package: AP() and SAT(). New exams
// (ACT, PSAT, GRE, …) plug in by adding a new factory in this package.
package profile

import "slices"

// Profile captures every exam-specific knob the importer pipeline needs.
type Profile struct {
	ExamType      string       // "ap" | "sat" | future: "act", "psat", "gre", …
	Subject       string       // "calc_bc" | ""
	ChoiceLabels  []string     // ["A".."E"] for AP, ["A".."D"] for SAT
	Modules       []ModuleSpec // ordered; one entry per timed module
	CurveScale    CurveScale   // 1..5 for AP, 200..800 for SAT
	CurveSections []string     // section IDs the curve extractor emits, e.g. ["mcq_total"] or ["rw","math"]
	// QuestionTypes lists the content.QuestionType* values this exam produces.
	// Defaults to ["mcq"] if empty. SAT() opts in to ["mcq", "spr"] to permit
	// Math student-produced-response items in addition to MCQ. The pipeline
	// uses this list to validate classifier/extract output.
	QuestionTypes []string
	Prompts       PromptSet
	Features      Features
}

// SupportsQuestionType returns true if the profile declared the given type.
// An empty QuestionTypes list defaults to ["mcq"] for back-compat.
func (p Profile) SupportsQuestionType(t string) bool {
	if len(p.QuestionTypes) == 0 {
		return t == "" || t == "mcq"
	}
	if t == "" {
		t = "mcq"
	}
	return slices.Contains(p.QuestionTypes, t)
}

// ModuleSpec is one timed section the importer should emit. ClassifierLabel
// is the exact lowercase token the page-classifier prompt asks the LLM to
// return for pages belonging to this module. Section is what ends up in the
// emitted content JSON; the two may differ (e.g. classifier label "rw_module_1"
// → Section "rw" with ID "rw-1").
type ModuleSpec struct {
	ID              string
	Section         string
	Title           string
	ClassifierLabel string
	TimeLimitS      int
	AcceptsPassage  bool
}

// CurveScale is the inclusive raw → scaled output range for one exam.
type CurveScale struct {
	Min, Max int
}

// Features carries coarse exam-shape flags that change pipeline behavior but
// don't deserve their own field on Profile.
type Features struct {
	HasPassage         bool // SAT R&W modules attach a reading passage per question
	PerModuleAnswerKey bool // SAT prints answer keys per module; AP has one key page
}

// PromptSet bundles the four task-specific prompts the pipeline issues to the
// vision LLM. Each is a function so the prompt can be parameterized by the
// surrounding Profile + ModuleSpec without string-formatting at every call
// site.
type PromptSet struct {
	Classify  func(p Profile) string
	Extract   func(p Profile, m ModuleSpec) string
	AnswerKey func(p Profile, m ModuleSpec) string
	Curve     func(p Profile) string
}

// PageClasses returns the canonical ordered set of page-classifier labels for
// this profile: ["instructions", <each module's ClassifierLabel>,
// "answer_key", "scoring_curve", "skip"]. The classifier prompt enumerates
// these so the LLM has a fixed vocabulary.
func (p Profile) PageClasses() []string {
	out := make([]string, 0, len(p.Modules)+4)
	out = append(out, "instructions")
	for _, m := range p.Modules {
		out = append(out, m.ClassifierLabel)
	}
	out = append(out, "answer_key", "scoring_curve", "skip")
	return out
}

// ModuleByClassifierLabel resolves a classifier-emitted label back to its
// ModuleSpec. Returns (zero, false) for non-module labels like "instructions"
// or "skip".
func (p Profile) ModuleByClassifierLabel(label string) (ModuleSpec, bool) {
	for _, m := range p.Modules {
		if m.ClassifierLabel == label {
			return m, true
		}
	}
	return ModuleSpec{}, false
}
