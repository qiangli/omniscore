---
name: convert-ap-pdf
description: Convert a College Board AP released-exam PDF into the omniscore content JSON layout under data/omni-data/ap/<slug>/.
user_invocable: true
---

# /convert-ap-pdf — AP released-exam PDF → omniscore content

Convert one College Board AP released-exam PDF (currently tuned for AP
Calculus BC — other subjects need a profile addition) into the per-test
self-contained content layout omniscore reads at boot.

## When to use

You have an AP released-exam PDF under `data/raw/ap/<slug>/` and want to
publish it. The AP importer (`bin/ap-import`) extracts only the
**multiple-choice** sections — free-response pages are classified but not
yet extracted. Each PDF produces:

- 4 modules at most: `mcq_no_calc` (Section I Part A) and `mcq_calc` (Section
  I Part B). The `frq_*` classifier labels exist but the FRQ extraction stage
  is not wired up.
- One curve section: `mcq_total` mapping raw composite → AP grade 1–5.

## Preconditions

- `pdftoppm` (from Poppler) on `PATH`.
- A vision LLM model spec via `-model vendor/model` or `$OMNI_MODEL`.
  Same env-var conventions as `sat-import`.
- Raw PDF under `data/raw/ap/<slug>/`.

## Build the binary

```bash
make ap-import     # produces bin/ap-import (not shipped in releases)
```

## Steps

1. **Stage the PDF.**
   ```bash
   mkdir -p data/raw/ap/ap-calc-bc-2014
   mv "AP Calc BC 2014.pdf" data/raw/ap/ap-calc-bc-2014/
   ```

2. **Run the converter.**
   ```bash
   ./bin/ap-import \
     -pdf-test "data/raw/ap/ap-calc-bc-2014/AP Calc BC 2014.pdf" \
     -slug    ap-calc-bc-2014 \
     -title   "AP Calculus BC — 2014 Released Exam" \
     -model   anthropic/claude-sonnet-4-6
   ```
   Or use `-in <dir>` and let the binary scan for `*_Bluebook.pdf` /
   matching patterns (less reliable for AP since College Board doesn't use
   a consistent filename suffix; explicit `-pdf-test` is recommended).

3. **Wait.** Same time profile as SAT: 5–15 min Ollama, 2–5 min cloud per
   PDF. LLM responses cached under `<workdir>/llm-cache/`.

   **Import cache.** Once `data/omni-data/ap/<slug>/.import-manifest.json`
   exists, a subsequent invocation with byte-identical inputs and matching
   parameters returns instantly with no LLM traffic. Pass `-force` to
   override.

4. **Read the review log** at `.import-cache/<slug>/.review/<slug>.md` and
   hand-fix flagged questions in `data/omni-data/ap/<slug>/test.json`.

5. **Output layout.**
   ```
   data/omni-data/ap/ap-calc-bc-2014/
   ├── test.json
   ├── curve.json
   ├── figures/qNN-{stem,a,b,c,d,e}.png
   ├── raw/                          # source PDF copied verbatim for review
   │   └── AP Calc BC 2014.pdf
   └── .import-manifest.json         # input hashes + run parameters (cache key)
   ```
   AP MCQs have 5 choices A–E. Legacy 2014–2017 exams use 4 choices; the
   extraction prompt pads with empty E and the validator accepts both.

6. **Re-validate.**
   ```bash
   ./bin/ap-import -validate-only data/omni-data/ap/<slug>/test.json
   ```

7. **Smoke-test.**
   ```bash
   make build && ./bin/omniscore -bind 127.0.0.1:28080 -content data/omni-data
   ```
   Confirm section labels render as "MCQ Part A — No calculator" / "MCQ
   Part B — Calculator" (from `frontend/src/lib/sectionLabels.ts` keyed by
   `ap:calc_bc`).

## Adding a new AP subject

The current `profile.AP()` factory hardcodes Subject="calc_bc" and the
prompts mention "AP Calculus BC". To add e.g. AP Statistics:

1. Add `profile.APStat()` in `internal/importer/profile/ap_stat.go` with
   different `Subject`, prompts, and curve sections if needed.
2. Add a frontend section-label entry in `frontend/src/lib/sectionLabels.ts`
   keyed by `ap:stat` (or whatever subject code you pick).
3. Either extend `cmd/ap-import` with a `-subject` flag dispatching between
   profile factories, or fork to `cmd/ap-stat-import`. The former is
   cheaper; the latter is what we did for SAT.

## Gotchas

- **FRQ pages classified, not extracted**: pages tagged `frq_no_calc` /
  `frq_calc` are detected but the pipeline doesn't (yet) emit free-response
  questions. They show up in the classify summary log line but produce zero
  output JSON.
- **Curve is single-section**: `curve.json` always has one `mcq_total`
  section because AP scoring works on a composite (MCQ + FRQ) score. We
  emit only MCQ-derived curves; if a real composite is desired, edit
  `curve.json` by hand or extend the curve extractor.
- **Subject code is keyed in two places**: in `test.json` and in
  `sectionLabels.ts`. They must agree or the frontend falls back to raw
  section IDs.
