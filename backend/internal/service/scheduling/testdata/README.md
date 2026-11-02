# Scheduling fixtures

Deterministic inputs for scheduling, DAG, and crash-recovery work. They are data,
not golden output: a test or a manual run feeds them to the real services, and the
validation test in `fixtures_test.go` keeps their shape and counts honest.

Run the validation:

```bash
cd backend
go test ./internal/service/scheduling/ -run TestSchedulingFixtures
```

## `load/project_100_sessions_200_edges.json`

A spawn plan with 100 worker sessions and 200 dependency edges, used to exercise
queue evaluation and FIFO admission at a realistic size.

```json
{
  "projectId": "scheduling-load",
  "projectConfig": {
    "worker": { "agent": "codex" },
    "scheduling": {
      "maxActiveSessions": 20,
      "maxActivePerTeam": 4,
      "maxDependenciesPerSession": 16,
      "maxDagNodesPerRun": 64,
      "defaultDependencyState": "auto"
    }
  },
  "sessions": [
    {
      "id": "load-010",
      "displayName": "Load session 010",
      "prompt": "Exercise the scheduling load fixture as session 010.",
      "requestedOutput": "artifact",
      "dependsOn": [
        { "sessionId": "load-000", "requiredState": "ready" }
      ]
    }
  ]
}
```

- `projectConfig` decodes as `domain.ProjectConfig`; `worker.agent` selects the
  harness and `scheduling` is the project's `domain.SchedulingConfig`.
- `id` is fixture-local. A consumer maps it to a real session ID when it creates
  the rows.
- `displayName` and `prompt` are required and non-empty.
- `requestedOutput` is optional. When present it is `artifact`.
- `dependsOn` is optional. Each entry names an earlier session in the array, so
  the graph is acyclic by construction. `requiredState` is one of `ready`,
  `merged`, or `completed`.
- No session exceeds the configured incoming-edge cap, and every required state
  appears at least once.

## `load/dag_64_nodes.json`

A complete DAG submission at the default per-run node cap.

```json
{
  "name": "load-64-nodes",
  "nodes": [
    {
      "id": "node-08",
      "name": "DAG load node 08",
      "prompt": "Exercise the DAG load fixture as node 08.",
      "dependsOn": ["node-00"],
      "requiredState": "merged"
    }
  ]
}
```

The document is exactly the daemon's `domain.DagRunSpec` wire shape, so it can be
posted as-is or submitted with `ao dag run --project <id> --file -`.

- `nodes` is exactly 64 entries, the `MaxDagNodesPerRun` default.
- Node IDs are unique; every `dependsOn` entry names an earlier node in the same
  run, so the graph is acyclic.
- `requiredState` is one of `ready`, `merged`, or `completed`; nodes with
  dependencies name a state explicitly and all three states appear.
- `requestedOutput` is optional. When present it is `artifact`.
- Every node has a non-empty `name` and `prompt`.

## `crash/matrix.json`

Durable pre- and post-recovery state for the launch paths that matter: a
reservation whose dependency has not resolved, a satisfied queue, project and
team limits, every dependency failure, each half of an interrupted launch, a
failed launch, explicit and forced admission, and an untouched immediate session.

```json
{
  "cases": [
    {
      "id": "admitted-never-launched",
      "phase": "recover",
      "before": {
        "schedulingState": "admitted",
        "blockedReason": "",
        "provisionState": "provisioning",
        "hasWorkspace": false,
        "hasRuntime": false,
        "isTerminated": false,
        "dependencyFact": "satisfied",
        "projectFull": false,
        "teamFull": false,
        "launchResult": "not_attempted"
      },
      "recovery": "boot",
      "expected": {
        "schedulingState": "immediate",
        "blockedReason": "",
        "provisionState": "ready",
        "launched": true,
        "launchAttempted": true,
        "isTerminated": false,
        "admissionOverride": false
      },
      "why": "The admission committed but the launch never ran. Boot reconcile relaunches it under the per-session launch lock."
    }
  ]
}
```

`before` describes the durable row before recovery:

| Field | Values | Meaning |
| --- | --- | --- |
| `schedulingState` | `immediate`, `queued`, `blocked`, `admitted`, `cancelled` | Stored scheduling state. |
| `blockedReason` | empty or a `domain.BlockedReason` | Stored reason; empty when not blocked. |
| `provisionState` | `queued`, `provisioning`, `ready`, `failed` | Stored or read-model provision state. |
| `hasWorkspace` | boolean | Whether the row has a workspace path. |
| `hasRuntime` | boolean | Whether the row has a runtime handle. |
| `isTerminated` | boolean | Whether the row is terminated. |
| `dependencyFact` | `none`, `pending`, `satisfied`, `failed`, `cancelled`, `missing`, `unsatisfiable`, `invalid` | What the evaluator sees for the edge. |
| `projectFull` | boolean | Project limit is already full. |
| `teamFull` | boolean | The session's team limit is already full. |
| `launchResult` | `not_attempted`, `succeeded`, `failed` | Outcome when a launch is attempted. |

`recovery` is the action applied: `boot` (startup reconcile), `sweep` (periodic
sweep), `admit` (non-forced `ao session admit`), `force_admit` (forced), or
`none`.

`expected` is the resulting durable state:

| Field | Meaning |
| --- | --- |
| `schedulingState` | Expected stored state. A successful launch becomes `immediate`. |
| `blockedReason` | Expected reason; empty when not blocked. |
| `provisionState` | Expected provision state. |
| `launched` | A workspace and agent were created. |
| `launchAttempted` | A launch was attempted, whether or not it succeeded. |
| `isTerminated` | Expected termination flag. |
| `admissionOverride` | The admission override was recorded. |

`phase` names the code path that produced the pre-recovery row (`reserve`,
`evaluate`, `admit`, `launch`, `recover`, or `cancel`), and `why` explains the
expected outcome in one sentence.

Invariants enforced by the test: IDs are unique, every case has a `why`, the enum
fields stay in their allowed sets, `launched` implies `launchAttempted` and
`provisionState: ready`, `launch_failed` implies `provisionState: failed`,
`admissionOverride` matches `force_admit`, and `isTerminated` does not change.
