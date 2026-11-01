import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { ProjectBoardEmpty } from "./BoardEmptyStates";
import { SessionGraphCanvas } from "./SessionGraphCanvas";
import { TopbarButton } from "./TopbarButton";
import { buildSessionGraph, type SessionGraphLayout } from "../lib/session-graph";
import { isArchivedSession } from "../lib/standalone-archive";
import type { WorkspaceSession } from "../types/workspace";

export function SessionsGraphView({
	sessions,
	showArchived,
	onShowArchivedChange,
	onOpenSession,
	layout = "spawn",
	onLayoutChange,
	showDependencies = true,
	onShowDependenciesChange,
}: {
	sessions: readonly WorkspaceSession[];
	showArchived: boolean;
	onShowArchivedChange: (show: boolean) => void;
	onOpenSession: (session: WorkspaceSession) => void;
	/** `dependency` layers the graph by dependency depth; `spawn` keeps the tree. */
	layout?: SessionGraphLayout;
	onLayoutChange?: (layout: SessionGraphLayout) => void;
	/** Whether dependency edges are drawn. */
	showDependencies?: boolean;
	onShowDependenciesChange?: (show: boolean) => void;
}) {
	const { t } = useTranslation();
	const graph = useMemo(
		() => buildSessionGraph(sessions, { includeArchived: showArchived, layout, showDependencies }),
		[sessions, showArchived, layout, showDependencies],
	);
	const allArchivedHidden =
		!showArchived && sessions.length > 0 && sessions.every(isArchivedSession);
	const showArchivedAction = allArchivedHidden ? (
		<button
			type="button"
			className="mt-4 rounded-md border border-border px-3 py-1.5 text-sm font-medium text-foreground hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
			onClick={() => onShowArchivedChange(true)}
		>
			{t("shell.showArchived")}
		</button>
	) : undefined;
	// Both controls are opt-in: a surface that does not own the graph's search
	// params (standalone, cloud, preview) renders the graph without them.
	const dependencyControl = onShowDependenciesChange ? (
		<TopbarButton
			variant="secondary"
			aria-pressed={showDependencies}
			className={showDependencies ? "bg-interactive-active text-foreground" : undefined}
			onClick={() => onShowDependenciesChange(!showDependencies)}
		>
			{t("shell.graphDependencies")}
		</TopbarButton>
	) : null;
	const layoutControl = onLayoutChange ? (
		<div
			role="group"
			aria-label={t("shell.graphLayout")}
			className="inline-flex shrink-0 items-center gap-0 rounded-md transition-none hover:bg-interactive-hover"
		>
			{(["spawn", "dependency"] as const).map((option) => (
				<TopbarButton
					key={option}
					variant="secondary"
					aria-pressed={layout === option}
					className={
						layout === option
							? "bg-interactive-active text-foreground hover:bg-interactive-active"
							: undefined
					}
					onClick={() => onLayoutChange(option)}
				>
					{option === "spawn" ? t("shell.graphLayoutSpawn") : t("shell.graphLayoutDependency")}
				</TopbarButton>
			))}
		</div>
	) : null;

	return (
		<div className="flex h-full min-h-0 flex-col">
			<div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2">
				<TopbarButton
					variant="secondary"
					aria-pressed={showArchived}
					className={showArchived ? "bg-interactive-active text-foreground" : undefined}
					onClick={() => onShowArchivedChange(!showArchived)}
				>
					{showArchived ? t("shell.hideArchived") : t("shell.showArchived")}
				</TopbarButton>
				{dependencyControl}
				<div className="min-w-0 flex-1" />
				{layoutControl}
			</div>
			{graph.nodes.length === 0 ? (
				<div className="min-h-0 flex-1">
					<ProjectBoardEmpty actions={showArchivedAction} />
				</div>
			) : (
				<div className="min-h-0 flex-1">
					<SessionGraphCanvas graph={graph} onOpenSession={onOpenSession} />
				</div>
			)}
		</div>
	);
}
