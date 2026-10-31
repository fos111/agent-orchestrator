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

-- name: ListSessionsByDagRun :many
SELECT id, project_id, num, issue_id, parent_session_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, agent_session_id_launch_id, native_identity_observed_at, prompt,
    created_at, updated_at, revision, display_name, first_signal_at, preview_url,
    preview_revision, cleanup_generation, runtime_launch_id,
    workspace_repo_path, terminate_on_pr_merge, diff_base_sha, diff_base_ref,
    reviewer_harness, reviewer_agent_config, is_pinned, pinned_at,
    session_mode, provider_conversation_id, controller_generation, browser_capability_verifier,
    artifact_dir, session_output_type,
    latest_user_prompt, latest_user_prompt_at, latest_interaction_at, latest_assistant_update, latest_assistant_update_at,
    conversation_checkpoint_state, conversation_checkpoint_generation, conversation_checkpoint_native_id,
    conversation_checkpoint_unsettled, conversation_checkpoint_turn_id, native_checkpoint_evidence,
    native_transcript_path, auto_inject_review, auto_inject_ci, auto_review_enabled, model, effort, session_permissions,
    requested_output,
    provision_state, provision_error, provision_steps, branch_state, branch_naming, temporary_branch, is_task_preparation, automation_run_id, automation_launch_completed,
    claude_activity_facts, codex_activity_facts,
    hibernated_at, stale_binary_since, stale_binary_version, stale_binary_launch, stale_binary_switch_failed_at,
    client_request_id, client_request_hash, client_request_committed,
    runtime_released_launch_id,
    scheduling_state, blocked_reason, completed_at, admitted_at, admission_override, queued_spawn_config,
    dag_run_id, dag_node_id
FROM sessions WHERE dag_run_id = ?
ORDER BY created_at, id;

-- name: ListSessionsByDagRunIDs :many
SELECT id, project_id, num, issue_id, parent_session_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, agent_session_id_launch_id, native_identity_observed_at, prompt,
    created_at, updated_at, revision, display_name, first_signal_at, preview_url,
    preview_revision, cleanup_generation, runtime_launch_id,
    workspace_repo_path, terminate_on_pr_merge, diff_base_sha, diff_base_ref,
    reviewer_harness, reviewer_agent_config, is_pinned, pinned_at,
    session_mode, provider_conversation_id, controller_generation, browser_capability_verifier,
    artifact_dir, session_output_type,
    latest_user_prompt, latest_user_prompt_at, latest_interaction_at, latest_assistant_update, latest_assistant_update_at,
    conversation_checkpoint_state, conversation_checkpoint_generation, conversation_checkpoint_native_id,
    conversation_checkpoint_unsettled, conversation_checkpoint_turn_id, native_checkpoint_evidence,
    native_transcript_path, auto_inject_review, auto_inject_ci, auto_review_enabled, model, effort, session_permissions,
    requested_output,
    provision_state, provision_error, provision_steps, branch_state, branch_naming, temporary_branch, is_task_preparation, automation_run_id, automation_launch_completed,
    claude_activity_facts, codex_activity_facts,
    hibernated_at, stale_binary_since, stale_binary_version, stale_binary_launch, stale_binary_switch_failed_at,
    client_request_id, client_request_hash, client_request_committed,
    runtime_released_launch_id,
    scheduling_state, blocked_reason, completed_at, admitted_at, admission_override, queued_spawn_config,
    dag_run_id, dag_node_id
FROM sessions WHERE dag_run_id IN (sqlc.slice('dag_run_ids'))
ORDER BY created_at, id;

-- name: CancelDagRunNodes :execrows
UPDATE sessions
SET scheduling_state = 'cancelled',
    is_terminated = 1,
    updated_at = ?
WHERE dag_run_id = ?
    AND scheduling_state IN ('queued', 'blocked')
    AND is_terminated = 0;
