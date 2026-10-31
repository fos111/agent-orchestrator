-- name: ListSessionDependencies :many
-- Incoming edges of one session, oldest first.
SELECT session_id, depends_on_session_id, required_state, created_at
FROM session_dependencies
WHERE session_id = ?
ORDER BY created_at, depends_on_session_id;

-- name: ListSessionDependenciesForSessions :many
-- Incoming edges of many sessions, used by the list read model.
SELECT session_id, depends_on_session_id, required_state, created_at
FROM session_dependencies
WHERE session_id IN (SELECT CAST(j.value AS TEXT) FROM json_each(?) AS j)
ORDER BY session_id, created_at, depends_on_session_id;

-- name: ListDependentSessionIDs :many
-- Reverse lookup for the detail panel.
SELECT session_id
FROM session_dependencies
WHERE depends_on_session_id = ?
ORDER BY session_id;

-- name: ListDependentSessionIDsForSessions :many
SELECT session_id, depends_on_session_id
FROM session_dependencies
WHERE depends_on_session_id IN (SELECT CAST(j.value AS TEXT) FROM json_each(?) AS j)
ORDER BY depends_on_session_id, session_id;

-- name: InsertSessionDependency :exec
INSERT INTO session_dependencies (session_id, depends_on_session_id, required_state, created_at)
VALUES (?, ?, ?, ?);

-- name: CountSessionDependencies :one
SELECT COUNT(*) AS count FROM session_dependencies WHERE session_id = ?;

-- name: ListSchedulingCandidates :many
-- Queued and blocked sessions of one project in FIFO order. A launch failure is
-- excluded: it never retries automatically in v1.
SELECT id FROM sessions
WHERE project_id = ?
  AND scheduling_state IN ('queued', 'blocked')
  AND blocked_reason <> 'launch_failed'
  AND is_terminated = 0
ORDER BY created_at, id;

-- name: ListSchedulingProjectIDs :many
SELECT DISTINCT project_id FROM sessions
WHERE project_id IS NOT NULL
  AND scheduling_state IN ('queued', 'blocked')
  AND is_terminated = 0;

-- name: CountActiveProjectSessions :one
-- Active means not terminated, not archived, and not cancelled. Queued and
-- blocked sessions do not occupy a slot.
SELECT COUNT(*) AS count FROM sessions
WHERE project_id = ?
  AND is_terminated = 0
  AND is_task_preparation = 0
  AND scheduling_state IN ('immediate', 'admitted');

-- name: SetSessionSchedulingState :execrows
-- Compare-and-set on the state the scheduler read. A newer decision (a kill, an
-- admission) wins because its state no longer matches.
UPDATE sessions
SET scheduling_state = sqlc.arg(scheduling_state),
    blocked_reason = sqlc.arg(blocked_reason),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND scheduling_state = sqlc.arg(expected_state);

-- name: AdmitSession :execrows
-- Admission is durable before launch: the compare-and-set proves this daemon
-- won the decision, and the previous state is recorded by the caller.
UPDATE sessions
SET scheduling_state = 'admitted',
    blocked_reason = '',
    admitted_at = sqlc.arg(admitted_at),
    admission_override = sqlc.arg(admission_override),
    -- provision_state has no 'queued' value: the storage column keeps the
    -- honest 'provisioning' and the read model derives the wire value.
    provision_state = 'provisioning',
    provision_error = '',
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND scheduling_state IN ('queued', 'blocked');

-- name: SetSessionAdmissionOverride :execrows
UPDATE sessions
SET admission_override = 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND admission_override = 0;

-- name: MarkSessionSchedulingLaunched :execrows
-- A successful launch leaves the admitted state and becomes an ordinary active
-- session. Guarded so a cancelled session is never resurrected.
UPDATE sessions
SET scheduling_state = 'immediate',
    blocked_reason = '',
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND scheduling_state = 'admitted';

-- name: SetSessionLaunchFailed :execrows
UPDATE sessions
SET scheduling_state = 'blocked',
    blocked_reason = 'launch_failed',
    provision_state = 'failed',
    provision_error = sqlc.arg(provision_error),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND scheduling_state = 'admitted';

-- name: CancelQueuedSession :execrows
-- Cancel a queued or blocked session without touching a workspace, because it
-- never owned one.
UPDATE sessions
SET scheduling_state = 'cancelled',
    blocked_reason = '',
    is_terminated = 1,
    provision_state = 'ready',
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND scheduling_state IN ('queued', 'blocked');

-- name: SetSessionCompleted :execrows
-- Explicit completion for artifact and no-PR sessions. Idempotent: the first
-- timestamp wins.
UPDATE sessions
SET completed_at = COALESCE(completed_at, sqlc.arg(completed_at)),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND is_terminated = 0;

-- name: SetSessionQueuedSpawnConfig :execrows
UPDATE sessions
SET queued_spawn_config = sqlc.arg(queued_spawn_config),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: ResetSessionForLaunchRetry :execrows
-- A user retry (admit) of a launch-failed session returns it to the queue.
UPDATE sessions
SET scheduling_state = 'queued',
    blocked_reason = '',
    provision_state = 'provisioning',
    provision_error = '',
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND scheduling_state = 'blocked'
  AND blocked_reason = 'launch_failed';

-- name: ListAdmittedSessionsWithoutWorkspace :many
-- Crash recovery: an admitted row with no workspace and no runtime was decided
-- but never launched.
SELECT id FROM sessions
WHERE project_id = ?
  AND scheduling_state = 'admitted'
  AND is_terminated = 0
  AND workspace_path = ''
  AND runtime_handle_id = ''
ORDER BY admitted_at, id;
