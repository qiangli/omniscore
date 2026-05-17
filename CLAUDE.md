# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

OmniScore — a local-first practice-test app that mirrors the Digital SAT and AP digital exam UI. **Runtime is a single pure-Go binary** with the React/TS frontend baked in via `go:embed`. SQLite (pure-Go `modernc.org/sqlite`) for storage. Zero outbound network calls at test-time. Apache-2.0 licensed (see `LICENSE` + `NOTICE`).

Source layout:

- `cmd/omniscore/main.go` — entrypoint, flags, LAN bind, ASCII landing-page banner. `-content` and `-key` flags accept `~` for `$HOME`.
- `cmd/ap-import/main.go` — AP-PDF→JSON importer binary. Thin (~150-line) wrapper that selects `profile.AP()` and delegates to `internal/importer/pipeline.Run`. Shells out to `pdftoppm` (Poppler) and a vision LLM. NOT shipped in releases.
- `cmd/sat-import/main.go` — Digital SAT (Bluebook) PDF→JSON importer. Same wrapper pattern, selects `profile.SAT()`. Accepts 1–3 PDFs (`-pdf-test` required; `-pdf-scoring` and `-pdf-explanation` optional) or scans `-in <dir>` for filename-suffix patterns. Output: `data/omni-data/sat/<slug>/`.
- `cmd/sat-pdf-eval/main.go` — local eval harness that compares pure-Go / shell-out PDF extractors against the LLM-extracted JSON. Dev-tool only; NOT shipped. Findings live in `docs/local-model-eval.md`.
- `internal/store/` — SQLite + embedded migrations. Canonical SQL is at `migrations/0001_init.sql` at the repo root and mirrored under `internal/store/migrations/` for `go:embed`; keep them in sync (guarded by `TestMigrationFilesIdentical`).
- `internal/admin/` — single-passphrase admin auth (`auth.go`) + teacher review/edit workflow mounted at `/api/admin/*` (login, whoami, list tests, get/patch question, per-question + bulk review state, **publish approved-only export**, sync status, users CRUD, tasks). Server reads the passphrase from `-admin-key` (default `omniscore.admin-key`); the file is auto-created on first boot and the cleartext passphrase is printed **once** in the landing-page banner — capture it then. **Publish workflow** (`publish.go`): `POST /api/admin/tests/{slug}/publish` filters questions by `question_review.status = 'approved'`, drops pending/flagged, and writes `<published-root>/<exam>/<slug>/{test.json, curve.json, figures/}` (figures de-duped to only those referenced by surviving questions). Question IDs are preserved across publish so review keys stay aligned on re-import. Rejects 422 if any module ends up empty. End-to-end content flow: **PDF → import → review → publish → boot a mock-test instance with `-content <published-root>`**.
- `internal/sync/` — outbox + pluggable `Adapter` for pushing canonical `users`/`standard_tests`/`tasks` to an external system. Driven by the `-sync` flag on the main binary (`noop` default; only `noop` is wired today, so the outbox just accumulates locally). `loop.go` is the polling driver, `store.go` is the outbox table, `adapter.go` is the interface.
- `internal/grading/` — manual grading for short-response questions (separate from the curve-driven scaled-score path in `internal/scoring/`).
- `internal/content/` — test JSON schema (`Test`, `Module`, `Question`, `Choice`, `Figure`) + disk loader. Loader signature is `LoadFromDisk(ctx, s, contentRoot)` and supports two layouts: legacy flat (`<root>/{tests,curves}/<slug>.json`) and per-test self-contained subfolder (`<root>/<exam>/<slug>/{test.json,curve.json,figures/*.png}`). Figure srcs in JSON are relative; the loader rewrites them to absolute `/api/figures/<exam>/<slug>/<file>` URLs at load time.
- `internal/scoring/` — raw → scaled curve lookup with nearest-neighbour fallback. Section names are user-defined (no SAT-only hardcoding).
- `internal/session/` — lifecycle (create / resume / advance / submit / score) + highlights. Server-authoritative timer math. `Summary` carries `ExamType` + `Subject` for the frontend section-label lookup.
- `internal/server/` — chi routes, signed cookie auth, embedded-FS SPA handler. `Server.New(...)` takes a `figuresRoot` arg (same path as `-content`); `/api/figures/{exam}/{slug}/*` is served by `internal/server/figures.go` (maps to `<root>/<exam>/<slug>/figures/<file>`, path-traversal guarded). End-to-end tests are `TestFullSessionFlow` (SAT, flat layout) and `TestFullSessionFlow_AP` (AP, per-test subfolder, 5-choice MCQs, embedded figures) in `internal/server/integration_test.go`.
- `internal/importer/profile/` — `Profile` struct + `AP()` / `SAT()` factories. Captures choice labels, sections, time limits, curve scale, classifier vocabulary, and per-stage prompt sets. The pipeline is parameterized on this — every exam-specific knob lives here, not scattered through stage files.
- `internal/importer/pipeline/` — shared, exam-agnostic stages (`classify.go`, `extract.go`, `answers.go`, `curve.go`, `validate.go`, `emit.go`) plus the orchestrator `pipeline.go` (`Run` does render → classify → extract → reconcile → curve → validate → emit). All stages take a `profile.Profile`; behavior diverges by profile, not by branching. Output schema matches `internal/content` types directly (no schema drift).
- `internal/importer/{render,vision}/` — Poppler wrapper and the `Provider` interface + `ollama` / `anthropic` / `openai` / `gemini` impls. `internal/importer/llm.go` is the `vendor/model` spec parser (stays in the parent `importer` package to avoid an import cycle with `vision/`).
- `frontend/` — Vite + React 18 + TS + Tailwind app; `frontend/embed.go` exposes `frontend/dist/` as an `embed.FS`. The cookie-auth swap point is `internal/server/cookies.go`. AP MCQs use 5 choices A–E; SAT MCQs use 4 choices A–D. Keyboard handler in `Question.tsx` accepts A–E. `Figure.tsx` renders inline `<img>`; `Passage.tsx` falls back to `passage_figure` when there's no markdown text. Section labels go through `lib/sectionLabels.ts`, keyed by `(exam_type[:subject], section)`.
- `content/tests/<slug>.json` + `content/curves/<slug>.json` — legacy flat-layout SAT demo that ships in the repo (still supported by the loader for back-compat).
- `tests/data/ap/<slug>/{test.json,curve.json,figures/*.png}` — committed *test/dev* fixture used by `TestFullSessionFlow_AP`. The hand-authored `ap-calc-bc-demo` lives at `tests/data/ap/ap-calc-bc-demo/`. Boot via `./bin/omniscore -content tests/data`.
- `data/omni-data/{sat,ap}/<slug>/{test.json,curve.json,figures/*.png}` — *private* sibling git repo, gitignored from this tree, populated by `cmd/{sat,ap}-import` runs against copyrighted PDFs. Boot the binary with `-content data/omni-data` (or `-content ~/omni-data`) once you have real content there.
- `data/raw/<exam>/...` — raw PDF inputs. In this repo `data/raw/` is conventionally a symlink to a sibling private location holding the copyrighted source PDFs. Each test set ideally lives in its own slug-named subfolder (`data/raw/sat/digital-sat-practice-1/{Bluebook.pdf,Scoring.pdf}`) so `bin/sat-import -in <subdir>` picks the right files by suffix.
- `skills/<skill>/skill.md` — operational playbooks for content-pipeline tasks (`/convert-sat-pdf`, `/convert-ap-pdf`, `/validate-imported-test`).
- `docs/backlog/<slug>.md` — Boss → Foreman → Worker task queue (see `docs/backlog.md`).

## Build / lint / test

The Makefile is the source of truth. Requires Go ≥ 1.22 and Node ≥ 20.

```bash
make help               # print all targets (the canonical list)
make build              # frontend (vite) + backend (go build with embedded dist) → bin/omniscore
make test               # go test ./... + go vet + frontend `npm run lint` (tsc --noEmit)
make dev                # build + run in the foreground on 0.0.0.0:28080
make start              # build + launch in the BACKGROUND; pid+log in .run/, content from $(CONTENT_DIR)
make stop               # kill the background server tracked in .run/omniscore.pid
make status             # report whether the background server is running (prints URLs)
make install            # go install ./cmd/omniscore (frontend built first)
make release            # cross-compile darwin/{arm64,amd64}, linux/amd64, windows/amd64
make clean              # nukes bin/, frontend/dist, node_modules, .run/, omniscore.db, omniscore.key, omniscore.admin-key
make tidy               # go mod tidy + go fmt + go vet
make ap-import          # build the AP-PDF→JSON importer to bin/ap-import (NOT shipped in releases)
make sat-import         # build the SAT-PDF→JSON importer to bin/sat-import (NOT shipped in releases)
make sat-pdf-eval       # build the PDF-extraction eval tool (dev only; NOT shipped)
```

`make start` accepts `CONTENT_DIR=` and `BIND=` overrides (defaults: `data/omni-data` and `0.0.0.0:28080`). It refuses to start if `$(CONTENT_DIR)` doesn't exist or another instance is already running — fix the underlying cause rather than deleting `.run/omniscore.pid` blindly.

Targeted commands:

```bash
go test ./...                                                  # Go unit + integration tests
go test ./internal/server/ -run TestFullSessionFlow -v         # SAT (flat) + AP (per-test subfolder) e2e
go test ./internal/server/ -run TestFullSessionFlow_AP -v      # AP-only e2e (per-test subfolder)
go test ./internal/importer/... -v                             # importer unit + round-trip + mock-provider e2e
go vet ./...
( cd frontend && npm install && npm run build )                # only the frontend
( cd frontend && npm run lint )                                # frontend typecheck only (tsc --noEmit)
./bin/omniscore -bind 127.0.0.1:28080 -content tests/data      # boot against the committed AP demo fixture
./bin/omniscore -bind 127.0.0.1:28080 -content data/omni-data  # boot against private real content
./bin/omniscore -bind 127.0.0.1:28080 -content ~/omni-data     # boot against a user-home content dir
./bin/ap-import  -validate-only data/omni-data/ap/<slug>/test.json   # validate a (hand-edited) AP test
./bin/sat-import -validate-only data/omni-data/sat/<slug>/test.json  # validate a (hand-edited) SAT test
./bin/ap-import  -pdf-test <pdf> -slug <slug> -title <title>
./bin/sat-import -in data/raw/sat/<slug> -slug <slug> -title <title>
```

`bin/omniscore` flags (defaults shown): `-bind 0.0.0.0:28080`, `-db omniscore.db`, `-content content` (accepts the flat legacy layout `<root>/{tests,curves}/<slug>.json` AND the per-test self-contained layout `<root>/<exam>/<slug>/{test.json,curve.json,figures/}`), `-published data/omni-approved` (destination tree for the publish endpoint; boot a separate mock-test instance with `-content <this>`), `-key omniscore.key` (HMAC cookie key, auto-created on first run), `-admin-key omniscore.admin-key` (single admin passphrase; auto-created on first boot and **printed once** in the landing banner — capture it then or `rm` the file to regenerate), `-sync noop` (external-sync adapter; only `noop` is wired today). `~` in `-content`, `-published`, `-key`, `-db`, `-admin-key` is expanded to `$HOME`.

Both importer binaries (`bin/{ap,sat}-import`) require `pdftoppm` (Poppler) plus a vision LLM, selected via `-model vendor/model` (or `OMNI_MODEL` env var). Supported vendors: `ollama`, `anthropic`, `openai`, `gemini`. API keys come from convention env vars: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`). Examples:

```bash
./bin/ap-import  -pdf-test <pdf> -slug <slug> -title <title> -model ollama/llama3.2-vision:90b
./bin/sat-import -in data/raw/sat/<slug>      -slug <slug> -title <title> -model anthropic/claude-sonnet-4-6
./bin/sat-import -pdf-test <test>.pdf -pdf-scoring <scoring>.pdf -slug <slug> -title <title> -model openai/gpt-4o

# Or set OMNI_MODEL once per shell session and omit -model entirely:
export OMNI_MODEL=anthropic/claude-sonnet-4-6
./bin/sat-import -in data/raw/sat/<slug> -slug <slug> -title <title>
```

Optional `-host http://...` overrides the Ollama URL (default `http://localhost:11434`); ignored for cloud vendors. Each PDF takes ~5–15 minutes end-to-end on a local 90B vision model with `-consistency 3`; cloud vendors are typically 2–5× faster but cost money (~$10–30 per PDF on Sonnet 4.6, ~$4 on Gemini 2.5 Pro). LLM responses are cached under `<workdir>/llm-cache/` so re-runs of the same import cost zero LLM calls until you change the model or DPI.

Default output for both importers is `-out data/omni-data` — they each append `<exam>/<slug>/` internally and emit `{test.json, curve.json, figures/, raw/, .import-manifest.json}` together in one self-contained subfolder. `raw/` carries verbatim copies of the source PDFs so teachers/admins can audit the conversion against the originals; `.import-manifest.json` fingerprints the run (SHA-256 of every input PDF + DPI + consistency + the `-model` spec) so re-running with identical inputs is a no-op (cache hit: no LLM traffic, no rasterization). Pass `-force` to bypass the cache.

## Working conventions

- **Tasks**: source of truth is `docs/backlog/<slug>.md`, not Gitea. Never edit Gitea state by hand expecting it to stick — the reconciler will overwrite it from markdown.
- **Test content**: source of truth is the on-disk JSON (`content/tests/<slug>.json` for the legacy SAT demo, or `<root>/<exam>/<slug>/test.json` for everything else), not SQLite. SQLite is rebuilt from disk on every startup, so do not edit the DB to change a question.
- **Per-test self-contained subfolder**: each published test lives in one directory containing `test.json`, `curve.json` (optional), and `figures/`. Moving/copying/deleting a test is a single-directory operation. The loader detects this layout automatically (any subdir of `<root>/<exam>/` containing a `test.json`).
- **Exam-specific knobs**: lift them into `internal/importer/profile/`. The pipeline itself (`internal/importer/pipeline/`) is exam-agnostic. Don't add `if examType == "ap"` branches inside pipeline code — add a `Profile` field instead.
- **Migration SQL**: `migrations/0001_init.sql` (root) and `internal/store/migrations/0001_init.sql` are duplicates; the embedded copy is what ships in the binary. Edit both, or you'll get drift between fresh-checkout dev and a release build.
- **Timer math is server-authoritative**: the client renders countdowns from `module_deadline_at` plus a periodic `server_now_ms` skew correction returned with every autosave. Never trust `Date.now()` alone — clock skew and tab-throttling will silently corrupt timing if you do.
- **Highlights**: persisted with a `passage_version` guard. Bumping passage text invalidates stored anchors by design — don't try to migrate them.
- **Open-join auth by design**: anyone with the LAN URL types a name and starts. There is no roster/signup. Each landing-page visit creates a fresh tracking session; this is the spec, not a bug.
- **Agent config**: shared defaults go in `.agents/ycode.json`; machine-local overrides in `.agents/ycode/settings.local.json` (gitignored).
- **Don't hand-edit the YCODE marker block** below — it's regenerated by `ycode init --refresh`. Put durable guidance above the marker.

## Backlog workflow

The backlog is the source of truth — Gitea issues and Loom leases are reconstructible from `docs/backlog/<slug>.md` (see `docs/backlog.md` for the full contract). Common commands:

```bash
ycode backlog new "<title>" --priority p1   # create a new task file
ycode backlog list [--priority p1]          # list tasks
ycode backlog show <slug>                   # render one
ycode backlog reconcile                     # force markdown → Gitea sync
```

To steer an autonomous Foreman loop: `ycode foreman {start|pause|resume|stop|skip|prio|tell|status}`. Touch `docs/backlog/PAUSE` for a kill-switch that survives Foreman restart.

<!-- BEGIN YCODE -->
## ycode

This repo expects [ycode](https://github.com/qiangli/ycode) running locally as
agentic infrastructure. When acting as an agentic coding tool, see
[`.agents/ycode/AGENTS.md`](.agents/ycode/AGENTS.md) for capability descriptions and when to
prefer them. Run `ycode init --refresh` to update this section.

### Self-Bootstrap (Foreman role)

You are the **Foreman** for this session. The Boss → Foreman → Worker
protocol is universal across every ycode-aware repo. When helping the
user plan, write tasks as `docs/backlog/<slug>.md` files (frontmatter:
`title`, `priority: p1|p2|p3`, `state: open`). When starting cold with no
specific user task, invoke `/foreman`. The skill body is at
`~/.config/ycode/skills/ycode-foreman/skill.md` (user-global, written
by `ycode init`; embedded in the binary as fallback). Boss control:
`ycode foreman pause/resume/stop/skip/prio/tell/status`.
Full protocol: [`docs/backlog.md`](docs/backlog.md). Available skills are
listed in [`.agents/ycode/AGENTS.md`](.agents/ycode/AGENTS.md#skills-available-via-ycode).
<!-- END YCODE -->
