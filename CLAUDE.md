# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

OmniScore — a local-first practice-test app that mirrors the Digital SAT and AP digital exam UI. **Runtime is a single pure-Go binary** with the React/TS frontend baked in via `go:embed`. SQLite (pure-Go `modernc.org/sqlite`) for storage. Zero outbound network calls at test-time. Apache-2.0 licensed (see `LICENSE` + `NOTICE`).

Source layout:

- `cmd/omniscore/main.go` — entrypoint, flags, LAN bind, ASCII landing-page banner.
- `cmd/ap-import/main.go` — separate AP-PDF→JSON importer binary (Phase 4 of `docs/backlog/`); shells out to `pdftoppm` (Poppler) and Ollama. NOT shipped in releases — keep its deps out of the runtime binary.
- `internal/store/` — SQLite + embedded migrations. Canonical SQL is at `migrations/0001_init.sql` at the repo root and mirrored under `internal/store/migrations/` for `go:embed`; keep them in sync (guarded by `TestMigrationFilesIdentical`).
- `internal/content/` — test JSON schema (`Test`, `Module`, `Question`, `Choice`, `Figure`) + disk loader. Loader signature is `LoadFromDisk(ctx, s, contentRoot)` and supports two layouts: legacy flat (`<root>/{tests,curves}/`) and per-exam-type (`<root>/<exam>/{tests,curves,figures}/`). Figure srcs in JSON are relative; the loader rewrites them to absolute `/api/figures/<exam>/<slug>/<file>` URLs at load time.
- `internal/scoring/` — raw → scaled curve lookup with nearest-neighbour fallback. Section names are user-defined (no SAT-only hardcoding).
- `internal/session/` — lifecycle (create / resume / advance / submit / score) + highlights. Server-authoritative timer math. `Summary` carries `ExamType` + `Subject` for the frontend section-label lookup.
- `internal/server/` — chi routes, signed cookie auth, embedded-FS SPA handler. `Server.New(...)` takes a `figuresRoot` arg (same path as `-content`); `/api/figures/{exam}/{slug}/*` is served by `internal/server/figures.go` with a path-traversal guard. End-to-end tests are `TestFullSessionFlow` (SAT, flat layout) and `TestFullSessionFlow_AP` (AP, per-exam-type subdir, 5-choice MCQs, embedded figures) in `internal/server/integration_test.go`.
- `internal/importer/` — AP PDF→JSON pipeline driven by `cmd/ap-import`: `vision/` (Provider interface + `ollama` / `anthropic` / `openai` / `gemini` impls; the factory parser `llm.go` lives in the parent `importer` package to avoid an import cycle), `render/` (Poppler wrapper), and stages `classify.go` / `extract.go` / `answers.go` / `curve.go` / `validate.go` / `emit.go`. Output schema matches `internal/content` types directly (no schema drift). Provider is selected via `vendor/model` spec — see CLI section.
- `frontend/` — Vite + React 18 + TS + Tailwind app; `frontend/embed.go` exposes `frontend/dist/` as an `embed.FS`. The cookie-auth swap point is `internal/server/cookies.go`. AP MCQs use 5 choices A–E; keyboard handler in `Question.tsx` accepts the full A–E range. `Figure.tsx` renders inline `<img>`; `Passage.tsx` falls back to `passage_figure` when there's no markdown text. Section labels go through `lib/sectionLabels.ts`, keyed by `(exam_type[:subject], section)`.
- `content/tests/<slug>.json` + `content/curves/<slug>.json` — legacy flat-layout SAT demo that ships in the repo (still supported by the loader).
- `tests/data/<exam>/{tests,curves,figures}/<slug>.*` — committed *test/dev* fixtures used for end-to-end smoke testing (`./bin/omniscore -content tests/data`). The hand-authored `ap-calc-bc-demo` lives here. Not real content; safe to publish.
- `data/omni-data/{sat,ap}/{tests,curves,figures}/` — *private* sibling git repo, gitignored from this tree, populated by `cmd/ap-import` runs against copyrighted PDFs. Boot the binary with `-content data/omni-data` once you have real content there.
- `docs/backlog/<slug>.md` — Boss → Foreman → Worker task queue (see `docs/backlog.md`).

## Build / lint / test

The Makefile is the source of truth. Requires Go ≥ 1.22 and Node ≥ 20.

```bash
make build              # frontend (vite) + backend (go build with embedded dist) → bin/omniscore
make test               # go test ./... + go vet + frontend `npm run lint` (tsc --noEmit)
make dev                # build + run on 0.0.0.0:28080 (the binary's default bind)
make install            # go install ./cmd/omniscore (frontend built first)
make release            # cross-compile darwin/{arm64,amd64}, linux/amd64, windows/amd64
make clean              # nukes bin/, frontend/dist, node_modules, omniscore.db, omniscore.key
make tidy               # go mod tidy + go fmt + go vet
make ap-import          # build the AP-PDF→JSON importer to bin/ap-import (NOT shipped in releases)
```

Targeted commands:

```bash
go test ./...                                                  # Go unit + integration tests
go test ./internal/server/ -run TestFullSessionFlow -v         # both SAT + AP e2e flows
go test ./internal/server/ -run TestFullSessionFlow_AP -v      # AP-only e2e flow (Phase 4 contract)
go test ./internal/importer/... -v                             # importer unit + round-trip
go vet ./...
( cd frontend && npm install && npm run build )                # only the frontend
( cd frontend && npm run lint )                                # frontend typecheck only (tsc --noEmit)
./bin/omniscore -bind 127.0.0.1:28080 -content tests/data      # boot against the AP demo fixture
./bin/omniscore -bind 127.0.0.1:28080 -content data/omni-data  # boot against private real content
./bin/ap-import -validate-only data/omni-data/ap/tests/<slug>.json    # validate a (hand-edited) test
./bin/ap-import -pdf <pdf> -slug <slug> -title <title> -out data/omni-data/ap -workdir .import-cache/<slug>
```

`bin/omniscore` flags (defaults shown): `-bind 0.0.0.0:28080`, `-db omniscore.db`, `-content content` (accepts both flat `<root>/{tests,curves}/` and per-exam-type `<root>/<exam>/{tests,curves,figures}/`), `-key omniscore.key` (HMAC cookie key, auto-created on first run).

`bin/ap-import` requires `pdftoppm` (Poppler) plus a vision LLM, selected via `-model vendor/model` (or `OMNI_MODEL` env var). Supported vendors: `ollama`, `anthropic`, `openai`, `gemini`. API keys come from convention env vars: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`). Examples:

```bash
./bin/ap-import -pdf <pdf> -slug <slug> -title <title> -model ollama/llama3.2-vision:90b
./bin/ap-import ... -model anthropic/claude-sonnet-4-6
./bin/ap-import ... -model openai/gpt-4o
./bin/ap-import ... -model gemini/gemini-2.5-pro

# Or set OMNI_MODEL once per shell session and omit -model entirely:
export OMNI_MODEL=anthropic/claude-sonnet-4-6
./bin/ap-import -pdf <pdf> -slug <slug> -title <title>
```

Optional `-host http://...` overrides the Ollama URL (default `http://localhost:11434`); ignored for cloud vendors. Each PDF takes ~5–15 minutes end-to-end on a local 90B vision model with `-consistency 3`; cloud vendors are typically 2–5× faster but cost money (~$10–30 per PDF on Sonnet 4.6, ~$4 on Gemini 2.5 Pro).

## Working conventions

- **Tasks**: source of truth is `docs/backlog/<slug>.md`, not Gitea. Never edit Gitea state by hand expecting it to stick — the reconciler will overwrite it from markdown.
- **Test content**: source of truth is `content/tests/<slug>.json` and `content/curves/<slug>.json`, not SQLite. SQLite is rebuilt from disk on every startup, so do not edit the DB to change a question.
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
