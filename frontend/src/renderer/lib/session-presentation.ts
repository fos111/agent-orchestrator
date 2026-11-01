import {
	attentionZone,
	attentionZoneOrder,
	boardAttentionZoneOrder,
	boardKanbanColumnOrder,
	getAgentActivityView as getPortableAgentActivityView,
	getAttentionZoneView as getPortableAttentionZoneView,
	getAttentionZoneViewForZone as getPortableAttentionZoneViewForZone,
	getKanbanColumnView as getPortableKanbanColumnView,
	getSessionStatusView as getPortableSessionStatusView,
	getSessionTimelinePillView as getPortableSessionTimelinePillView,
	isAgentActivityWorking,
	isSessionIdle,
	type AgentActivityView,
	type AttentionZone,
	type AttentionZoneView,
	type KanbanColumn,
	type KanbanColumnView,
	type ProductUITranslator,
	type SessionStatusView,
	type SessionTimelinePillStatus,
	type SessionTimelinePillView,
} from "@aoagents/product-ui";
import type { TFunction } from "i18next";
import { appI18n, type MessageKey } from "../i18n";
import {
	sessionSchedulingState,
	type SessionActivity,
	type SessionBlockedReason,
	type SessionSchedulingState,
	type SessionStatus,
	type WorkspaceSession,
} from "../types/workspace";

function translator(t: TFunction): ProductUITranslator {
	return (key, values) => t(key as MessageKey, values);
}

export function getAgentActivityView(
	activity?: SessionActivity | null,
	t: TFunction = appI18n.t,
): AgentActivityView {
	return getPortableAgentActivityView(activity, translator(t));
}

export function getSessionStatusView(
	status: SessionStatus,
	t: TFunction = appI18n.t,
): SessionStatusView {
	return getPortableSessionStatusView(status, translator(t));
}

const BLOCKED_REASON_KEYS: Record<SessionBlockedReason, MessageKey> = {
	dependency_pending: "shell.blockedReason.dependencyPending",
	dependency_failed: "shell.blockedReason.dependencyFailed",
	dependency_cancelled: "shell.blockedReason.dependencyCancelled",
	dependency_missing: "shell.blockedReason.dependencyMissing",
	dependency_cycle: "shell.blockedReason.dependencyCycle",
	dependency_unsatisfiable: "shell.blockedReason.dependencyUnsatisfiable",
	project_limit: "shell.blockedReason.projectLimit",
	team_limit: "shell.blockedReason.teamLimit",
	launch_failed: "shell.blockedReason.launchFailed",
	invalid: "shell.blockedReason.invalid",
};

export type SessionSchedulingView = {
	state: SessionSchedulingState;
	/** Short state label. Empty for a session that starts immediately. */
	label: string;
	/** Why it is blocked, when the daemon named a reason. */
	reason?: string;
	/** Which visual treatment the board and graph use. */
	tone: "none" | "queued" | "blocked";
};

/**
 * How the scheduler's verdict on a session reads on the board and graph. A
 * session without scheduling state is `immediate` and gets no label at all, so
 * dependency-free surfaces render exactly as they did before.
 */
export function getSessionSchedulingView(
	session: Pick<WorkspaceSession, "schedulingState" | "blockedReason">,
	t: TFunction = appI18n.t,
): SessionSchedulingView {
	const state = sessionSchedulingState(session);
	switch (state) {
		case "queued":
			return { state, label: t("shell.schedulingQueued"), tone: "queued" };
		case "blocked":
			return {
				state,
				label: t("shell.schedulingBlocked"),
				reason: session.blockedReason
					? t(BLOCKED_REASON_KEYS[session.blockedReason])
					: t("shell.blockedReasonUnknown"),
				tone: "blocked",
			};
		case "cancelled":
			return { state, label: t("shell.schedulingCancelled"), tone: "none" };
		case "admitted":
			return { state, label: t("shell.schedulingAdmitted"), tone: "none" };
		default:
			return { state, label: "", tone: "none" };
	}
}

export function getAttentionZoneView(
	status: SessionStatus,
	t: TFunction = appI18n.t,
): AttentionZoneView {
	return getPortableAttentionZoneView(status, translator(t));
}

export function getAttentionZoneViewForZone(
	zone: AttentionZone,
	t: TFunction = appI18n.t,
): AttentionZoneView {
	return getPortableAttentionZoneViewForZone(zone, translator(t));
}

export type SessionStatusDotView = {
	className: string;
	breathe: boolean;
};

// The session dot carries two independent signals. Colour comes from the board
// section represented by the SCM state, which survives a running agent —
// `status` is activity-first, so it collapses to `working` the moment an agent
// wakes and would otherwise take every pull request tone with it. Merged keeps
// its split-section tone instead of sharing Ready to merge's tone.
//
// Motion stays on raw agent activity. A no-PR idle session is the exception to
// the preserved section colour: when its agent starts working it blinks blue.
export function getSessionStatusDotView(
	session: {
		activity?: SessionActivity | null;
		displayStatus?: string;
		scmStatus?: SessionStatus;
		status: SessionStatus;
	},
	t: TFunction = appI18n.t,
): SessionStatusDotView {
	const working = isAgentActivityWorking(session.activity);
	const closedWithoutMerge = session.displayStatus === "Closed without merge";
	const sectionStatus: SessionStatus =
		closedWithoutMerge ? "exited" : (session.scmStatus ?? session.status);
	const toneStatus = sectionStatus === "idle" && working ? "working" : sectionStatus;
	const className =
		closedWithoutMerge
			? getSessionStatusView("exited", t).dotClassName
			: toneStatus === "exited"
				? "bg-passive"
				: toneStatus === "idle" || toneStatus === "merged"
					? getSessionStatusView(toneStatus, t).dotClassName
					: getAttentionZoneView(toneStatus, t).dotClassName;

	return {
		className,
		breathe: working,
	};
}

export function getKanbanColumnView(
	column: KanbanColumn,
	t: TFunction = appI18n.t,
): KanbanColumnView {
	return getPortableKanbanColumnView(column, translator(t));
}

export function getSessionTimelinePillView(
	status: SessionTimelinePillStatus,
	t: TFunction = appI18n.t,
): SessionTimelinePillView {
	return getPortableSessionTimelinePillView(status, translator(t));
}

/** Live labels for the current locale (getters re-resolve on each access). */
export const attentionZoneLabel: Record<AttentionZone, string> = {
	get merge() {
		return getAttentionZoneViewForZone("merge").label;
	},
	get action() {
		return getAttentionZoneViewForZone("action").label;
	},
	get pending() {
		return getAttentionZoneViewForZone("pending").label;
	},
	get working() {
		return getAttentionZoneViewForZone("working").label;
	},
	get done() {
		return getAttentionZoneViewForZone("done").label;
	},
};

export {
	attentionZone,
	attentionZoneOrder,
	boardAttentionZoneOrder,
	boardKanbanColumnOrder,
	isAgentActivityWorking,
	isSessionIdle,
};
export type {
	AgentActivityView,
	AttentionZone,
	AttentionZoneView,
	KanbanColumnView,
	SessionStatusView,
	SessionTimelinePillStatus,
	SessionTimelinePillView,
};
