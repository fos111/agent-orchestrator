package sqlite

import "testing"

func TestMigration20261031031529839DagRunsUpCreatesRunAndNodeColumns(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261031031529839)

	for table, columns := range map[string][]string{
		"dag_runs": {"id", "project_id", "root_session_id", "name", "status", "cancel_requested_at", "created_at", "completed_at"},
		"sessions": {"dag_run_id", "dag_node_id"},
	} {
		for _, column := range columns {
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("%s.%s count = %d, want 1", table, column, count)
			}
		}
	}
	for _, index := range []string{"idx_dag_runs_project", "idx_sessions_dag_run", "idx_sessions_dag_node"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count = %d, want 1", index, count)
		}
	}
}

func TestMigration20261031031529839DagRunsDownRemovesRunState(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261031031529839)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('dag-project', '/tmp/dag-project', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO dag_runs (id, project_id, name, status, created_at) VALUES ('run-1', 'dag-project', 'feature-x', 'pending', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, dag_run_id, dag_node_id, created_at, updated_at, activity_last_at) VALUES ('node-1', 'dag-project', 1, 'claude-code', 'run-1', 'build', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)

	downTo(t, db, 20261030193302355)

	for _, column := range []string{"dag_run_id", "dag_node_id"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = ?`, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("down migration left sessions.%s behind", column)
		}
	}
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'dag_runs'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatal("down migration left dag_runs behind")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
	}
}
