package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// A DAG run is a coordinator over parentless worker sessions. The CLI submits
// a whole graph in one call, reads the derived run status, and cancels the
// queued/blocked part of a run; it never stages nodes locally.

type dagRunOptions struct {
	project string
	file    string
	json    bool
}

type dagRunReadOptions struct {
	json bool
}

type dagCancelOptions struct {
	includeActive bool
	json          bool
}

// dagRunSubmitRequest is the CLI's own copy of the daemon's submission DTO
// (controllers.CreateDagRunRequest). Nothing shares these field names at
// compile time, so they stay explicit.
type dagRunSubmitRequest struct {
	Name  string           `json:"name"`
	Nodes []dagNodeRequest `json:"nodes"`
}

type dagNodeRequest struct {
	ID              string   `json:"id"`
	Name            string   `json:"name,omitempty"`
	Prompt          string   `json:"prompt"`
	Harness         string   `json:"harness,omitempty"`
	DependsOn       []string `json:"dependsOn,omitempty"`
	RequiredState   string   `json:"requiredState,omitempty"`
	RequestedOutput string   `json:"requestedOutput,omitempty"`
}

type dagCancelRequest struct {
	IncludeActive bool `json:"includeActive"`
}

type dagRunCountsDTO struct {
	Total     int `json:"total"`
	Queued    int `json:"queued"`
	Blocked   int `json:"blocked"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

type dagRunNodeDTO struct {
	ID            string     `json:"id"`
	SessionID     string     `json:"sessionId,omitempty"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	BlockedReason string     `json:"blockedReason,omitempty"`
	DependsOn     []string   `json:"dependsOn,omitempty"`
	CreatedAt     *time.Time `json:"createdAt,omitempty"`
}

type dagRunDTO struct {
	ID                string          `json:"id"`
	ProjectID         string          `json:"projectId"`
	RootSessionID     string          `json:"rootSessionId,omitempty"`
	Name              string          `json:"name"`
	Status            string          `json:"status"`
	CancelRequestedAt *time.Time      `json:"cancelRequestedAt,omitempty"`
	CreatedAt         *time.Time      `json:"createdAt,omitempty"`
	CompletedAt       *time.Time      `json:"completedAt,omitempty"`
	Counts            dagRunCountsDTO `json:"counts"`
	Nodes             []dagRunNodeDTO `json:"nodes"`
}

type dagRunEnvelope struct {
	Run dagRunDTO `json:"run"`
}

func newDagCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dag",
		Short: "Submit, inspect, and cancel DAG runs",
	}
	cmd.AddCommand(newDagRunCommand(ctx))
	cmd.AddCommand(newDagStatusCommand(ctx))
	cmd.AddCommand(newDagCancelCommand(ctx))
	return cmd
}

func newDagRunCommand(ctx *commandContext) *cobra.Command {
	var opts dagRunOptions
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Submit a DAG run from a JSON file",
		Long: `Submit a whole DAG run atomically.

The file is validated in full before any session row exists: node IDs must be
unique, every dependsOn entry must name a node in the same run, the graph must
be acyclic, and it may not exceed the project's DAG node limit. A rejected file
creates nothing.

Nodes are created non-admitted. A node without in-run dependencies starts
queued unless the project limit is already full; a dependent node stays blocked
until its dependencies reach requiredState.

Pass --file - to read the graph from stdin.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.submitDagRun(cmd.Context(), cmd, opts)
		},
	}
	f := cmd.Flags()
	addSessionProjectFlag(f, &opts.project, "Project ID to submit the run into")
	f.StringVarP(&opts.file, "file", "f", "", "Path to the DAG run JSON file (- for stdin)")
	f.BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func newDagStatusCommand(ctx *commandContext) *cobra.Command {
	var opts dagRunReadOptions
	cmd := &cobra.Command{
		Use:   "status <run-id>",
		Short: "Fetch one DAG run and its node status",
		Args:  oneDagRunIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.getDagRun(cmd.Context(), cmd, strings.TrimSpace(args[0]), opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func newDagCancelCommand(ctx *commandContext) *cobra.Command {
	var opts dagCancelOptions
	cmd := &cobra.Command{
		Use:   "cancel <run-id>",
		Short: "Cancel a DAG run's queued and blocked nodes",
		Long: `Cancel a DAG run.

Queued and blocked nodes are cancelled. Nodes that have already been admitted
keep running, and their later completion does not clear the cancelled status.
Pass --include-active to kill the active nodes too.`,
		Args: oneDagRunIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.cancelDagRun(cmd.Context(), cmd, strings.TrimSpace(args[0]), opts)
		},
	}
	cmd.Flags().BoolVar(&opts.includeActive, "include-active", false, "Also kill nodes that are already running")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func oneDagRunIDArg(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(args[0]) == "" {
		return usageError{errors.New("usage: a DAG run id is required")}
	}
	return nil
}

func (c *commandContext) submitDagRun(ctx context.Context, cmd *cobra.Command, opts dagRunOptions) error {
	path := strings.TrimSpace(opts.file)
	if path == "" {
		return usageError{errors.New("usage: --file is required")}
	}
	spec, err := readDagRunFile(cmd, path)
	if err != nil {
		return err
	}
	project, err := c.resolveSpawnProject(ctx, opts.project)
	if err != nil {
		return err
	}
	var res dagRunEnvelope
	if err := c.postJSON(ctx, "projects/"+url.PathEscape(project.ID)+"/dag-runs", spec, &res); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	return writeDagRun(cmd.OutOrStdout(), res.Run)
}

func (c *commandContext) getDagRun(ctx context.Context, cmd *cobra.Command, id string, opts dagRunReadOptions) error {
	var res dagRunEnvelope
	if err := c.getJSON(ctx, "dag-runs/"+url.PathEscape(id), &res); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	return writeDagRun(cmd.OutOrStdout(), res.Run)
}

func (c *commandContext) cancelDagRun(ctx context.Context, cmd *cobra.Command, id string, opts dagCancelOptions) error {
	var res dagRunEnvelope
	if err := c.postJSON(ctx, "dag-runs/"+url.PathEscape(id)+"/cancel", dagCancelRequest{IncludeActive: opts.includeActive}, &res); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "run %s cancelled\n", res.Run.ID); err != nil {
		return err
	}
	return writeDagRun(cmd.OutOrStdout(), res.Run)
}

// readDagRunFile decodes the submitted graph. The daemon owns semantic
// validation; this only rejects input that is not a run document at all, so a
// malformed file fails before a daemon round trip.
func readDagRunFile(cmd *cobra.Command, path string) (dagRunSubmitRequest, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
	} else {
		raw, err = os.ReadFile(path) //nolint:gosec // user-supplied path, read-only
	}
	if err != nil {
		return dagRunSubmitRequest{}, usageError{fmt.Errorf("read DAG run file: %w", err)}
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var spec dagRunSubmitRequest
	if err := dec.Decode(&spec); err != nil {
		return dagRunSubmitRequest{}, usageError{fmt.Errorf("parse DAG run file: %w", err)}
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return dagRunSubmitRequest{}, usageError{errors.New("parse DAG run file: trailing data after the run document")}
	}
	if len(spec.Nodes) == 0 {
		return dagRunSubmitRequest{}, usageError{errors.New("parse DAG run file: nodes is required")}
	}
	return spec, nil
}

func writeDagRun(w io.Writer, run dagRunDTO) error {
	if _, err := fmt.Fprintf(w, "run %s  %s  %s\n", run.ID, run.Status, run.Name); err != nil {
		return err
	}
	counts := run.Counts
	if _, err := fmt.Fprintf(w,
		"nodes: %d total, %d queued, %d blocked, %d running, %d completed, %d failed, %d cancelled\n",
		counts.Total, counts.Queued, counts.Blocked, counts.Running, counts.Completed, counts.Failed, counts.Cancelled,
	); err != nil {
		return err
	}
	if len(run.Nodes) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tSESSION\tSTATUS\tNAME"); err != nil {
		return err
	}
	for _, node := range run.Nodes {
		status := node.Status
		if node.BlockedReason != "" {
			status += " (" + node.BlockedReason + ")"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", node.ID, node.SessionID, status, node.Name); err != nil {
			return err
		}
	}
	return tw.Flush()
}
