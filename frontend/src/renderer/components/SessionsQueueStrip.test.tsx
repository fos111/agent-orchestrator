import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { SessionsQueueStrip } from "./SessionsQueueStrip";

function makeSession(
	overrides: Partial<WorkspaceSession> & Pick<WorkspaceSession, "id">,
): WorkspaceSession {
	return {
		workspaceId: "p1",
		workspaceName: "Project",
		title: overrides.id,
		provider: "claude-code",
		status: "working",
		createdAt: new Date().toISOString(),
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

function renderStrip(sessions: WorkspaceSession[]) {
	const onOpenDependencies = vi.fn();
	const onAdmitNow = vi.fn();
	const result = render(
		<SessionsQueueStrip
			sessions={sessions}
			onOpenDependencies={onOpenDependencies}
			onAdmitNow={onAdmitNow}
		/>,
	);
	return { ...result, onOpenDependencies, onAdmitNow };
}

describe("SessionsQueueStrip", () => {
	it("renders nothing when the queue is empty", () => {
		renderStrip([]);

		expect(screen.queryByTestId("sessions-queue-strip")).not.toBeInTheDocument();
	});

	it("lists a queued session with its reason, dependency count, and age", () => {
		renderStrip([
			makeSession({
				id: "queued",
				title: "Waiting worker",
				schedulingState: "queued",
				dependsOn: [
					{ sessionId: "a", requiredState: "merged", satisfied: false },
					{ sessionId: "b", requiredState: "merged", satisfied: false },
				],
			}),
		]);

		const strip = screen.getByTestId("sessions-queue-strip");
		expect(within(strip).getByText("Waiting worker")).toBeInTheDocument();
		expect(within(strip).getByText("Queued")).toBeInTheDocument();
		expect(within(strip).getByText("2 dependencies")).toBeInTheDocument();
		expect(within(strip).getByText("Waiting just now")).toBeInTheDocument();
		expect(screen.getByTestId("sessions-queue-count")).toHaveTextContent("1");
	});

	it("shows the blocked reason and opens the dependency panel from the name", () => {
		const { onOpenDependencies } = renderStrip([
			makeSession({
				id: "blocked",
				title: "Blocked worker",
				schedulingState: "blocked",
				blockedReason: "dependency_pending",
			}),
		]);

		expect(screen.getByText("Waiting for a dependency")).toBeInTheDocument();
		fireEvent.click(
			screen.getByRole("button", { name: "Dependencies for Blocked worker" }),
		);
		expect(onOpenDependencies).toHaveBeenCalledWith(
			expect.objectContaining({ id: "blocked" }),
		);
	});

	it("opens the admit confirmation through the Admit now action", () => {
		const { onAdmitNow } = renderStrip([
			makeSession({ id: "blocked", title: "Blocked worker", schedulingState: "blocked" }),
		]);

		fireEvent.click(screen.getByRole("button", { name: "Admit now" }));

		expect(onAdmitNow).toHaveBeenCalledWith(expect.objectContaining({ id: "blocked" }));
	});

	it("collapses and expands the list from a keyboard-reachable toggle", () => {
		renderStrip([
			makeSession({ id: "queued", title: "Waiting worker", schedulingState: "queued" }),
		]);
		const toggle = screen.getByRole("button", { name: "Queue" });

		expect(toggle).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByRole("list")).toBeInTheDocument();

		fireEvent.click(toggle);
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByRole("list")).not.toBeInTheDocument();

		fireEvent.click(toggle);
		expect(screen.getByRole("list")).toBeInTheDocument();
	});
});
