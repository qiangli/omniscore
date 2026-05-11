-- 0001_init.sql — MVP schema for OmniScore
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS test_templates (
  slug              TEXT PRIMARY KEY,
  title             TEXT NOT NULL,
  exam_type         TEXT NOT NULL CHECK (exam_type IN ('sat','ap')),
  json_blob         TEXT NOT NULL,
  published_at      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS students (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  display_name      TEXT NOT NULL,
  joined_at         INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS student_sessions (
  id                  TEXT PRIMARY KEY,
  student_id          INTEGER NOT NULL REFERENCES students(id),
  test_slug           TEXT NOT NULL REFERENCES test_templates(slug),
  current_module      INTEGER NOT NULL DEFAULT 0,
  module_started_at   INTEGER NOT NULL,
  module_deadline_at  INTEGER NOT NULL,
  state               TEXT NOT NULL CHECK (state IN ('in_progress','submitted','expired')),
  raw_score           INTEGER,
  scaled_score        INTEGER,
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_student ON student_sessions(student_id);

CREATE TABLE IF NOT EXISTS responses (
  session_id          TEXT NOT NULL REFERENCES student_sessions(id) ON DELETE CASCADE,
  question_id         TEXT NOT NULL,
  choice              TEXT,
  first_answered_at   INTEGER,
  last_answered_at    INTEGER NOT NULL,
  time_on_question_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (session_id, question_id)
);

CREATE TABLE IF NOT EXISTS highlights (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id          TEXT NOT NULL REFERENCES student_sessions(id) ON DELETE CASCADE,
  question_id         TEXT NOT NULL,
  anchor_json         TEXT NOT NULL,
  color               TEXT NOT NULL DEFAULT 'yellow',
  note                TEXT,
  created_at          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_highlights_session_q ON highlights(session_id, question_id);

CREATE TABLE IF NOT EXISTS test_scoring_curves (
  test_slug           TEXT NOT NULL REFERENCES test_templates(slug),
  section             TEXT NOT NULL,
  raw_score           INTEGER NOT NULL,
  scaled_score        INTEGER NOT NULL,
  PRIMARY KEY (test_slug, section, raw_score)
);

CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);
