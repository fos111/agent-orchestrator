package domain

import (
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"
)

// Teams are derived, not stored. A team is the subtree rooted at an
// orchestrator session: the root is the nearest orchestrator ancestor of every
// member, an orchestrator is its own root, and a worker with no orchestrator
// ancestor belongs to no team. ParentSessionID is the only durable membership
// fact, so derivation is a pure walk over session records.

// DefaultMaxActivePerTeam is the default concurrent-active-worker limit for one
// team. Zero means unlimited. The lead (orchestrator) never counts.
const DefaultMaxActivePerTeam = 4

// MaxTeamMessageBodyLength bounds one broadcast body. The bound is enforced in
// the domain and by the team_messages CHECK constraint.
const MaxTeamMessageBodyLength = 32768

// TeamMessageID identifies one persisted broadcast.
type TeamMessageID string

// String renders the id for logs and wire values.
func (id TeamMessageID) String() string { return string(id) }

// TeamDeliveryStatus is one member's broadcast outcome.
type TeamDeliveryStatus string

// Delivery outcomes. pending is only visible while the broadcast is running.
const (
	TeamDeliveryPending   TeamDeliveryStatus = "pending"
	TeamDeliveryDelivered TeamDeliveryStatus = "delivered"
	TeamDeliveryFailed    TeamDeliveryStatus = "failed"
)

// TeamMessage is one persisted broadcast to a team.
type TeamMessage struct {
	ID                TeamMessageID `json:"id"`
	ProjectID         ProjectID     `json:"projectId"`
	TeamRootSessionID SessionID     `json:"teamRootSessionId"`
	// SenderSessionID is empty when the broadcast came from a surface with no
	// owning session, such as the desktop team composer.
	SenderSessionID SessionID `json:"senderSessionId,omitempty"`
	Body            string    `json:"body"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Validate rejects a broadcast that cannot be stored.
func (m TeamMessage) Validate() error {
	if m.ID == "" {
		return errors.New("team message id is required")
	}
	if m.ProjectID == "" {
		return errors.New("team message project id is required")
	}
	if m.TeamRootSessionID == "" {
		return errors.New("team message root session id is required")
	}
	if m.Body == "" {
		return errors.New("team message body is required")
	}
	if utf8.RuneCountInString(m.Body) > MaxTeamMessageBodyLength {
		return fmt.Errorf("team message body must be %d characters or fewer", MaxTeamMessageBodyLength)
	}
	if m.CreatedAt.IsZero() {
		return errors.New("team message created-at is required")
	}
	return nil
}

// TeamMessageDelivery is one member's delivery result for a broadcast.
type TeamMessageDelivery struct {
	MessageID TeamMessageID      `json:"messageId"`
	SessionID SessionID          `json:"sessionId"`
	Status    TeamDeliveryStatus `json:"status"`
	Error     string             `json:"error,omitempty"`
}

// TeamMessageReport is the persisted broadcast plus its per-member results.
type TeamMessageReport struct {
	Message    TeamMessage           `json:"message"`
	Deliveries []TeamMessageDelivery `json:"deliveries"`
}

// DeliveredCount and FailedCount summarize a report for CLI and UI surfaces.
func (r TeamMessageReport) DeliveredCount() int { return r.count(TeamDeliveryDelivered) }

// FailedCount reports how many member deliveries failed.
func (r TeamMessageReport) FailedCount() int { return r.count(TeamDeliveryFailed) }

func (r TeamMessageReport) count(status TeamDeliveryStatus) int {
	n := 0
	for _, d := range r.Deliveries {
		if d.Status == status {
			n++
		}
	}
	return n
}

// TeamMember is one session in a derived team roster.
type TeamMember struct {
	SessionID       SessionID   `json:"sessionId"`
	ParentSessionID SessionID   `json:"parentSessionId,omitempty"`
	DisplayName     string      `json:"displayName,omitempty"`
	Kind            SessionKind `json:"kind"`
	IsTerminated    bool        `json:"isTerminated"`
	CreatedAt       time.Time   `json:"createdAt"`
}

func teamMemberFromRecord(rec SessionRecord) TeamMember {
	return TeamMember{
		SessionID:       rec.ID,
		ParentSessionID: rec.ParentSessionID,
		DisplayName:     rec.DisplayName,
		Kind:            rec.Kind,
		IsTerminated:    rec.IsTerminated,
		CreatedAt:       rec.CreatedAt,
	}
}

// Team is a derived snapshot of one orchestrator subtree.
//
// Members holds the non-terminated roster, including the lead. ActiveWorkers
// counts non-terminated workers only: the lead is exempt from the limit and a
// terminated member is not active. MaxActiveWorkers is the effective limit; a
// zero limit means unlimited.
type Team struct {
	RootSessionID    SessionID    `json:"rootSessionId"`
	ProjectID        ProjectID    `json:"projectId"`
	Orchestrator     TeamMember   `json:"orchestrator"`
	Members          []TeamMember `json:"members"`
	ActiveWorkers    int          `json:"activeWorkers"`
	MaxActiveWorkers int          `json:"maxActiveWorkers"`
}

// LimitReached reports whether the team already runs its full complement of
// active workers. A zero MaxActiveWorkers means unlimited.
func (t Team) LimitReached() bool {
	return t.MaxActiveWorkers > 0 && t.ActiveWorkers >= t.MaxActiveWorkers
}

// TeamRoot returns the nearest orchestrator ancestor of rec. ok is false when
// rec is not in a team: no orchestrator ancestor, a missing parent, a cycle, or
// a parent in another project. rec itself is an orchestrator root.
func TeamRoot(rec SessionRecord, byID func(SessionID) (SessionRecord, bool)) (SessionID, bool) {
	if rec.Kind == KindOrchestrator {
		return rec.ID, true
	}
	seen := map[SessionID]struct{}{rec.ID: {}}
	current := rec
	for current.ParentSessionID != "" {
		if _, cycle := seen[current.ParentSessionID]; cycle {
			return "", false
		}
		seen[current.ParentSessionID] = struct{}{}
		parent, ok := byID(current.ParentSessionID)
		if !ok {
			return "", false
		}
		if parent.ProjectID != rec.ProjectID {
			return "", false
		}
		if parent.Kind == KindOrchestrator {
			return parent.ID, true
		}
		current = parent
	}
	return "", false
}

// TeamIndex is an immutable same-project session snapshot. Build one per read
// and ask it for roots, rosters, and teams; it never writes.
type TeamIndex struct {
	records map[SessionID]SessionRecord
	// order is deterministic: creation time, then id.
	order []SessionID
}

// NewTeamIndex indexes non-hidden records. Duplicate ids keep the first record;
// the caller normally passes one row per session.
func NewTeamIndex(records []SessionRecord) *TeamIndex {
	idx := &TeamIndex{records: make(map[SessionID]SessionRecord, len(records))}
	for _, rec := range records {
		if rec.IsTaskPreparation {
			continue
		}
		if _, exists := idx.records[rec.ID]; exists {
			continue
		}
		idx.records[rec.ID] = rec
		idx.order = append(idx.order, rec.ID)
	}
	sort.SliceStable(idx.order, func(i, j int) bool {
		a, b := idx.records[idx.order[i]], idx.records[idx.order[j]]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	return idx
}

// Record returns one indexed record.
func (i *TeamIndex) Record(id SessionID) (SessionRecord, bool) {
	rec, ok := i.records[id]
	return rec, ok
}

// TeamRoot resolves the root for one indexed session.
func (i *TeamIndex) TeamRoot(id SessionID) (SessionID, bool) {
	rec, ok := i.records[id]
	if !ok {
		return "", false
	}
	return TeamRoot(rec, func(parent SessionID) (SessionRecord, bool) {
		return i.Record(parent)
	})
}

// Roots returns every orchestrator root in creation order.
func (i *TeamIndex) Roots() []SessionID {
	var roots []SessionID
	for _, id := range i.order {
		if i.records[id].Kind == KindOrchestrator {
			roots = append(roots, id)
		}
	}
	return roots
}

// Subtree returns every indexed record whose resolved root is root, in
// creation order. The root itself is included.
func (i *TeamIndex) Subtree(root SessionID) []SessionRecord {
	rootRec, ok := i.records[root]
	if !ok || rootRec.Kind != KindOrchestrator {
		return nil
	}
	var out []SessionRecord
	for _, id := range i.order {
		resolved, inTeam := i.TeamRoot(id)
		if inTeam && resolved == root {
			out = append(out, i.records[id])
		}
	}
	return out
}

// Members returns the non-terminated subtree roster, including the lead.
func (i *TeamIndex) Members(root SessionID) []TeamMember {
	subtree := i.Subtree(root)
	members := make([]TeamMember, 0, len(subtree))
	for _, rec := range subtree {
		if rec.IsTerminated {
			continue
		}
		members = append(members, teamMemberFromRecord(rec))
	}
	return members
}

// Team builds one team snapshot. ok is false when root is missing or is not an
// orchestrator.
func (i *TeamIndex) Team(root SessionID, maxActiveWorkers int) (Team, bool) {
	rootRec, ok := i.records[root]
	if !ok || rootRec.Kind != KindOrchestrator {
		return Team{}, false
	}
	return i.buildTeam(root, maxActiveWorkers), true
}

func (i *TeamIndex) buildTeam(root SessionID, maxActiveWorkers int) Team {
	rootRec := i.records[root]
	members := i.Members(root)
	active := 0
	for _, member := range members {
		if member.Kind == KindWorker {
			active++
		}
	}
	return Team{
		RootSessionID:    rootRec.ID,
		ProjectID:        rootRec.ProjectID,
		Orchestrator:     teamMemberFromRecord(rootRec),
		Members:          members,
		ActiveWorkers:    active,
		MaxActiveWorkers: maxActiveWorkers,
	}
}

// Teams builds every team in the index, in root creation order.
func (i *TeamIndex) Teams(maxActiveWorkers int) []Team {
	roots := i.Roots()
	teams := make([]Team, 0, len(roots))
	for _, root := range roots {
		teams = append(teams, i.buildTeam(root, maxActiveWorkers))
	}
	return teams
}

// TeamFor builds the team that contains id. ok is false when the session has no
// orchestrator ancestor.
func (i *TeamIndex) TeamFor(id SessionID, maxActiveWorkers int) (Team, bool) {
	root, ok := i.TeamRoot(id)
	if !ok {
		return Team{}, false
	}
	return i.Team(root, maxActiveWorkers)
}
