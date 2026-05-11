# AGENTS.md

This file provides guidance to AI coding assistants working in this repository.

**Read [CLAUDE.md](./CLAUDE.md)** for Claude Code-specific conventions.

## Project Overview

This repository contains documentation and configuration for the omniscore project. Primary content lives in `docs/` including backlog items in `docs/backlog/`.

## Tooling

### ycode Integration

This repository is configured for ycode (local MCP-based agent tooling). Key capabilities:

- **`ycode-stdio`** — treesitter AST search for Go, Python, JS/TS, Rust, Java, C, Ruby. Prefer over grep when language is supported.
- **`ycode-gitea`** — local Gitea forge operations for repos, branches, PRs, issues.
- **`ycode-loom`** — workspace substrate for parallel sub-agents and isolated git workspaces.
- **`ycode-pulse`** — observability stack for traces, logs, metrics.

If tools return *connection refused*, run `ycode serve` first.

### Skills

Universal skills shipped with ycode can be invoked with a leading slash (e.g., `/foreman`). Skills are resolved from cwd → project → user (`~/.config/ycode/skills/`) → embedded.

| Skill | Purpose |
|-------|---------|
| `/foreman` | Boss → Foreman → Worker autonomous task loop. Picks up next prioritized backlog item. |

To customize globally, edit `~/.config/ycode/skills/<name>/skill.md`. To override per-repo, copy to `.agents/ycode/skills/<name>/skill.md`.

### Configuration

- Shared defaults: `.agents/ycode.json`
- Machine-local overrides: `.agents/ycode/settings.local.json`

## Workflow

Run `ycode init --refresh` to regenerate tool configurations and MCP entries.

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
