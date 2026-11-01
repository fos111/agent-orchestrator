import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronRight, Clock, Lock } from "lucide-react";
import { Badge } from "./ui/badge";
import { getSessionSchedulingView } from "../lib/session-presentation";
import { cn } from "../lib/utils";
import {
	sessionDependencies,
	sessionSchedulingState,
	type WorkspaceSession,
} from "../types/workspace";

function waitingTime(
	timestamp: string | undefined,
	format: (key: "time.justNow") => string,
): string {
	if (!timestamp) return "";
	const elapsedMinutes = Math.floor((Date.now() - Date.parse(timestamp)) / 60_000);
	if (!Number.isFinite(elapsedMinutes) || elapsedMinutes < 1) return format("time.justNow");
	const unit: Intl.RelativeTimeFormatUnit =
		elapsedMinutes < 60 ? "minute" : elapsedMinutes < 1_440 ? "hour" : "day";
	const amount =
		unit === "minute"
			? elapsedMinutes
			: unit === "hour"
				? Math.floor(elapsedMinutes / 60)
				: Math.floor(elapsedMinutes / 1_440);
	return new Intl.RelativeTimeFormat(undefined, { numeric: "always" }).format(-amount, unit);
}

/**
 * Pre-delivery work sits above the lanes rather than in a column of its own: a
 * queued or blocked session has not started, so it is not in any delivery lane's
 * state. The strip is hidden when nothing is waiting, which is every board
 * without dependencies — the lanes below keep their current behavior.
 */
export function SessionsQueueStrip({
	sessions,
	onOpenDependencies,
	onAdmitNow,
}: {
	sessions: readonly WorkspaceSession[];
	/** Opens the dependency detail panel for a session. */
	onOpenDependencies: (session: WorkspaceSession) => void;
	/** Opens the dependency panel with the admit confirmation already up. */
	onAdmitNow: (session: WorkspaceSession) => void;
}) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(true);
	const listId = useId();
	if (sessions.length === 0) return null;
	return (
		<section
			data-testid="sessions-queue-strip"
			aria-label={t("shell.queueAria")}
			className="shrink-0 border-b border-border"
		>
			<div className="flex items-center gap-2 px-3 py-1.5">
				<button
					type="button"
					aria-expanded={expanded}
					aria-controls={listId}
					className="inline-flex items-center gap-1.5 rounded-md px-1.5 py-0.5 text-xs font-medium text-foreground hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					onClick={() => setExpanded((current) => !current)}
				>
					{expanded ? (
						<ChevronDown className="size-3.5 shrink-0" aria-hidden="true" />
					) : (
						<ChevronRight className="size-3.5 shrink-0" aria-hidden="true" />
					)}
					{t("shell.queue")}
				</button>
				<Badge variant="neutral" data-testid="sessions-queue-count">
					{sessions.length}
				</Badge>
			</div>
			{expanded ? (
				<ul id={listId} className="flex flex-col gap-1 px-3 pb-2">
					{sessions.map((session) => {
						const scheduling = getSessionSchedulingView(session, t);
						const dependencyCount = sessionDependencies(session).length;
						return (
							<li
								key={session.id}
								data-session-id={session.id}
								data-scheduling-state={sessionSchedulingState(session)}
								className={cn(
									"flex min-w-0 items-center gap-2 rounded-md border bg-surface px-2 py-1.5 text-xs",
									scheduling.tone === "blocked" ? "border-warning/60" : "border-border",
								)}
							>
								<span
									className={cn(
										"shrink-0",
										scheduling.tone === "blocked" ? "text-warning" : "text-muted-foreground",
									)}
								>
									{scheduling.tone === "blocked" ? (
										<Lock className="size-3.5" aria-hidden="true" />
									) : (
										<Clock className="size-3.5" aria-hidden="true" />
									)}
								</span>
								<button
									type="button"
									aria-label={t("shell.dependenciesOpen", { title: session.title })}
									className="min-w-0 flex-1 truncate rounded-sm text-left font-medium text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
									onClick={() => onOpenDependencies(session)}
								>
									{session.title}
								</button>
								<span className="shrink-0 text-muted-foreground">
									{scheduling.reason ?? scheduling.label}
								</span>
								{dependencyCount > 0 ? (
									<span className="shrink-0 text-muted-foreground">
										{t("shell.graphDependenciesCount", { count: dependencyCount })}
									</span>
								) : null}
								<span className="shrink-0 tabular-nums text-muted-foreground">
									{t("shell.queueWaiting", { time: waitingTime(session.createdAt, t) })}
								</span>
								<button
									type="button"
									className="shrink-0 rounded-md border border-border px-2 py-0.5 font-medium text-foreground hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
									onClick={() => onAdmitNow(session)}
								>
									{t("shell.admitNow")}
								</button>
							</li>
						);
					})}
				</ul>
			) : null}
		</section>
	);
}
