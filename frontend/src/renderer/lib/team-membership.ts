import type { WorkspaceSession } from "../types/workspace";

/**
 * Resolve the derived team root for one session, mirroring the daemon's rule:
 * the nearest orchestrator ancestor, an orchestrator is its own root, and
 * anything else has no team. A standalone worker, a desktop New Task session, a
 * DAG node with no orchestrator ancestor, a missing parent, a parent in another
 * workspace, and a cycle all resolve to `undefined`.
 */
export function teamRootForSession(
	session: WorkspaceSession,
	byId: ReadonlyMap<string, WorkspaceSession>,
): string | undefined {
	if (session.kind === "orchestrator") return session.id;
	const seen = new Set<string>([session.id]);
	let current = session;
	while (current.parentSessionId) {
		if (seen.has(current.parentSessionId)) return undefined;
		seen.add(current.parentSessionId);
		const parent = byId.get(current.parentSessionId);
		if (!parent) return undefined;
		if (parent.workspaceId !== session.workspaceId) return undefined;
		if (parent.kind === "orchestrator") return parent.id;
		current = parent;
	}
	return undefined;
}

/** Index sessions by id; the first record wins, like the daemon's derivation. */
export function indexSessions(sessions: readonly WorkspaceSession[]): Map<string, WorkspaceSession> {
	const byId = new Map<string, WorkspaceSession>();
	for (const session of sessions) {
		if (!byId.has(session.id)) byId.set(session.id, session);
	}
	return byId;
}

/**
 * The ids of `sessions` that belong to the team rooted at `root`. `all` is the
 * full project list used for parent lookups, so a filtered subset still resolves
 * its ancestry correctly.
 */
export function sessionIdsInTeam(
	sessions: readonly WorkspaceSession[],
	all: readonly WorkspaceSession[],
	root: string,
): Set<string> {
	const byId = indexSessions(all);
	const ids = new Set<string>();
	for (const session of sessions) {
		if (teamRootForSession(session, byId) === root) ids.add(session.id);
	}
	return ids;
}
