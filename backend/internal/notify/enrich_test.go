package notify

import (
	"errors"
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

func TestEnrichSessionScopedTypesDoNotRequirePRURL(t *testing.T) {
	t.Parallel()

	for _, typ := range []domain.NotificationType{
		domain.NotificationNeedsInput,
		domain.NotificationSessionCrashed,
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
