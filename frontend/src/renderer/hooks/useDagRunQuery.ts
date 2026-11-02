import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type DagRun = components["schemas"]["DagRunResponse"];
export type DagRunNode = components["schemas"]["DagRunNodeResponse"];
export type DagRunStatus = DagRun["status"];
export type DagRunNodeStatus = DagRunNode["status"];

export const dagRunQueryKey = (dagRunId: string) => ["dag-run", dagRunId] as const;

async function fetchDagRun(dagRunId: string): Promise<DagRun> {
	const { data, error } = await apiClient.GET("/api/v1/dag-runs/{dagRunId}", {
		params: { path: { dagRunId } },
	});
	if (error) throw new Error(apiErrorMessage(error));
	if (!data?.run) throw new Error("DAG run not found");
	return data.run;
}

/** The run's status and node counts for the graph's run header. */
export function useDagRunQuery(dagRunId: string | undefined) {
	return useQuery({
		queryKey: dagRunQueryKey(dagRunId ?? ""),
		enabled: Boolean(dagRunId),
		queryFn: () => fetchDagRun(dagRunId ?? ""),
	});
}
