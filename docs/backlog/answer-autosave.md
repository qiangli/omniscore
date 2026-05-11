---
title: Debounced answer autosave (500ms)
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - PATCH /api/sessions/:id/answer fires 500ms after the last choice change
  - optimistic UI; failures roll back the local state
  - response includes server_now + module_deadline_at for skew correction
gitea_issue: 5
---
