-- 0003_review_workflow.sql — admin review workflow + users/tasks sync model.
--
-- Three new concerns added in one migration:
--   1. Synced records: users, standard_tests, tasks. Source of truth is an
--      external system; OmniScore keeps a local mirror so it works offline.
--   2. Per-question review state (question_review) lives in SQLite because
--      json_blob is regenerated from disk on every boot.
--   3. Outbox + cursor tables back the pluggable sync.Adapter interface.
--      The wire format is deliberately deferred; the adapter is swappable.
--
-- student_sessions gains a nullable user_id so a registered student
-- (assigned a task) can be linked to a synced user. Open-join visitors
-- continue to use student_id only — user_id stays NULL for them.

CREATE TABLE IF NOT EXISTS users (
  id          TEXT PRIMARY KEY,
  role        TEXT NOT NULL CHECK (role IN ('student','teacher','admin')),
  name        TEXT NOT NULL,
  created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS standard_tests (
  id             TEXT PRIMARY KEY,
  name           TEXT NOT NULL,
  exam_type      TEXT,
  subject        TEXT,
  template_slug  TEXT REFERENCES test_templates(slug),
  created_at     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL REFERENCES users(id),
  test_id         TEXT NOT NULL REFERENCES standard_tests(id),
  from_datetime   INTEGER NOT NULL,
  to_datetime     INTEGER NOT NULL,
  created_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_user_window ON tasks(user_id, from_datetime);

CREATE TABLE IF NOT EXISTS question_review (
  test_slug      TEXT NOT NULL REFERENCES test_templates(slug),
  question_id    TEXT NOT NULL,
  status         TEXT NOT NULL CHECK (status IN ('pending','approved','flagged')),
  note           TEXT,
  reviewed_by    TEXT REFERENCES users(id),
  reviewed_at    INTEGER,
  PRIMARY KEY (test_slug, question_id)
);

CREATE TABLE IF NOT EXISTS sync_outbox (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type   TEXT NOT NULL,
  entity_id     TEXT NOT NULL,
  op            TEXT NOT NULL CHECK (op IN ('upsert','delete')),
  payload_json  TEXT NOT NULL,
  enqueued_at   INTEGER NOT NULL,
  delivered_at  INTEGER,
  attempts      INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON sync_outbox(delivered_at, id) WHERE delivered_at IS NULL;

CREATE TABLE IF NOT EXISTS sync_cursor (
  entity_type   TEXT PRIMARY KEY,
  cursor        TEXT,
  updated_at    INTEGER NOT NULL
);

ALTER TABLE student_sessions ADD COLUMN user_id TEXT REFERENCES users(id);
