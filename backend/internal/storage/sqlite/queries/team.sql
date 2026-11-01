-- name: InsertTeamMessage :exec
INSERT INTO team_messages (
    id, project_id, team_root_session_id, sender_session_id, body, created_at
) VALUES (?, ?, ?, ?, ?, ?);

-- name: InsertTeamMessageDelivery :exec
INSERT INTO team_message_deliveries (
    message_id, session_id, status, error
) VALUES (?, ?, ?, ?);

-- name: UpdateTeamMessageDelivery :execrows
UPDATE team_message_deliveries
SET status = ?, error = ?
WHERE message_id = ? AND session_id = ?;

-- name: GetTeamMessage :one
SELECT * FROM team_messages WHERE id = ?;

-- name: ListTeamMessagesByRoot :many
SELECT * FROM team_messages
WHERE team_root_session_id = ?
ORDER BY created_at DESC, id DESC;

-- name: ListTeamMessageDeliveries :many
SELECT * FROM team_message_deliveries
WHERE message_id = ?
ORDER BY session_id;
