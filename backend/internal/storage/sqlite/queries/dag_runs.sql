-- name: InsertDagRun :one
INSERT INTO dag_runs (
  id, project_id, root_session_id, name, status,
  cancel_requested_at, created_at, completed_at
) VALUES (
  ?, ?, ?, ?, ?, ?, ?, ?
)
RETURNING *;

-- name: SelectDagRunByID :one
SELECT * FROM dag_runs WHERE id = ?;

-- name: SelectDagRunsByProject :many
SELECT * FROM dag_runs
WHERE project_id = ?
ORDER BY created_at DESC, id DESC;

-- name: UpdateDagRunStatus :execrows
UPDATE dag_runs
SET status = ?, completed_at = ?
WHERE id = ? AND status IN ('pending', 'running');

-- name: CancelDagRun :execrows
UPDATE dag_runs
SET status = 'cancelled',
    cancel_requested_at = ?,
    completed_at = COALESCE(completed_at, ?)
WHERE id = ? AND status IN ('pending', 'running');

-- name: CountDagRunNodes :one
SELECT COUNT(*) FROM sessions WHERE dag_run_id = ?;
