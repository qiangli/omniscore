---
name: convert-sat-pdf
description: Convert one Digital SAT practice PDF set (Bluebook test + optional Scoring + optional Explanation) into the omniscore content JSON layout under data/omni-data/sat/<slug>/.
user_invocable: true
---

# /convert-sat-pdf — Digital SAT PDF → omniscore content

Convert a Bluebook-format Digital SAT practice test PDF (and its optional
scoring + explanation companions) into the per-test self-contained content
layout omniscore reads at boot.

## When to use

You have one or more Digital SAT PDFs sitting under `data/raw/sat/<slug>/`
and want to publish the test so omniscore can run a session against it. The
SAT importer (`bin/sat-import`) handles 1–3 PDFs:

- **test** (required) — `*_Bluebook.pdf` with the four modules of MCQs.
- **scoring** (optional) — `*_Bluebook_Scoring.pdf` with the answer key + raw→scaled curve.
- **explanation** (optional) — `*_Bluebook_explanation.pdf` with per-question rationales.

Yellowbook-style "questions only" PDFs work too — just pass `-pdf-test` and
omit the others; scoring + rationales will be missing in the published JSON.

## Preconditions

- `pdftoppm` (from Poppler) on `PATH`.
- A vision LLM model spec, either via `-model vendor/model` or `$OMNI_MODEL`.
  Cloud vendors need `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, or
  `GEMINI_API_KEY`/`GOOGLE_API_KEY`; Ollama needs a local daemon at `-host`.
- The raw PDFs live under `data/raw/sat/<slug>/` (one directory per test).
  Don't reference paths outside `data/raw/` — that breaks the convention the
  `data/raw/README.md` documents.

## Build the binary

```bash
make sat-import     # produces bin/sat-import (not shipped in releases)
```

## Steps

1. **Stage the PDFs.** If you have a fresh PDF set, drop the files into a
   slug-named subdir of `data/raw/sat/`:
   ```bash
   mkdir -p data/raw/sat/digital-sat-practice-1
   mv "Digital SAT Practice#1_Bluebook.pdf"          data/raw/sat/digital-sat-practice-1/
   mv "Digital SAT Practice#1_Bluebook_Scoring.pdf"  data/raw/sat/digital-sat-practice-1/
   ```
   The slug becomes the test's directory name on output, so pick a stable
   one — kebab-case, no spaces, no `#`.

2. **Run the converter.** The simplest invocation lets the binary scan the
   `-in` directory for known PDF suffix patterns:
   ```bash
   ./bin/sat-import \
     -in    data/raw/sat/digital-sat-practice-1 \
     -slug  digital-sat-practice-1 \
     -title "Digital SAT Practice #1" \
     -model anthropic/claude-sonnet-4-6
   ```
   If the auto-scan picks the wrong files, pass them explicitly:
   ```bash
     -pdf-test       data/raw/sat/.../Digital SAT Practice#1_Bluebook.pdf \
     -pdf-scoring    data/raw/sat/.../Digital SAT Practice#1_Bluebook_Scoring.pdf \
     -pdf-explanation data/raw/sat/.../Digital SAT Practice#1_Bluebook_explanation.pdf
   ```
   Defaults you usually don't need to override:
   `-out data/omni-data`, `-workdir .import-cache/<slug>`, `-consistency 3`,
   `-dpi-classify 200`, `-dpi-extract 300`.

3. **Wait.** Expect 5–15 min on Ollama 90B, 2–5 min on cloud Sonnet/Gemini,
   per PDF. Each LLM response is cached under `<workdir>/llm-cache/` so
   re-runs cost zero LLM calls until you change DPI or prompt versions.

4. **Read the review log.** Open `.import-cache/<slug>/.review/<slug>.md` —
   it lists every question flagged for human inspection (disagreement
   between self-consistency rounds, missing answer-key entry, unbalanced
   KaTeX, etc.). Fix problems by hand-editing
   `data/omni-data/sat/<slug>/test.json` and re-running validate (step 6).

5. **Inspect the output.** The importer writes a self-contained subfolder:
   ```
   data/omni-data/sat/digital-sat-practice-1/
   ├── test.json
   ├── curve.json
   └── figures/qNN-{stem,a,b,c,d}.png
   ```
   Confirm the `test.json` has four modules (`rw-1`, `rw-2`, `math-1`,
   `math-2`), 4-choice MCQs labeled A–D, and `answer_label` set on every
   question. Confirm `curve.json` has two sections (`rw`, `math`) with the
   200–800 scale.

6. **Re-validate after manual edits.**
   ```bash
   ./bin/sat-import -validate-only data/omni-data/sat/<slug>/test.json
   ```
   Non-zero exit means at least one question still has a structural issue.

7. **Smoke-test in the runtime.**
   ```bash
   make build && ./bin/omniscore -bind 127.0.0.1:28080 -content data/omni-data
   ```
   Open http://127.0.0.1:28080, pick the new test, walk through a few
   questions, confirm section labels render as "Reading & Writing" / "Math"
   (from `frontend/src/lib/sectionLabels.ts`).

## Gotchas

- **Per-module answer keys**: SAT Scoring PDFs print four separate answer
  tables (RW Module 1, RW Module 2, Math Module 1, Math Module 2). The
  importer expects this — don't try to "merge" them in the PDF first.
- **Numeric vs letter labels**: SAT R&W answer keys sometimes print "1, 2,
  3, 4" instead of "A, B, C, D". The importer's `AnswerKeyDecoder` for the
  RW modules falls back to numeric → letter; if you see a slew of "answer
  key does not list question N" notes in the review log, the decoder
  probably failed — pass `-debug-answer-key` to dump the raw LLM response.
- **Passages**: R&W questions carry `passage_md`. Math questions don't. If
  the importer fills `passage_md` on a Math question, that's a
  classification bug — flag it in the review log.
- **Slug stability**: changing the slug after publish invalidates every
  in-progress session for that test (SQLite key is `slug`). Pick once.

## Bulk runs

To convert all four practice PDFs:
```bash
for n in 1 2 3 4; do
  ./bin/sat-import \
    -in    data/raw/sat/digital-sat-practice-$n \
    -slug  digital-sat-practice-$n \
    -title "Digital SAT Practice #$n" \
    -model anthropic/claude-sonnet-4-6
done
```
Each test gets its own `.import-cache/` so a failure on #3 doesn't poison
#1, #2, #4.
