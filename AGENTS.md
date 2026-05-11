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
