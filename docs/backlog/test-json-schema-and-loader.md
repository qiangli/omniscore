---
title: Test JSON schema + sat-practice-1 fixture
priority: p1
state: open
created: 2026-05-11T00:00:00Z
acceptance:
  - content/tests/sat-practice-1.json validates against the schema
  - content/curves/sat-practice-1.json provides raw→scaled lookups for both sections
  - internal/content loads JSON files into test_templates at startup (idempotent)
gitea_issue: 4
---

JSON shape:

```json
{
  "slug": "sat-practice-1",
  "title": "SAT Practice Test 1",
  "exam_type": "sat",
  "modules": [
    {
      "id": "rw-1",
      "section": "rw",
      "title": "Reading and Writing — Module 1",
      "time_limit_s": 1920,
      "questions": [
        {
          "id": "rw-1-q1",
          "passage_md": "…",
          "stem_md": "Which choice best …",
          "choices": [
            { "label": "A", "text_md": "…" },
            { "label": "B", "text_md": "…" },
            { "label": "C", "text_md": "…" },
            { "label": "D", "text_md": "…" }
          ],
          "answer_label": "B",
          "rationale_md": "…"
        }
      ]
    }
  ]
}
```

Curve shape:

```json
{
  "test_slug": "sat-practice-1",
  "sections": {
    "rw": [{ "raw": 0, "scaled": 200 }, …],
    "math": [{ "raw": 0, "scaled": 200 }, …]
  }
}
```
