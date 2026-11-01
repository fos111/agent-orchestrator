import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { buildSessionGraph } from "../lib/session-graph";
import type { PullRequestFacts, WorkspaceSession } from "../types/workspace";
import { aoBridge } from "../lib/bridge";
import { SessionGraphCanvas } from "./SessionGraphCanvas";

function makeSession(
	overrides: Partial<WorkspaceSession> & Pick<WorkspaceSession, "id">,
): WorkspaceSession {
	return {
		workspaceId: "p1",
		workspaceName: "Project",
		title: overrides.id,
		provider: "claude-code",
		status: "working",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

function makePR(number: number, state: PullRequestFacts["state"] = "open"): PullRequestFacts {
	return {
		url: `https://github.com/example/project/pull/${number}`,
		number,
		state,
		ci: "passing",
		review: "approved",
		mergeability: "mergeable",
		reviewComments: false,
		updatedAt: "2026-01-01T00:00:00Z",
	};
}

function boardSessions(): WorkspaceSession[] {
	return [
		makeSession({
			id: "orch",
			kind: "orchestrator",
			title: "Root orchestrator",
			createdAt: "2026-01-01T00:00:00Z",
		}),
		makeSession({
			id: "w1",
			kind: "worker",
			parentSessionId: "orch",
			title: "Build graph",
			branch: "feat/session-graph",
			createdAt: "2026-01-01T00:01:00Z",
			prs: [makePR(7)],
		}),
		makeSession({
			id: "manual",
			kind: "worker",
			title: "Manual worker",
			createdAt: "2026-01-01T00:02:00Z",
		}),
	];
}

function renderCanvas(sessions: WorkspaceSession[], includeArchived = false) {
	const onOpenSession = vi.fn();
	const graph = buildSessionGraph(sessions, { includeArchived });
	const result = render(
		<SessionGraphCanvas graph={graph} onOpenSession={onOpenSession} />,
	);
	return { ...result, graph, onOpenSession };
}

describe("SessionGraphCanvas", () => {
	beforeEach(() => {
		vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
	});

	afterEach(() => {
		vi.restoreAllMocks();
	});

	it("renders session and PR nodes with accessible names and one path per edge", () => {
		const { container, graph } = renderCanvas(boardSessions());

		expect(
			screen.getByRole("button", { name: "Root orchestrator, Orchestrator, Working" }),
		).toBeInTheDocument();
		expect(
			screen.getByRole("button", { name: "Build graph, Worker, Working" }),
		).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "PR #7, open" })).toHaveAttribute(
			"href",
			"https://github.com/example/project/pull/7",
		);
		expect(container.querySelectorAll("[data-edge-id]")).toHaveLength(
			graph.edges.length,
		);
	});

	it("opens a session when its node is clicked", () => {
		const { onOpenSession } = renderCanvas(boardSessions());

		fireEvent.click(screen.getByRole("button", { name: /Build graph/ }));

		expect(onOpenSession).toHaveBeenCalledWith(
			expect.objectContaining({ id: "w1" }),
		);
	});

	it("opens PR links without opening the owning session", () => {
		const { onOpenSession } = renderCanvas(boardSessions());
		const link = screen.getByRole("link", { name: "PR #7, open" });

		fireEvent.click(link);

		expect(link).toHaveAttribute(
			"href",
			"https://github.com/example/project/pull/7",
		);
		expect(onOpenSession).not.toHaveBeenCalled();
	});

	it("highlights the active node, its parent, and direct children on hover and focus", () => {
		renderCanvas(boardSessions());
		const worker = screen.getByRole("button", { name: /Build graph/ });
		const orchestrator = screen.getByRole("button", { name: /Root orchestrator/ });
		const manual = document.querySelector('[data-node-key="session:manual"]');
		const summary = screen.getByTestId("session-graph-summary");

		fireEvent.mouseEnter(worker);
		expect(manual).toHaveAttribute("data-dimmed", "true");
		expect(summary).toHaveTextContent("Build graph");

		fireEvent.focus(orchestrator);
		expect(summary).toHaveTextContent("Root orchestrator");
		expect(manual).toHaveAttribute("data-dimmed", "true");
		expect(worker).not.toHaveAttribute("data-dimmed", "true");

		fireEvent.blur(orchestrator);
		expect(summary).toHaveTextContent("Build graph");
	});

	it("shows the owning session, PR number, state, and link in the summary", () => {
		renderCanvas(boardSessions());
		const summary = screen.getByTestId("session-graph-summary");

		fireEvent.mouseEnter(screen.getByRole("link", { name: "PR #7, open" }));

		expect(summary).toHaveTextContent("Build graph");
		expect(summary).toHaveTextContent("PR #7");
		expect(summary).toHaveTextContent("open");
		expect(summary.querySelector("a")).toHaveAttribute(
			"href",
			"https://github.com/example/project/pull/7",
		);
	});

	it("zooms between 0.5 and 1.5 and resets to 1", () => {
		renderCanvas(boardSessions());
		const canvas = screen.getByTestId("session-graph-canvas");
		const zoomIn = screen.getByRole("button", { name: "Zoom in" });
		const zoomOut = screen.getByRole("button", { name: "Zoom out" });
		const reset = screen.getByRole("button", { name: "Reset zoom" });

		fireEvent.click(zoomIn);
		expect(canvas.style.transform).toBe("scale(1.1)");

		for (let index = 0; index < 10; index += 1) fireEvent.click(zoomIn);
		expect(canvas.style.transform).toBe("scale(1.5)");

		for (let index = 0; index < 20; index += 1) fireEvent.click(zoomOut);
		expect(canvas.style.transform).toBe("scale(0.5)");

		fireEvent.click(reset);
		expect(canvas.style.transform).toBe("scale(1)");
	});

	it("marks context-only and archived nodes as muted with the archived label", () => {
		const archived = makeSession({
			id: "orch",
			kind: "orchestrator",
			title: "Archived orchestrator",
			status: "terminated",
			isTerminated: true,
			kanbanColumn: "archive",
			createdAt: "2026-01-01T00:00:00Z",
		});
		const activeChild = makeSession({
			id: "w1",
			kind: "worker",
			parentSessionId: "orch",
			title: "Active child",
			createdAt: "2026-01-01T00:01:00Z",
		});

		const hidden = renderCanvas([archived, activeChild]);
		const contextNode = hidden.container.querySelector(
			'[data-node-key="session:orch"]',
		);
		expect(contextNode).toHaveAttribute("data-context-only", "true");
		expect(contextNode).toHaveClass("border-dashed");
		expect(contextNode).toHaveTextContent("Archived");
		expect(contextNode).toHaveAttribute(
			"title",
			"Archived session shown because an active session descends from it",
		);
		hidden.unmount();

		const shown = renderCanvas([archived], true);
		const archivedNode = shown.container.querySelector(
			'[data-node-key="session:orch"]',
		);
		expect(archivedNode).not.toHaveAttribute("data-context-only");
		expect(archivedNode).toHaveClass("border-dashed");
		expect(archivedNode).toHaveTextContent("Archived");
	});
});

describe("SessionGraphCanvas dependency and queue visuals", () => {
	beforeEach(() => {
		vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
	});

	afterEach(() => {
		vi.restoreAllMocks();
	});

	function dependency(id: string) {
		return { sessionId: id, requiredState: "merged" as const, satisfied: false };
	}

	it("draws a dashed dependency edge with an arrowhead toward the dependent", () => {
		const sessions = [
			makeSession({ id: "a", title: "Foundation", createdAt: "2026-01-01T00:00:00Z" }),
			makeSession({
				id: "b",
				title: "Follow-up",
				createdAt: "2026-01-01T00:01:00Z",
				dependsOn: [dependency("a")],
			}),
		];
		const { container, graph } = renderCanvas(sessions);
		const dependencyEdge = container.querySelector('[data-edge-kind="depends"]');

		expect(dependencyEdge).not.toBeNull();
		expect(dependencyEdge).toHaveAttribute("stroke-dasharray", "6 4");
		expect(dependencyEdge).toHaveAttribute("marker-end", "url(#session-graph-dependency-arrow)");
		expect(dependencyEdge?.querySelector("title")).toHaveTextContent(
			"Dependency: Foundation → Follow-up",
		);
		expect(graph.edges.filter((edge) => edge.kind === "depends")).toHaveLength(1);
	});

	it("labels a queued node in its accessible name and badge", () => {
		const sessions = [
			makeSession({
				id: "queued",
				title: "Waiting worker",
				createdAt: "2026-01-01T00:00:00Z",
				schedulingState: "queued",
			}),
		];
		renderCanvas(sessions);

		const node = screen.getByRole("button", {
			name: "Waiting worker, Worker, Working, Queued",
		});
		expect(node).toHaveAttribute("data-scheduling-state", "queued");
		expect(node.querySelector('[data-scheduling-badge="queued"]')).toHaveTextContent("Queued");
	});

	it("names the blocked reason and carries it as the node title", () => {
		const sessions = [
			makeSession({
				id: "blocked",
				title: "Blocked worker",
				createdAt: "2026-01-01T00:00:00Z",
				schedulingState: "blocked",
				blockedReason: "dependency_pending",
			}),
		];
		renderCanvas(sessions);

		const node = screen.getByRole("button", {
			name: "Blocked worker, Worker, Working, Blocked, Waiting for a dependency",
		});
		expect(node).toHaveAttribute("data-scheduling-state", "blocked");
		expect(node).toHaveAttribute("title", "Waiting for a dependency");
		expect(node.querySelector('[data-scheduling-badge="blocked"]')).toHaveTextContent("Blocked");
	});
});

describe("SessionGraphCanvas hidden dependencies", () => {
	it("counts a dependency the graph is not drawing", () => {
		const sessions = [
			makeSession({ id: "a", title: "Foundation", createdAt: "2026-01-01T00:00:00Z" }),
			makeSession({
				id: "b",
				title: "Follow-up",
				createdAt: "2026-01-01T00:01:00Z",
				dependsOn: [{ sessionId: "a", requiredState: "merged", satisfied: false }],
			}),
		];
		const graph = buildSessionGraph(sessions, {
			includeArchived: false,
			showDependencies: false,
		});
		render(<SessionGraphCanvas graph={graph} onOpenSession={vi.fn()} />);

		expect(graph.edges.filter((edge) => edge.kind === "depends")).toHaveLength(0);
		expect(screen.getByTestId("session-graph-hidden-dependencies")).toHaveTextContent(
			"1 hidden dependency",
		);
	});
});
