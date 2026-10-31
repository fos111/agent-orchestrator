import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { SessionDependenciesPanel } from "./SessionDependenciesPanel";

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

function sessionsById(sessions: WorkspaceSession[]): Map<string, WorkspaceSession> {
	return new Map(sessions.map((session) => [session.id, session]));
}

function renderPanel(
	session: WorkspaceSession | undefined,
	all: WorkspaceSession[],
	options: { startWithAdmitConfirm?: boolean } = {},
) {
	const onOpenChange = vi.fn();
	const onOpenSession = vi.fn();
	const onAdmit = vi.fn();
	const onComplete = vi.fn();
	const onCancel = vi.fn();
	const lookup = sessionsById(all);
	const result = render(
		<SessionDependenciesPanel
			session={session}
			open={session !== undefined}
			onOpenChange={onOpenChange}
			resolveSession={(id) => lookup.get(id)}
			onOpenSession={onOpenSession}
			onAdmit={onAdmit}
			onComplete={onComplete}
			onCancel={onCancel}
			startWithAdmitConfirm={options.startWithAdmitConfirm}
		/>,
	);
	return { ...result, onOpenChange, onOpenSession, onAdmit, onComplete, onCancel };
}

describe("SessionDependenciesPanel", () => {
	it("lists each dependency with its required state, current state, and satisfaction", () => {
		const dependency = makeSession({ id: "dep", title: "Foundation", status: "working" });
		const session = makeSession({
			id: "blocked",
			title: "Follow-up",
			schedulingState: "blocked",
			blockedReason: "dependency_pending",
			dependsOn: [
				{
					sessionId: "dep",
					requiredState: "merged",
					satisfied: false,
					dependencyState: "building",
				},
			],
		});
		renderPanel(session, [dependency, session]);

		const row = screen.getByTestId("dependency-row");
		expect(row).toHaveAttribute("data-satisfied", "false");
		expect(within(row).getByRole("button", { name: "Foundation" })).toBeInTheDocument();
		expect(row).toHaveTextContent("Required: Merged");
		expect(row).toHaveTextContent("Current: building");
		expect(row).toHaveTextContent("Waiting");
	});

	it("lists dependents with their scheduling state", () => {
		const session = makeSession({ id: "dep", title: "Foundation", dependents: ["next"] });
		const dependent = makeSession({
			id: "next",
			title: "Follow-up",
			schedulingState: "queued",
		});
		renderPanel(session, [session, dependent]);

		const row = screen.getByTestId("dependent-row");
		expect(within(row).getByRole("button", { name: "Follow-up" })).toBeInTheDocument();
		expect(row).toHaveTextContent("Queued");
	});

	it("shows the blocked reason and the override badge", () => {
		const session = makeSession({
			id: "blocked",
			schedulingState: "blocked",
			blockedReason: "project_limit",
			admissionOverride: true,
		});
		renderPanel(session, [session]);

		expect(screen.getByText("Blocked because")).toBeInTheDocument();
		expect(
			screen.getByText("The project is at its active-session limit"),
		).toBeInTheDocument();
		expect(screen.getByText("Admitted with an override")).toBeInTheDocument();
	});

	it("opens the admit confirmation and admits with force", () => {
		const session = makeSession({
			id: "blocked",
			title: "Blocked worker",
			schedulingState: "blocked",
			blockedReason: "dependency_pending",
		});
		const { onAdmit } = renderPanel(session, [session]);

		fireEvent.click(screen.getByRole("button", { name: "Admit now" }));
		expect(screen.getByRole("dialog")).toBeInTheDocument();
		expect(
			screen.getByText(/Admitting now skips this session's dependencies/),
		).toBeInTheDocument();

		fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Admit now" }));
		expect(onAdmit).toHaveBeenCalledWith(expect.objectContaining({ id: "blocked" }), true);
	});

	it("opens straight onto the admit confirmation for the queue strip action", () => {
		const session = makeSession({
			id: "queued",
			schedulingState: "queued",
		});
		renderPanel(session, [session], { startWithAdmitConfirm: true });

		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});

	it("marks a non-PR session complete", () => {
		const session = makeSession({ id: "done", title: "Artifact worker" });
		const { onComplete } = renderPanel(session, [session]);

		fireEvent.click(screen.getByRole("button", { name: "Mark complete" }));

		expect(onComplete).toHaveBeenCalledWith(expect.objectContaining({ id: "done" }));
	});

	it("does not offer completion for a session with a PR", () => {
		const session = makeSession({
			id: "pr",
			prs: [
				{
					url: "https://example.com/pr/1",
					number: 1,
					state: "open",
					ci: "passing",
					review: "pending",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-01-01T00:00:00Z",
				},
			],
		});
		renderPanel(session, [session]);

		expect(screen.queryByRole("button", { name: "Mark complete" })).not.toBeInTheDocument();
	});

	it("confirms before cancelling a queued session", () => {
		const session = makeSession({
			id: "queued",
			title: "Waiting worker",
			schedulingState: "queued",
		});
		const { onCancel } = renderPanel(session, [session]);

		fireEvent.click(screen.getByRole("button", { name: "Cancel session" }));
		fireEvent.click(
			within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel session" }),
		);

		expect(onCancel).toHaveBeenCalledWith(expect.objectContaining({ id: "queued" }));
	});

	it("moves focus to the panel heading when it opens", () => {
		const session = makeSession({ id: "queued", schedulingState: "queued" });
		renderPanel(session, [session]);

		const heading = screen.getByRole("heading", { name: "Dependencies" });
		expect(heading).toHaveFocus();
	});

	it("closes through the close button", () => {
		const session = makeSession({ id: "queued", schedulingState: "queued" });
		const { onOpenChange } = renderPanel(session, [session]);

		fireEvent.click(screen.getByRole("button", { name: "Close dependencies" }));

		expect(onOpenChange).toHaveBeenCalledWith(false);
	});
});
