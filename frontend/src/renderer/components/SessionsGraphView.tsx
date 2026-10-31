import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { ProjectBoardEmpty } from "./BoardEmptyStates";
import { SessionGraphCanvas } from "./SessionGraphCanvas";
import { TopbarButton } from "./TopbarButton";
import { buildSessionGraph } from "../lib/session-graph";
import { isArchivedSession } from "../lib/standalone-archive";
import type { WorkspaceSession } from "../types/workspace";

export function SessionsGraphView({
	sessions,
	showArchived,
	onShowArchivedChange,
	onOpenSession,
}: {
	sessions: readonly WorkspaceSession[];
	showArchived: boolean;
	onShowArchivedChange: (show: boolean) => void;
	onOpenSession: (session: WorkspaceSession) => void;
}) {
	const { t } = useTranslation();
	const graph = useMemo(
		() => buildSessionGraph(sessions, { includeArchived: showArchived }),
		[sessions, showArchived],
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
