package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

const (
	sessionOutcomeMigrationPreviousVersion int64 = 20261031185751173
	sessionOutcomeMigrationVersion         int64 = 20261101004318122
)

// TestSessionOutcomeNotificationsMigration proves the outcome migration stacks
// on the crash migration: it widens the CHECK by rebuilding the table without
// losing the crash rows and dedupe indexes the previous build introduced, and
// its rollback returns to that crash schema rather than to the build before it.
func TestSessionOutcomeNotificationsMigration(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, sessionOutcomeMigrationPreviousVersion)
	const seededAt = "2026-11-01T09:00:00Z"
	mustExec(t, db, `
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES
    ('notice-needs-input', 'sess-1', NULL, '', 'needs_input', 'Waiting on you',
     'Agent needs input', 'unread', ?, NULL, NULL, ''),
    ('notice-pr-merged', 'sess-1', 'proj-1', 'https://github.com/acme/app/pull/42',
     'pr_merged', 'Merged', '', 'read', ?, ?, NULL,
     'pr_merged:https://github.com/acme/app/pull/42'),
    ('notice-crash', 'sess-1', NULL, '', 'session_crashed', 'Session crashed',
     'Resume the session', 'unread', ?, NULL, NULL, 'session_crashed:sess-1:launch-1');
`, seededAt, seededAt, seededAt, seededAt)

	upTo(t, db, sessionOutcomeMigrationVersion)

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
		var crash int
		if err := db.QueryRow(
			`SELECT count(*) FROM notifications WHERE id = 'notice-crash' AND type = 'session_crashed'`,
		).Scan(&crash); err != nil {
			t.Fatal(err)
		}
		if crash != 1 {
			t.Fatal("the crash row from the previous migration did not survive the rebuild")
		}
	}

	assertOutcomeKindsAccepted := func() {
		t.Helper()
		mustExec(t, db, `
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES
    ('notice-stuck', 'sess-1', NULL, '', 'session_stuck', 'Checkout flow is stuck',
     'Cannot reach the staging API', 'unread', ?, NULL, NULL, 'session_stuck:rpt-1'),
    ('notice-done', 'sess-1', NULL, '', 'session_done', 'Checkout flow finished',
     'Shipped the totals fix', 'unread', ?, NULL, NULL, 'session_done:rpt-2'),
    ('notice-failed', 'sess-1', NULL, '', 'session_failed', 'Checkout flow failed',
     'The agent turn ended on an error', 'unread', ?, NULL, NULL, 'session_failed:sess-1:launch-1:1');
`, seededAt, seededAt, seededAt)
		wantTypes := map[string]string{
			"notice-stuck":  "session_stuck",
			"notice-done":   "session_done",
			"notice-crash":  "session_crashed",
			"notice-failed": "session_failed",
		}
		for id, want := range wantTypes {
			var typ string
			if err := db.QueryRow(`SELECT type FROM notifications WHERE id = ?`, id).Scan(&typ); err != nil {
				t.Fatalf("read %s: %v", id, err)
			}
			if typ != want {
				t.Fatalf("%s notification type = %q, want %q", id, typ, want)
			}
		}
	}

	assertDedupe := func() {
		t.Helper()
		// Source-key dedupe: one row per durable transition.
		_, err := db.Exec(`
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES ('notice-stuck-dupe', 'sess-1', NULL, '', 'session_stuck', 'Checkout flow is stuck',
    '', 'unread', ?, NULL, NULL, 'session_stuck:rpt-1');
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
	assertOutcomeKindsAccepted()
	assertDedupe()

	// Rollback returns to the crash build: the worker-reported kinds are removed,
	// but the crash rows, indexes, and session_crashed CHECK stay in place.
	downTo(t, db, sessionOutcomeMigrationPreviousVersion)
	assertIndexes()
	assertExisting()
	var outcomeRows int
	if err := db.QueryRow(
		`SELECT count(*) FROM notifications WHERE type IN ('session_stuck', 'session_done', 'session_failed')`,
	).Scan(&outcomeRows); err != nil {
		t.Fatal(err)
	}
	if outcomeRows != 0 {
		t.Fatalf("widened-only rows after rollback = %d, want 0", outcomeRows)
	}
	if _, err := db.Exec(`
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES ('notice-stuck-after-down', 'sess-1', NULL, '', 'session_stuck', 'Checkout flow is stuck',
    '', 'unread', ?, NULL, NULL, 'session_stuck:rpt-9');
`, seededAt); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("rolled-back schema accepted session_stuck: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES ('notice-failed-after-down', 'sess-1', NULL, '', 'session_failed', 'Checkout flow failed',
    '', 'unread', ?, NULL, NULL, 'session_failed:sess-1:launch-1:2');
`, seededAt); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("rolled-back schema accepted session_failed: %v", err)
	}
	// The crash schema is the rollback target, so crash rows remain valid.
	mustExec(t, db, `
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES ('notice-crash-after-down', 'sess-1', NULL, '', 'session_crashed', 'Session crashed',
    '', 'unread', ?, NULL, NULL, 'session_crashed:sess-1:launch-2');
`, seededAt)

	// Re-applying the migration restores the widened contract.
	upTo(t, db, sessionOutcomeMigrationVersion)
	assertSchema()
	assertOutcomeKindsAccepted()
}
