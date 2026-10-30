import { isArchivedSession } from "./standalone-archive";
import {
	sortedPRs,
	type PullRequestFacts,
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
};

export type SessionGraphEdge = {
	id: string;
	from: string;
	to: string;
	kind: "spawn" | "delivers";
};

export type SessionGraphDroppedEdge = {
	from: string;
	to: string;
	reason: "missing-parent" | "cross-project" | "cycle" | "self";
};

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

export function buildSessionGraph(
	sessions: readonly WorkspaceSession[],
	options: BuildSessionGraphOptions,
): SessionGraph {
	const byId = new Map<string, WorkspaceSession>();
	for (const session of sessions) {
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

	const includedIds = new Set<string>();
	const contextOnlyIds = new Set<string>();
	for (const session of ordered) {
		if (options.includeArchived || !isArchivedSession(session)) {
			includedIds.add(session.id);
		}
	}
	if (!options.includeArchived) {
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

	const nodeOrder: string[] = [];
	const collect = (key: string): void => {
		nodeOrder.push(key);
		for (const child of childrenByKey.get(key) ?? []) collect(child);
	};
	for (const key of roots) collect(key);

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
