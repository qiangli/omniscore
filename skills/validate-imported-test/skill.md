---
name: validate-imported-test
description: Run structural validation against an imported test JSON (no LLM calls) and surface any questions that need human attention.
user_invocable: true
---

# /validate-imported-test — structural validation for imported tests

Run the importer's validation stage against a hand-edited or fresh-imported
`test.json` to catch structural issues before publishing.

## When to use

- After running `/convert-sat-pdf` or `/convert-ap-pdf`, when the review log
  flagged questions and you've fixed them by hand.
- After any manual edit to `data/omni-data/<exam>/<slug>/test.json` — e.g.
  retyping a passage, fixing a KaTeX expression, swapping a wrong
  answer_label.
- Before booting `bin/omniscore` against a fresh `data/omni-data/` if you
  want a fast pre-flight check that doesn't actually load into SQLite.

## What gets checked

Each question in each module is run through `ValidateQuestion`. The checks
are exam-aware via `ExamProfile`:

- Stem non-empty (or `stem_figure` present).
- Choice count matches `Profile.ChoiceLabels` (4 for SAT, 4-or-5 for AP).
- Choice labels match expected sequence (A–D or A–E).
- Each choice has text or a figure (not both empty).
- `answer_label` corresponds to an actual choice.
- KaTeX delimiters (`$...$`) balance; braces `{}` and brackets `[]` balance
  inside math regions.

What it does **not** check:

- Whether the answer is *correct* — that's an AP/SAT-pedagogy problem.
- Whether figures actually exist on disk — that's enforced at server boot
  by the static figure handler.
- Whether the curve JSON makes sense — it isn't loaded by validate-only.

## Run

```bash
# SAT
./bin/sat-import -validate-only data/omni-data/sat/<slug>/test.json

# AP
./bin/ap-import -validate-only data/omni-data/ap/<slug>/test.json
```

Exit code 0 = clean, non-zero = at least one issue. Issues print to
stderr per question, e.g.:

```
level=WARN msg=issue module=rw-1 question=rw-1-q3 issues="[choice C has empty text and no figure]"
```

## Run all imported tests

```bash
for f in data/omni-data/*/*/test.json; do
  case "$f" in
    *sat*) ./bin/sat-import -validate-only "$f" ;;
    *ap*)  ./bin/ap-import  -validate-only "$f" ;;
  esac
done
```

Returns non-zero on the first failing file.

## After validation passes

```bash
make build
./bin/omniscore -content data/omni-data -bind 127.0.0.1:28080
```

The loader at `internal/content/loader.go` will upsert every passing
`test.json` + `curve.json` into SQLite. Validation passing does NOT
guarantee the loader will accept the file — the loader requires `slug`,
`exam_type`, and `modules[]` to be present. Missing those is a
schema-level issue; the validator catches per-question issues only.
