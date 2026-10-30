import { describe, expect, it } from "vitest";
import {
	boardSearchWithArchived,
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
