---
title: SQLite store + embedded migrations
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - modernc.org/sqlite driver (pure-Go, no CGO)
  - WAL mode, foreign_keys ON, busy_timeout = 5000
  - migrations/0001_init.sql creates all MVP tables
  - migrations are embedded via go:embed and applied at startup
gitea_issue: 3
---

MVP tables: `test_templates`, `students`, `student_sessions`, `responses`, `highlights`, `test_scoring_curves`. Schema is in the plan. Pure-Go driver chosen to keep cross-compile trivial.
