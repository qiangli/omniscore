---
title: Bootstrap Go module + chi server
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - go.mod targets Go 1.22+
  - cmd/omniscore/main.go starts a chi router on :8080
  - GET /healthz returns 200 OK
  - structured logging via log/slog
gitea_issue: 1
---

Greenfield Go module for OmniScore. Use `chi` for routing, stdlib `log/slog` for logs, stdlib `flag` (or `envconfig`) for config. No framework smell — this is the seed everything else builds on.
