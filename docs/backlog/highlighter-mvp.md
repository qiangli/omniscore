---
title: Highlighter MVP (rangy, yellow only, Ctrl+H)
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - select text in the passage, hit Ctrl+H → yellow highlight
  - highlight persists across reloads (POST /api/sessions/:id/highlights)
  - re-select an existing highlight + Ctrl+H removes it
  - passage is memoized on (question_id, passage_version); anchors store passage_version
gitea_issue: 6
---

Multi-color + per-highlight notes are v0.2.
