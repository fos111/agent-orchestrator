import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DagRunHeader } from "./DagRunHeader";

const { useDagRunQueryMock } = vi.hoisted(() => ({ useDagRunQueryMock: vi.fn() }));

vi.mock("../hooks/useDagRunQuery", () => ({ useDagRunQuery: useDagRunQueryMock }));

const run = {
	id: "dag-1",
	projectId: "p1",
	name: "Feature X",
	status: "running" as const,
	createdAt: "2026-01-01T00:00:00Z",
	counts: {
		total: 4,
		queued: 1,
		blocked: 1,
		running: 1,
		completed: 1,
		failed: 0,
		cancelled: 0,
	},
	nodes: [],
};

describe("DagRunHeader", () => {
	it("shows the run name, derived status, and node counts", () => {
		useDagRunQueryMock.mockReturnValue({ data: run, isPending: false, isError: false });

		render(<DagRunHeader dagRunId="dag-1" />);

		expect(screen.getByRole("region", { name: "DAG run" })).toBeInTheDocument();
		expect(screen.getByText("Feature X")).toBeInTheDocument();
		expect(screen.getByTestId("dag-run-status")).toHaveTextContent("Running");
		expect(screen.getByTestId("dag-run-status")).toHaveAttribute("data-status", "running");
		expect(screen.getByTestId("dag-run-count-total")).toHaveTextContent("4 nodes");
		expect(screen.getByTestId("dag-run-count-completed")).toHaveTextContent("1 completed");
		expect(screen.getByTestId("dag-run-count-running")).toHaveTextContent("1 running");
		expect(screen.getByTestId("dag-run-count-queued")).toHaveTextContent("1 queued");
		expect(screen.getByTestId("dag-run-count-blocked")).toHaveTextContent("1 blocked");
		expect(screen.queryByTestId("dag-run-count-failed")).not.toBeInTheDocument();
	});

	it("falls back to the run id while loading", () => {
		useDagRunQueryMock.mockReturnValue({ data: undefined, isPending: true, isError: false });

		render(<DagRunHeader dagRunId="dag-1" />);

		expect(screen.getByTestId("dag-run-status")).toHaveTextContent("Loading run…");
		expect(screen.getByText("dag-1")).toBeInTheDocument();
		expect(screen.queryByTestId("dag-run-count-total")).not.toBeInTheDocument();
	});

	it("explains an unavailable run", () => {
		useDagRunQueryMock.mockReturnValue({ data: undefined, isPending: false, isError: true });

		render(<DagRunHeader dagRunId="dag-1" />);

		expect(screen.getByTestId("dag-run-status")).toHaveTextContent("Run details unavailable");
	});

	it("clears the filter through the callback", () => {
		useDagRunQueryMock.mockReturnValue({ data: run, isPending: false, isError: false });
		const onClear = vi.fn();

		render(<DagRunHeader dagRunId="dag-1" onClear={onClear} />);
		fireEvent.click(screen.getByRole("button", { name: "Clear run filter" }));

		expect(onClear).toHaveBeenCalledTimes(1);
	});
});
