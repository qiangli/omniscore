# Local Ollama models for SAT PDF conversion

Summary of the `cmd/sat-pdf-eval` runs against the four Bluebook SAT practice
PDFs. Raw run artifacts: `.run/pdf-eval/{*.log, out-*/, fb-workdir-*/}` (gitignored).

## Baseline extractors (no LLM)

| Source                     | qs   | stems≥.5 | chs_ok | mean_sim |
| -------------------------- | ---- | -------- | ------ | -------- |
| `ledongthuc` (pure-Go)     | 301  | 32%      | 27%    | 0.285    |
| `pdftotext` (shell)        | 177  | 8%       | 3%     | 0.102    |
| LLM reference              | 480  | —        | —      | 1.000    |

`ledongthuc` wins decisively on every metric; `pdftotext` reorders columns badly
on the Bluebook layout. **The shell-out path is not worth keeping for SAT.**

## Text — agent-with-tools fallback over `ledongthuc`

The fallback runs an Ollama agent that reads the `ledongthuc` text and fills
in missing questions/choices.

- **`qwen3.5:35b` — chosen.** `fallback.log` shows real progress (e.g. SAT #1
  rw-1: 19→33; fb_filled=23/61 across the test). Tool-calling works.
- `gemma3:27b` — **disqualified**: Ollama returns
  `400 does not support tools` on every call (`gemma3.log:5`). Don't try it
  again unless Ollama adds tool-call support upstream.
- Smaller `qwen3.5` variants were not evaluated; 35b fit on the target host so
  there was no reason to drop down.

## Vision — LLM page-render fallback

The fallback rasterizes each page with `pdftoppm` and asks a vision model to
extract the page contents.

- `qwen2.5vl:32b` — completes correctly when it doesn't time out (e.g. SAT #2:
  fb_filled=43/40), but with the old 90s HTTP timeout most pages failed.
- `qwen2.5vl:7b` — competitive fb_filled on the two PDFs it finished, but the
  run was abandoned early; not enough signal to call.
- `qwen3-vl:32b-instruct` (think=true) — timeouts dominated. Chain-of-thought
  tokens push past any reasonable per-page budget on structured extraction
  where reasoning doesn't help.
- **`qwen3-vl:32b-instruct` with `think=false` — the target.** Commit `b7fa364`
  raised the Ollama HTTP timeout to 5min and forces `Think: false` specifically
  to make this combination viable. *No post-fix eval is logged yet* — re-run
  `make sat-pdf-eval` and compare before committing to this as the final pick.

## Provisional defaults

- Text fallback: `ollama/qwen3.5:35b`
- Vision fallback: `ollama/qwen3-vl:32b-instruct` with `think=false`
  (pending a clean post-fix eval; `qwen2.5vl:32b` is the safe fallback if
  Qwen3-VL still misbehaves)
- Baseline extractor: `ledongthuc` only; drop `pdftotext` from the SAT path.

## Caveats

- All numbers are from four 2024 Bluebook SAT PDFs. AP and older SAT layouts
  may shift the verdict — re-run the eval per exam family before generalizing.
- `mean_sim` is a rough stem-similarity score, not a content-correctness
  score. Visual inspection of `.run/pdf-eval/out-*/` is still required before
  promoting a model.
- Costs and wall-clock times for these runs are not captured here; see the
  raw logs.
