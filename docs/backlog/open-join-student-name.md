---
title: Open-join student auth (display name + cookie)
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - landing page: name field + "Join" button
  - POST /api/students sets a signed cookie carrying student_id
  - all /api/sessions/* require the cookie; missing cookie → 401
  - no PIN, no roster, no email — that's v0.2+
---
