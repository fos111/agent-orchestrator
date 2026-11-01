package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

const (
	sessionCrashMigrationPreviousVersion int64 = 20261031031318871
	sessionCrashMigrationVersion         int64 = 20261031185751173
)

// TestSessionCrashNotificationsMigration covers the CHECK widening rebuild:
// existing rows and both dedupe indexes survive, the new terminal type is
// accepted, and the down migration removes only crash rows and restores the
// previous constraint.
func TestSessionCrashNotificationsMigration(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, sessionCrashMigrationPreviousVersion)
	const seededAt = "2026-10-31T09:00:00Z"
	mustExec(t, db, `
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES
    ('notice-needs-input', 'sess-1', NULL, '', 'needs_input', 'Waiting on you',
     'Agent needs input', 'unread', ?, NULL, NULL, ''),
    ('notice-pr-merged', 'sess-1', 'proj-1', 'https://github.com/acme/app/pull/42',
     'pr_merged', 'Merged', '', 'read', ?, ?, NULL,
     'pr_merged:https://github.com/acme/app/pull/42');
`, seededAt, seededAt, seededAt)

	upTo(t, db, sessionCrashMigrationVersion)

	assertIndexes := func() {
		t.Helper()
		for _, name := range []string{
			"idx_notifications_status_history",
			"idx_notifications_history",
			"idx_notifications_unresolved",
			"idx_notifications_live_history",
			"idx_notifications_open_dedupe",
			"idx_notifications_source_dedupe",
		} {
			var count int
			if err := db.QueryRow(
				`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name,
			).Scan(&count); err != nil {
				t.Fatalf("read index %s: %v", name, err)
			}
			if count != 1 {
				t.Fatalf("index %s count = %d, want 1", name, count)
			}
		}
	}

	assertExisting := func() {
		t.Helper()
		var (
			typ, title, body, status, sourceKey string
			prURL                               string
			resolvedAt                          sql.NullTime
		)
		if err := db.QueryRow(`
SELECT type, title, body, status, pr_url, source_key, resolved_at
FROM notifications WHERE id = 'notice-pr-merged'`).Scan(
			&typ, &title, &body, &status, &prURL, &sourceKey, &resolvedAt,
		); err != nil {
			t.Fatalf("read preserved notification: %v", err)
		}
		if typ != "pr_merged" || title != "Merged" || body != "" || status != "read" ||
			prURL != "https://github.com/acme/app/pull/42" ||
			sourceKey != "pr_merged:https://github.com/acme/app/pull/42" || !resolvedAt.Valid {
			t.Fatalf("notification changed during migration: type=%q title=%q body=%q status=%q pr=%q source=%q resolved=%v",
				typ, title, body, status, prURL, sourceKey, resolvedAt)
		}
		var needsInput int
		if err := db.QueryRow(
			`SELECT count(*) FROM notifications WHERE id = 'notice-needs-input' AND type = 'needs_input'`,
		).Scan(&needsInput); err != nil {
			t.Fatal(err)
		}
		if needsInput != 1 {
			t.Fatal("pre-existing needs_input notification did not survive the rebuild")
		}
	}

	assertCrashAccepted := func() {
		t.Helper()
		mustExec(t, db, `
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES ('notice-crash', 'sess-1', NULL, '', 'session_crashed', 'Session crashed',
    'Resume the session', 'unread', ?, NULL, NULL, 'session_crashed:sess-1:launch-1');
`, seededAt)
		var typ string
		if err := db.QueryRow(`SELECT type FROM notifications WHERE id = 'notice-crash'`).Scan(&typ); err != nil {
			t.Fatalf("read crash notification: %v", err)
		}
		if typ != "session_crashed" {
			t.Fatalf("crash notification type = %q, want session_crashed", typ)
		}
	}

	assertDedupe := func() {
		t.Helper()
		// Source-key dedupe: one row per durable transition.
		_, err := db.Exec(`
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES ('notice-crash-dupe', 'sess-1', NULL, '', 'session_crashed', 'Session crashed',
    '', 'unread', ?, NULL, NULL, 'session_crashed:sess-1:launch-1');
`, seededAt)
		if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
			t.Fatalf("duplicate source_key was accepted: %v", err)
		}
		// Open dedupe: one unread row per (session, type, pr) without a source key.
		_, err = db.Exec(`
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES ('notice-needs-input-dupe', 'sess-1', NULL, '', 'needs_input', 'Waiting on you',
    '', 'unread', ?, NULL, NULL, '');
`, seededAt)
		if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
			t.Fatalf("duplicate open notification was accepted: %v", err)
		}
	}

	assertSchema := func() {
		t.Helper()
		assertIndexes()
		assertExisting()
		var foreignKeys int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('notifications')`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 2 {
			t.Fatalf("notification foreign keys = %d, want 2 (sessions, projects)", foreignKeys)
		}
		var leftovers int
		if err := db.QueryRow(`
SELECT count(*) FROM sqlite_master
WHERE type = 'table' AND name IN ('notifications_next', 'notifications_previous')`).Scan(&leftovers); err != nil {
			t.Fatal(err)
		}
		if leftovers != 0 {
			t.Fatalf("table rebuild left %d staging tables behind", leftovers)
		}
	}

	assertSchema()
	assertCrashAccepted()
	assertDedupe()

	// Rollback removes crash rows only and restores the prior CHECK.
	downTo(t, db, sessionCrashMigrationPreviousVersion)
	assertIndexes()
	assertExisting()
	var crashRows int
	if err := db.QueryRow(`SELECT count(*) FROM notifications WHERE type = 'session_crashed'`).Scan(&crashRows); err != nil {
		t.Fatal(err)
	}
	if crashRows != 0 {
		t.Fatalf("crash rows after rollback = %d, want 0", crashRows)
	}
	_, err := db.Exec(`
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES ('notice-crash-after-down', 'sess-1', NULL, '', 'session_crashed', 'Session crashed',
    '', 'unread', ?, NULL, NULL, 'session_crashed:sess-1:launch-1');
`, seededAt)
	if err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("rolled-back schema accepted session_crashed: %v", err)
	}

	// Re-applying the migration restores the widened contract.
	upTo(t, db, sessionCrashMigrationVersion)
	assertSchema()
	assertCrashAccepted()
}
