---
title: Raw → scaled scoring engine + results endpoint
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - on POST /submit, server tallies raw correct per section
  - looks up scaled score from test_scoring_curves
  - GET /api/sessions/:id/results returns scaled total + per-section + per-question breakdown
gitea_issue: 8
---
