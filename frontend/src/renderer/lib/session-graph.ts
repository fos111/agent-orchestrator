import { isArchivedSession } from "./standalone-archive";
import {
	sessionDependencies,
	sessionDependents,
	sessionSchedulingState,
	sortedPRs,
	type PullRequestFacts,
	type SessionBlockedReason,
	type SessionSchedulingState,
	type WorkspaceSession,
} from "../types/workspace";

export type SessionGraphNode = {
	key: string;
	kind: "session" | "pr";
	session: WorkspaceSession;
	pr?: PullRequestFacts;
	parentKey: string | null;
	depth: number;
	x: number;
	y: number;
	width: number;
	height: number;
	contextOnly: boolean;
	/** Where the scheduler put this session; drives the queued and blocked visuals. */
	schedulingState: SessionSchedulingState;
	/** Why a blocked node is waiting, printed beside its state. */
	blockedReason?: SessionBlockedReason;
	/**
	 * Dependencies this session waits on, including ones the current filter
	 * hides, so the node can say what it is not showing.
	 */
	dependencyCount: number;
	/** Of {@link dependencyCount}, how many have no drawn edge. */
	hiddenDependencyCount: number;
	/** Sessions waiting on this one. */
	dependentCount: number;
};

export type SessionGraphEdgeKind = "spawn" | "delivers" | "depends";

export type SessionGraphEdge = {
	id: string;
	from: string;
	to: string;
	kind: SessionGraphEdgeKind;
};

export type SessionGraphDroppedEdgeReason =
	| "missing-parent"
	| "cross-project"
	| "cycle"
	| "self"
	| "filtered";

export type SessionGraphDroppedEdge = {
	from: string;
	to: string;
	reason: SessionGraphDroppedEdgeReason;
};

export type SessionGraphLayout = "spawn" | "dependency";

export type SessionGraph = {
	nodes: SessionGraphNode[];
	edges: SessionGraphEdge[];
	roots: string[];
	width: number;
	height: number;
	droppedEdges: SessionGraphDroppedEdge[];
};

export type BuildSessionGraphOptions = {
	includeArchived: boolean;
	/**
	 * Draw dependency edges. Defaults to true: they are the whole point of the
	 * coordination graph, and a session without dependencies draws none either way.
	 */
	showDependencies?: boolean;
	/**
	 * `spawn` keeps the tree the board has always drawn, with dependency edges as
	 * an overlay. `dependency` lays sessions out by dependency depth instead.
	 */
	layout?: SessionGraphLayout;
	/**
	 * Show only the sessions that belong to this DAG run. The run is authoritative:
	 * archived members stay visible even when `includeArchived` is false, and an
	 * edge to a session outside the run is reported as filtered.
	 */
	dagRunId?: string;
};

export const SESSION_GRAPH_NODE_WIDTH = 232;
export const SESSION_GRAPH_SESSION_HEIGHT = 84;
export const SESSION_GRAPH_PR_HEIGHT = 52;
export const SESSION_GRAPH_H_GAP = 32;
export const SESSION_GRAPH_V_GAP = 72;
export const SESSION_GRAPH_ROOT_GAP = 64;
export const SESSION_GRAPH_PADDING = 24;

const SESSION_KEY_PREFIX = "session:";
const PR_KEY_PREFIX = "pr:";

function sessionKey(id: string): string {
	return `${SESSION_KEY_PREFIX}${id}`;
}

function prKey(sessionId: string, number: number): string {
	return `${PR_KEY_PREFIX}${sessionId}:${number}`;
}

function compareSessions(left: WorkspaceSession, right: WorkspaceSession): number {
	const leftKind = left.kind === "orchestrator" ? 0 : 1;
	const rightKind = right.kind === "orchestrator" ? 0 : 1;
	if (leftKind !== rightKind) return leftKind - rightKind;
	const created = (left.createdAt ?? "").localeCompare(right.createdAt ?? "");
	if (created !== 0) return created;
	return left.id.localeCompare(right.id);
}

function dropCycleEdges(
	ordered: readonly WorkspaceSession[],
	parentById: Map<string, string>,
	droppedEdges: SessionGraphDroppedEdge[],
): void {
	const state = new Map<string, "visiting" | "done">();
	for (const session of ordered) {
		if (state.has(session.id)) continue;
		const path: string[] = [];
		let current: string | undefined = session.id;
		while (current !== undefined && state.get(current) !== "done") {
			if (state.get(current) === "visiting") {
				const parent = parentById.get(current);
				if (parent !== undefined) {
					droppedEdges.push({
						from: sessionKey(current),
						to: sessionKey(parent),
						reason: "cycle",
					});
					parentById.delete(current);
				}
				break;
			}
			state.set(current, "visiting");
			path.push(current);
			current = parentById.get(current);
		}
		for (const id of path) state.set(id, "done");
	}
}

type DependencyPair = { from: string; to: string };

/**
 * The dependency edges to draw, from dependency to dependent. A dependency the
 * session list does not contain cannot be drawn and is reported instead, so the
 * node can still say how many it is not showing.
 */
function collectDependencyPairs(
	ordered: readonly WorkspaceSession[],
	byId: ReadonlyMap<string, WorkspaceSession>,
	droppedEdges: SessionGraphDroppedEdge[],
): DependencyPair[] {
	const pairs: DependencyPair[] = [];
	const seen = new Set<string>();
	for (const session of ordered) {
		for (const dependency of sessionDependencies(session)) {
			const from = dependency.sessionId;
			const to = session.id;
			if (from === to) {
				droppedEdges.push({ from: sessionKey(from), to: sessionKey(to), reason: "self" });
				continue;
			}
			if (!byId.has(from)) {
				droppedEdges.push({ from: sessionKey(from), to: sessionKey(to), reason: "filtered" });
				continue;
			}
			const id = `${from}\u0000${to}`;
			if (seen.has(id)) continue;
			seen.add(id);
			pairs.push({ from, to });
		}
	}
	return pairs;
}

/**
 * Drop the edges that close a dependency cycle. The daemon refuses to create
 * one, but a stored graph can still be cyclic after a partial write, and the
 * layered layout needs a DAG.
 */
function dropDependencyCycles(
	pairs: readonly DependencyPair[],
	orderedIds: readonly string[],
	droppedEdges: SessionGraphDroppedEdge[],
): DependencyPair[] {
	const adjacency = new Map<string, string[]>();
	for (const pair of pairs) {
		const list = adjacency.get(pair.from) ?? [];
		list.push(pair.to);
		adjacency.set(pair.from, list);
	}
	const state = new Map<string, "visiting" | "done">();
	const dropped = new Set<string>();
	const visit = (id: string): void => {
		state.set(id, "visiting");
		for (const next of adjacency.get(id) ?? []) {
			const edgeId = `${id}\u0000${next}`;
			if (dropped.has(edgeId)) continue;
			const nextState = state.get(next);
			if (nextState === "visiting") {
				dropped.add(edgeId);
				droppedEdges.push({ from: sessionKey(id), to: sessionKey(next), reason: "cycle" });
				continue;
			}
			if (nextState === undefined) visit(next);
		}
		state.set(id, "done");
	};
	for (const id of orderedIds) {
		if (state.get(id) === undefined) visit(id);
	}
	return pairs.filter((pair) => !dropped.has(`${pair.from}\u0000${pair.to}`));
}

/**
 * Layered DAG placement for `layout=dependency`. Roots are the sessions with no
 * dependency inside the filter; depth is the longest path from a root, so every
 * dependency edge points one or more rows downwards. Rows are centred and
 * separated, which keeps a multi-parent DAG overlap-free without a graph
 * library or the tree subtree-width algorithm (which cannot express two parents).
 */
function placeDependencyLayers(
	nodeByKey: ReadonlyMap<string, SessionGraphNode>,
	dependencyPairs: readonly DependencyPair[],
	ordered: readonly WorkspaceSession[],
): string[] {
	const sessionOrder = new Map(ordered.map((session, index) => [session.id, index]));
	const incoming = new Map<string, string[]>();
	for (const pair of dependencyPairs) {
		const list = incoming.get(pair.to) ?? [];
		list.push(pair.from);
		incoming.set(pair.to, list);
	}
	const depthBySession = new Map<string, number>();
	const depthOf = (id: string): number => {
		const known = depthBySession.get(id);
		if (known !== undefined) return known;
		// Written before recursing so a cycle that survived the drop pass cannot
		// recurse forever.
		depthBySession.set(id, 0);
		let depth = 0;
		for (const from of incoming.get(id) ?? []) {
			if (!nodeByKey.has(sessionKey(from))) continue;
			depth = Math.max(depth, depthOf(from) + 1);
		}
		depthBySession.set(id, depth);
		return depth;
	};

	const layerByKey = new Map<string, number>();
	for (const node of nodeByKey.values()) {
		if (node.kind === "session") layerByKey.set(node.key, depthOf(node.session.id));
	}
	for (const node of nodeByKey.values()) {
		if (node.kind !== "pr") continue;
		layerByKey.set(node.key, (node.parentKey ? layerByKey.get(node.parentKey) ?? 0 : 0) + 1);
	}

	const maxLayer = Math.max(0, ...layerByKey.values());
	const layers: SessionGraphNode[][] = Array.from({ length: maxLayer + 1 }, () => []);
	for (const node of nodeByKey.values()) {
		layers[layerByKey.get(node.key) ?? 0].push(node);
	}
	const sortKey = (node: SessionGraphNode): [number, number, number] => [
		sessionOrder.get(node.session.id) ?? 0,
		node.kind === "session" ? 0 : 1,
		node.pr?.number ?? 0,
	];
	for (const row of layers) {
		row.sort((left, right) => {
			const [leftSession, leftKind, leftPR] = sortKey(left);
			const [rightSession, rightKind, rightPR] = sortKey(right);
			return leftSession - rightSession || leftKind - rightKind || leftPR - rightPR;
		});
	}

	const rowWidths = layers.map(
		(row) => row.reduce((width, node) => width + node.width, 0) + SESSION_GRAPH_H_GAP * Math.max(row.length - 1, 0),
	);
	const widestRow = Math.max(0, ...rowWidths);

	const nodeOrder: string[] = [];
	let y = SESSION_GRAPH_PADDING;
	layers.forEach((row, layer) => {
		let x = SESSION_GRAPH_PADDING + (widestRow - (rowWidths[layer] ?? 0)) / 2;
		let rowHeight = 0;
		for (const node of row) {
			node.depth = layer;
			node.x = x;
			node.y = y;
			x += node.width + SESSION_GRAPH_H_GAP;
			rowHeight = Math.max(rowHeight, node.height);
			nodeOrder.push(node.key);
		}
		y += rowHeight + SESSION_GRAPH_V_GAP;
	});
	return nodeOrder;
}

export function buildSessionGraph(
	sessions: readonly WorkspaceSession[],
	options: BuildSessionGraphOptions,
): SessionGraph {
	const source = options.dagRunId
		? sessions.filter((session) => session.dagRunId === options.dagRunId)
		: sessions;
	// A run filter is the whole view: every member belongs to it, including ones
	// the board would otherwise hide as archived.
	const includeArchived = options.includeArchived || options.dagRunId !== undefined;
	const byId = new Map<string, WorkspaceSession>();
	for (const session of source) {
		if (!byId.has(session.id)) byId.set(session.id, session);
	}
	const ordered = [...byId.values()].sort(compareSessions);
	const droppedEdges: SessionGraphDroppedEdge[] = [];
	const parentById = new Map<string, string>();

	for (const session of ordered) {
		const declaredParentId = session.parentSessionId;
		if (!declaredParentId) continue;
		if (declaredParentId === session.id) {
			droppedEdges.push({
				from: sessionKey(session.id),
				to: sessionKey(declaredParentId),
				reason: "self",
			});
			continue;
		}
		const parent = byId.get(declaredParentId);
		if (!parent) {
			droppedEdges.push({
				from: sessionKey(session.id),
				to: sessionKey(declaredParentId),
				reason: "missing-parent",
			});
			continue;
		}
		if (parent.workspaceId !== session.workspaceId) {
			droppedEdges.push({
				from: sessionKey(session.id),
				to: sessionKey(declaredParentId),
				reason: "cross-project",
			});
			continue;
		}
		parentById.set(session.id, parent.id);
	}

	dropCycleEdges(ordered, parentById, droppedEdges);

	const dependencyPairs = dropDependencyCycles(
		collectDependencyPairs(ordered, byId, droppedEdges),
		ordered.map((session) => session.id),
		droppedEdges,
	);

	const includedIds = new Set<string>();
	const contextOnlyIds = new Set<string>();
	for (const session of ordered) {
		if (includeArchived || !isArchivedSession(session)) {
			includedIds.add(session.id);
		}
	}
	if (!includeArchived) {
		// A visible session's archived dependency stays in the graph as a
		// context-only node, matching the archived-ancestor rule, so the
		// dependency edge does not dangle. Its own dependencies follow, so the
		// whole chain stays visible.
		const pending = [...includedIds];
		const visited = new Set(pending);
		while (pending.length > 0) {
			const id = pending.pop();
			if (id === undefined) continue;
			const session = byId.get(id);
			if (!session) continue;
			for (const dependency of sessionDependencies(session)) {
				const dependencyId = dependency.sessionId;
				if (visited.has(dependencyId) || !byId.has(dependencyId)) continue;
				visited.add(dependencyId);
				includedIds.add(dependencyId);
				contextOnlyIds.add(dependencyId);
				pending.push(dependencyId);
			}
		}
		for (const session of ordered) {
			if (!includedIds.has(session.id)) continue;
			const seen = new Set<string>([session.id]);
			let parentId = parentById.get(session.id);
			while (parentId !== undefined && !seen.has(parentId)) {
				seen.add(parentId);
				if (!includedIds.has(parentId)) {
					includedIds.add(parentId);
					contextOnlyIds.add(parentId);
				}
				parentId = parentById.get(parentId);
			}
		}
	}

	const nodeByKey = new Map<string, SessionGraphNode>();
	const childrenByKey = new Map<string, string[]>();
	const roots: string[] = [];

	for (const session of ordered) {
		if (!includedIds.has(session.id)) continue;
		const key = sessionKey(session.id);
		const parentId = parentById.get(session.id);
		const parentKey = parentId !== undefined && includedIds.has(parentId)
			? sessionKey(parentId)
			: null;
		const node: SessionGraphNode = {
			key,
			kind: "session",
			session,
			parentKey,
			depth: 0,
			x: 0,
			y: 0,
			width: SESSION_GRAPH_NODE_WIDTH,
			height: SESSION_GRAPH_SESSION_HEIGHT,
			contextOnly: contextOnlyIds.has(session.id),
			schedulingState: sessionSchedulingState(session),
			blockedReason: session.blockedReason,
			dependencyCount: sessionDependencies(session).length,
			hiddenDependencyCount: 0,
			dependentCount: sessionDependents(session).length,
		};
		nodeByKey.set(key, node);
		if (parentKey === null) roots.push(key);
	}

	for (const session of ordered) {
		if (!includedIds.has(session.id)) continue;
		const parentKey = sessionKey(session.id);
		for (const pr of sortedPRs(session)) {
			const key = prKey(session.id, pr.number);
			if (nodeByKey.has(key)) continue;
			const node: SessionGraphNode = {
				key,
				kind: "pr",
				session,
				pr,
				parentKey,
				depth: 0,
				x: 0,
				y: 0,
				width: SESSION_GRAPH_NODE_WIDTH,
				height: SESSION_GRAPH_PR_HEIGHT,
				contextOnly: contextOnlyIds.has(session.id),
				schedulingState: sessionSchedulingState(session),
				blockedReason: session.blockedReason,
				dependencyCount: 0,
				hiddenDependencyCount: 0,
				dependentCount: 0,
			};
			nodeByKey.set(key, node);
		}
	}

	for (const node of nodeByKey.values()) {
		if (node.parentKey === null) continue;
		const siblings = childrenByKey.get(node.parentKey) ?? [];
		siblings.push(node.key);
		childrenByKey.set(node.parentKey, siblings);
	}

	const edges: SessionGraphEdge[] = [];
	for (const node of nodeByKey.values()) {
		if (node.parentKey === null) continue;
		const parentSessionId = node.parentKey.startsWith(SESSION_KEY_PREFIX)
			? node.parentKey.slice(SESSION_KEY_PREFIX.length)
			: node.parentKey;
		edges.push({
			id: node.kind === "pr"
				? `delivers:${node.session.id}:${node.pr?.number ?? 0}`
				: `spawn:${parentSessionId}:${node.session.id}`,
			from: node.parentKey,
			to: node.key,
			kind: node.kind === "pr" ? "delivers" : "spawn",
		});
	}
	if (options.showDependencies !== false) {
		for (const pair of dependencyPairs) {
			if (!includedIds.has(pair.from) || !includedIds.has(pair.to)) {
				droppedEdges.push({
					from: sessionKey(pair.from),
					to: sessionKey(pair.to),
					reason: "filtered",
				});
				continue;
			}
			edges.push({
				id: `depends:${pair.from}:${pair.to}`,
				from: sessionKey(pair.from),
				to: sessionKey(pair.to),
				kind: "depends",
			});
		}
	}

	// Count every dependency the graph does not draw — filtered, missing from the
	// data, or hidden by the toggle — so a node can say what it is not showing.
	const drawnDependencies = new Set(
		edges
			.filter((edge) => edge.kind === "depends")
			.map((edge) => `${edge.from}\u0000${edge.to}`),
	);
	for (const node of nodeByKey.values()) {
		if (node.kind !== "session") continue;
		node.hiddenDependencyCount = sessionDependencies(node.session).filter(
			(dependency) =>
				!drawnDependencies.has(`${sessionKey(dependency.sessionId)}\u0000${node.key}`),
		).length;
	}

	const subtreeWidth = new Map<string, number>();
	const computeSubtreeWidth = (key: string): number => {
		const node = nodeByKey.get(key);
		if (!node) return 0;
		const children = childrenByKey.get(key) ?? [];
		if (children.length === 0) {
			subtreeWidth.set(key, node.width);
			return node.width;
		}
		let childrenWidth = 0;
		for (const child of children) childrenWidth += computeSubtreeWidth(child);
		childrenWidth += SESSION_GRAPH_H_GAP * (children.length - 1);
		const width = Math.max(node.width, childrenWidth);
		subtreeWidth.set(key, width);
		return width;
	};

	const placeNode = (key: string, left: number, depth: number): void => {
		const node = nodeByKey.get(key);
		if (!node) return;
		node.depth = depth;
		node.y = depth * (SESSION_GRAPH_SESSION_HEIGHT + SESSION_GRAPH_V_GAP);
		const children = childrenByKey.get(key) ?? [];
		if (children.length === 0) {
			node.x = left;
			return;
		}
		let childrenWidth = 0;
		for (const child of children) childrenWidth += subtreeWidth.get(child) ?? 0;
		childrenWidth += SESSION_GRAPH_H_GAP * (children.length - 1);
		const width = subtreeWidth.get(key) ?? node.width;
		let cursor = left + (width - childrenWidth) / 2;
		for (const child of children) {
			placeNode(child, cursor, depth + 1);
			cursor += (subtreeWidth.get(child) ?? 0) + SESSION_GRAPH_H_GAP;
		}
		const firstChild = nodeByKey.get(children[0]);
		const lastChild = nodeByKey.get(children[children.length - 1]);
		if (firstChild && lastChild) {
			const centered = (firstChild.x + lastChild.x + lastChild.width) / 2 - node.width / 2;
			node.x = Math.max(left, Math.min(centered, left + width - node.width));
		}
	};

	for (const key of roots) computeSubtreeWidth(key);
	let cursor = SESSION_GRAPH_PADDING;
	for (const key of roots) {
		placeNode(key, cursor, 0);
		cursor += (subtreeWidth.get(key) ?? 0) + SESSION_GRAPH_ROOT_GAP;
	}

	const spawnOrder: string[] = [];
	const collect = (key: string): void => {
		spawnOrder.push(key);
		for (const child of childrenByKey.get(key) ?? []) collect(child);
	};
	for (const key of roots) collect(key);

	const nodeOrder = options.layout === "dependency"
		? placeDependencyLayers(nodeByKey, dependencyPairs, ordered)
		: spawnOrder;

	let right = 0;
	let bottom = 0;
	for (const key of nodeOrder) {
		const node = nodeByKey.get(key);
		if (!node) continue;
		right = Math.max(right, node.x + node.width);
		bottom = Math.max(bottom, node.y + node.height);
	}

	return {
		nodes: nodeOrder.flatMap((key) => {
			const node = nodeByKey.get(key);
			return node ? [node] : [];
		}),
		edges,
		roots,
		width: nodeOrder.length > 0 ? right + SESSION_GRAPH_PADDING : 0,
		height: nodeOrder.length > 0 ? bottom + SESSION_GRAPH_PADDING : 0,
		droppedEdges,
	};
}
