import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Team } from "../hooks/useTeam";
import { TeamPanel } from "./TeamPanel";

const { useTeamQueryMock, useSendTeamMessageMock, mutateMock, resetMock } = vi.hoisted(() => ({
	useTeamQueryMock: vi.fn(),
	useSendTeamMessageMock: vi.fn(),
	mutateMock: vi.fn(),
	resetMock: vi.fn(),
}));

vi.mock("../hooks/useTeam", () => ({
	useTeamQuery: useTeamQueryMock,
	useSendTeamMessage: useSendTeamMessageMock,
}));

const team: Team = {
	rootSessionId: "ao-1",
	projectId: "proj",
	activeWorkers: 1,
	maxActiveWorkers: 4,
	orchestrator: {
		sessionId: "ao-1",
		displayName: "Lead orchestrator",
		kind: "orchestrator",
		isTerminated: false,
		createdAt: "2026-01-01T00:00:00Z",
	},
	members: [
		{
			sessionId: "ao-1",
			displayName: "Lead orchestrator",
			kind: "orchestrator",
			isTerminated: false,
			createdAt: "2026-01-01T00:00:00Z",
		},
		{
			sessionId: "ao-2",
			displayName: "Worker two",
			kind: "worker",
			isTerminated: false,
			createdAt: "2026-01-01T00:01:00Z",
		},
	],
};

function mutation(overrides: Record<string, unknown> = {}) {
	return {
		mutate: mutateMock,
		reset: resetMock,
		isPending: false,
		data: undefined,
		error: null,
		...overrides,
	};
}

beforeEach(() => {
	useTeamQueryMock.mockReset().mockReturnValue({ data: team, isPending: false, isError: false });
	useSendTeamMessageMock.mockReset().mockReturnValue(mutation());
	mutateMock.mockReset();
	resetMock.mockReset();
});

describe("TeamPanel", () => {
	it("lists the roster, marking the lead and linking other members", () => {
		const onOpenSession = vi.fn();
		render(<TeamPanel rootSessionId="ao-1" onClose={vi.fn()} onOpenSession={onOpenSession} />);

		expect(screen.getByRole("list", { name: "Team members" })).toBeInTheDocument();
		expect(screen.getByText("Lead orchestrator")).toBeInTheDocument();
		expect(screen.getByText("Lead")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Worker two/ })).toBeInTheDocument();

		fireEvent.click(screen.getByRole("button", { name: /Worker two/ }));
		expect(onOpenSession).toHaveBeenCalledWith("ao-2");
	});

	it("explains an empty roster", () => {
		useTeamQueryMock.mockReturnValue({ data: { ...team, members: [] }, isPending: false, isError: false });
		render(<TeamPanel rootSessionId="ao-1" onClose={vi.fn()} />);

		expect(screen.getByText("No active members")).toBeInTheDocument();
	});

	it("disables send until the composer has a body and trims before broadcasting", () => {
		render(<TeamPanel rootSessionId="ao-1" onClose={vi.fn()} />);
		const send = screen.getByRole("button", { name: "Send to team" });
		expect(send).toBeDisabled();

		fireEvent.change(screen.getByPlaceholderText("Write a message for every team member…"), {
			target: { value: "  hello team  " },
		});
		expect(send).toBeEnabled();
		fireEvent.click(send);

		expect(mutateMock).toHaveBeenCalledWith("  hello team  ", expect.anything());
	});

	it("reports per-member delivery counts after a broadcast", () => {
		render(<TeamPanel rootSessionId="ao-1" onClose={vi.fn()} />);

		expect(screen.queryByTestId("team-message-result")).not.toBeInTheDocument();
	});

	it("shows the broadcast result when the mutation settles", () => {
		useSendTeamMessageMock.mockReturnValue(
			mutation({ data: { delivered: 3, failed: 1, deliveries: [] } }),
		);
		render(<TeamPanel rootSessionId="ao-1" onClose={vi.fn()} />);

		expect(screen.getByTestId("team-message-result")).toHaveTextContent("Delivered to 3, 1 failed");
	});

	it("shows a request-level failure as an alert", () => {
		useSendTeamMessageMock.mockReturnValue(mutation({ error: new Error("daemon offline") }));
		render(<TeamPanel rootSessionId="ao-1" onClose={vi.fn()} />);

		expect(screen.getByRole("alert")).toHaveTextContent("daemon offline");
	});

	it("closes through the panel action", () => {
		const onClose = vi.fn();
		render(<TeamPanel rootSessionId="ao-1" onClose={onClose} />);

		fireEvent.click(screen.getByRole("button", { name: "Close team panel" }));
		expect(onClose).toHaveBeenCalledTimes(1);
	});
});
