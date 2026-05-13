package profile

// SAT returns the Profile for the College Board Digital SAT (Bluebook).
//
// Structure: two sections (Reading & Writing, Math), each with two adaptive
// modules. The importer emits all four modules as standalone — adaptive
// Module 1 → Module 2 difficulty selection happens at runtime, not at
// authoring time. Choices are A–D (vs. AP's A–E). Scoring scale is 200–800
// per section.
//
// Reading & Writing questions carry a `passage_md` field; Math questions
// don't. AcceptsPassage flags the relevant modules so the extract prompt
// can request the passage block.
func SAT() Profile {
	return Profile{
		ExamType:      "sat",
		Subject:       "",
		ChoiceLabels:  []string{"A", "B", "C", "D"},
		CurveScale:    CurveScale{Min: 200, Max: 800},
		CurveSections: []string{"rw", "math"},
		QuestionTypes: []string{"mcq", "spr"},
		Modules: []ModuleSpec{
			{
				ID:              "rw-1",
				Section:         "rw",
				Title:           "Reading and Writing — Module 1",
				ClassifierLabel: "rw_module_1",
				TimeLimitS:      1920,
				AcceptsPassage:  true,
			},
			{
				ID:              "rw-2",
				Section:         "rw",
				Title:           "Reading and Writing — Module 2",
				ClassifierLabel: "rw_module_2",
				TimeLimitS:      1920,
				AcceptsPassage:  true,
			},
			{
				ID:              "math-1",
				Section:         "math",
				Title:           "Math — Module 1",
				ClassifierLabel: "math_module_1",
				TimeLimitS:      2100,
			},
			{
				ID:              "math-2",
				Section:         "math",
				Title:           "Math — Module 2",
				ClassifierLabel: "math_module_2",
				TimeLimitS:      2100,
			},
		},
		Features: Features{
			HasPassage:         true,
			PerModuleAnswerKey: true,
		},
		Prompts: PromptSet{
			Classify:  satClassifyPrompt,
			Extract:   satExtractPrompt,
			AnswerKey: satAnswerKeyPrompt,
			Curve:     satCurvePrompt,
		},
	}
}

func satClassifyPrompt(_ Profile) string {
	return `You are looking at a single page from a College Board Digital SAT (Bluebook) practice exam PDF.

Reply with EXACTLY ONE token from this list, lowercase, no punctuation, no explanation:

  instructions    — exam instructions, cover, copyright, table of contents
  rw_module_1     — Reading and Writing Module 1 (first half of the R&W section)
  rw_module_2     — Reading and Writing Module 2 (second half of the R&W section, harder/easier per adaptive branch)
  math_module_1   — Math Module 1 (first half of the math section)
  math_module_2   — Math Module 2 (second half of the math section, harder/easier per adaptive branch)
  answer_key      — answer key page (lists question_number → letter or 1–4 number)
  scoring_curve   — raw-to-scaled conversion table (200–800 per section)
  skip            — anything else (blank pages, bubble answer sheet, demographic form, instructions for the proctor)

Reply with only one token. No prose.`
}

func satExtractPrompt(_ Profile, m ModuleSpec) string {
	passageBlock := ""
	if m.AcceptsPassage {
		passageBlock = `
- For each question, copy the left-pane reading passage into "passage_md" verbatim (preserve paragraph breaks as blank lines). Use Markdown for emphasis (*italic*, **bold**, > blockquote).
- If the passage is a graph/chart/image rather than text, set "passage_md" to "" and "has_passage_figure": true.`
	}
	sprBlock := ""
	if m.Section == "math" {
		sprBlock = `
- Math may contain TWO kinds of items:
    1. Multiple-choice ("mcq") with choices A–D — the default.
    2. Student-produced response ("spr") — the page has NO choices and instead shows an instructions strip such as "Enter your answer in the answer box" or a number-entry box. For SPR questions emit "type": "spr" and "choices": [] (leave the array empty).`
	}
	return `You are extracting Digital SAT (Bluebook) questions from a single page image. The section is "` + m.Section + `" — module "` + m.ID + `".

Return ONE JSON object, no prose, no Markdown code fence, with this exact shape:

{
  "questions": [
    {
      "question_number": <int>,
      "type": "mcq" | "spr",
      "passage_md": "<reading passage in Markdown; empty string for math questions>",
      "has_passage_figure": <bool>,
      "stem_md": "<question prompt in Markdown; math in $...$ KaTeX>",
      "has_stem_figure": <true if the stem references a graph/figure printed on this page>,
      "choices": [
        {"label": "A", "text_md": "<...>", "has_figure": <bool>},
        {"label": "B", "text_md": "<...>", "has_figure": <bool>},
        {"label": "C", "text_md": "<...>", "has_figure": <bool>},
        {"label": "D", "text_md": "<...>", "has_figure": <bool>}
      ]
    }
  ]
}

Rules:
- "type" defaults to "mcq" when omitted.
- MCQ items have exactly 4 choices labeled A–D.
- All math expressions MUST use KaTeX-compatible $...$ delimiters. Use \\dfrac for prominent fractions.
- Preserve original wording verbatim — do not paraphrase or summarize.` + passageBlock + sprBlock + `
- If a question is illegible or partially cut off, include it anyway with whatever text you can read.
- If the page has NO questions, return {"questions": []}.
- Output ONLY the JSON object. No explanation. No markdown fence.`
}

func satAnswerKeyPrompt(_ Profile, m ModuleSpec) string {
	return `You are looking at the answer key page for the Digital SAT module "` + m.ID + `" (section "` + m.Section + `").

Return ONE JSON object, no prose, no Markdown code fence:

{
  "answers": [
    {"question_number": 1, "label": "A"},
    {"question_number": 2, "label": "C"},
    {"question_number": 6, "values": ["2520"]},
    {"question_number": 14, "values": ["2", "-12"]},
    ...
  ]
}

Rules:
- For multiple-choice rows: emit {"label": "A|B|C|D"}. If the printed key uses 1–4 numerics instead of letters, the post-processor maps 1→A, 2→B, 3→C, 4→D — emit either form.
- For student-produced response rows (numeric/free-response): emit {"values": [<string>, ...]}. Each string is one accepted form. If the printed key shows "2; -12" or "2, -12" (two acceptable answers to one question), emit each as a separate string in "values". Preserve fractions like "1/2" and decimals like "0.5" verbatim.
- Only include answers for this specific module (` + m.ID + `). Ignore other modules' tables on the same page.
- Include every numbered question listed.
- If a number is unreadable, omit that entry.
- Output ONLY the JSON object. No prose.`
}

func satCurvePrompt(_ Profile) string {
	return `You are looking at the Digital SAT scoring worksheet. Find the table that maps each section's raw score to a scaled score on the 200–800 scale.

Return ONE JSON object, no prose, no Markdown code fence:

{
  "sections": {
    "rw":   [{"raw": <int>, "scaled_low": <int 200..800>, "scaled_high": <int 200..800>}, ...],
    "math": [{"raw": <int>, "scaled_low": <int 200..800>, "scaled_high": <int 200..800>}, ...]
  }
}

Rules:
- Two sections: "rw" (Reading and Writing) and "math".
- The College Board prints two columns per section: "lower" and "upper". Use "scaled_low" for lower and "scaled_high" for upper. If the table only prints one column, set scaled_low == scaled_high to that value.
- "raw" is the integer raw-score row.
- Both scaled bounds must be integers in [200, 800].
- Output ONLY the JSON object. No prose.`
}
