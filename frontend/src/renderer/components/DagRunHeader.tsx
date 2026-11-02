import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { MessageKey } from "../i18n";
import { useDagRunQuery, type DagRunNodeStatus, type DagRunStatus } from "../hooks/useDagRunQuery";

const STATUS_LABEL_KEYS: Record<DagRunStatus, MessageKey> = {
	pending: "shell.dagRunStatus.pending",
	running: "shell.dagRunStatus.running",
	completed: "shell.dagRunStatus.completed",
	failed: "shell.dagRunStatus.failed",
	cancelled: "shell.dagRunStatus.cancelled",
};

const COUNT_LABEL_KEYS: Record<DagRunNodeStatus, MessageKey> = {
	queued: "shell.dagRunNodesQueued",
	blocked: "shell.dagRunNodesBlocked",
	running: "shell.dagRunNodesRunning",
	completed: "shell.dagRunNodesCompleted",
	failed: "shell.dagRunNodesFailed",
	cancelled: "shell.dagRunNodesCancelled",
};

const COUNT_ORDER: DagRunNodeStatus[] = [
	"completed",
	"running",
	"queued",
	"blocked",
	"failed",
	"cancelled",
];

/**
 * The header above a run-filtered graph: which run is in view, its derived
 * status, and how its nodes are distributed. The ids and counts come from the
 * daemon so the graph never derives run state locally.
 */
export function DagRunHeader({
	dagRunId,
	onClear,
}: {
	dagRunId: string;
	onClear?: () => void;
}) {
	const { t } = useTranslation();
	const runQuery = useDagRunQuery(dagRunId);
	const run = runQuery.data;
	const statusLabel = run
		? t(STATUS_LABEL_KEYS[run.status])
		: runQuery.isError
			? t("shell.dagRunUnavailable")
			: t("shell.dagRunLoading");
	const counts = run?.counts;

	return (
		<div
			role="region"
			aria-label={t("shell.dagRun")}
			data-testid="dag-run-header"
			className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-border bg-surface px-3 py-2 text-xs"
		>
			<span className="font-medium text-foreground">{run?.name ?? dagRunId}</span>
			<span
				data-testid="dag-run-status"
				data-status={run?.status}
				className="rounded border border-border px-1.5 py-0.5 text-muted-foreground"
			>
				{statusLabel}
			</span>
			{counts ? (
				<span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground">
					<span data-testid="dag-run-count-total">
						{t("shell.dagRunNodesTotal", { count: counts.total })}
					</span>
					{COUNT_ORDER.filter((status) => counts[status] > 0).map((status) => (
						<span key={status} data-testid={`dag-run-count-${status}`}>
							{t(COUNT_LABEL_KEYS[status], { count: counts[status] })}
						</span>
					))}
				</span>
			) : null}
			{onClear ? (
				<button
					type="button"
					aria-label={t("shell.dagRunClear")}
					className="ml-auto rounded p-1 text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					onClick={onClear}
				>
					<X className="size-3.5" aria-hidden="true" />
				</button>
			) : null}
		</div>
	);
}
