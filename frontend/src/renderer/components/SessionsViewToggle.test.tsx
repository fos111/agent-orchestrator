import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
	SessionsViewToggle,
	shouldShowSessionsViewToggle,
} from "./SessionsViewToggle";

describe("shouldShowSessionsViewToggle", () => {
	it("requires a project and view callbacks", () => {
		expect(
			shouldShowSessionsViewToggle({
				projectId: "p1",
				hasCallbacks: true,
				preview: false,
				cloudProject: false,
			}),
		).toBe(true);
		expect(
			shouldShowSessionsViewToggle({
				hasCallbacks: true,
				preview: false,
				cloudProject: false,
			}),
		).toBe(false);
		expect(
			shouldShowSessionsViewToggle({
				projectId: "p1",
				hasCallbacks: false,
				preview: false,
				cloudProject: false,
			}),
		).toBe(false);
	});

	it("hides the toggle for preview and cloud projects", () => {
		expect(
			shouldShowSessionsViewToggle({
				projectId: "p1",
				hasCallbacks: true,
				preview: true,
				cloudProject: false,
			}),
		).toBe(false);
		expect(
			shouldShowSessionsViewToggle({
				projectId: "p1",
				hasCallbacks: true,
				preview: false,
				cloudProject: true,
			}),
		).toBe(false);
	});
});

describe("SessionsViewToggle", () => {
	it("marks the active view and reports changes", () => {
		const onChange = vi.fn();
		render(<SessionsViewToggle view="graph" onChange={onChange} />);

		const group = screen.getByRole("group", { name: "Sessions view" });
		expect(group).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Board" })).toHaveAttribute(
			"aria-pressed",
			"false",
		);
		expect(screen.getByRole("button", { name: "Graph" })).toHaveAttribute(
			"aria-pressed",
			"true",
		);

		fireEvent.click(screen.getByRole("button", { name: "Board" }));
		expect(onChange).toHaveBeenCalledWith("board");
	});
});
