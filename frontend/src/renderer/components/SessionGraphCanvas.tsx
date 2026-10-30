import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { GitBranch, GitPullRequest, Minus, Plus, RotateCcw } from "lucide-react";
import { AgentAvatar } from "./AgentAvatar";
import { ProductExternalLink } from "./ProductExternalLink";
import {
	getSessionStatusDotView,
	getSessionStatusView,
} from "../lib/session-presentation";
import { isArchivedSession } from "../lib/standalone-archive";
import {
	SESSION_GRAPH_NODE_WIDTH,
	type SessionGraph,
	type SessionGraphEdge,
	type SessionGraphNode,
} from "../lib/session-graph";
import type { WorkspaceSession } from "../types/workspace";
import { cn } from "../lib/utils";

const MIN_ZOOM = 0.5;
const MAX_ZOOM = 1.5;
const ZOOM_STEP = 0.1;

type SessionGraphCanvasProps = {
	graph: SessionGraph;
	onOpenSession: (session: WorkspaceSession) => void;
};

function clampZoom(zoom: number): number {
	return Math.round(Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom)) * 10) / 10;
}

function edgePath(parent: SessionGraphNode, child: SessionGraphNode): string {
	const startX = parent.x + parent.width / 2;
	const startY = parent.y + parent.height;
	const endX = child.x + child.width / 2;
	const endY = child.y;
	const middle = (endY - startY) / 2;
	return `M ${startX} ${startY} C ${startX} ${startY + middle}, ${endX} ${endY - middle}, ${endX} ${endY}`;
}

export function SessionGraphCanvas({ graph, onOpenSession }: SessionGraphCanvasProps) {
	const { t } = useTranslation();
	const [zoom, setZoom] = useState(1);
	const [hoveredKey, setHoveredKey] = useState<string | null>(null);
	const [focusedKey, setFocusedKey] = useState<string | null>(null);

	const nodeByKey = useMemo(
		() => new Map(graph.nodes.map((node) => [node.key, node])),
		[graph.nodes],
	);
	const activeKey = focusedKey ?? hoveredKey;
	const activeNode = activeKey ? nodeByKey.get(activeKey) : undefined;
	const highlightedKeys = useMemo(() => {
		const keys = new Set<string>();
		if (!activeKey) return keys;
		keys.add(activeKey);
		const active = nodeByKey.get(activeKey);
		if (active?.parentKey) keys.add(active.parentKey);
		for (const node of graph.nodes) {
			if (node.parentKey === activeKey) keys.add(node.key);
		}
		return keys;
	}, [activeKey, graph.nodes, nodeByKey]);

	const handleLeave = (key: string) => {
		if (focusedKey !== key) setHoveredKey(null);
	};

	const summary = (() => {
		if (!activeNode) return null;
		const session = activeNode.session;
		if (activeNode.kind === "pr" && activeNode.pr) {
			const pr = activeNode.pr;
			const stateLabel = t(`pr.state.${pr.state}`, { defaultValue: pr.state });
			return (
				<>
					<span className="truncate font-medium text-foreground">{session.title}</span>
					<span>
						{t("pr.short")} #{pr.number} · {stateLabel}
					</span>
					<ProductExternalLink
						className="underline underline-offset-2 hover:text-foreground focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						href={pr.url}
						stopPropagation
					>
						{pr.url}
					</ProductExternalLink>
				</>
			);
		}
		const kindLabel = session.kind === "orchestrator" ? t("shell.orchestrator") : t("shell.worker");
		const statusLabel = getSessionStatusView(session.status, t).label;
		const parent = activeNode.parentKey ? nodeByKey.get(activeNode.parentKey) : undefined;
		return (
			<>
				<span className="truncate font-medium text-foreground">{session.title}</span>
				<span>
					{kindLabel} · {statusLabel}
				</span>
				{session.branch ? (
					<span className="inline-flex min-w-0 items-center gap-1">
						<GitBranch className="size-3 shrink-0" aria-hidden="true" />
						<span className="truncate">{session.branch}</span>
					</span>
				) : null}
				<span>
					{t("shell.graphParent")}: {parent?.session.title ?? "—"}
				</span>
				<span>
					{t("shell.graphPullRequests")}: {session.prs.length}
				</span>
			</>
		);
	})();

	const renderEdge = (edge: SessionGraphEdge) => {
		const parent = nodeByKey.get(edge.from);
		const child = nodeByKey.get(edge.to);
		if (!parent || !child) return null;
		const dimmed =
			activeKey !== null && edge.from !== activeKey && edge.to !== activeKey;
		return (
			<path
				key={edge.id}
				data-edge-id={edge.id}
				data-edge-kind={edge.kind}
				data-dimmed={dimmed ? "true" : undefined}
				className={cn(
					"fill-none stroke-border motion-safe:transition-opacity",
					dimmed && "opacity-20",
				)}
				d={edgePath(parent, child)}
				strokeDasharray={edge.kind === "delivers" ? "4 4" : undefined}
				strokeWidth={1.5}
				vectorEffect="non-scaling-stroke"
			/>
		);
	};

	const renderSessionNode = (node: SessionGraphNode) => {
		const session = node.session;
		const kindLabel = session.kind === "orchestrator" ? t("shell.orchestrator") : t("shell.worker");
		const status = getSessionStatusView(session.status, t);
		const dot = getSessionStatusDotView(session, t);
		const archived = node.contextOnly || isArchivedSession(session);
		const dimmed = activeKey !== null && !highlightedKeys.has(node.key);
		return (
			<button
				key={node.key}
				type="button"
				data-node-key={node.key}
				data-node-kind={node.kind}
				data-context-only={node.contextOnly ? "true" : undefined}
				data-dimmed={dimmed ? "true" : undefined}
				aria-label={`${session.title}, ${kindLabel}, ${status.label}`}
				title={
					node.contextOnly
						? t("shell.graphArchivedContext")
						: archived
							? t("shell.graphArchived")
							: undefined
				}
				className={cn(
					"absolute flex flex-col justify-center gap-1 rounded-lg border border-border bg-card px-3 text-left text-card-foreground shadow-sm",
					"motion-safe:transition-opacity focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
					archived && "border-dashed opacity-70",
					dimmed && "opacity-30",
				)}
				style={{
					left: node.x,
					top: node.y,
					width: node.width,
					height: node.height,
				}}
				onClick={() => onOpenSession(session)}
				onMouseEnter={() => setHoveredKey(node.key)}
				onMouseLeave={() => handleLeave(node.key)}
				onFocus={() => setFocusedKey(node.key)}
				onBlur={() => setFocusedKey((current) => (current === node.key ? null : current))}
			>
				<span className="flex min-w-0 items-center gap-2">
					<AgentAvatar className="size-5 shrink-0" decorative provider={session.provider} />
					<span className="truncate text-sm font-medium">{session.title}</span>
					{session.kind === "orchestrator" ? (
						<span className="shrink-0 rounded bg-interactive-hover px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
							{kindLabel}
						</span>
					) : null}
				</span>
				<span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
					<span
						aria-hidden="true"
						className={cn(
							"size-1.5 shrink-0 rounded-full",
							dot.className,
							dot.breathe && "animate-status-pulse",
						)}
					/>
					<span className="truncate">{status.label}</span>
					{archived ? (
						<span className="shrink-0 rounded border border-border px-1 text-[10px]">
							{t("shell.graphArchived")}
						</span>
					) : null}
				</span>
				{session.branch ? (
					<span className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
						<GitBranch className="size-3 shrink-0" aria-hidden="true" />
						<span className="truncate">{session.branch}</span>
					</span>
				) : null}
			</button>
		);
	};

	const renderPRNode = (node: SessionGraphNode) => {
		const pr = node.pr;
		if (!pr) return null;
		const archived = node.contextOnly || isArchivedSession(node.session);
		const dimmed = activeKey !== null && !highlightedKeys.has(node.key);
		const stateLabel = t(`pr.state.${pr.state}`, { defaultValue: pr.state });
		return (
			<div
				key={node.key}
				data-node-key={node.key}
				data-node-kind={node.kind}
				data-context-only={node.contextOnly ? "true" : undefined}
				data-dimmed={dimmed ? "true" : undefined}
				title={
					node.contextOnly
						? t("shell.graphArchivedContext")
						: archived
							? t("shell.graphArchived")
							: undefined
				}
				className={cn(
					"absolute rounded-lg border border-border bg-card shadow-sm",
					"motion-safe:transition-opacity",
					archived && "border-dashed opacity-70",
					dimmed && "opacity-30",
				)}
				style={{
					left: node.x,
					top: node.y,
					width: SESSION_GRAPH_NODE_WIDTH,
					height: node.height,
				}}
				onMouseEnter={() => setHoveredKey(node.key)}
				onMouseLeave={() => handleLeave(node.key)}
				onFocus={() => setFocusedKey(node.key)}
				onBlur={() => setFocusedKey((current) => (current === node.key ? null : current))}
			>
				<ProductExternalLink
					className="flex h-full flex-col justify-center gap-1 rounded-lg px-3 text-card-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					href={pr.url}
					stopPropagation
					ariaLabel={`${t("pr.short")} #${pr.number}, ${stateLabel}`}
				>
					<span className="flex min-w-0 items-center gap-1.5 text-sm font-medium">
						<GitPullRequest className="size-4 shrink-0" aria-hidden="true" />
						<span className="truncate">
							{t("pr.short")} #{pr.number}
						</span>
					</span>
					<span className="truncate text-xs text-muted-foreground">{stateLabel}</span>
				</ProductExternalLink>
			</div>
		);
	};

	return (
		<div className="flex h-full min-h-0 flex-col">
			<div className="flex items-center justify-between gap-4 border-b border-border px-3 py-2">
				<div
					data-testid="session-graph-summary"
					aria-live="polite"
					className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground"
				>
					{summary}
				</div>
				<div className="flex shrink-0 items-center gap-1">
					<button
						type="button"
						aria-label={t("shell.graphZoomOut")}
						className="grid size-8 place-items-center rounded-md border border-border bg-card text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						onClick={() => setZoom((current) => clampZoom(current - ZOOM_STEP))}
					>
						<Minus className="size-4" aria-hidden="true" />
					</button>
					<span className="min-w-10 text-center text-xs tabular-nums text-muted-foreground">
						{Math.round(zoom * 100)}%
					</span>
					<button
						type="button"
						aria-label={t("shell.graphZoomIn")}
						className="grid size-8 place-items-center rounded-md border border-border bg-card text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						onClick={() => setZoom((current) => clampZoom(current + ZOOM_STEP))}
					>
						<Plus className="size-4" aria-hidden="true" />
					</button>
					<button
						type="button"
						aria-label={t("shell.graphZoomReset")}
						className="grid size-8 place-items-center rounded-md border border-border bg-card text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						onClick={() => setZoom(1)}
					>
						<RotateCcw className="size-4" aria-hidden="true" />
					</button>
				</div>
			</div>
			<div className="min-h-0 flex-1 overflow-auto" data-testid="session-graph-scroll">
				<div
					className="relative"
					style={{ width: graph.width * zoom, height: graph.height * zoom }}
				>
					<div
						data-testid="session-graph-canvas"
						className="relative origin-top-left motion-safe:transition-transform"
						style={{
							width: graph.width,
							height: graph.height,
							transform: `scale(${zoom})`,
							transformOrigin: "top left",
						}}
					>
						<svg
							aria-hidden="true"
							className="pointer-events-none absolute inset-0"
							width={graph.width}
							height={graph.height}
							viewBox={`0 0 ${graph.width} ${graph.height}`}
						>
							{graph.edges.map(renderEdge)}
						</svg>
						{graph.nodes.map((node) =>
							node.kind === "pr" ? renderPRNode(node) : renderSessionNode(node),
						)}
					</div>
				</div>
			</div>
		</div>
	);
}
