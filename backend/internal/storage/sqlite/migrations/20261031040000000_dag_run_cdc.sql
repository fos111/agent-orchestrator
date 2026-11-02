-- DAG run CDC adds two project-level event types to change_log. SQLite cannot
-- widen a CHECK constraint in place, so this migration rebuilds change_log and
-- recreates every trigger that writes to it. The event names are the wire names
-- the events stream publishes (dag_run.created / dag_run.updated).
-- +goose Up
-- +goose StatementBegin
DROP TRIGGER IF EXISTS agent_model_catalog_cdc_insert;
DROP TRIGGER IF EXISTS agent_model_catalog_cdc_update;
DROP TRIGGER IF EXISTS agent_switches_cdc_insert;
DROP TRIGGER IF EXISTS agent_switches_cdc_update;
DROP TRIGGER IF EXISTS conversation_account_cdc_update;
DROP TRIGGER IF EXISTS conversation_activities_cdc_insert;
DROP TRIGGER IF EXISTS conversation_activities_cdc_update;
DROP TRIGGER IF EXISTS conversation_messages_cdc_insert;
DROP TRIGGER IF EXISTS conversation_messages_cdc_update;
DROP TRIGGER IF EXISTS conversation_turns_auto_resume_cdc_update;
DROP TRIGGER IF EXISTS conversation_turns_cdc_update;
DROP TRIGGER IF EXISTS pr_cdc_insert;
DROP TRIGGER IF EXISTS pr_cdc_update;
DROP TRIGGER IF EXISTS pr_checks_cdc_insert;
DROP TRIGGER IF EXISTS pr_checks_cdc_update;
DROP TRIGGER IF EXISTS pr_review_threads_cdc_insert;
DROP TRIGGER IF EXISTS pr_review_threads_cdc_update;
DROP TRIGGER IF EXISTS pr_session_cdc_update;
DROP TRIGGER IF EXISTS report_outputs_pr_created_cdc;
DROP TRIGGER IF EXISTS review_conversation_title_cdc_update;
DROP TRIGGER IF EXISTS review_run_cdc_insert;
DROP TRIGGER IF EXISTS review_run_cdc_update;
DROP TRIGGER IF EXISTS session_cleanup_facts_cdc_insert;
DROP TRIGGER IF EXISTS session_cleanup_facts_cdc_update;
DROP TRIGGER IF EXISTS session_interface_transitions_cdc_insert;
DROP TRIGGER IF EXISTS session_interface_transitions_cdc_update;
DROP TRIGGER IF EXISTS sessions_cdc_insert;
DROP TRIGGER IF EXISTS sessions_cdc_update;
DROP TRIGGER IF EXISTS sessions_hibernation_cdc_update;
DROP TRIGGER IF EXISTS sessions_stale_binary_cdc_update;
DROP TRIGGER IF EXISTS usage_bindings_cdc_insert;
DROP TRIGGER IF EXISTS usage_bindings_cdc_update;
DROP TRIGGER IF EXISTS usage_sources_cdc_update;
DROP TRIGGER IF EXISTS dag_runs_cdc_insert;
DROP TRIGGER IF EXISTS dag_runs_cdc_update;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE change_log_new (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT REFERENCES projects (id),
    session_id TEXT REFERENCES sessions (id),
    event_type TEXT NOT NULL
        CHECK (event_type IN (
            'session_created',
            'session_updated',
            'pr_created',
            'pr_updated',
            'pr_check_recorded',
            'pr_session_changed',
            'pr_review_thread_added',
            'pr_review_thread_resolved',
            'review_run_created',
            'review_run_updated',
            'dag_run.created',
            'dag_run.updated'
        )),
    payload    TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO change_log_new (seq, project_id, session_id, event_type, payload, created_at)
SELECT seq, project_id, session_id, event_type, payload, created_at
FROM change_log;

DROP INDEX IF EXISTS idx_change_log_project;
DROP INDEX IF EXISTS idx_change_log_created_at_seq;
DROP TABLE change_log;
ALTER TABLE change_log_new RENAME TO change_log;
CREATE INDEX idx_change_log_project ON change_log (project_id, seq);
CREATE INDEX idx_change_log_created_at_seq ON change_log (created_at, seq);
-- +goose StatementEnd

-- +goose StatementBegin
-- from 0151_global_agent_model_catalog_cdc.sql
CREATE TRIGGER agent_model_catalog_cdc_insert
AFTER INSERT ON agent_model_catalog
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'model_catalog', 'agentId', NEW.agent_id, 'projectId', projects.id),
        datetime('now')
    FROM projects
    WHERE (NEW.project_id = '' AND projects.archived_at IS NULL)
       OR projects.id = NEW.project_id;
END;

-- from 0151_global_agent_model_catalog_cdc.sql
CREATE TRIGGER agent_model_catalog_cdc_update
AFTER UPDATE ON agent_model_catalog
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'model_catalog', 'agentId', NEW.agent_id, 'projectId', projects.id),
        datetime('now')
    FROM projects
    WHERE (NEW.project_id = '' AND projects.archived_at IS NULL)
       OR projects.id = NEW.project_id;
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER agent_switches_cdc_insert
AFTER INSERT ON agent_switches
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id, 'session_updated', json_object('id', NEW.session_id), NEW.updated_at
    );
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER agent_switches_cdc_update
AFTER UPDATE ON agent_switches
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id, 'session_updated', json_object('id', NEW.session_id), NEW.updated_at
    );
END;

-- from 0174_conversation_account_cdc.sql
CREATE TRIGGER conversation_account_cdc_update
AFTER UPDATE OF account_json ON conversations
WHEN OLD.account_json IS NOT NEW.account_json
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated',
           json_object('id', s.id, 'sessionId', s.id, 'conversationId', NEW.id,
                       'activity', s.activity_state,
                       'isTerminated', json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),
           NEW.updated_at
    FROM sessions s WHERE s.id = NEW.current_session_id
    UNION ALL
    SELECT s.project_id, s.id, 'session_updated',
           json_object('id', s.id, 'sessionId', s.id, 'reviewId', r.id, 'conversationId', NEW.id,
                       'activity', s.activity_state,
                       'isTerminated', json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),
           NEW.updated_at
    FROM review r JOIN sessions s ON s.id = r.session_id WHERE r.id = NEW.current_review_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER conversation_activities_cdc_insert AFTER INSERT ON conversation_activities BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN sessions s ON s.id=c.current_session_id WHERE c.id=NEW.conversation_id
  UNION ALL SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'reviewId',r.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN review r ON r.id=c.current_review_id JOIN sessions s ON s.id=r.session_id WHERE c.id=NEW.conversation_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER conversation_activities_cdc_update AFTER UPDATE ON conversation_activities WHEN OLD.revision <> NEW.revision BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN sessions s ON s.id=c.current_session_id WHERE c.id=NEW.conversation_id
  UNION ALL SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'reviewId',r.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN review r ON r.id=c.current_review_id JOIN sessions s ON s.id=r.session_id WHERE c.id=NEW.conversation_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER conversation_messages_cdc_insert AFTER INSERT ON conversation_messages BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id, s.id, 'session_updated', json_object('id',s.id,'sessionId',s.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)), NEW.updated_at FROM conversations c JOIN sessions s ON s.id=c.current_session_id WHERE c.id=NEW.conversation_id
  UNION ALL SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'reviewId',r.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN review r ON r.id=c.current_review_id JOIN sessions s ON s.id=r.session_id WHERE c.id=NEW.conversation_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER conversation_messages_cdc_update AFTER UPDATE ON conversation_messages WHEN OLD.revision <> NEW.revision BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN sessions s ON s.id=c.current_session_id WHERE c.id=NEW.conversation_id
  UNION ALL SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'reviewId',r.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN review r ON r.id=c.current_review_id JOIN sessions s ON s.id=r.session_id WHERE c.id=NEW.conversation_id;
END;

-- from 20261019220421514_usage_limit_reset_cdc.sql
CREATE TRIGGER conversation_turns_auto_resume_cdc_update
AFTER UPDATE OF usage_limit_resets_at, usage_limit_auto_resume, usage_limit_resume_error ON conversation_turns
WHEN OLD.usage_limit_resets_at IS NOT NEW.usage_limit_resets_at
  OR OLD.usage_limit_auto_resume <> NEW.usage_limit_auto_resume
  OR OLD.usage_limit_resume_error <> NEW.usage_limit_resume_error
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated',
        json_object('id', s.id, 'sessionId', s.id, 'conversationId', NEW.conversation_id), CURRENT_TIMESTAMP
    FROM conversations c JOIN sessions s ON s.id = c.current_session_id
    WHERE c.id = NEW.conversation_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER conversation_turns_cdc_update AFTER UPDATE ON conversation_turns WHEN OLD.state <> NEW.state BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'reviewId',NEW.handled_by_review_id,'conversationId',NEW.conversation_id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),COALESCE(NEW.completed_at,NEW.started_at,NEW.requested_at) FROM sessions s WHERE s.id=NEW.handled_by_session_id;
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_cdc_insert
AFTER INSERT ON pr
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_created',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability),
        NEW.updated_at);
END;

-- from 0111_pr_auto_inject_ci_cdc.sql
CREATE TRIGGER pr_cdc_update
AFTER UPDATE ON pr
WHEN OLD.pr_state <> NEW.pr_state
    OR OLD.ci_state <> NEW.ci_state
    OR OLD.review_decision <> NEW.review_decision
    OR OLD.mergeability <> NEW.mergeability
    OR OLD.auto_inject_ci <> NEW.auto_inject_ci
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_updated',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability,
                    'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_checks_cdc_insert
AFTER INSERT ON pr_checks
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        NEW.created_at);
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_checks_cdc_update
AFTER UPDATE ON pr_checks
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        datetime('now'));
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_review_threads_cdc_insert
AFTER INSERT ON pr_review_threads
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_review_thread_added',
        json_object(
            'pr', NEW.pr_url,
            'thread', NEW.thread_id,
            'path', NEW.path,
            'line', NEW.line,
            'resolved', json(CASE WHEN NEW.resolved THEN 'true' ELSE 'false' END),
            'isBot', json(CASE WHEN NEW.is_bot THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_review_threads_cdc_update
AFTER UPDATE ON pr_review_threads
WHEN OLD.resolved <> NEW.resolved
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_review_thread_resolved',
        json_object(
            'pr', NEW.pr_url,
            'thread', NEW.thread_id,
            'path', NEW.path,
            'line', NEW.line,
            'resolved', json(CASE WHEN NEW.resolved THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_session_cdc_update
AFTER UPDATE ON pr
WHEN OLD.session_id <> NEW.session_id
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'pr_session_changed',
        json_object(
            'url', NEW.url,
            'fromSession', OLD.session_id,
            'toSession', NEW.session_id),
        NEW.updated_at);
END;

-- from 0169_reported_pr_cdc.sql
CREATE TRIGGER report_outputs_pr_created_cdc
AFTER INSERT ON report_outputs
WHEN NEW.kind = 'pr_created'
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT r.project_id, r.session_id, 'session_updated', json_object('id', r.session_id), r.created_at
    FROM reports r WHERE r.id = NEW.report_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER review_conversation_title_cdc_update AFTER UPDATE OF provider_title ON conversations
WHEN OLD.provider_title <> NEW.provider_title AND NEW.current_review_id IS NOT NULL BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id, s.id, 'session_updated', json_object('id', s.id, 'sessionId', s.id, 'reviewId', r.id, 'conversationId', NEW.id, 'activity', s.activity_state, 'isTerminated', json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)), NEW.updated_at
  FROM review r JOIN sessions s ON s.id = r.session_id WHERE r.id = NEW.current_review_id;
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER review_run_cdc_insert
AFTER INSERT ON review_run
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'review_run_created',
        json_object(
            'id', NEW.id,
            'reviewId', NEW.review_id,
            'sessionId', NEW.session_id,
            'pr', NEW.pr_url,
            'targetSha', NEW.target_sha,
            'status', NEW.status,
            'verdict', NEW.verdict,
            'triggerSource', NEW.trigger_source,
            'githubReviewId', NEW.github_review_id,
            'autoInjectReview', json(CASE WHEN NEW.auto_inject_review THEN 'true' ELSE 'false' END)
        ),
        NEW.created_at);
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER review_run_cdc_update
AFTER UPDATE ON review_run
WHEN OLD.status <> NEW.status
    OR OLD.verdict <> NEW.verdict
    OR OLD.body <> NEW.body
    OR OLD.github_review_id <> NEW.github_review_id
    OR OLD.auto_inject_review <> NEW.auto_inject_review
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'review_run_updated',
        json_object(
            'id', NEW.id,
            'reviewId', NEW.review_id,
            'sessionId', NEW.session_id,
            'pr', NEW.pr_url,
            'targetSha', NEW.target_sha,
            'status', NEW.status,
            'verdict', NEW.verdict,
            'triggerSource', NEW.trigger_source,
            'githubReviewId', NEW.github_review_id,
            'autoInjectReview', json(CASE WHEN NEW.auto_inject_review THEN 'true' ELSE 'false' END)
        ),
        datetime('now'));
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER session_cleanup_facts_cdc_insert
AFTER INSERT ON session_cleanup_facts
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id),
        datetime('now'));
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER session_cleanup_facts_cdc_update
AFTER UPDATE ON session_cleanup_facts
WHEN OLD.workspace_disposition <> NEW.workspace_disposition
    OR (OLD.runtime_released_at IS NULL) <> (NEW.runtime_released_at IS NULL)
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id),
        datetime('now'));
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER session_interface_transitions_cdc_insert
AFTER INSERT ON session_interface_transitions
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated',
           json_object('id', s.id, 'sessionId', s.id,
                       'interfaceTransitionId', NEW.id,
                       'interfaceTransitionPhase', NEW.phase,
                       'activity', s.activity_state,
                       'isTerminated', json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),
           NEW.updated_at
    FROM sessions s WHERE s.id = NEW.session_id;
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER session_interface_transitions_cdc_update
AFTER UPDATE ON session_interface_transitions
WHEN OLD.phase <> NEW.phase
    OR OLD.error_code <> NEW.error_code
    OR OLD.error_detail <> NEW.error_detail
    OR OLD.notice_acknowledged_at IS NOT NEW.notice_acknowledged_at
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated',
           json_object('id', s.id, 'sessionId', s.id,
                       'interfaceTransitionId', NEW.id,
                       'interfaceTransitionPhase', NEW.phase,
                       'activity', s.activity_state,
                       'isTerminated', json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),
           COALESCE(NEW.notice_acknowledged_at, NEW.updated_at)
    FROM sessions s WHERE s.id = NEW.session_id;
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER sessions_cdc_insert
AFTER INSERT ON sessions
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_created',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;

-- from 20261031031318871_session_dependencies.sql
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

-- from 0190_session_hibernation.sql
CREATE TRIGGER sessions_hibernation_cdc_update
AFTER UPDATE OF hibernated_at ON sessions
WHEN OLD.hibernated_at IS NOT NEW.hibernated_at
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'conversationId',
            (SELECT id FROM conversations WHERE current_session_id = NEW.id LIMIT 1)),
        CURRENT_TIMESTAMP);
END;

-- from 0213_session_stale_binary.sql
CREATE TRIGGER sessions_stale_binary_cdc_update
AFTER UPDATE OF stale_binary_since, stale_binary_launch, stale_binary_switch_failed_at ON sessions
WHEN OLD.stale_binary_since IS NOT NEW.stale_binary_since
    OR OLD.stale_binary_launch IS NOT NEW.stale_binary_launch
    OR OLD.stale_binary_switch_failed_at IS NOT NEW.stale_binary_switch_failed_at
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'conversationId',
            (SELECT id FROM conversations WHERE current_session_id = NEW.id LIMIT 1)),
        CURRENT_TIMESTAMP);
END;

-- from 0117_allow_kimi_usage.sql
CREATE TRIGGER usage_bindings_cdc_insert AFTER INSERT ON usage_bindings BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id),
            NEW.session_id, 'session_updated', json_object('id', NEW.session_id), NEW.updated_at);
END;

-- from 0117_allow_kimi_usage.sql
CREATE TRIGGER usage_bindings_cdc_update AFTER UPDATE ON usage_bindings BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id),
            NEW.session_id, 'session_updated', json_object('id', NEW.session_id), NEW.updated_at);
END;

-- from 0117_allow_kimi_usage.sql
CREATE TRIGGER usage_sources_cdc_update AFTER UPDATE ON usage_sources
WHEN OLD.anomaly_count IS NOT NEW.anomaly_count
  OR OLD.last_error_code IS NOT NEW.last_error_code
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, ub.session_id, 'session_updated', json_object('id', ub.session_id), NEW.updated_at
    FROM usage_bindings ub JOIN sessions s ON s.id = ub.session_id WHERE ub.id = NEW.binding_id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER dag_runs_cdc_insert
AFTER INSERT ON dag_runs
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'dag_run.created',
        json_object(
            'id', NEW.id,
            'projectId', NEW.project_id,
            'name', NEW.name,
            'status', NEW.status
        ),
        NEW.created_at);
END;

CREATE TRIGGER dag_runs_cdc_update
AFTER UPDATE OF status, cancel_requested_at, completed_at ON dag_runs
WHEN OLD.status <> NEW.status
    OR OLD.cancel_requested_at IS NOT NEW.cancel_requested_at
    OR OLD.completed_at IS NOT NEW.completed_at
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'dag_run.updated',
        json_object(
            'id', NEW.id,
            'projectId', NEW.project_id,
            'name', NEW.name,
            'status', NEW.status,
            'cancelRequestedAt', NEW.cancel_requested_at,
            'completedAt', NEW.completed_at
        ),
        CURRENT_TIMESTAMP);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS agent_model_catalog_cdc_insert;
DROP TRIGGER IF EXISTS agent_model_catalog_cdc_update;
DROP TRIGGER IF EXISTS agent_switches_cdc_insert;
DROP TRIGGER IF EXISTS agent_switches_cdc_update;
DROP TRIGGER IF EXISTS conversation_account_cdc_update;
DROP TRIGGER IF EXISTS conversation_activities_cdc_insert;
DROP TRIGGER IF EXISTS conversation_activities_cdc_update;
DROP TRIGGER IF EXISTS conversation_messages_cdc_insert;
DROP TRIGGER IF EXISTS conversation_messages_cdc_update;
DROP TRIGGER IF EXISTS conversation_turns_auto_resume_cdc_update;
DROP TRIGGER IF EXISTS conversation_turns_cdc_update;
DROP TRIGGER IF EXISTS pr_cdc_insert;
DROP TRIGGER IF EXISTS pr_cdc_update;
DROP TRIGGER IF EXISTS pr_checks_cdc_insert;
DROP TRIGGER IF EXISTS pr_checks_cdc_update;
DROP TRIGGER IF EXISTS pr_review_threads_cdc_insert;
DROP TRIGGER IF EXISTS pr_review_threads_cdc_update;
DROP TRIGGER IF EXISTS pr_session_cdc_update;
DROP TRIGGER IF EXISTS report_outputs_pr_created_cdc;
DROP TRIGGER IF EXISTS review_conversation_title_cdc_update;
DROP TRIGGER IF EXISTS review_run_cdc_insert;
DROP TRIGGER IF EXISTS review_run_cdc_update;
DROP TRIGGER IF EXISTS session_cleanup_facts_cdc_insert;
DROP TRIGGER IF EXISTS session_cleanup_facts_cdc_update;
DROP TRIGGER IF EXISTS session_interface_transitions_cdc_insert;
DROP TRIGGER IF EXISTS session_interface_transitions_cdc_update;
DROP TRIGGER IF EXISTS sessions_cdc_insert;
DROP TRIGGER IF EXISTS sessions_cdc_update;
DROP TRIGGER IF EXISTS sessions_hibernation_cdc_update;
DROP TRIGGER IF EXISTS sessions_stale_binary_cdc_update;
DROP TRIGGER IF EXISTS usage_bindings_cdc_insert;
DROP TRIGGER IF EXISTS usage_bindings_cdc_update;
DROP TRIGGER IF EXISTS usage_sources_cdc_update;
DROP TRIGGER IF EXISTS dag_runs_cdc_insert;
DROP TRIGGER IF EXISTS dag_runs_cdc_update;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE change_log_old (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT REFERENCES projects (id),
    session_id TEXT REFERENCES sessions (id),
    event_type TEXT NOT NULL
        CHECK (event_type IN (
            'session_created',
            'session_updated',
            'pr_created',
            'pr_updated',
            'pr_check_recorded',
            'pr_session_changed',
            'pr_review_thread_added',
            'pr_review_thread_resolved',
            'review_run_created',
            'review_run_updated'
        )),
    payload    TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO change_log_old (seq, project_id, session_id, event_type, payload, created_at)
SELECT seq, project_id, session_id, event_type, payload, created_at
FROM change_log
WHERE event_type NOT IN ('dag_run.created', 'dag_run.updated');

DROP INDEX IF EXISTS idx_change_log_project;
DROP INDEX IF EXISTS idx_change_log_created_at_seq;
DROP TABLE change_log;
ALTER TABLE change_log_old RENAME TO change_log;
CREATE INDEX idx_change_log_project ON change_log (project_id, seq);
CREATE INDEX idx_change_log_created_at_seq ON change_log (created_at, seq);
-- +goose StatementEnd

-- +goose StatementBegin
-- from 0151_global_agent_model_catalog_cdc.sql
CREATE TRIGGER agent_model_catalog_cdc_insert
AFTER INSERT ON agent_model_catalog
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'model_catalog', 'agentId', NEW.agent_id, 'projectId', projects.id),
        datetime('now')
    FROM projects
    WHERE (NEW.project_id = '' AND projects.archived_at IS NULL)
       OR projects.id = NEW.project_id;
END;

-- from 0151_global_agent_model_catalog_cdc.sql
CREATE TRIGGER agent_model_catalog_cdc_update
AFTER UPDATE ON agent_model_catalog
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT projects.id, NULL, 'session_updated',
        json_object('kind', 'model_catalog', 'agentId', NEW.agent_id, 'projectId', projects.id),
        datetime('now')
    FROM projects
    WHERE (NEW.project_id = '' AND projects.archived_at IS NULL)
       OR projects.id = NEW.project_id;
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER agent_switches_cdc_insert
AFTER INSERT ON agent_switches
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id, 'session_updated', json_object('id', NEW.session_id), NEW.updated_at
    );
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER agent_switches_cdc_update
AFTER UPDATE ON agent_switches
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id, 'session_updated', json_object('id', NEW.session_id), NEW.updated_at
    );
END;

-- from 0174_conversation_account_cdc.sql
CREATE TRIGGER conversation_account_cdc_update
AFTER UPDATE OF account_json ON conversations
WHEN OLD.account_json IS NOT NEW.account_json
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated',
           json_object('id', s.id, 'sessionId', s.id, 'conversationId', NEW.id,
                       'activity', s.activity_state,
                       'isTerminated', json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),
           NEW.updated_at
    FROM sessions s WHERE s.id = NEW.current_session_id
    UNION ALL
    SELECT s.project_id, s.id, 'session_updated',
           json_object('id', s.id, 'sessionId', s.id, 'reviewId', r.id, 'conversationId', NEW.id,
                       'activity', s.activity_state,
                       'isTerminated', json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),
           NEW.updated_at
    FROM review r JOIN sessions s ON s.id = r.session_id WHERE r.id = NEW.current_review_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER conversation_activities_cdc_insert AFTER INSERT ON conversation_activities BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN sessions s ON s.id=c.current_session_id WHERE c.id=NEW.conversation_id
  UNION ALL SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'reviewId',r.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN review r ON r.id=c.current_review_id JOIN sessions s ON s.id=r.session_id WHERE c.id=NEW.conversation_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER conversation_activities_cdc_update AFTER UPDATE ON conversation_activities WHEN OLD.revision <> NEW.revision BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN sessions s ON s.id=c.current_session_id WHERE c.id=NEW.conversation_id
  UNION ALL SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'reviewId',r.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN review r ON r.id=c.current_review_id JOIN sessions s ON s.id=r.session_id WHERE c.id=NEW.conversation_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER conversation_messages_cdc_insert AFTER INSERT ON conversation_messages BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id, s.id, 'session_updated', json_object('id',s.id,'sessionId',s.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)), NEW.updated_at FROM conversations c JOIN sessions s ON s.id=c.current_session_id WHERE c.id=NEW.conversation_id
  UNION ALL SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'reviewId',r.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN review r ON r.id=c.current_review_id JOIN sessions s ON s.id=r.session_id WHERE c.id=NEW.conversation_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER conversation_messages_cdc_update AFTER UPDATE ON conversation_messages WHEN OLD.revision <> NEW.revision BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN sessions s ON s.id=c.current_session_id WHERE c.id=NEW.conversation_id
  UNION ALL SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'reviewId',r.id,'conversationId',c.id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),NEW.updated_at FROM conversations c JOIN review r ON r.id=c.current_review_id JOIN sessions s ON s.id=r.session_id WHERE c.id=NEW.conversation_id;
END;

-- from 20261019220421514_usage_limit_reset_cdc.sql
CREATE TRIGGER conversation_turns_auto_resume_cdc_update
AFTER UPDATE OF usage_limit_resets_at, usage_limit_auto_resume, usage_limit_resume_error ON conversation_turns
WHEN OLD.usage_limit_resets_at IS NOT NEW.usage_limit_resets_at
  OR OLD.usage_limit_auto_resume <> NEW.usage_limit_auto_resume
  OR OLD.usage_limit_resume_error <> NEW.usage_limit_resume_error
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated',
        json_object('id', s.id, 'sessionId', s.id, 'conversationId', NEW.conversation_id), CURRENT_TIMESTAMP
    FROM conversations c JOIN sessions s ON s.id = c.current_session_id
    WHERE c.id = NEW.conversation_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER conversation_turns_cdc_update AFTER UPDATE ON conversation_turns WHEN OLD.state <> NEW.state BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id,s.id,'session_updated',json_object('id',s.id,'sessionId',s.id,'reviewId',NEW.handled_by_review_id,'conversationId',NEW.conversation_id,'activity',s.activity_state,'isTerminated',json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),COALESCE(NEW.completed_at,NEW.started_at,NEW.requested_at) FROM sessions s WHERE s.id=NEW.handled_by_session_id;
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_cdc_insert
AFTER INSERT ON pr
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_created',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability),
        NEW.updated_at);
END;

-- from 0111_pr_auto_inject_ci_cdc.sql
CREATE TRIGGER pr_cdc_update
AFTER UPDATE ON pr
WHEN OLD.pr_state <> NEW.pr_state
    OR OLD.ci_state <> NEW.ci_state
    OR OLD.review_decision <> NEW.review_decision
    OR OLD.mergeability <> NEW.mergeability
    OR OLD.auto_inject_ci <> NEW.auto_inject_ci
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_updated',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability,
                    'autoInjectCI', json(CASE WHEN NEW.auto_inject_ci THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_checks_cdc_insert
AFTER INSERT ON pr_checks
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        NEW.created_at);
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_checks_cdc_update
AFTER UPDATE ON pr_checks
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        datetime('now'));
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_review_threads_cdc_insert
AFTER INSERT ON pr_review_threads
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_review_thread_added',
        json_object(
            'pr', NEW.pr_url,
            'thread', NEW.thread_id,
            'path', NEW.path,
            'line', NEW.line,
            'resolved', json(CASE WHEN NEW.resolved THEN 'true' ELSE 'false' END),
            'isBot', json(CASE WHEN NEW.is_bot THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_review_threads_cdc_update
AFTER UPDATE ON pr_review_threads
WHEN OLD.resolved <> NEW.resolved
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_review_thread_resolved',
        json_object(
            'pr', NEW.pr_url,
            'thread', NEW.thread_id,
            'path', NEW.path,
            'line', NEW.line,
            'resolved', json(CASE WHEN NEW.resolved THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER pr_session_cdc_update
AFTER UPDATE ON pr
WHEN OLD.session_id <> NEW.session_id
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'pr_session_changed',
        json_object(
            'url', NEW.url,
            'fromSession', OLD.session_id,
            'toSession', NEW.session_id),
        NEW.updated_at);
END;

-- from 0169_reported_pr_cdc.sql
CREATE TRIGGER report_outputs_pr_created_cdc
AFTER INSERT ON report_outputs
WHEN NEW.kind = 'pr_created'
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT r.project_id, r.session_id, 'session_updated', json_object('id', r.session_id), r.created_at
    FROM reports r WHERE r.id = NEW.report_id;
END;

-- from 0149_reviewer_chat_conversations.sql
CREATE TRIGGER review_conversation_title_cdc_update AFTER UPDATE OF provider_title ON conversations
WHEN OLD.provider_title <> NEW.provider_title AND NEW.current_review_id IS NOT NULL BEGIN
  INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
  SELECT s.project_id, s.id, 'session_updated', json_object('id', s.id, 'sessionId', s.id, 'reviewId', r.id, 'conversationId', NEW.id, 'activity', s.activity_state, 'isTerminated', json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)), NEW.updated_at
  FROM review r JOIN sessions s ON s.id = r.session_id WHERE r.id = NEW.current_review_id;
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER review_run_cdc_insert
AFTER INSERT ON review_run
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'review_run_created',
        json_object(
            'id', NEW.id,
            'reviewId', NEW.review_id,
            'sessionId', NEW.session_id,
            'pr', NEW.pr_url,
            'targetSha', NEW.target_sha,
            'status', NEW.status,
            'verdict', NEW.verdict,
            'triggerSource', NEW.trigger_source,
            'githubReviewId', NEW.github_review_id,
            'autoInjectReview', json(CASE WHEN NEW.auto_inject_review THEN 'true' ELSE 'false' END)
        ),
        NEW.created_at);
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER review_run_cdc_update
AFTER UPDATE ON review_run
WHEN OLD.status <> NEW.status
    OR OLD.verdict <> NEW.verdict
    OR OLD.body <> NEW.body
    OR OLD.github_review_id <> NEW.github_review_id
    OR OLD.auto_inject_review <> NEW.auto_inject_review
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT project_id FROM sessions WHERE id = NEW.session_id),
        NEW.session_id,
        'review_run_updated',
        json_object(
            'id', NEW.id,
            'reviewId', NEW.review_id,
            'sessionId', NEW.session_id,
            'pr', NEW.pr_url,
            'targetSha', NEW.target_sha,
            'status', NEW.status,
            'verdict', NEW.verdict,
            'triggerSource', NEW.trigger_source,
            'githubReviewId', NEW.github_review_id,
            'autoInjectReview', json(CASE WHEN NEW.auto_inject_review THEN 'true' ELSE 'false' END)
        ),
        datetime('now'));
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER session_cleanup_facts_cdc_insert
AFTER INSERT ON session_cleanup_facts
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id),
        datetime('now'));
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER session_cleanup_facts_cdc_update
AFTER UPDATE ON session_cleanup_facts
WHEN OLD.workspace_disposition <> NEW.workspace_disposition
    OR (OLD.runtime_released_at IS NULL) <> (NEW.runtime_released_at IS NULL)
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'session_updated',
        json_object('id', NEW.session_id),
        datetime('now'));
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER session_interface_transitions_cdc_insert
AFTER INSERT ON session_interface_transitions
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated',
           json_object('id', s.id, 'sessionId', s.id,
                       'interfaceTransitionId', NEW.id,
                       'interfaceTransitionPhase', NEW.phase,
                       'activity', s.activity_state,
                       'isTerminated', json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),
           NEW.updated_at
    FROM sessions s WHERE s.id = NEW.session_id;
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER session_interface_transitions_cdc_update
AFTER UPDATE ON session_interface_transitions
WHEN OLD.phase <> NEW.phase
    OR OLD.error_code <> NEW.error_code
    OR OLD.error_detail <> NEW.error_detail
    OR OLD.notice_acknowledged_at IS NOT NEW.notice_acknowledged_at
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, s.id, 'session_updated',
           json_object('id', s.id, 'sessionId', s.id,
                       'interfaceTransitionId', NEW.id,
                       'interfaceTransitionPhase', NEW.phase,
                       'activity', s.activity_state,
                       'isTerminated', json(CASE WHEN s.is_terminated THEN 'true' ELSE 'false' END)),
           COALESCE(NEW.notice_acknowledged_at, NEW.updated_at)
    FROM sessions s WHERE s.id = NEW.session_id;
END;

-- from 0103_review_run_cdc.sql
CREATE TRIGGER sessions_cdc_insert
AFTER INSERT ON sessions
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_created',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;

-- from 20261031031318871_session_dependencies.sql
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

-- from 0190_session_hibernation.sql
CREATE TRIGGER sessions_hibernation_cdc_update
AFTER UPDATE OF hibernated_at ON sessions
WHEN OLD.hibernated_at IS NOT NEW.hibernated_at
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'conversationId',
            (SELECT id FROM conversations WHERE current_session_id = NEW.id LIMIT 1)),
        CURRENT_TIMESTAMP);
END;

-- from 0213_session_stale_binary.sql
CREATE TRIGGER sessions_stale_binary_cdc_update
AFTER UPDATE OF stale_binary_since, stale_binary_launch, stale_binary_switch_failed_at ON sessions
WHEN OLD.stale_binary_since IS NOT NEW.stale_binary_since
    OR OLD.stale_binary_launch IS NOT NEW.stale_binary_launch
    OR OLD.stale_binary_switch_failed_at IS NOT NEW.stale_binary_switch_failed_at
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'conversationId',
            (SELECT id FROM conversations WHERE current_session_id = NEW.id LIMIT 1)),
        CURRENT_TIMESTAMP);
END;

-- from 0117_allow_kimi_usage.sql
CREATE TRIGGER usage_bindings_cdc_insert AFTER INSERT ON usage_bindings BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id),
            NEW.session_id, 'session_updated', json_object('id', NEW.session_id), NEW.updated_at);
END;

-- from 0117_allow_kimi_usage.sql
CREATE TRIGGER usage_bindings_cdc_update AFTER UPDATE ON usage_bindings BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id),
            NEW.session_id, 'session_updated', json_object('id', NEW.session_id), NEW.updated_at);
END;

-- from 0117_allow_kimi_usage.sql
CREATE TRIGGER usage_sources_cdc_update AFTER UPDATE ON usage_sources
WHEN OLD.anomaly_count IS NOT NEW.anomaly_count
  OR OLD.last_error_code IS NOT NEW.last_error_code
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT s.project_id, ub.session_id, 'session_updated', json_object('id', ub.session_id), NEW.updated_at
    FROM usage_bindings ub JOIN sessions s ON s.id = ub.session_id WHERE ub.id = NEW.binding_id;
END;
-- +goose StatementEnd
