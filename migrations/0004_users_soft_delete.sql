-- 0004_users_soft_delete.sql — soft-delete column for admin-managed users.
--
-- /api/admin/users supports create/update/delete by the admin-key holder.
-- We tombstone rather than hard-delete so tasks and student_sessions that
-- FK into users(id) keep resolving against historical rows (a deleted
-- teacher account must not break audit trails on tests they assigned).
--
-- NULL = active; INTEGER ms-epoch = the moment the user was deleted.

ALTER TABLE users ADD COLUMN deleted_at INTEGER;
