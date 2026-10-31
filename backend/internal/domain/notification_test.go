package domain

import "testing"

// TestNotificationTypeSessionCrashed pins the crash type as a valid v1 kind and
// as a terminal fact: there is no user action that "resolves" a dead runtime, so
// it must never be held in the unresolved list.
func TestNotificationTypeSessionCrashed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		typ              NotificationType
		wantValue        string
		wantValid        bool
		wantNeedsResolve bool
	}{
		{
			name:             "session crashed is valid and terminal",
			typ:              NotificationSessionCrashed,
			wantValue:        "session_crashed",
			wantValid:        true,
			wantNeedsResolve: false,
		},
		{
			name:             "needs input stays unresolved",
			typ:              NotificationNeedsInput,
			wantValue:        "needs_input",
			wantValid:        true,
			wantNeedsResolve: true,
		},
		{
			name:             "ready to merge stays unresolved",
			typ:              NotificationReadyToMerge,
			wantValue:        "ready_to_merge",
			wantValid:        true,
			wantNeedsResolve: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := string(tt.typ); got != tt.wantValue {
				t.Fatalf("notification type = %q, want %q", got, tt.wantValue)
			}
			if got := tt.typ.Valid(); got != tt.wantValid {
				t.Fatalf("%q.Valid() = %v, want %v", tt.typ, got, tt.wantValid)
			}
			if got := tt.typ.NeedsResolution(); got != tt.wantNeedsResolve {
				t.Fatalf("%q.NeedsResolution() = %v, want %v", tt.typ, got, tt.wantNeedsResolve)
			}
			if tt.typ == NotificationSessionCrashed && tt.typ.NeedsResolution() {
				t.Fatal("a crashed session must not inflate unresolvedCount")
			}
		})
	}
}

// TestNotificationTypeResolution covers the whole enum so adding a type cannot
// silently change which kinds count as open issues.
func TestNotificationTypeResolution(t *testing.T) {
	t.Parallel()
	tests := []struct {
		typ              NotificationType
		wantNeedsResolve bool
	}{
		{NotificationNeedsInput, true},
		{NotificationReadyToMerge, true},
		{NotificationPRMerged, false},
		{NotificationPRClosedUnmerged, false},
		{NotificationReviewCompleted, false},
		{NotificationReviewChangesRequested, false},
		{NotificationSessionCrashed, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.typ), func(t *testing.T) {
			t.Parallel()
			if !tt.typ.Valid() {
				t.Fatalf("%q must be a valid notification type", tt.typ)
			}
			if got := tt.typ.NeedsResolution(); got != tt.wantNeedsResolve {
				t.Fatalf("%q.NeedsResolution() = %v, want %v", tt.typ, got, tt.wantNeedsResolve)
			}
		})
	}
}
