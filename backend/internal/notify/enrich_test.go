package notify

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestEnrichReadyToMergePrioritizesPRContext(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationReadyToMerge,
		SessionID:          "sess-1",
		ProjectID:          "proj-1",
		PRURL:              "https://github.com/acme/app/pull/67",
		SessionDisplayName: "Checkout flow",
		PRNumber:           67,
		PRTitle:            "Fix checkout totals",
		CreatedAt:          time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "Fix checkout totals · PR #67"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "PR from session Checkout flow is ready to merge. CI passed with no blocking review feedback."; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
}

func TestEnrichReadyToMergeFallsBackWithoutPRTitle(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:      domain.NotificationReadyToMerge,
		SessionID: "sess-1",
		ProjectID: "proj-1",
		PRURL:     "https://github.com/acme/app/pull/67",
		PRNumber:  67,
		CreatedAt: time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "PR #67 is ready to merge"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
}

func TestEnrichSessionCrashedCarriesSafeSessionOnlyCopy(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationSessionCrashed,
		SessionID:          "sess-1",
		ProjectID:          "proj-1",
		SessionDisplayName: "Checkout flow",
		SourceKey:          "session_crashed:sess-1:launch-1",
		CreatedAt:          time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich crash notification: %v", err)
	}

	if want := "Checkout flow crashed"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "The session's runtime stopped unexpectedly. Resume the session to continue."; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
	if rec.PRURL != "" {
		t.Fatalf("crash notification carried a PR URL: %q", rec.PRURL)
	}
	if rec.Type != domain.NotificationSessionCrashed || rec.Status != domain.NotificationUnread {
		t.Fatalf("record = %+v, want unread session_crashed", rec)
	}
	// A crash already happened: it must never inflate the unresolved count.
	if rec.Type.NeedsResolution() {
		t.Fatal("session_crashed must stay terminal")
	}
	if rec.SourceKey != "session_crashed:sess-1:launch-1" {
		t.Fatalf("source key = %q", rec.SourceKey)
	}
}

func TestEnrichSessionCrashedFallsBackToSessionIDLabel(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:      domain.NotificationSessionCrashed,
		SessionID: "sess-1",
		ProjectID: "proj-1",
		CreatedAt: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich crash notification: %v", err)
	}
	if want := "sess-1 crashed"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
}

// A failed turn or a start that never completed is reported as session_crashed.
// The producer's detail becomes the body, so the crash alert says what actually
// went wrong instead of guessing at a dead runtime.
func TestEnrichSessionCrashedCarriesProducerDetail(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationSessionCrashed,
		SessionID:          "sess-1",
		ProjectID:          "proj-1",
		SessionDisplayName: "Checkout flow",
		Detail:             "The agent's turn ended on an error. Open the session to see what happened.",
		CreatedAt:          time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich crash notification: %v", err)
	}
	if want := "Checkout flow crashed"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "The agent's turn ended on an error. Open the session to see what happened."; rec.Body != want {
		t.Fatalf("body = %q, want the producer detail %q", rec.Body, want)
	}
	if rec.Type != domain.NotificationSessionCrashed || rec.Status != domain.NotificationUnread {
		t.Fatalf("record = %+v, want unread session_crashed", rec)
	}
	if rec.Type.NeedsResolution() {
		t.Fatal("session_crashed must stay terminal")
	}
}

// The legacy session_failed kind stays enrichable for rows written by earlier
// builds. AO itself reports failures as session_crashed (see the crash tests
// above), but a stored row must still render with its producer detail.
func TestEnrichSessionFailedCarriesProducerDetail(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationSessionFailed,
		SessionID:          "sess-1",
		ProjectID:          "proj-1",
		SessionDisplayName: "Checkout flow",
		Detail:             "branch already checked out",
		CreatedAt:          time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich failed notification: %v", err)
	}
	if want := "Checkout flow failed"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "branch already checked out"; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
	if rec.Type != domain.NotificationSessionFailed || rec.Status != domain.NotificationUnread {
		t.Fatalf("record = %+v, want unread session_failed", rec)
	}
	if rec.Type.NeedsResolution() {
		t.Fatal("session_failed must stay terminal")
	}
}

// With no producer detail the legacy kind still gets its own fallback copy.
func TestEnrichSessionFailedFallsBackToGenericCopy(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:      domain.NotificationSessionFailed,
		SessionID: "sess-1",
		ProjectID: "proj-1",
		CreatedAt: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich failed notification: %v", err)
	}
	if want := "sess-1 failed"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "The session stopped on an error. Open it to see what happened."; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
	if strings.Contains(rec.Body, "runtime stopped unexpectedly") {
		t.Fatalf("failed body reused the crash copy: %q", rec.Body)
	}
}

func TestEnrichSessionScopedTypesDoNotRequirePRURL(t *testing.T) {
	t.Parallel()

	for _, typ := range []domain.NotificationType{
		domain.NotificationNeedsInput,
		domain.NotificationSessionCrashed,
		domain.NotificationSessionFailed,
		domain.NotificationSessionStuck,
		domain.NotificationSessionDone,
	} {
		t.Run(string(typ), func(t *testing.T) {
			t.Parallel()
			rec, err := enrich(Intent{
				Type:      typ,
				SessionID: "sess-1",
				ProjectID: "proj-1",
				CreatedAt: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatalf("enrich %s without a PR URL: %v", typ, err)
			}
			if rec.PRURL != "" {
				t.Fatalf("%s carried a PR URL: %q", typ, rec.PRURL)
			}
		})
	}
}

func TestEnrichPRScopedTypesRequirePRURL(t *testing.T) {
	t.Parallel()

	for _, typ := range []domain.NotificationType{
		domain.NotificationReadyToMerge,
		domain.NotificationPRMerged,
		domain.NotificationPRClosedUnmerged,
		domain.NotificationReviewCompleted,
		domain.NotificationReviewChangesRequested,
	} {
		t.Run(string(typ), func(t *testing.T) {
			t.Parallel()
			_, err := enrich(Intent{
				Type:      typ,
				SessionID: "sess-1",
				ProjectID: "proj-1",
				CreatedAt: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
			})
			if !errors.Is(err, domain.ErrInvalidNotificationRecord) {
				t.Fatalf("enrich %s without a PR URL: err = %v, want %v", typ, err, domain.ErrInvalidNotificationRecord)
			}
		})
	}
}

// A worker's own stuck/done report has no pull request behind it, and the PR-URL
// requirement that guards the PR-shaped kinds must not reject it.
func TestEnrichSessionReportOutcomesNeedNoPR(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		typ       domain.NotificationType
		wantTitle string
		wantBody  string
	}{
		{
			name:      "stuck",
			typ:       domain.NotificationSessionStuck,
			wantTitle: "Checkout flow is stuck",
			wantBody:  "Your agent reported it cannot make progress.",
		},
		{
			name:      "done",
			typ:       domain.NotificationSessionDone,
			wantTitle: "Checkout flow finished",
			wantBody:  "Your agent reported its work is finished.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec, err := enrich(Intent{
				Type:               tc.typ,
				SessionID:          "sess-1",
				ProjectID:          "proj-1",
				SessionDisplayName: "Checkout flow",
				CreatedAt:          time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatalf("enrich %s notification: %v", tc.typ, err)
			}
			if rec.Title != tc.wantTitle {
				t.Fatalf("title = %q, want %q", rec.Title, tc.wantTitle)
			}
			if rec.Body != tc.wantBody {
				t.Fatalf("body = %q, want %q", rec.Body, tc.wantBody)
			}
			if rec.PRURL != "" {
				t.Fatalf("prUrl = %q, want empty", rec.PRURL)
			}
		})
	}
}

// The worker's own note is what the user reads, flattened to one line so a
// note cannot rewrite what the banner shows.
func TestEnrichSessionOutcomePrefersTheWorkerNote(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationSessionDone,
		SessionID:          "sess-1",
		SessionDisplayName: "Checkout flow",
		Detail:             "Shipped the totals fix\nand the tax rounding",
		CreatedAt:          time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich done notification: %v", err)
	}
	if want := "Shipped the totals fix and the tax rounding"; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
}

func TestEnrichSessionOutcomeTruncatesALongNote(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:      domain.NotificationSessionStuck,
		SessionID: "sess-1",
		Detail:    strings.Repeat("x", maxDetailRunes+40),
		CreatedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich stuck notification: %v", err)
	}
	if got := []rune(rec.Body); len(got) != maxDetailRunes+1 || got[len(got)-1] != '…' {
		t.Fatalf("body length = %d runes, want %d with a trailing ellipsis", len(got), maxDetailRunes+1)
	}
}
