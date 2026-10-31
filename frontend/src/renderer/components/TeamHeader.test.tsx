import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Team } from "../hooks/useTeam";
import { TeamHeader } from "./TeamHeader";

const { useTeamQueryMock, useSendTeamMessageMock, mutateMock } = vi.hoisted(() => ({
	useTeamQueryMock: vi.fn(),
	useSendTeamMessageMock: vi.fn(),
	mutateMock: vi.fn(),
}));

vi.mock("../hooks/useTeam", () => ({
	useTeamQuery: useTeamQueryMock,
	useSendTeamMessage: useSendTeamMessageMock,
}));

const team: Team = {
	rootSessionId: "ao-1",
	projectId: "proj",
	activeWorkers: 2,
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

function query(overrides: Record<string, unknown>) {
	return { data: undefined, isPending: false, isError: false, ...overrides };
}

beforeEach(() => {
	useTeamQueryMock.mockReset();
	useSendTeamMessageMock.mockReset().mockReturnValue({
		mutate: mutateMock,
		reset: vi.fn(),
		isPending: false,
		data: undefined,
		error: null,
	});
	mutateMock.mockReset();
});

describe("TeamHeader", () => {
	it("shows the lead, member count, and limit usage once the team loads", () => {
		useTeamQueryMock.mockReturnValue(query({ data: team }));

		render(<TeamHeader rootSessionId="ao-1" />);

		expect(screen.getByRole("region", { name: "Team summary" })).toBeInTheDocument();
		expect(screen.getByTestId("team-header-lead")).toHaveTextContent("Lead orchestrator");
		expect(screen.getByTestId("team-header-members")).toHaveTextContent("2 members");
		expect(screen.getByTestId("team-header-limit")).toHaveTextContent("2 of 4 active workers");
		expect(screen.queryByTestId("team-header-status")).not.toBeInTheDocument();
	});

	it("uses singular wording for a one-member team", () => {
		useTeamQueryMock.mockReturnValue(query({ data: { ...team, members: team.members.slice(0, 1) } }));

		render(<TeamHeader rootSessionId="ao-1" />);

		expect(screen.getByTestId("team-header-members")).toHaveTextContent("1 member");
	});

	it("explains a team that is still loading and keeps the filter clearable", () => {
		useTeamQueryMock.mockReturnValue(query({ isPending: true }));
		const onClear = vi.fn();

		render(<TeamHeader rootSessionId="ao-1" onClear={onClear} />);

		// The root id stands in for the lead until the daemon answers.
		expect(screen.getByTestId("team-header-lead")).toHaveTextContent("ao-1");
		expect(screen.getByTestId("team-header-status")).toHaveTextContent("Loading team…");
		expect(screen.getByRole("button", { name: "Message team" })).toBeDisabled();

		fireEvent.click(screen.getByRole("button", { name: "Clear team filter" }));
		expect(onClear).toHaveBeenCalledTimes(1);
	});

	it("explains an unavailable team and still allows clearing the filter", () => {
		useTeamQueryMock.mockReturnValue(query({ isError: true }));
		const onClear = vi.fn();

		render(<TeamHeader rootSessionId="ao-1" onClear={onClear} />);

		expect(screen.getByTestId("team-header-status")).toHaveTextContent("Team unavailable");
		expect(screen.getByRole("button", { name: "Message team" })).toBeDisabled();
		fireEvent.click(screen.getByRole("button", { name: "Clear team filter" }));
		expect(onClear).toHaveBeenCalledTimes(1);
	});

	it("opens the roster and composer from the message action", () => {
		useTeamQueryMock.mockReturnValue(query({ data: team }));

		render(<TeamHeader rootSessionId="ao-1" />);
		fireEvent.click(screen.getByRole("button", { name: "Message team" }));

		expect(screen.getByTestId("team-panel")).toBeInTheDocument();
		expect(screen.getByRole("list", { name: "Team members" })).toBeInTheDocument();
		expect(screen.getByText("Worker two")).toBeInTheDocument();
	});

	it("renders no clear action when the board does not own the filter", () => {
		useTeamQueryMock.mockReturnValue(query({ data: team }));

		render(<TeamHeader rootSessionId="ao-1" />);

		expect(screen.queryByRole("button", { name: "Clear team filter" })).not.toBeInTheDocument();
	});
});
