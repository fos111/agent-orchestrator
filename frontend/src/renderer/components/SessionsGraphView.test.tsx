import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { PullRequestFacts, WorkspaceSession } from "../types/workspace";
import { SessionsGraphView } from "./SessionsGraphView";

function makeSession(
	overrides: Partial<WorkspaceSession> & Pick<WorkspaceSession, "id">,
): WorkspaceSession {
	return {
		workspaceId: "p1",
		workspaceName: "Project",
		title: overrides.id,
		provider: "claude-code",
		status: "working",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

function makePR(number: number): PullRequestFacts {
	return {
		url: `https://github.com/example/project/pull/${number}`,
		number,
		state: "open",
		ci: "passing",
		review: "approved",
		mergeability: "mergeable",
		reviewComments: false,
		updatedAt: "2026-01-01T00:00:00Z",
	};
}

function sessions(): WorkspaceSession[] {
	return [
		makeSession({
			id: "orch",
			kind: "orchestrator",
			title: "Root orchestrator",
			createdAt: "2026-01-01T00:00:00Z",
		}),
		makeSession({
			id: "w1",
			kind: "worker",
			parentSessionId: "orch",
			title: "Build graph",
			createdAt: "2026-01-01T00:01:00Z",
			prs: [makePR(7)],
		}),
	];
}

function archivedSessions(): WorkspaceSession[] {
	return [
		makeSession({
			id: "orch",
			kind: "orchestrator",
			title: "Archived orchestrator",
			status: "terminated",
			isTerminated: true,
			kanbanColumn: "archive",
			createdAt: "2026-01-01T00:00:00Z",
		}),
	];
}

function renderGraph(
	items: WorkspaceSession[],
	showArchived = false,
	onShowArchivedChange = vi.fn(),
) {
	const onOpenSession = vi.fn();
	const result = render(
		<SessionsGraphView
			sessions={items}
			showArchived={showArchived}
			onShowArchivedChange={onShowArchivedChange}
			onOpenSession={onOpenSession}
		/>,
	);
	return { ...result, onOpenSession, onShowArchivedChange };
}

describe("SessionsGraphView", () => {
	it("renders the orchestrator, worker, and PR nodes from the full session list", () => {
		renderGraph(sessions());

		expect(
			screen.getByRole("button", { name: "Root orchestrator, Orchestrator, Working" }),
		).toBeInTheDocument();
		expect(
			screen.getByRole("button", { name: "Build graph, Worker, Working" }),
		).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "PR #7, open" })).toBeInTheDocument();
	});

	it("toggles the archived sessions", () => {
		const onShowArchivedChange = vi.fn();
		const { rerender } = renderGraph(sessions(), false, onShowArchivedChange);

		fireEvent.click(screen.getByRole("button", { name: "Show archived" }));
		expect(onShowArchivedChange).toHaveBeenCalledWith(true);

		rerender(
			<SessionsGraphView
				sessions={sessions()}
				showArchived
				onShowArchivedChange={onShowArchivedChange}
				onOpenSession={vi.fn()}
			/>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Hide archived" }));
		expect(onShowArchivedChange).toHaveBeenCalledWith(false);
	});

	it("renders the project empty state for an empty project", () => {
		renderGraph([]);

		expect(screen.getByTestId("project-board-empty")).toBeInTheDocument();
	});

	it("offers a working Show archived action when every session is archived and hidden", () => {
		const onShowArchivedChange = vi.fn();
		renderGraph(archivedSessions(), false, onShowArchivedChange);
		const empty = screen.getByTestId("project-board-empty");

		fireEvent.click(within(empty).getByRole("button", { name: "Show archived" }));

		expect(onShowArchivedChange).toHaveBeenCalledWith(true);
	});

	it("opens a session from the graph", () => {
		const { onOpenSession } = renderGraph(sessions());

		fireEvent.click(screen.getByRole("button", { name: /Build graph/ }));

		expect(onOpenSession).toHaveBeenCalledWith(
			expect.objectContaining({ id: "w1" }),
		);
	});

	it("renders no board archive panel or board grid", () => {
		renderGraph(sessions());

		expect(screen.queryByTestId("board-horizontal-scroll")).not.toBeInTheDocument();
		expect(
			screen.queryByRole("button", { name: /^Archive, \d+ sessions?$/ }),
		).not.toBeInTheDocument();
	});
});
