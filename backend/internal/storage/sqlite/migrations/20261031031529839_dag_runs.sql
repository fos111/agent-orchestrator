-- +goose Up
CREATE TABLE dag_runs (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  root_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  name TEXT NOT NULL,
  status TEXT NOT NULL
    CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
  cancel_requested_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL,
  completed_at TIMESTAMP
);

ALTER TABLE sessions ADD COLUMN dag_run_id TEXT REFERENCES dag_runs(id) ON DELETE SET NULL;
ALTER TABLE sessions ADD COLUMN dag_node_id TEXT;

CREATE INDEX idx_dag_runs_project ON dag_runs(project_id, created_at DESC);
CREATE INDEX idx_sessions_dag_run ON sessions(dag_run_id)
WHERE dag_run_id IS NOT NULL;
CREATE UNIQUE INDEX idx_sessions_dag_node ON sessions(dag_run_id, dag_node_id)
WHERE dag_run_id IS NOT NULL;

-- +goose Down
DROP INDEX idx_sessions_dag_node;
DROP INDEX idx_sessions_dag_run;
DROP INDEX idx_dag_runs_project;
ALTER TABLE sessions DROP COLUMN dag_node_id;
ALTER TABLE sessions DROP COLUMN dag_run_id;
DROP TABLE dag_runs;
