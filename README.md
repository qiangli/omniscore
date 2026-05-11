# OmniScore

A local-first practice-test app for the Digital SAT and AP digital exams.
One Go binary, an embedded React UI, a single SQLite file. No cloud, no
account, no install — drop the binary on a laptop, share the LAN URL,
done.

OmniScore aims for **fidelity to the official Digital SAT (Bluebook)
experience** — the split-pane passage/question layout, the timer with
the 5-minute red lock, the keyboard chord conventions, the
question-navigator popup, the module-review screen — so students who
practice here feel at home on test day.

> Status: **MVP**. The student-facing exam loop, scoring, and
> crash-resume are working end-to-end against a demo SAT fixture.
> v0.2 (full Bluebook toolset: marks/eliminator/notes/Desmos/reference
> sheet) and v0.3 (PDF importer + AP support) are tracked in
> [`docs/backlog/`](docs/backlog/).

## Quick start

```bash
git clone https://github.com/qiangli/omniscore.git
cd omniscore
make build           # frontend (vite) + Go binary with embedded dist
./bin/omniscore      # bind 0.0.0.0:28080 by default
```

On boot it prints the landing-page URL — share it with students on the
same Wi-Fi:

```
  OmniScore is ready.

  Landing page: http://10.0.0.207:28080
```

Each visitor types a name and starts a session. Multiple students can
share one device or all bring their own — every "Join" creates a new
tracked student.

Flags:

```
-bind     host:port      default 0.0.0.0:28080
-db       path           default omniscore.db
-content  path           default ./content (expects tests/ and curves/ subdirs)
-key      path           default omniscore.key (HMAC cookie key; auto-created)
```

## What works today (MVP)

- **Two-pane resizable exam layout** (passage left, question right) with
  persisted drag ratio
- **Server-authoritative timer** with hide/show toggle, 5-minute red
  lock, and auto-submit on expiry; client renders countdowns from
  `module_deadline_at` plus a skew correction returned with every
  autosave
- **Question renderer** with KaTeX inline math, minimal Markdown
  (`**bold**`, `*italic*`, `> blockquote`), and A/B/C/D keyboard
  selection
- **Debounced answer autosave** (500 ms) with optimistic UI
- **Question navigator popup** showing answered / unanswered / current
- **Module review screen** before submit
- **Crash-resume**: kill the server mid-test, restart, every answer +
  highlight + remaining time is intact
- **Highlighter** (Ctrl+H toggle, yellow) using DOM TreeWalker
  character-offset anchors stable across React re-renders, persisted to
  SQLite per question with a `passage_version` guard
- **Scoring** raw → scaled per section from a curve JSON, plus a
  per-question results page with rationales and time-on-question
- **Open-join auth**: anyone with the LAN URL types a name and starts
  practicing — no roster, no signup, no email. Each landing-page visit
  generates a fresh tracking session
- **Pure-Go single binary**: cross-compiles to `darwin/{arm64,amd64}`,
  `linux/amd64`, `windows/amd64` with zero CGO

## Architecture

```
omniscore/
├── cmd/omniscore/                 # entrypoint, flags, LAN bind
├── internal/
│   ├── store/                     # SQLite (modernc.org/sqlite) + embedded migrations
│   ├── content/                   # test JSON schema + loader
│   ├── scoring/                   # raw → scaled curve lookup
│   ├── session/                   # lifecycle (create / resume / advance / submit / score) + highlights
│   └── server/                    # chi routes, signed-cookie auth, SPA static FS
├── migrations/0001_init.sql       # canonical migration (mirrored under internal/store/migrations for go:embed)
├── frontend/                      # Vite + React 18 + TypeScript + Tailwind
│   ├── src/pages/{Home,Exam,Review,Results}
│   ├── src/components/{Header,Footer,Passage,Question}
│   ├── src/lib/{api,timer,anchors,markdown,types}
│   └── embed.go                   # //go:embed all:dist — bakes the Vite build into the binary
├── content/tests/<slug>.json      # published practice tests (canonical, git-tracked)
├── content/curves/<slug>.json     # per-test raw → scaled lookup
└── docs/
    ├── backlog.md                 # Boss → Foreman → Worker task protocol
    └── backlog/<slug>.md          # one task per file (frontmatter: priority, state)
```

**Runtime is pure Go.** No Node, Python, or system runtime deps. The
React/TS app is built with Vite and embedded at compile time via
`go:embed`. Pure-Go SQLite driver means the binary cross-compiles
trivially.

**Source of truth.**
- Tests: `content/tests/<slug>.json` — SQLite is rebuilt from disk on
  every startup
- Tasks: `docs/backlog/<slug>.md` — Gitea issues are reconstructible
  from these files via the ycode reconciler

## Building from source

| Make target | What it does |
|---|---|
| `make help`    | List all targets |
| `make tidy`    | `go mod tidy` + `go fmt ./...` + `go vet ./...` |
| `make test`    | Go tests + vet + frontend `tsc --noEmit` |
| `make build`   | Vite production build + `go build` → `bin/omniscore` |
| `make install` | `go install ./cmd/omniscore` (frontend built first) |
| `make dev`     | Build then run on `0.0.0.0:28080` |
| `make clean`   | Nuke `bin/`, `frontend/dist`, `node_modules`, the DB, the cookie key |
| `make release` | Cross-compile binaries for darwin/{arm64,amd64}, linux/amd64, windows/amd64 |

Requirements: Go ≥ 1.22, Node ≥ 20.

## Authoring tests

Drop two files under `content/`:

```
content/tests/my-test.json
content/curves/my-test.json
```

`tests/<slug>.json` shape:

```json
{
  "slug": "my-test",
  "title": "My Test",
  "exam_type": "sat",
  "modules": [
    {
      "id": "rw-1",
      "section": "rw",
      "title": "Reading and Writing — Module 1",
      "time_limit_s": 1920,
      "questions": [
        {
          "id": "rw-1-q1",
          "passage_md": "…",
          "stem_md": "Which choice best …",
          "choices": [
            { "label": "A", "text_md": "…" },
            { "label": "B", "text_md": "…" },
            { "label": "C", "text_md": "…" },
            { "label": "D", "text_md": "…" }
          ],
          "answer_label": "C",
          "rationale_md": "…"
        }
      ]
    }
  ]
}
```

`curves/<slug>.json` shape:

```json
{
  "test_slug": "my-test",
  "sections": {
    "rw":   [{"raw": 0, "scaled": 200}, … , {"raw": N, "scaled": 800}],
    "math": [{"raw": 0, "scaled": 200}, … , {"raw": N, "scaled": 800}]
  }
}
```

Restart the binary — both files are loaded into SQLite idempotently
on startup. See [`content/tests/sat-practice-1.json`](content/tests/sat-practice-1.json)
for a working example.

## Roadmap

**v0.2 — Full Bluebook fidelity** (tracked under `docs/backlog/`)
- Mark-for-review (`Ctrl+Alt+V` / `Cmd+Shift+V`)
- Answer eliminator (ABC ⊘)
- Multi-color highlights + per-highlight notes
- Line reader (`Ctrl+L`)
- SAT Math reference sheet (`Ctrl+Alt+R`)
- Embedded Desmos calculator (`Ctrl+Alt+C`)
- Time-per-question heatmap on results

**v0.3 — Content pipeline + AP**
- PDF → JSON importer using Poppler + Ollama (local LLM)
- Teacher review UI for fixing extraction errors before publishing
- Adaptive Module 1 → Module 2 difficulty switch
- AP exam support: subject codes, MCQ + free-response

**Beyond**
- External auth integration (the open-join cookie layer is the swap
  point — see `internal/server/cookies.go`)
- Real-time teacher dashboard during a session

## License

[Apache License 2.0](LICENSE). Copyright 2026 Qiang Li.
