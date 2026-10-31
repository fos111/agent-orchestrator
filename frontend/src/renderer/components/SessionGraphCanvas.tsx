import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Clock, GitBranch, GitPullRequest, Lock, Minus, Plus, RotateCcw } from "lucide-react";
import { AgentAvatar } from "./AgentAvatar";
import { ProductExternalLink } from "./ProductExternalLink";
import {
	getSessionSchedulingView,
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

/**
 * Where a straight line between two node centres leaves a node's border, so a
 * dependency arrow starts and ends on the box instead of inside it — the spawn
 * layout puts a dependency above, below, or beside its dependent.
 */
function borderPoint(node: SessionGraphNode, dx: number, dy: number): { x: number; y: number } {
	const centerX = node.x + node.width / 2;
	const centerY = node.y + node.height / 2;
	if (dx === 0 && dy === 0) return { x: centerX, y: centerY };
	const scaleX = dx === 0 ? Number.POSITIVE_INFINITY : node.width / 2 / Math.abs(dx);
	const scaleY = dy === 0 ? Number.POSITIVE_INFINITY : node.height / 2 / Math.abs(dy);
	const scale = Math.min(scaleX, scaleY);
	return { x: centerX + dx * scale, y: centerY + dy * scale };
}

/** Dependency edges run in any direction, so they curve on their dominant axis. */
function dependencyPath(from: SessionGraphNode, to: SessionGraphNode): string {
	const dx = to.x + to.width / 2 - (from.x + from.width / 2);
	const dy = to.y + to.height / 2 - (from.y + from.height / 2);
	const start = borderPoint(from, dx, dy);
	const end = borderPoint(to, -dx, -dy);
	const horizontal = Math.abs(dx) >= Math.abs(dy);
	const firstControl = horizontal
		? { x: start.x + dx / 2, y: start.y }
		: { x: start.x, y: start.y + dy / 2 };
	const secondControl = horizontal
		? { x: end.x - dx / 2, y: end.y }
		: { x: end.x, y: end.y - dy / 2 };
	return `M ${start.x} ${start.y} C ${firstControl.x} ${firstControl.y}, ${secondControl.x} ${secondControl.y}, ${end.x} ${end.y}`;
}

const EDGE_CLASS: Record<SessionGraphEdge["kind"], string> = {
	spawn: "stroke-border",
	delivers: "stroke-border opacity-60",
	depends: "stroke-muted-foreground",
};

const DEPENDENCY_ARROW_ID = "session-graph-dependency-arrow";

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
		const scheduling = getSessionSchedulingView(session, t);
		const parent = activeNode.parentKey ? nodeByKey.get(activeNode.parentKey) : undefined;
		return (
			<>
				<span className="truncate font-medium text-foreground">{session.title}</span>
				<span>
					{kindLabel} · {statusLabel}
				</span>
				{scheduling.tone !== "none" ? (
					<span data-testid="session-graph-scheduling">
						{scheduling.label}
						{scheduling.reason ? ` · ${scheduling.reason}` : ""}
					</span>
				) : null}
				{activeNode.dependencyCount > 0 ? (
					<span>{t("shell.graphDependenciesCount", { count: activeNode.dependencyCount })}</span>
				) : null}
				{activeNode.dependentCount > 0 ? (
					<span>{t("shell.graphDependentsCount", { count: activeNode.dependentCount })}</span>
				) : null}
				{activeNode.hiddenDependencyCount > 0 ? (
					<span>
						{t("shell.graphHiddenDependencies", { count: activeNode.hiddenDependencyCount })}
					</span>
				) : null}
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
		const isDependency = edge.kind === "depends";
		return (
			<path
				key={edge.id}
				data-edge-id={edge.id}
				data-edge-kind={edge.kind}
				data-dimmed={dimmed ? "true" : undefined}
				className={cn(
					"fill-none motion-safe:transition-opacity",
					EDGE_CLASS[edge.kind],
					dimmed && "opacity-20",
				)}
				d={isDependency ? dependencyPath(parent, child) : edgePath(parent, child)}
				strokeDasharray={isDependency ? "6 4" : edge.kind === "delivers" ? "4 4" : undefined}
				strokeWidth={isDependency ? 1.75 : 1.5}
				markerEnd={isDependency ? `url(#${DEPENDENCY_ARROW_ID})` : undefined}
				vectorEffect="non-scaling-stroke"
			>
				{isDependency ? (
					<title>
						{t("shell.graphDependencyEdge", {
							from: parent.session.title,
							to: child.session.title,
						})}
					</title>
				) : null}
			</path>
		);
	};

	const renderSessionNode = (node: SessionGraphNode) => {
		const session = node.session;
		const kindLabel = session.kind === "orchestrator" ? t("shell.orchestrator") : t("shell.worker");
		const status = getSessionStatusView(session.status, t);
		const dot = getSessionStatusDotView(session, t);
		const scheduling = getSessionSchedulingView(session, t);
		const archived = node.contextOnly || isArchivedSession(session);
		const dimmed = activeKey !== null && !highlightedKeys.has(node.key);
		const accessibleName = [
			session.title,
			kindLabel,
			status.label,
			scheduling.label,
			scheduling.reason,
		]
			.filter(Boolean)
			.join(", ");
		return (
			<button
				key={node.key}
				type="button"
				data-node-key={node.key}
				data-node-kind={node.kind}
				data-scheduling-state={scheduling.tone === "none" ? undefined : scheduling.state}
				data-context-only={node.contextOnly ? "true" : undefined}
				data-dimmed={dimmed ? "true" : undefined}
				aria-label={accessibleName}
				title={
					node.contextOnly
						? t("shell.graphArchivedContext")
						: scheduling.reason
							? scheduling.reason
							: archived
								? t("shell.graphArchived")
								: undefined
				}
				className={cn(
					"absolute flex flex-col justify-center gap-1 rounded-lg border border-border bg-card px-3 text-left text-card-foreground shadow-sm",
					"motion-safe:transition-opacity focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
					archived && "border-dashed opacity-70",
					scheduling.tone === "queued" && "border-dashed",
					scheduling.tone === "blocked" && "border-warning",
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
					{scheduling.tone !== "none" ? (
						<span
							data-scheduling-badge={scheduling.state}
							className={cn(
								"inline-flex shrink-0 items-center gap-1 rounded border px-1 text-[10px]",
								scheduling.tone === "blocked"
									? "border-warning/60 text-warning"
									: "border-border text-muted-foreground",
							)}
						>
							{scheduling.tone === "blocked" ? (
								<Lock className="size-3" aria-hidden="true" />
							) : (
								<Clock className="size-3" aria-hidden="true" />
							)}
							{scheduling.label}
						</span>
					) : null}
					{archived ? (
						<span className="shrink-0 rounded border border-border px-1 text-[10px]">
							{t("shell.graphArchived")}
						</span>
					) : null}
				</span>
				{node.dependencyCount > 0 || node.dependentCount > 0 || node.hiddenDependencyCount > 0 ? (
					<span className="flex min-w-0 items-center gap-2 truncate text-xs text-muted-foreground">
						{node.dependencyCount > 0 ? (
							<span className="truncate">
								{t("shell.graphDependenciesCount", { count: node.dependencyCount })}
							</span>
						) : null}
						{node.dependentCount > 0 ? (
							<span className="truncate">
								{t("shell.graphDependentsCount", { count: node.dependentCount })}
							</span>
						) : null}
						{/* A dependency the graph cannot draw still exists; say how many. */}
						{node.hiddenDependencyCount > 0 ? (
							<span
								className="truncate"
								data-testid="session-graph-hidden-dependencies"
							>
								{t("shell.graphHiddenDependencies", { count: node.hiddenDependencyCount })}
							</span>
						) : null}
					</span>
				) : null}
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
							<defs>
								{/* The arrowhead is the dependency edge's direction signal, so
								    the meaning survives without colour. */}
								<marker
									id={DEPENDENCY_ARROW_ID}
									viewBox="0 0 8 8"
									refX="7"
									refY="4"
									markerWidth="7"
									markerHeight="7"
									orient="auto"
								>
									<path d="M 0 0 L 8 4 L 0 8 z" className="fill-muted-foreground" />
								</marker>
							</defs>
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
