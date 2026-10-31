import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { indexSessions, sessionIdsInTeam, teamRootForSession } from "./team-membership";

function makeSession(
	overrides: Partial<WorkspaceSession> & Pick<WorkspaceSession, "id">,
): WorkspaceSession {
	return {
		workspaceId: "proj",
		workspaceName: "Project",
		title: overrides.id,
		provider: "claude-code",
		status: "working",
		kind: "worker",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

function lookup(sessions: readonly WorkspaceSession[]): Map<string, WorkspaceSession> {
	return indexSessions(sessions);
}

describe("teamRootForSession", () => {
	it("treats an orchestrator as its own root", () => {
		const orchestrator = makeSession({ id: "ao-1", kind: "orchestrator" });
		expect(teamRootForSession(orchestrator, lookup([orchestrator]))).toBe("ao-1");
	});

	it("walks a worker chain to the nearest orchestrator", () => {
		const orchestrator = makeSession({ id: "ao-1", kind: "orchestrator" });
		const worker = makeSession({ id: "ao-2", parentSessionId: "ao-1" });
		const nested = makeSession({ id: "ao-3", parentSessionId: "ao-2" });
		const byId = lookup([orchestrator, worker, nested]);
		expect(teamRootForSession(worker, byId)).toBe("ao-1");
		expect(teamRootForSession(nested, byId)).toBe("ao-1");
	});

	it("stops at a nested orchestrator, which roots its own team", () => {
		const outer = makeSession({ id: "ao-1", kind: "orchestrator" });
		const inner = makeSession({ id: "ao-2", kind: "orchestrator", parentSessionId: "ao-1" });
		const worker = makeSession({ id: "ao-3", parentSessionId: "ao-2" });
		const byId = lookup([outer, inner, worker]);
		expect(teamRootForSession(inner, byId)).toBe("ao-2");
		expect(teamRootForSession(worker, byId)).toBe("ao-2");
	});

	it("has no team for standalone or desktop New Task work", () => {
		const standalone = makeSession({ id: "ao-9", workspaceId: "__standalone__" });
		expect(teamRootForSession(standalone, lookup([standalone]))).toBeUndefined();
	});

	it("has no team when the walk cannot reach an orchestrator", () => {
		const orphan = makeSession({ id: "ao-2", parentSessionId: "ao-1" });
		expect(teamRootForSession(orphan, lookup([orphan]))).toBeUndefined();

		const workerParent = makeSession({ id: "ao-1" });
		expect(teamRootForSession(orphan, lookup([workerParent, orphan]))).toBeUndefined();
	});

	it("stops at a parent in another workspace", () => {
		const parent = makeSession({ id: "ao-1", kind: "orchestrator", workspaceId: "other" });
		const worker = makeSession({ id: "ao-2", parentSessionId: "ao-1" });
		expect(teamRootForSession(worker, lookup([parent, worker]))).toBeUndefined();
	});

	it("stops on a cycle instead of looping", () => {
		const a = makeSession({ id: "ao-1", parentSessionId: "ao-2" });
		const b = makeSession({ id: "ao-2", parentSessionId: "ao-1" });
		expect(teamRootForSession(a, lookup([a, b]))).toBeUndefined();
		expect(teamRootForSession(b, lookup([a, b]))).toBeUndefined();
	});

	it("keeps the first record for a duplicated id", () => {
		const orchestrator = makeSession({ id: "ao-1", kind: "orchestrator" });
		const duplicate = makeSession({ id: "ao-1", kind: "worker" });
		const worker = makeSession({ id: "ao-2", parentSessionId: "ao-1" });
		expect(teamRootForSession(worker, lookup([orchestrator, duplicate, worker]))).toBe("ao-1");
	});
});

describe("sessionIdsInTeam", () => {
	const orchestrator = makeSession({ id: "ao-1", kind: "orchestrator" });
	const worker = makeSession({ id: "ao-2", parentSessionId: "ao-1" });
	const nested = makeSession({ id: "ao-3", parentSessionId: "ao-2" });
	const otherOrchestrator = makeSession({ id: "ao-4", kind: "orchestrator" });
	const otherWorker = makeSession({ id: "ao-5", parentSessionId: "ao-4" });
	const standalone = makeSession({ id: "ao-6" });
	const archived = makeSession({ id: "ao-7", parentSessionId: "ao-1", isTerminated: true });
	const all = [orchestrator, worker, nested, otherOrchestrator, otherWorker, standalone, archived];

	it("includes the lead and every descendant of the root", () => {
		expect([...sessionIdsInTeam(all, all, "ao-1")].sort()).toEqual([
			"ao-1",
			"ao-2",
			"ao-3",
			"ao-7",
		]);
	});

	it("resolves ancestry against the full list for a filtered subset", () => {
		// The subset holds only the grandchild: its parent is missing from it but
		// present in `all`, so the team still resolves.
		expect([...sessionIdsInTeam([nested], all, "ao-1")]).toEqual(["ao-3"]);
	});

	it("returns nothing for a root that is not an orchestrator", () => {
		expect(sessionIdsInTeam(all, all, "ao-2").size).toBe(0);
		expect(sessionIdsInTeam(all, all, "missing").size).toBe(0);
	});

	it("leaves standalone, other-team, and New Task work out", () => {
		const ids = sessionIdsInTeam(all, all, "ao-1");
		for (const id of ["ao-4", "ao-5", "ao-6"]) {
			expect(ids.has(id), id).toBe(false);
		}
	});
});
