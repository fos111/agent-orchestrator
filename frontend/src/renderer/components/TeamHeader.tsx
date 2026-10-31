import { useState } from "react";
import { useTranslation } from "react-i18next";
import { MessageSquare, Users, X } from "lucide-react";
import type { HostId } from "../lib/hosts";
import { useTeamQuery } from "../hooks/useTeam";
import { TeamPanel } from "./TeamPanel";

/**
 * The summary above a board or graph filtered to one derived team: its lead,
 * member count, and limit usage. The daemon derives every number, so the board
 * never counts members locally. A standalone worker, a desktop New Task
 * session, or a cloud board has no team and never reaches this component.
 *
 * The header always renders when the filter is active, even before the team
 * loads or when the daemon cannot answer, so a filtered board is never
 * unexplained and the clear action is never lost.
 */
export function TeamHeader({
	rootSessionId,
	hostId,
	onClear,
	onOpenSession,
}: {
	rootSessionId: string;
	hostId?: HostId;
	onClear?: () => void;
	onOpenSession?: (sessionId: string) => void;
}) {
	const { t } = useTranslation();
	const teamQuery = useTeamQuery(rootSessionId, hostId);
	const [panelOpen, setPanelOpen] = useState(false);
	const team = teamQuery.data;
	const leadName = team?.orchestrator.displayName || team?.orchestrator.sessionId || rootSessionId;
	const limitLabel = team
		? team.maxActiveWorkers > 0
			? t("shell.teamActiveLimit", {
					active: team.activeWorkers,
					max: team.maxActiveWorkers,
				})
			: t("shell.teamUnlimited")
		: undefined;
	const statusLabel = team ? undefined : teamQuery.isError ? t("shell.teamUnavailable") : t("shell.teamLoading");
	const panelVisible = panelOpen && team !== undefined;

	return (
		<div className="shrink-0 border-b border-border bg-surface" data-testid="team-header">
			<div
				role="region"
				aria-label={t("shell.teamHeaderAria")}
				className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-xs"
			>
				<span className="flex items-center gap-1.5 font-medium text-foreground">
					<Users className="size-3.5 text-muted-foreground" aria-hidden="true" />
					{t("shell.team")}
				</span>
				<span data-testid="team-header-lead" className="text-foreground">
					{leadName}
				</span>
				{team ? (
					<>
						<span data-testid="team-header-members" className="text-muted-foreground">
							{t("shell.teamMemberCount", { count: team.members.length })}
						</span>
						<span data-testid="team-header-limit" className="text-muted-foreground">
							{limitLabel}
						</span>
					</>
				) : (
					<span data-testid="team-header-status" className="text-muted-foreground">
						{statusLabel}
					</span>
				)}
				<button
					type="button"
					className="inline-flex items-center gap-1 rounded border border-border px-1.5 py-0.5 text-muted-foreground hover:bg-interactive-hover hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					aria-expanded={panelVisible}
					disabled={!team}
					onClick={() => setPanelOpen((open) => !open)}
				>
					<MessageSquare className="size-3.5" aria-hidden="true" />
					{t("shell.teamMessage")}
				</button>
				{onClear ? (
					<button
						type="button"
						aria-label={t("shell.teamClear")}
						className="ml-auto rounded p-1 text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						onClick={onClear}
					>
						<X className="size-3.5" aria-hidden="true" />
					</button>
				) : null}
			</div>
			{panelVisible ? (
				<TeamPanel
					hostId={hostId}
					onClose={() => setPanelOpen(false)}
					onOpenSession={onOpenSession}
					rootSessionId={rootSessionId}
				/>
			) : null}
		</div>
	);
}
