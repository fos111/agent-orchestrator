import { describe, expect, it } from "vitest";
import type {
	PullRequestFacts,
	WorkspaceSession,
} from "../types/workspace";
import {
	buildSessionGraph,
	SESSION_GRAPH_NODE_WIDTH,
} from "./session-graph";

function makeSession(
	overrides: Partial<WorkspaceSession> & Pick<WorkspaceSession, "id">,
): WorkspaceSession {
	return {
		workspaceId: "p1",
		workspaceName: "project",
		title: overrides.id,
		provider: "claude-code",
		status: "working",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

function makeOrchestrator(
	id: string,
	createdAt: string,
	overrides: Partial<WorkspaceSession> = {},
): WorkspaceSession {
	return makeSession({
		id,
		kind: "orchestrator",
		createdAt,
		...overrides,
	});
}

function makeWorker(
	id: string,
	createdAt: string,
	parentSessionId?: string,
	overrides: Partial<WorkspaceSession> = {},
): WorkspaceSession {
	return makeSession({
		id,
		kind: "worker",
		createdAt,
		parentSessionId,
		...overrides,
	});
}

function makePR(
	number: number,
	state: PullRequestFacts["state"] = "open",
): PullRequestFacts {
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

describe("buildSessionGraph", () => {
	it("places an orchestrator above its workers and their PR leaves", () => {
		const graph = buildSessionGraph(
			[
				makeOrchestrator("orch", "2026-01-01T00:00:00Z"),
				makeWorker("w1", "2026-01-01T00:01:00Z", "orch", {
					prs: [makePR(8, "merged"), makePR(7, "open")],
				}),
				makeWorker("w2", "2026-01-01T00:02:00Z", "orch"),
				makeWorker("w3", "2026-01-01T00:03:00Z", "orch"),
			],
			{ includeArchived: false },
		);

		expect(graph.roots).toEqual(["session:orch"]);
		expect(graph.nodes.filter((node) => node.kind === "session")).toHaveLength(4);
		expect(graph.edges.filter((edge) => edge.kind === "spawn")).toHaveLength(3);
		expect(graph.edges.filter((edge) => edge.kind === "delivers")).toHaveLength(2);

		const orchestrator = graph.nodes.find((node) => node.key === "session:orch");
		const worker = graph.nodes.find((node) => node.key === "session:w1");
		const pr = graph.nodes.find((node) => node.key === "pr:w1:7");
		expect(orchestrator?.depth).toBe(0);
		expect(worker?.depth).toBe(1);
		expect(pr?.depth).toBe(2);
		expect(orchestrator?.y).toBe(0);
		expect(worker?.y).toBeGreaterThan(0);
		expect(pr?.y).toBeGreaterThan(worker?.y ?? 0);
		expect(worker?.parentKey).toBe("session:orch");
		expect(pr?.parentKey).toBe("session:w1");
	});

	it("orders PR leaves by state rank and then by number", () => {
		const graph = buildSessionGraph(
			[
				makeWorker("w1", "2026-01-01T00:01:00Z", undefined, {
					prs: [makePR(9, "merged"), makePR(4, "draft"), makePR(3, "open"), makePR(1, "open")],
				}),
			],
			{ includeArchived: false },
		);

		expect(
			graph.nodes.filter((node) => node.kind === "pr").map((node) => node.key),
		).toEqual(["pr:w1:1", "pr:w1:3", "pr:w1:4", "pr:w1:9"]);
	});

	it("keeps multiple roots, including a manual worker, in deterministic order", () => {
		const graph = buildSessionGraph(
			[
				makeWorker("manual", "2026-01-01T00:00:00Z"),
				makeOrchestrator("orch", "2026-01-01T00:05:00Z"),
				makeWorker("w1", "2026-01-01T00:06:00Z", "orch"),
			],
			{ includeArchived: false },
		);

		expect(graph.roots).toEqual(["session:orch", "session:manual"]);
		expect(graph.nodes.find((node) => node.key === "session:w1")?.parentKey).toBe(
			"session:orch",
		);
	});

	it("records missing and cross-project parents and makes the child a root", () => {
		const graph = buildSessionGraph(
			[
				makeWorker("missing", "2026-01-01T00:01:00Z", "ghost"),
				makeSession({
					id: "other",
					workspaceId: "p2",
					kind: "orchestrator",
					createdAt: "2026-01-01T00:02:00Z",
				}),
				makeWorker("cross", "2026-01-01T00:03:00Z", "other"),
			],
			{ includeArchived: false },
		);

		expect(graph.nodes.find((node) => node.key === "session:missing")?.parentKey).toBeNull();
		expect(graph.nodes.find((node) => node.key === "session:cross")?.parentKey).toBeNull();
		expect(graph.droppedEdges).toEqual(
			expect.arrayContaining([
				{
					from: "session:missing",
					to: "session:ghost",
					reason: "missing-parent",
				},
				{
					from: "session:cross",
					to: "session:other",
					reason: "cross-project",
				},
			]),
		);
	});

	it("drops self-parent edges and cycle-closing edges without looping", () => {
		const graph = buildSessionGraph(
			[
				makeWorker("a", "2026-01-01T00:01:00Z", "c"),
				makeWorker("b", "2026-01-01T00:02:00Z", "a"),
				makeWorker("c", "2026-01-01T00:03:00Z", "b"),
				makeWorker("self", "2026-01-01T00:04:00Z", "self"),
			],
			{ includeArchived: false },
		);

		expect(graph.nodes.filter((node) => node.kind === "session")).toHaveLength(4);
		expect(graph.droppedEdges).toEqual(
			expect.arrayContaining([
				{ from: "session:self", to: "session:self", reason: "self" },
				{ from: "session:a", to: "session:c", reason: "cycle" },
			]),
		);
		expect(graph.roots).toEqual(["session:a", "session:self"]);
	});

	it("includes an archived parent as context for an active child", () => {
		const archivedParent = makeOrchestrator("orch", "2026-01-01T00:00:00Z", {
			status: "terminated",
			isTerminated: true,
			kanbanColumn: "archive",
		});
		const activeChild = makeWorker("w1", "2026-01-01T00:01:00Z", "orch");
		const sessions = [archivedParent, activeChild];

		const hidden = buildSessionGraph(sessions, { includeArchived: false });
		expect(hidden.nodes.map((node) => node.key)).toEqual([
			"session:orch",
			"session:w1",
		]);
		expect(hidden.nodes.find((node) => node.key === "session:orch")?.contextOnly).toBe(
			true,
		);
		expect(hidden.edges).toContainEqual(
			expect.objectContaining({
				from: "session:orch",
				to: "session:w1",
				kind: "spawn",
			}),
		);

		const shown = buildSessionGraph(sessions, { includeArchived: true });
		expect(shown.nodes.find((node) => node.key === "session:orch")?.contextOnly).toBe(
			false,
		);
	});

	it("hides an archived-only tree until archived sessions are included", () => {
		const sessions = [
			makeOrchestrator("orch", "2026-01-01T00:00:00Z", {
				status: "terminated",
				isTerminated: true,
				kanbanColumn: "archive",
			}),
			makeWorker("w1", "2026-01-01T00:01:00Z", "orch", {
				status: "terminated",
				isTerminated: true,
				kanbanColumn: "archive",
			}),
		];

		expect(buildSessionGraph(sessions, { includeArchived: false }).nodes).toEqual([]);
		expect(
			buildSessionGraph(sessions, { includeArchived: true }).nodes.map(
				(node) => node.key,
			),
		).toEqual(["session:orch", "session:w1"]);
	});

	it("produces identical output for shuffled input", () => {
		const sessions = [
			makeOrchestrator("orch", "2026-01-01T00:00:00Z"),
			makeWorker("w1", "2026-01-01T00:01:00Z", "orch", {
				prs: [makePR(2), makePR(1)],
			}),
			makeWorker("w2", "2026-01-01T00:02:00Z", "orch"),
			makeWorker("manual", "2026-01-01T00:03:00Z"),
		];

		const forward = buildSessionGraph(sessions, { includeArchived: false });
		const reversed = buildSessionGraph([...sessions].reverse(), {
			includeArchived: false,
		});
		expect(reversed).toEqual(forward);
	});

	it("keeps nodes inside bounds, non-overlapping, and edges attached to nodes", () => {
		const graph = buildSessionGraph(
			[
				makeOrchestrator("orch", "2026-01-01T00:00:00Z"),
				makeWorker("w1", "2026-01-01T00:01:00Z", "orch", {
					prs: [makePR(2), makePR(1)],
				}),
				makeWorker("w2", "2026-01-01T00:02:00Z", "orch"),
				makeWorker("manual", "2026-01-01T00:03:00Z"),
			],
			{ includeArchived: false },
		);
		const keys = new Set(graph.nodes.map((node) => node.key));

		for (const node of graph.nodes) {
			expect(node.x).toBeGreaterThanOrEqual(0);
			expect(node.y).toBeGreaterThanOrEqual(0);
			expect(node.width).toBe(SESSION_GRAPH_NODE_WIDTH);
			expect(node.x + node.width).toBeLessThanOrEqual(graph.width);
			expect(node.y + node.height).toBeLessThanOrEqual(graph.height);
		}

		for (let index = 0; index < graph.nodes.length; index += 1) {
			for (let other = index + 1; other < graph.nodes.length; other += 1) {
				const left = graph.nodes[index];
				const right = graph.nodes[other];
				const overlaps =
					left.x < right.x + right.width &&
					right.x < left.x + left.width &&
					left.y < right.y + right.height &&
					right.y < left.y + left.height;
				expect(overlaps).toBe(false);
			}
		}

		for (const edge of graph.edges) {
			expect(keys.has(edge.from)).toBe(true);
			expect(keys.has(edge.to)).toBe(true);
		}
	});

	it("keeps the first session when ids are duplicated", () => {
		const graph = buildSessionGraph(
			[
				makeWorker("dup", "2026-01-01T00:01:00Z", undefined, {
					title: "first",
				}),
				makeWorker("dup", "2026-01-01T00:02:00Z", undefined, {
					title: "second",
				}),
			],
			{ includeArchived: false },
		);

		expect(graph.nodes).toHaveLength(1);
		expect(graph.nodes[0]?.session.title).toBe("first");
	});
});

describe("buildSessionGraph dependency edges", () => {
	function dependency(sessionId: string, requiredState: "ready" | "merged" | "completed" = "merged") {
		return { sessionId, requiredState, satisfied: false };
	}

	it("draws an edge from the dependency to the dependent and counts both directions", () => {
		const graph = buildSessionGraph(
			[
				makeWorker("a", "2026-01-01T00:00:00Z"),
				makeWorker("b", "2026-01-01T00:01:00Z", undefined, {
					dependsOn: [dependency("a")],
					dependents: [],
				}),
			],
			{ includeArchived: false },
		);

		expect(graph.edges).toContainEqual({
			id: "depends:a:b",
			from: "session:a",
			to: "session:b",
			kind: "depends",
		});
		const dependencyNode = graph.nodes.find((node) => node.key === "session:a");
		const dependentNode = graph.nodes.find((node) => node.key === "session:b");
		expect(dependencyNode?.dependentCount).toBe(0);
		expect(dependentNode?.dependencyCount).toBe(1);
		expect(dependentNode?.hiddenDependencyCount).toBe(0);
	});

	it("reports and counts a dependency the filter hides", () => {
		const graph = buildSessionGraph(
			[
				makeWorker("b", "2026-01-01T00:01:00Z", undefined, {
					dependsOn: [dependency("ghost")],
				}),
			],
			{ includeArchived: false },
		);

		expect(graph.edges.filter((edge) => edge.kind === "depends")).toEqual([]);
		expect(graph.droppedEdges).toContainEqual({
			from: "session:ghost",
			to: "session:b",
			reason: "filtered",
		});
		const dependentNode = graph.nodes.find((node) => node.key === "session:b");
		expect(dependentNode?.dependencyCount).toBe(1);
		expect(dependentNode?.hiddenDependencyCount).toBe(1);
	});

	it("includes an archived dependency as a context-only node", () => {
		const archived = makeWorker("a", "2026-01-01T00:00:00Z", undefined, {
			status: "terminated",
			isTerminated: true,
			kanbanColumn: "archive",
		});
		const dependent = makeWorker("b", "2026-01-01T00:01:00Z", undefined, {
			dependsOn: [dependency("a")],
		});

		const graph = buildSessionGraph([archived, dependent], { includeArchived: false });

		expect(graph.nodes.find((node) => node.key === "session:a")?.contextOnly).toBe(true);
		expect(graph.edges).toContainEqual(
			expect.objectContaining({ from: "session:a", to: "session:b", kind: "depends" }),
		);
	});

	it("drops a dependency edge that closes a cycle", () => {
		const graph = buildSessionGraph(
			[
				makeWorker("a", "2026-01-01T00:00:00Z", undefined, { dependsOn: [dependency("b")] }),
				makeWorker("b", "2026-01-01T00:01:00Z", undefined, { dependsOn: [dependency("a")] }),
			],
			{ includeArchived: false },
		);

		expect(graph.edges.filter((edge) => edge.kind === "depends")).toHaveLength(1);
		expect(graph.droppedEdges).toContainEqual(
			expect.objectContaining({ reason: "cycle" }),
		);
	});

	it("layers a multi-parent DAG by longest path without overlap", () => {
		const graph = buildSessionGraph(
			[
				makeWorker("a", "2026-01-01T00:00:00Z"),
				makeWorker("b", "2026-01-01T00:01:00Z"),
				makeWorker("c", "2026-01-01T00:02:00Z", undefined, {
					dependsOn: [dependency("a"), dependency("b")],
				}),
				makeWorker("d", "2026-01-01T00:03:00Z", undefined, {
					dependsOn: [dependency("c")],
				}),
			],
			{ includeArchived: false, layout: "dependency" },
		);

		const node = (id: string) => graph.nodes.find((candidate) => candidate.key === `session:${id}`);
		expect(node("a")?.depth).toBe(0);
		expect(node("b")?.depth).toBe(0);
		expect(node("c")?.depth).toBe(1);
		expect(node("d")?.depth).toBe(2);
		expect(node("c")?.y).toBeGreaterThan(node("a")?.y ?? 0);
		expect(node("d")?.y).toBeGreaterThan(node("c")?.y ?? 0);

		for (const edge of graph.edges.filter((candidate) => candidate.kind === "depends")) {
			expect(node(edge.from.slice("session:".length))?.y).toBeLessThan(
				node(edge.to.slice("session:".length))?.y ?? 0,
			);
		}

		for (let index = 0; index < graph.nodes.length; index += 1) {
			for (let other = index + 1; other < graph.nodes.length; other += 1) {
				const left = graph.nodes[index];
				const right = graph.nodes[other];
				const overlaps =
					left.x < right.x + right.width &&
					right.x < left.x + left.width &&
					left.y < right.y + right.height &&
					right.y < left.y + left.height;
				expect(overlaps).toBe(false);
			}
		}
	});
});
