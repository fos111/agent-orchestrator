import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { WorkspaceSession } from "../types/workspace";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";

/** The scheduler actions the queue strip and dependency panel offer. */
export type SessionSchedulingAction = "admit" | "complete" | "cancel";

export type SessionSchedulingRequest = {
	session: WorkspaceSession;
	action: SessionSchedulingAction;
	/** Admit regardless of dependencies and limits. */
	force?: boolean;
};

/**
 * Drive a queued or blocked session from the board: admit it now, record it
 * complete, or cancel it before it launches. The daemon owns every decision —
 * a non-forced admit still re-evaluates dependencies and limits — so this hook
 * only posts the request and refreshes the board.
 */
export function useSessionScheduling() {
	const queryClient = useQueryClient();
	const { t } = useTranslation();
	const mutation = useMutation({
		mutationFn: async ({ session, action, force }: SessionSchedulingRequest): Promise<void> => {
			const client = session.hostId ? clientForHost(session.hostId) : apiClient;
			const params = { path: { sessionId: session.id } };
			if (action === "admit") {
				const { error } = await client.POST("/api/v1/sessions/{sessionId}/admit", {
					params,
					body: { force: force === true },
				});
				if (error) throw new Error(apiErrorMessage(error, t("shell.schedulingActionFailed")));
				return;
			}
			if (action === "complete") {
				const { error } = await client.POST("/api/v1/sessions/{sessionId}/complete", { params });
				if (error) throw new Error(apiErrorMessage(error, t("shell.schedulingActionFailed")));
				return;
			}
			const { error } = await client.POST("/api/v1/sessions/{sessionId}/kill", { params });
			if (error) throw new Error(apiErrorMessage(error, t("shell.schedulingActionFailed")));
		},
		onSuccess: async (_result, { session }) => {
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) });
		},
	});
	const variables = mutation.variables as SessionSchedulingRequest | undefined;
	return {
		admit: (session: WorkspaceSession, force = false) =>
			mutation.mutateAsync({ session, action: "admit", force }),
		complete: (session: WorkspaceSession) => mutation.mutateAsync({ session, action: "complete" }),
		cancel: (session: WorkspaceSession) => mutation.mutateAsync({ session, action: "cancel" }),
		isPending: mutation.isPending,
		pendingSessionId: mutation.isPending ? variables?.session.id : undefined,
		error: mutation.error instanceof Error ? mutation.error.message : undefined,
		reset: mutation.reset,
	};
}
