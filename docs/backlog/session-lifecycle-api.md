---
title: Session lifecycle + API surface
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - POST /api/sessions creates a session, returns deadline + first module
  - GET /api/sessions/:id returns resumable state
  - module_deadline_at is server-authoritative
  - integration test drives the full flow with httptest
gitea_issue: 9
---

Endpoints:

- `POST /api/students` `{display_name}` → cookie session
- `GET  /api/tests`
- `GET  /api/tests/:slug` (answers stripped pre-submit)
- `POST /api/sessions` `{test_slug}`
- `GET  /api/sessions/:id`
- `PATCH /api/sessions/:id/answer` `{question_id, choice, time_on_question_ms}`
- `POST /api/sessions/:id/advance`
- `POST /api/sessions/:id/submit`
- `POST /api/sessions/:id/highlights` / `DELETE /api/sessions/:id/highlights/:hid`
- `GET  /api/sessions/:id/results`
