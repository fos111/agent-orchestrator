-- +goose Up
ALTER TABLE sessions ADD COLUMN parent_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL;

CREATE INDEX idx_sessions_parent_session_id
ON sessions(parent_session_id)
WHERE parent_session_id IS NOT NULL;

-- +goose Down
DROP INDEX idx_sessions_parent_session_id;
ALTER TABLE sessions DROP COLUMN parent_session_id;
