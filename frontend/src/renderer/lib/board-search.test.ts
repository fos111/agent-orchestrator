import { describe, expect, it } from "vitest";
import {
	boardSearchEdges,
	boardSearchRun,
	boardSearchWithArchived,
	boardSearchWithEdges,
	boardSearchWithLayout,
	boardSearchWithRun,
	boardSearchWithView,
	validateBoardSearch,
} from "./board-search";

describe("validateBoardSearch", () => {
	it("accepts only the graph view", () => {
		expect(validateBoardSearch({ view: "graph" })).toEqual({ view: "graph" });
		expect(validateBoardSearch({ view: "board" })).toEqual({});
		expect(validateBoardSearch({ view: "other" })).toEqual({});
		expect(validateBoardSearch({})).toEqual({});
	});

	it("accepts the three archived shapes", () => {
		expect(validateBoardSearch({ archived: true })).toEqual({ archived: true });
		expect(validateBoardSearch({ archived: 1 })).toEqual({ archived: true });
		expect(validateBoardSearch({ archived: "1" })).toEqual({ archived: true });
	});

	it("rejects other archived shapes", () => {
		expect(validateBoardSearch({ archived: false })).toEqual({});
		expect(validateBoardSearch({ archived: 0 })).toEqual({});
		expect(validateBoardSearch({ archived: "0" })).toEqual({});
		expect(validateBoardSearch({ archived: "true" })).toEqual({});
	});
});

describe("boardSearchWithView", () => {
	it("clears both keys for the board view", () => {
		expect(boardSearchWithView({ view: "graph", archived: true }, "board")).toEqual({});
	});

	it("keeps archived when switching to the graph view", () => {
		expect(boardSearchWithView({ archived: true }, "graph")).toEqual({
			view: "graph",
			archived: true,
		});
		expect(boardSearchWithView({}, "graph")).toEqual({ view: "graph" });
	});
});

describe("boardSearchWithArchived", () => {
	it("sets and clears archived while keeping the graph view", () => {
		expect(boardSearchWithArchived({ view: "graph" }, true)).toEqual({
			view: "graph",
			archived: true,
		});
		expect(boardSearchWithArchived({ view: "graph", archived: true }, false)).toEqual({
			view: "graph",
		});
	});

	it("keeps the board search free of view", () => {
		expect(boardSearchWithArchived({}, true)).toEqual({ archived: true });
		expect(boardSearchWithArchived({ archived: true }, false)).toEqual({});
	});
});

describe("validateBoardSearch layout and edges", () => {
	it("accepts only the dependency layout", () => {
		expect(validateBoardSearch({ layout: "dependency" })).toEqual({ layout: "dependency" });
		expect(validateBoardSearch({ layout: "spawn" })).toEqual({});
		expect(validateBoardSearch({ layout: "circular" })).toEqual({});
	});

	it("parses a comma-separated edge list in catalog order", () => {
		expect(validateBoardSearch({ edges: "delivers,spawn" })).toEqual({
			edges: ["spawn", "delivers"],
		});
	});

	it("parses a repeated edge query key and drops unknown kinds", () => {
		expect(validateBoardSearch({ edges: ["depends", "spawn", "bogus"] })).toEqual({
			edges: ["spawn", "depends"],
		});
	});

	it("falls back to the default full edge set", () => {
		expect(validateBoardSearch({ edges: "spawn,depends,delivers" })).toEqual({});
		expect(validateBoardSearch({ edges: "bogus" })).toEqual({});
		expect(validateBoardSearch({ edges: "" })).toEqual({});
	});
});

describe("boardSearchEdges", () => {
	// The router spreads raw URL values over the validated ones, so a pruned
	// default still reaches the board as whatever JSON.parse produced.
	it("normalizes the raw shapes useSearch can hand a component", () => {
		expect(boardSearchEdges("spawn,depends")).toEqual(["spawn", "depends"]);
		expect(boardSearchEdges(["depends", "spawn"])).toEqual(["spawn", "depends"]);
		expect(boardSearchEdges("0")).toBeUndefined();
		expect(boardSearchEdges(0)).toBeUndefined();
		expect(boardSearchEdges(true)).toBeUndefined();
		expect(boardSearchEdges(null)).toBeUndefined();
		expect(boardSearchEdges(undefined)).toBeUndefined();
		expect(boardSearchEdges("spawn,depends,delivers")).toBeUndefined();
	});
});

describe("boardSearchWithLayout", () => {
	it("sets and clears the layout while keeping the graph view", () => {
		expect(boardSearchWithLayout({ view: "graph" }, "dependency")).toEqual({
			view: "graph",
			layout: "dependency",
		});
		expect(boardSearchWithLayout({ view: "graph", layout: "dependency" }, "spawn")).toEqual({
			view: "graph",
		});
	});
});

describe("boardSearchWithEdges", () => {
	it("keeps a narrowed set and clears the default full set", () => {
		expect(boardSearchWithEdges({ view: "graph" }, ["spawn", "depends"])).toEqual({
			view: "graph",
			edges: ["spawn", "depends"],
		});
		expect(
			boardSearchWithEdges({ view: "graph", edges: ["spawn", "depends"] }, [
				"spawn",
				"depends",
				"delivers",
			]),
		).toEqual({ view: "graph" });
	});

	it("drops the dependency edges and falls back to the default for an empty selection", () => {
		expect(
			boardSearchWithEdges({ view: "graph", edges: ["spawn", "depends"] }, ["spawn"]),
		).toEqual({ view: "graph", edges: ["spawn"] });
		expect(boardSearchWithEdges({ view: "graph" }, [])).toEqual({ view: "graph" });
	});
});

describe("DAG run filter", () => {
	it("accepts a non-empty run id and drops everything else", () => {
		expect(validateBoardSearch({ run: "dag-1" })).toEqual({ run: "dag-1" });
		expect(validateBoardSearch({ run: " dag-1 " })).toEqual({ run: "dag-1" });
		expect(validateBoardSearch({ run: "" })).toEqual({});
		expect(validateBoardSearch({ run: 1 })).toEqual({});
		expect(validateBoardSearch({ run: ["dag-1"] })).toEqual({});
	});

	it("normalizes the raw values the router can hand the board", () => {
		expect(boardSearchRun("dag-1")).toBe("dag-1");
		expect(boardSearchRun(" ")).toBeUndefined();
		expect(boardSearchRun(0)).toBeUndefined();
		expect(boardSearchRun(null)).toBeUndefined();
		expect(boardSearchRun(undefined)).toBeUndefined();
	});

	it("sets the run and selects the graph so the filter is visible", () => {
		expect(boardSearchWithRun({}, "dag-1")).toEqual({ view: "graph", run: "dag-1" });
		expect(boardSearchWithRun({ view: "graph", archived: true, edges: ["spawn"] }, "dag-1")).toEqual({
			view: "graph",
			archived: true,
			run: "dag-1",
			edges: ["spawn"],
		});
	});

	it("clears the run without dropping the view", () => {
		expect(boardSearchWithRun({ view: "graph", run: "dag-1" }, undefined)).toEqual({
			view: "graph",
		});
		expect(boardSearchWithRun({ view: "graph", run: "dag-1" }, "  ")).toEqual({
			view: "graph",
		});
	});
});

describe("boardSearchWithView clearing graph filters", () => {
	it("clears layout, edges, and the run when returning to the board", () => {
		expect(
			boardSearchWithView(
				{ view: "graph", run: "dag-1", layout: "dependency", edges: ["spawn"] },
				"board",
			),
		).toEqual({});
	});
});
