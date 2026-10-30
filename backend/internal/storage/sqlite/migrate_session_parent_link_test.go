package sqlite

import "testing"

func TestMigration20261030193302355SessionParentLinkDownRemovesIndexAndColumn(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261030193302355)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('cc-project', '/tmp/cc-project', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, created_at, updated_at, activity_last_at) VALUES ('parent', 'cc-project', 1, 'claude-code', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, parent_session_id, created_at, updated_at, activity_last_at) VALUES ('child', 'cc-project', 2, 'claude-code', 'parent', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)

	downTo(t, db, 20261028124744441)

	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'parent_session_id'`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if columnCount != 0 {
		t.Fatal("down migration left parent_session_id on sessions")
	}
	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_sessions_parent_session_id'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 0 {
		t.Fatal("down migration left idx_sessions_parent_session_id behind")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
	}
}
