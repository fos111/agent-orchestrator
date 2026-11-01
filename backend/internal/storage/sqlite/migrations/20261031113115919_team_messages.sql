-- +goose Up
-- Team broadcasts. A team is derived from the spawn-parent tree, so the only
-- durable team fact stored here is the root session a broadcast targeted.
-- Delivery is best-effort: one row per recipient records what happened, and a
-- failed member never rolls back the members that already received the message.
CREATE TABLE team_messages (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    team_root_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    -- Empty when the broadcast came from a surface with no owning session, such
    -- as the desktop team composer.
    sender_session_id TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 32768),
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_team_messages_root
ON team_messages(team_root_session_id, created_at DESC);

-- One row per member the broadcast attempted. pending is only visible while the
-- broadcast is running; the service records delivered or failed for every row.
CREATE TABLE team_message_deliveries (
    message_id TEXT NOT NULL REFERENCES team_messages(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'delivered', 'failed')),
    error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (message_id, session_id)
);

CREATE INDEX idx_team_message_deliveries_session
ON team_message_deliveries(session_id);

-- +goose Down
DROP INDEX idx_team_message_deliveries_session;
DROP TABLE team_message_deliveries;
DROP INDEX idx_team_messages_root;
DROP TABLE team_messages;
