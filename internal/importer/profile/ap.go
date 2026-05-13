package profile

// AP returns the Profile for the College Board AP Calculus BC released
// exams. The numbers and prompts here match the (formerly hardcoded) values
// in cmd/ap-import/main.go and internal/importer/{classify,extract,answers,curve}.go.
//
// Sections, time limits, and curve scale are AP Calc BC specific. When other
// AP subjects come online they'll either get their own factory (APCalcAB,
// APStat, etc.) or this becomes a constructor taking a subject argument.
func AP() Profile {
	return Profile{
		ExamType:      "ap",
		Subject:       "calc_bc",
		ChoiceLabels:  []string{"A", "B", "C", "D", "E"},
		CurveScale:    CurveScale{Min: 1, Max: 5},
		CurveSections: []string{"mcq_total"},
		Modules: []ModuleSpec{
			{
				ID:              "mcq-no-calc",
				Section:         "mcq_no_calc",
				Title:           "Section I, Part A — No calculator",
				ClassifierLabel: "mcq_no_calc",
				TimeLimitS:      3300,
			},
			{
				ID:              "mcq-calc",
				Section:         "mcq_calc",
				Title:           "Section I, Part B — Calculator",
				ClassifierLabel: "mcq_calc",
				TimeLimitS:      3000,
			},
		},
		Prompts: PromptSet{
			Classify:  apClassifyPrompt,
			Extract:   apExtractPrompt,
			AnswerKey: apAnswerKeyPrompt,
			Curve:     apCurvePrompt,
		},
	}
}

func apClassifyPrompt(_ Profile) string {
	return `You are looking at a single page from a College Board AP Calculus BC released practice exam PDF.

Reply with EXACTLY ONE token from this list, lowercase, no punctuation, no explanation:

  instructions    — exam instructions, cover, copyright, table of contents
  mcq_no_calc     — Section I, Part A multiple-choice questions (calculator NOT permitted)
  mcq_calc        — Section I, Part B multiple-choice questions (calculator permitted)
  frq_no_calc     — Section II, Part B free-response questions (calculator NOT permitted)
  frq_calc        — Section II, Part A free-response questions (calculator permitted)
  answer_key      — multiple-choice answer key page
  scoring_curve   — scoring worksheet / raw-to-scaled conversion table
  skip            — anything else (blank pages, bubble answer sheet, demographic form)

Reply with only one token. No prose.`
}

func apExtractPrompt(_ Profile, _ ModuleSpec) string {
	return `You are extracting AP Calculus BC multiple-choice questions from a single page image of a College Board released practice exam.

Return ONE JSON object, no prose, no Markdown code fence, with this exact shape:

{
  "questions": [
    {
      "question_number": <int>,
      "stem_md": "<question text in Markdown; math in $...$ KaTeX, e.g. $\\int_0^1 x^2 \\, dx$>",
      "has_stem_figure": <true if the stem references a graph/figure printed on this page, else false>,
      "choices": [
        {"label": "A", "text_md": "<...>", "has_figure": <bool>},
        {"label": "B", "text_md": "<...>", "has_figure": <bool>},
        {"label": "C", "text_md": "<...>", "has_figure": <bool>},
        {"label": "D", "text_md": "<...>", "has_figure": <bool>},
        {"label": "E", "text_md": "<...>", "has_figure": <bool>}
      ]
    }
  ]
}

Rules:
- Exactly 5 choices labeled A–E (some 2014–2017 exams use only A–D; pad with empty E if needed and set "has_figure": false on E).
- All math expressions MUST use KaTeX-compatible $...$ delimiters. Use \\dfrac for prominent fractions.
- Preserve original wording verbatim — do not paraphrase or summarize.
- If a question is illegible or partially cut off, include it anyway with whatever text you can read.
- If the page has NO multiple-choice questions, return {"questions": []}.
- Output ONLY the JSON object. No explanation. No markdown fence.`
}

func apAnswerKeyPrompt(_ Profile, _ ModuleSpec) string {
	return `You are looking at the multiple-choice answer key page of an AP Calculus BC released exam.

Return ONE JSON object, no prose, no Markdown code fence:

{
  "answers": [
    {"question_number": 1, "label": "A"},
    {"question_number": 2, "label": "C"},
    ...
  ]
}

Rules:
- "label" must be one of A, B, C, D, E.
- Include every numbered question listed on the page.
- If a number is unreadable, omit that entry.
- Output ONLY the JSON object. No prose.`
}

func apCurvePrompt(_ Profile) string {
	return `You are looking at the scoring worksheet of an AP Calculus BC released exam.

Find the table that maps composite raw score → AP grade (1–5). Return ONE JSON object, no prose, no Markdown code fence:

{
  "ranges": [
    {"raw_min": <int>, "raw_max": <int>, "scaled": <int 1..5>},
    ...
  ]
}

Rules:
- One entry per AP grade (so usually exactly 5: one each for grades 1, 2, 3, 4, 5).
- raw_min and raw_max are inclusive endpoints of the raw composite score range.
- "scaled" must be an integer between 1 and 5.
- If only the multiple-choice raw → AP grade table is shown, use it.
- Output ONLY the JSON object. No prose.`
}
