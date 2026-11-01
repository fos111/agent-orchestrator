import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import type { HostId } from "../lib/hosts";

export type Team = components["schemas"]["Team"];
export type TeamMember = components["schemas"]["TeamMember"];
export type TeamBroadcast = components["schemas"]["BroadcastTeamMessageResponse"];

export const teamQueryKey = (rootSessionId: string, hostId?: HostId) =>
	["team", hostId ?? "local", rootSessionId] as const;

async function fetchTeam(rootSessionId: string, hostId?: HostId): Promise<Team> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/teams/{rootSessionId}", {
		params: { path: { rootSessionId } },
	});
	if (error) throw new Error(apiErrorMessage(error));
	if (!data?.team) throw new Error("Team not found");
	return data.team;
}

/** The daemon-derived team rooted at one orchestrator session. */
export function useTeamQuery(rootSessionId: string | undefined, hostId?: HostId) {
	return useQuery({
		queryKey: teamQueryKey(rootSessionId ?? "", hostId),
		enabled: Boolean(rootSessionId),
		queryFn: () => fetchTeam(rootSessionId ?? "", hostId),
	});
}

/**
 * Broadcast one message to every non-terminated member except the sender. A
 * per-member failure comes back in the response counts; only a request-level
 * failure rejects.
 */
export function useSendTeamMessage(rootSessionId: string | undefined, hostId?: HostId) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (message: string): Promise<TeamBroadcast> => {
			const body = message.trim();
			if (!body) throw new Error("Message is empty");
			const { data, error } = await clientForSessionHost(hostId).POST(
				"/api/v1/teams/{rootSessionId}/messages",
				{
					params: { path: { rootSessionId: rootSessionId ?? "" } },
					body: { message: body },
				},
			);
			if (error) throw new Error(apiErrorMessage(error));
			if (!data) throw new Error("Team broadcast failed");
			return data;
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: teamQueryKey(rootSessionId ?? "", hostId) });
		},
	});
}
