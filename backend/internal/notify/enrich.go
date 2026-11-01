package notify

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func enrich(intent Intent) (domain.NotificationRecord, error) {
	rec := domain.NotificationRecord{
		SessionID: intent.SessionID,
		ProjectID: intent.ProjectID,
		PRURL:     strings.TrimSpace(intent.PRURL),
		Type:      intent.Type,
		Status:    domain.NotificationUnread,
		CreatedAt: intent.CreatedAt,
		SourceKey: strings.TrimSpace(intent.SourceKey),
	}
	if !intent.Type.Valid() {
		return domain.NotificationRecord{}, domain.ErrInvalidNotificationType
	}
	if prScoped(intent.Type) && rec.PRURL == "" {
		return domain.NotificationRecord{}, domain.ErrInvalidNotificationRecord
	}
	rec.Title = titleForIntent(intent)
	rec.Body = bodyForIntent(intent)
	if err := rec.Validate(); err != nil {
		return domain.NotificationRecord{}, err
	}
	return rec, nil
}

func titleForIntent(intent Intent) string {
	switch intent.Type {
	case domain.NotificationNeedsInput:
		return fmt.Sprintf("%s needs your input", sessionLabel(intent))
	case domain.NotificationSessionCrashed:
		return fmt.Sprintf("%s crashed", sessionLabel(intent))
	case domain.NotificationSessionFailed:
		return fmt.Sprintf("%s failed", sessionLabel(intent))
	case domain.NotificationReadyToMerge:
		if title := strings.TrimSpace(intent.PRTitle); title != "" {
			if label := prLabel(intent); label != "PR" {
				return fmt.Sprintf("%s · %s", title, label)
			}
			return title
		}
		return fmt.Sprintf("%s is ready to merge", prLabel(intent))
	case domain.NotificationPRMerged:
		return fmt.Sprintf("%s merged", prLabel(intent))
	case domain.NotificationPRClosedUnmerged:
		return fmt.Sprintf("%s closed", prLabel(intent))
	case domain.NotificationReviewCompleted:
		return fmt.Sprintf("Review complete for %s", prLabel(intent))
	case domain.NotificationReviewChangesRequested:
		return fmt.Sprintf("Review found changes for %s", prLabel(intent))
	case domain.NotificationSessionStuck:
		return fmt.Sprintf("%s is stuck", sessionLabel(intent))
	case domain.NotificationSessionDone:
		return fmt.Sprintf("%s finished", sessionLabel(intent))
	default:
		return "Notification"
	}
}

func bodyForIntent(intent Intent) string {
	switch intent.Type {
	case domain.NotificationNeedsInput:
		return "Your agent is waiting on you to continue."
	case domain.NotificationSessionCrashed:
		// A dead runtime and an agent turn that ended on an error share this
		// verified crash alert. The failure producer supplies its own detail, so
		// the body says what actually happened instead of guessing at a crash.
		return withDetail(intent.Detail, "The session's runtime stopped unexpectedly. Resume the session to continue.")
	case domain.NotificationSessionFailed:
		return withDetail(intent.Detail, "The session stopped on an error. Open it to see what happened.")
	case domain.NotificationReadyToMerge:
		if session := sessionLabel(intent); session != "session" {
			return fmt.Sprintf("PR from session %s is ready to merge. CI passed with no blocking review feedback.", session)
		}
		return "CI passed with no blocking review feedback."
	case domain.NotificationPRMerged:
		title := strings.TrimSpace(intent.PRTitle)
		if target := strings.TrimSpace(intent.PRTargetBranch); title != "" && target != "" {
			return fmt.Sprintf("%s is now on %s.", title, target)
		}
		if title != "" {
			return fmt.Sprintf("%s was merged.", title)
		}
		return "The pull request was merged."
	case domain.NotificationPRClosedUnmerged:
		if title := strings.TrimSpace(intent.PRTitle); title != "" {
			return fmt.Sprintf("%s was closed without merging. Reopen it if this wasn't intended.", title)
		}
		return "Closed without merging. Reopen it if this wasn't intended."
	case domain.NotificationReviewCompleted:
		return "Orchestrator finished reviewing this pull request and approved it."
	case domain.NotificationReviewChangesRequested:
		return "Orchestrator finished reviewing this pull request and found changes to address."
	case domain.NotificationSessionStuck:
		return withDetail(intent.Detail, "Your agent reported it cannot make progress.")
	case domain.NotificationSessionDone:
		return withDetail(intent.Detail, "Your agent reported its work is finished.")
	default:
		return ""
	}
}

// maxDetailRunes bounds a producer-supplied detail. The detail comes from a
// worker's own report note, and a banner only shows a line or two, so a long
// note must not push the part that identifies the session out of view.
const maxDetailRunes = 160

// withDetail prefers what the producer said about its own outcome, collapsed to
// a single line, and falls back to a sentence written here when it said nothing.
func withDetail(detail, fallback string) string {
	if v := oneLine(detail, maxDetailRunes); v != "" {
		return v
	}
	return fallback
}

// oneLine flattens text headed for an OS banner: control and format characters
// would otherwise let a worker's note rewrite what the user reads.
func oneLine(v string, limit int) string {
	v = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, v)), " ")
	if v == "" {
		return ""
	}
	if runes := []rune(v); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return v
}

// prScoped reports whether a notification type describes a pull request and
// therefore cannot be stored without one. Session-scoped types describe a
// session's own lifecycle (an agent waiting on the user, a runtime crash, a
// failure, or a worker's own stuck/done report) and carry no PR context.
func prScoped(t domain.NotificationType) bool {
	switch t {
	case domain.NotificationReadyToMerge, domain.NotificationPRMerged, domain.NotificationPRClosedUnmerged,
		domain.NotificationReviewCompleted, domain.NotificationReviewChangesRequested:
		return true
	default:
		return false
	}
}

func sessionLabel(intent Intent) string {
	if v := strings.TrimSpace(intent.SessionDisplayName); v != "" {
		return v
	}
	if intent.SessionID != "" {
		return string(intent.SessionID)
	}
	return "session"
}

func prLabel(intent Intent) string {
	if intent.PRNumber > 0 {
		return fmt.Sprintf("PR #%d", intent.PRNumber)
	}
	if title := strings.TrimSpace(intent.PRTitle); title != "" {
		return "PR " + title
	}
	return "PR"
}
