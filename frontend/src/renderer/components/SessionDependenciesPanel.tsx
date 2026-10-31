import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, CircleDashed, X } from "lucide-react";
import type { TFunction } from "i18next";
import type { MessageKey } from "../i18n";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { ConfirmDialog } from "./ConfirmDialog";
import {
	getSessionSchedulingView,
	getSessionStatusView,
} from "../lib/session-presentation";
import { cn } from "../lib/utils";
import {
	sessionDependencies,
	sessionDependents,
	sessionIsPreAdmission,
	type SessionDependencyRequiredState,
	type WorkspaceSession,
} from "../types/workspace";

const REQUIRED_STATE_KEYS: Record<SessionDependencyRequiredState, MessageKey> = {
	ready: "shell.dependencyRequiredReady",
	merged: "shell.dependencyRequiredMerged",
	completed: "shell.dependencyRequiredCompleted",
};

function requiredStateLabel(state: SessionDependencyRequiredState, t: TFunction): string {
	return t(REQUIRED_STATE_KEYS[state]);
}

/**
 * The coordination detail for one session: what it waits on, what waits on it,
 * why the scheduler blocked it, and the actions that move it. It is the
 * accessible source of dependency truth — the graph's dependency edges are
 * decorative and `aria-hidden`.
 */
export function SessionDependenciesPanel({
	session,
	open,
	onOpenChange,
	resolveSession,
	onOpenSession,
	onAdmit,
	onComplete,
	onCancel,
	busy = false,
	error,
	startWithAdmitConfirm = false,
}: {
	/** The session the panel describes; nothing renders while it is undefined. */
	session?: WorkspaceSession;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	/** Look a dependency up in the current board data. */
	resolveSession: (sessionId: string) => WorkspaceSession | undefined;
	onOpenSession: (session: WorkspaceSession) => void;
	onAdmit: (session: WorkspaceSession, force: boolean) => void;
	onComplete: (session: WorkspaceSession) => void;
	onCancel: (session: WorkspaceSession) => void;
	busy?: boolean;
	error?: string;
	/** The queue strip's "Admit now" opens straight onto the confirmation. */
	startWithAdmitConfirm?: boolean;
}) {
	const { t } = useTranslation();
	const headingRef = useRef<HTMLHeadingElement>(null);
	const [confirm, setConfirm] = useState<"admit" | "cancel" | null>(null);

	const sessionId = session?.id;
	useEffect(() => {
		if (!open) {
			setConfirm(null);
			return;
		}
		setConfirm(startWithAdmitConfirm ? "admit" : null);
		// Move focus into the panel so a keyboard user lands on what opened.
		headingRef.current?.focus();
	}, [open, startWithAdmitConfirm, sessionId]);

	if (!session || !open) return null;

	const scheduling = getSessionSchedulingView(session, t);
	const dependencies = sessionDependencies(session);
	const dependents = sessionDependents(session);
	const preAdmission = sessionIsPreAdmission(session);
	// A session with a PR is completed by the merge, not by hand; the daemon
	// rejects a manual completion that would contradict PR facts.
	const canMarkComplete = session.prs.length === 0;

	return (
		<aside
			data-testid="session-dependencies-panel"
			aria-labelledby="session-dependencies-panel-title"
			className="absolute inset-y-0 right-0 z-10 flex w-[min(360px,100%)] flex-col border-l border-border bg-background shadow-lg"
		>
			<div className="flex shrink-0 items-start gap-2 border-b border-border px-4 py-3">
				<div className="min-w-0 flex-1">
					<h2
						id="session-dependencies-panel-title"
						ref={headingRef}
						tabIndex={-1}
						className="truncate text-sm font-semibold text-foreground focus-visible:outline-none"
					>
						{t("shell.dependenciesPanel")}
					</h2>
					<p className="truncate text-xs text-muted-foreground">{session.title}</p>
				</div>
				<button
					type="button"
					aria-label={t("shell.dependenciesClose")}
					className="shrink-0 rounded-md p-1 text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					onClick={() => onOpenChange(false)}
				>
					<X className="size-4" aria-hidden="true" />
				</button>
			</div>

			<div className="min-h-0 flex-1 overflow-y-auto px-4 py-3 text-xs">
				<div className="flex flex-wrap items-center gap-2">
					<span className="text-muted-foreground">{t("shell.schedulingLabel")}</span>
					<Badge
						variant={scheduling.tone === "blocked" ? "warning" : "neutral"}
						data-testid="dependencies-panel-scheduling"
					>
						{scheduling.label || t("shell.schedulingImmediate")}
					</Badge>
					{session.admissionOverride ? (
						<Badge variant="outline">{t("shell.admissionOverride")}</Badge>
					) : null}
				</div>

				{scheduling.reason ? (
					<div className="mt-3 rounded-md border border-warning/50 bg-surface px-3 py-2">
						<p className="font-medium text-warning">{t("shell.blockedBecause")}</p>
						<p className="mt-0.5 text-foreground">{scheduling.reason}</p>
					</div>
				) : null}

				<section className="mt-4" aria-labelledby="dependencies-panel-depends-on">
					<h3 id="dependencies-panel-depends-on" className="text-xs font-semibold text-foreground">
						{t("shell.dependenciesTitle")}
					</h3>
					{dependencies.length === 0 ? (
						<p className="mt-1 text-muted-foreground">{t("shell.dependenciesEmpty")}</p>
					) : (
						<ul className="mt-1 flex flex-col gap-1">
							{dependencies.map((dependency) => {
								const target = resolveSession(dependency.sessionId);
								return (
									<li
										key={dependency.sessionId}
										data-testid="dependency-row"
										data-satisfied={dependency.satisfied ? "true" : "false"}
										className="flex min-w-0 items-start gap-2 rounded-md border border-border px-2 py-1.5"
									>
										<span
											className={cn(
												"mt-0.5 shrink-0",
												dependency.satisfied ? "text-success" : "text-muted-foreground",
											)}
											aria-hidden="true"
										>
											{dependency.satisfied ? (
												<Check className="size-3.5" />
											) : (
												<CircleDashed className="size-3.5" />
											)}
										</span>
										<span className="min-w-0 flex-1">
											<span className="flex min-w-0 items-center gap-1">
												<button
													type="button"
													className="min-w-0 truncate rounded-sm text-left font-medium text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
													onClick={() => target && onOpenSession(target)}
													disabled={!target}
												>
													{target?.title ?? dependency.sessionId}
												</button>
											</span>
											<span className="mt-0.5 flex flex-wrap items-center gap-1 text-muted-foreground">
												<span>
													{t("shell.dependencyRequired")}: {requiredStateLabel(dependency.requiredState, t)}
												</span>
												{dependency.dependencyState ? (
													<span>· {t("shell.dependencyCurrent")}: {dependency.dependencyState}</span>
												) : null}
											</span>
										</span>
										<span
											className={cn(
												"shrink-0 rounded border px-1 text-[10px]",
												dependency.satisfied
													? "border-success/50 text-success"
													: "border-border text-muted-foreground",
											)}
										>
											{dependency.satisfied
												? t("shell.dependencySatisfied")
												: t("shell.dependencyUnsatisfied")}
										</span>
									</li>
								);
							})}
						</ul>
					)}
				</section>

				<section className="mt-4" aria-labelledby="dependencies-panel-dependents">
					<h3 id="dependencies-panel-dependents" className="text-xs font-semibold text-foreground">
						{t("shell.dependentsTitle")}
					</h3>
					{dependents.length === 0 ? (
						<p className="mt-1 text-muted-foreground">{t("shell.dependentsEmpty")}</p>
					) : (
						<ul className="mt-1 flex flex-col gap-1">
							{dependents.map((dependentId) => {
								const target = resolveSession(dependentId);
								const dependentScheduling = target ? getSessionSchedulingView(target, t) : undefined;
								return (
									<li
										key={dependentId}
										data-testid="dependent-row"
										className="flex min-w-0 items-center gap-2 rounded-md border border-border px-2 py-1.5"
									>
										<button
											type="button"
											className="min-w-0 flex-1 truncate rounded-sm text-left font-medium text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
											onClick={() => target && onOpenSession(target)}
											disabled={!target}
										>
											{target?.title ?? dependentId}
										</button>
										{target ? (
											<span className="shrink-0 text-muted-foreground">
												{dependentScheduling?.label || getSessionStatusView(target.status, t).label}
											</span>
										) : null}
									</li>
								);
							})}
						</ul>
					)}
				</section>
			</div>

			{error ? (
				<p role="alert" className="shrink-0 border-t border-border px-4 py-2 text-xs text-destructive">
					{error}
				</p>
			) : null}

			{preAdmission || canMarkComplete ? (
				<div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-border px-4 py-3">
					{preAdmission ? (
						<Button
							type="button"
							variant="primary"
							size="sm"
							disabled={busy}
							onClick={() => setConfirm("admit")}
						>
							{t("shell.admitNow")}
						</Button>
					) : null}
					{canMarkComplete ? (
						<Button
							type="button"
							variant="secondary"
							size="sm"
							disabled={busy}
							onClick={() => onComplete(session)}
						>
							{t("shell.markComplete")}
						</Button>
					) : null}
					{preAdmission ? (
						<Button
							type="button"
							variant="secondary"
							size="sm"
							disabled={busy}
							onClick={() => setConfirm("cancel")}
						>
							{t("shell.cancelSession")}
						</Button>
					) : null}
				</div>
			) : null}

			<ConfirmDialog
				open={confirm === "admit"}
				title={t("shell.admitNowTitle")}
				description={t("shell.admitNowBody")}
				confirmLabel={t("shell.admitNow")}
				busy={busy}
				onConfirm={() => {
					setConfirm(null);
					onAdmit(session, true);
				}}
				onOpenChange={(next) => {
					if (!next) setConfirm(null);
				}}
			/>
			<ConfirmDialog
				open={confirm === "cancel"}
				title={t("shell.cancelSessionTitle")}
				description={t("shell.cancelSessionBody")}
				confirmLabel={t("shell.cancelSession")}
				destructive
				busy={busy}
				onConfirm={() => {
					setConfirm(null);
					onCancel(session);
				}}
				onOpenChange={(next) => {
					if (!next) setConfirm(null);
				}}
			/>
		</aside>
	);
}
