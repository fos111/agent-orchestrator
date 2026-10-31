-- +goose Up
-- Dependency scheduling for sessions. scheduling_state answers "may this
-- session launch?" and is deliberately separate from delivery status and the
-- Kanban column: a session can be blocked while its delivery state is still
-- building. Existing rows default to immediate, so spawn behavior is unchanged
-- until a caller asks for dependencies.
ALTER TABLE sessions ADD COLUMN scheduling_state TEXT NOT NULL DEFAULT 'immediate'
    CHECK (scheduling_state IN ('immediate', 'queued', 'blocked', 'admitted', 'cancelled'));
ALTER TABLE sessions ADD COLUMN blocked_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN completed_at TIMESTAMP;
ALTER TABLE sessions ADD COLUMN admitted_at TIMESTAMP;
ALTER TABLE sessions ADD COLUMN admission_override INTEGER NOT NULL DEFAULT 0
    CHECK (admission_override IN (0, 1));
-- The normalized spawn request a queued or blocked row will launch with once
-- admitted. It contains no credentials.
ALTER TABLE sessions ADD COLUMN queued_spawn_config TEXT NOT NULL DEFAULT '';

-- One row per incoming edge. The primary key makes a duplicate edge impossible
-- at the storage layer; the CHECK rejects a self edge. ON DELETE RESTRICT keeps
-- a referenced session from disappearing while a dependent still names it.
CREATE TABLE session_dependencies (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    depends_on_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE RESTRICT,
    required_state TEXT NOT NULL CHECK (required_state IN ('ready', 'merged', 'completed')),
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (session_id, depends_on_session_id),
    CHECK (session_id <> depends_on_session_id)
);

CREATE INDEX idx_session_dependencies_dep
ON session_dependencies(depends_on_session_id);

CREATE INDEX idx_sessions_scheduling
ON sessions(project_id, scheduling_state, created_at)
WHERE scheduling_state IN ('queued', 'blocked');

CREATE INDEX idx_sessions_admitted
ON sessions(project_id, admitted_at)
WHERE scheduling_state = 'admitted';

-- Scheduling transitions must reach the event stream so the queue strip and the
-- board react without polling. session_updated is an invalidation-only nudge;
-- the payload carries the new scheduling facts for telemetry.
DROP TRIGGER IF EXISTS sessions_cdc_update;

-- +goose StatementBegin
CREATE TRIGGER sessions_cdc_update
AFTER UPDATE ON sessions
WHEN OLD.activity_state <> NEW.activity_state
    OR OLD.is_terminated <> NEW.is_terminated
    OR (OLD.first_signal_at IS NULL AND NEW.first_signal_at IS NOT NULL)
    OR OLD.preview_url <> NEW.preview_url
    OR OLD.preview_revision <> NEW.preview_revision
    OR OLD.display_name <> NEW.display_name
    OR OLD.terminate_on_pr_merge <> NEW.terminate_on_pr_merge
    OR OLD.is_pinned <> NEW.is_pinned
    OR OLD.pinned_at <> NEW.pinned_at
    OR (OLD.pinned_at IS NULL AND NEW.pinned_at IS NOT NULL)
    OR (OLD.pinned_at IS NOT NULL AND NEW.pinned_at IS NULL)
    OR OLD.session_mode <> NEW.session_mode
    OR OLD.auto_inject_review <> NEW.auto_inject_review
    OR OLD.auto_review_enabled <> NEW.auto_review_enabled
    OR OLD.harness <> NEW.harness
    OR OLD.runtime_launch_id <> NEW.runtime_launch_id
    OR OLD.agent_session_id <> NEW.agent_session_id
    OR OLD.native_transcript_path <> NEW.native_transcript_path
    OR OLD.auto_inject_ci <> NEW.auto_inject_ci
    OR OLD.latest_user_prompt_at IS NOT NEW.latest_user_prompt_at
    OR OLD.latest_interaction_at IS NOT NEW.latest_interaction_at
    OR OLD.provision_state <> NEW.provision_state
    OR OLD.provision_steps <> NEW.provision_steps
    OR OLD.branch_state <> NEW.branch_state
    OR OLD.branch <> NEW.branch
    OR OLD.branch_naming <> NEW.branch_naming
    OR OLD.scheduling_state <> NEW.scheduling_state
    OR OLD.blocked_reason <> NEW.blocked_reason
    OR OLD.completed_at IS NOT NEW.completed_at
    OR OLD.admitted_at IS NOT NEW.admitted_at
    OR OLD.admission_override <> NEW.admission_override
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object(
            'id', NEW.id,
            'activity', NEW.activity_state,
            'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END),
            'terminateOnPrMerge', json(CASE WHEN NEW.terminate_on_pr_merge THEN 'true' ELSE 'false' END),
            'previewUrl', NEW.preview_url,
            'previewRevision', NEW.preview_revision,
            'isPinned', json(CASE WHEN NEW.is_pinned THEN 'true' ELSE 'false' END),
            'mode', NEW.session_mode,
            'autoInjectReview', json(CASE WHEN NEW.auto_inject_review THEN 'true' ELSE 'false' END),
            'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END),
            'autoReviewEnabled', json(CASE WHEN NEW.auto_review_enabled THEN 'true' ELSE 'false' END),
            'provisionState', NEW.provision_state,
            'schedulingState', NEW.scheduling_state,
            'blockedReason', NEW.blocked_reason
        ),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS sessions_cdc_update;

-- +goose StatementBegin
CREATE TRIGGER sessions_cdc_update
AFTER UPDATE ON sessions
WHEN OLD.activity_state <> NEW.activity_state
    OR OLD.is_terminated <> NEW.is_terminated
    OR (OLD.first_signal_at IS NULL AND NEW.first_signal_at IS NOT NULL)
    OR OLD.preview_url <> NEW.preview_url
    OR OLD.preview_revision <> NEW.preview_revision
    OR OLD.display_name <> NEW.display_name
    OR OLD.terminate_on_pr_merge <> NEW.terminate_on_pr_merge
    OR OLD.is_pinned <> NEW.is_pinned
    OR OLD.pinned_at <> NEW.pinned_at
    OR (OLD.pinned_at IS NULL AND NEW.pinned_at IS NOT NULL)
    OR (OLD.pinned_at IS NOT NULL AND NEW.pinned_at IS NULL)
    OR OLD.session_mode <> NEW.session_mode
    OR OLD.auto_inject_review <> NEW.auto_inject_review
    OR OLD.auto_review_enabled <> NEW.auto_review_enabled
    OR OLD.harness <> NEW.harness
    OR OLD.runtime_launch_id <> NEW.runtime_launch_id
    OR OLD.agent_session_id <> NEW.agent_session_id
    OR OLD.native_transcript_path <> NEW.native_transcript_path
    OR OLD.auto_inject_ci <> NEW.auto_inject_ci
    OR OLD.latest_user_prompt_at IS NOT NEW.latest_user_prompt_at
    OR OLD.latest_interaction_at IS NOT NEW.latest_interaction_at
    OR OLD.provision_state <> NEW.provision_state
    OR OLD.provision_steps <> NEW.provision_steps
    OR OLD.branch_state <> NEW.branch_state
    OR OLD.branch <> NEW.branch
    OR OLD.branch_naming <> NEW.branch_naming
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object(
            'id', NEW.id,
            'activity', NEW.activity_state,
            'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END),
            'terminateOnPrMerge', json(CASE WHEN NEW.terminate_on_pr_merge THEN 'true' ELSE 'false' END),
            'previewUrl', NEW.preview_url,
            'previewRevision', NEW.preview_revision,
            'isPinned', json(CASE WHEN NEW.is_pinned THEN 'true' ELSE 'false' END),
            'mode', NEW.session_mode,
            'autoInjectReview', json(CASE WHEN NEW.auto_inject_review THEN 'true' ELSE 'false' END),
            'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END),
            'autoReviewEnabled', json(CASE WHEN NEW.auto_review_enabled THEN 'true' ELSE 'false' END),
            'provisionState', NEW.provision_state
        ),
        NEW.updated_at);
END;
-- +goose StatementEnd

DROP INDEX idx_sessions_admitted;
DROP INDEX idx_sessions_scheduling;
DROP INDEX idx_session_dependencies_dep;
DROP TABLE session_dependencies;
ALTER TABLE sessions DROP COLUMN queued_spawn_config;
ALTER TABLE sessions DROP COLUMN admission_override;
ALTER TABLE sessions DROP COLUMN admitted_at;
ALTER TABLE sessions DROP COLUMN completed_at;
ALTER TABLE sessions DROP COLUMN blocked_reason;
ALTER TABLE sessions DROP COLUMN scheduling_state;
