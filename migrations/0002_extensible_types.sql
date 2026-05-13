-- 0002_extensible_types.sql — open the schema for new exam types and SAT-style curve ranges.
--
-- Two changes:
--   1. Drop the `exam_type` CHECK constraint on test_templates so future
--      exams (act, psat, gre, …) can be loaded without a schema bump.
--      SQLite can't ALTER CHECK in place, so we rebuild the table.
--   2. Add nullable scaled_score_low / scaled_score_high columns on
--      test_scoring_curves to capture SAT's lower/upper score bands.
--      Legacy rows leave the new columns NULL; new SAT rows populate all
--      three (scaled_score holds the midpoint for back-compat).

PRAGMA foreign_keys = OFF;

CREATE TABLE test_templates_new (
  slug              TEXT PRIMARY KEY,
  title             TEXT NOT NULL,
  exam_type         TEXT NOT NULL,
  json_blob         TEXT NOT NULL,
  published_at      INTEGER NOT NULL
);

INSERT INTO test_templates_new (slug, title, exam_type, json_blob, published_at)
  SELECT slug, title, exam_type, json_blob, published_at FROM test_templates;

DROP TABLE test_templates;
ALTER TABLE test_templates_new RENAME TO test_templates;

ALTER TABLE test_scoring_curves ADD COLUMN scaled_score_low INTEGER;
ALTER TABLE test_scoring_curves ADD COLUMN scaled_score_high INTEGER;

PRAGMA foreign_keys = ON;
