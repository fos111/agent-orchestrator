package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// These DTOs mirror the daemon's team API without importing controller types.
type teamMemberDTO struct {
	SessionID       string    `json:"sessionId"`
	ParentSessionID string    `json:"parentSessionId"`
	DisplayName     string    `json:"displayName"`
	Kind            string    `json:"kind"`
	IsTerminated    bool      `json:"isTerminated"`
	CreatedAt       time.Time `json:"createdAt"`
}

type teamDTO struct {
	RootSessionID    string          `json:"rootSessionId"`
	ProjectID        string          `json:"projectId"`
	Orchestrator     teamMemberDTO   `json:"orchestrator"`
	Members          []teamMemberDTO `json:"members"`
	ActiveWorkers    int             `json:"activeWorkers"`
	MaxActiveWorkers int             `json:"maxActiveWorkers"`
}

type teamEnvelopeDTO struct {
	Team teamDTO `json:"team"`
}

type teamListDTO struct {
	Teams []teamDTO `json:"teams"`
}

type teamMessageRequestDTO struct {
	SenderSessionID string `json:"senderSessionId,omitempty"`
	Message         string `json:"message"`
}

type teamMessageDeliveryDTO struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Error     string `json:"error"`
}

type teamMessageDTO struct {
	ID                string    `json:"id"`
	ProjectID         string    `json:"projectId"`
	TeamRootSessionID string    `json:"teamRootSessionId"`
	SenderSessionID   string    `json:"senderSessionId"`
	Body              string    `json:"body"`
	CreatedAt         time.Time `json:"createdAt"`
}

type teamBroadcastDTO struct {
	OK         bool                     `json:"ok"`
	Message    teamMessageDTO           `json:"message"`
	Deliveries []teamMessageDeliveryDTO `json:"deliveries"`
	Delivered  int                      `json:"delivered"`
	Failed     int                      `json:"failed"`
}

func newTeamCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "team",
		Aliases: []string{"teams"},
		Short:   "Inspect orchestrator teams and broadcast to their members",
		Long: "Teams are derived from the spawn-parent tree below an orchestrator; " +
			"there is nothing to create or delete. Broadcasts are best-effort.",
	}
	cmd.AddCommand(newTeamListCommand(ctx), newTeamStatusCommand(ctx), newTeamMessageCommand(ctx))
	return cmd
}

func newTeamListCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List a project's orchestrator teams",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := ctx.resolveSpawnProject(cmd.Context(), project)
			if err != nil {
				return err
			}
			var response teamListDTO
			if err := ctx.getJSON(cmd.Context(), "projects/"+url.PathEscape(resolved.ID)+"/teams", &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeTeamList(cmd, response.Teams)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: AO_PROJECT_ID, current session, or current registered repo)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newTeamStatusCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status <rootSessionId>",
		Short: "Show one team's lead, members, and active-worker limit",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := strings.TrimSpace(args[0])
			if root == "" {
				return usageError{errors.New("root session id is required")}
			}
			var response teamEnvelopeDTO
			if err := ctx.getJSON(cmd.Context(), "teams/"+url.PathEscape(root), &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeTeamStatus(cmd, response.Team)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newTeamMessageCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "message <rootSessionId> <message>",
		Short: "Broadcast a message to every team member except the sender",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := strings.TrimSpace(args[0])
			message := strings.TrimSpace(args[1])
			if root == "" {
				return usageError{errors.New("root session id is required")}
			}
			if message == "" {
				return usageError{errors.New("message is required")}
			}
			var response teamBroadcastDTO
			if err := ctx.postJSON(cmd.Context(), "teams/"+url.PathEscape(root)+"/messages", teamMessageRequestDTO{
				SenderSessionID: strings.TrimSpace(os.Getenv("AO_SESSION_ID")),
				Message:         message,
			}, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeTeamBroadcast(cmd, response)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func writeTeamList(cmd *cobra.Command, teams []teamDTO) error {
	out := cmd.OutOrStdout()
	if len(teams) == 0 {
		_, err := fmt.Fprintln(out, "(no teams)")
		return err
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ROOT\tPROJECT\tLEAD\tWORKERS\tMEMBERS")
	for _, team := range teams {
		lead := team.Orchestrator.SessionID
		if lead == "" {
			lead = team.RootSessionID
		}
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%d\n", team.RootSessionID, team.ProjectID, lead, formatTeamWorkers(team), len(team.Members))
	}
	return writer.Flush()
}

func writeTeamStatus(cmd *cobra.Command, team teamDTO) error {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "Team %s in project %s\n", team.RootSessionID, team.ProjectID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Lead: %s\n", team.Orchestrator.SessionID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Workers: %s active\n", formatTeamWorkers(team)); err != nil {
		return err
	}
	if len(team.Members) == 0 {
		return nil
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "MEMBER\tKIND\tNAME")
	for _, member := range team.Members {
		name := member.DisplayName
		if name == "" {
			name = "-"
		}
		_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\n", member.SessionID, member.Kind, name)
	}
	return writer.Flush()
}

func writeTeamBroadcast(cmd *cobra.Command, response teamBroadcastDTO) error {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "Broadcast %s to %d members: %d delivered, %d failed\n",
		response.Message.ID, len(response.Deliveries), response.Delivered, response.Failed); err != nil {
		return err
	}
	for _, delivery := range response.Deliveries {
		if delivery.Status != "failed" {
			continue
		}
		if _, err := fmt.Fprintf(out, "  %s failed: %s\n", delivery.SessionID, delivery.Error); err != nil {
			return err
		}
	}
	return nil
}

// formatTeamWorkers renders the lead-exempt active worker count and its limit.
// A zero limit means unlimited.
func formatTeamWorkers(team teamDTO) string {
	if team.MaxActiveWorkers <= 0 {
		return fmt.Sprintf("%d/unlimited", team.ActiveWorkers)
	}
	return fmt.Sprintf("%d/%d", team.ActiveWorkers, team.MaxActiveWorkers)
}
