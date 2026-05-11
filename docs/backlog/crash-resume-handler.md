---
title: Crash-resume on session GET
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - kill the server mid-test, restart, GET /api/sessions/:id
  - response includes current_module, deadline, responses, highlights
  - frontend re-hydrates store and the student lands on the right question
  - integration test: kill+restart pattern survives without data loss
---
