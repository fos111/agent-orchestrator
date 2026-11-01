/** Which graph edges are drawn. All three is the default, so it stays out of the URL. */
export type BoardSearchEdge = "spawn" | "depends" | "delivers";

/** `dependency` layers the graph by dependency depth; `spawn` is the default tree. */
export type BoardSearchLayout = "spawn" | "dependency";

export type BoardSearch = {
	view?: "graph";
	archived?: boolean;
	/** A DAG run to filter the graph to. Absent when the whole project is shown. */
	run?: string;
	/** A team root to filter the board and graph to. Absent when the whole project is shown. */
	team?: string;
	/** Only `dependency` is carried; `spawn` is the default and stays absent. */
	layout?: "dependency";
	/** Only a narrowed set is carried; the full set is the default. */
	edges?: BoardSearchEdge[];
};

const BOARD_SEARCH_EDGES: readonly BoardSearchEdge[] = ["spawn", "depends", "delivers"];

/** The edge set drawn when the URL says nothing. */
export const DEFAULT_BOARD_SEARCH_EDGES: readonly BoardSearchEdge[] = BOARD_SEARCH_EDGES;

function isBoardSearchEdge(value: string): value is BoardSearchEdge {
	return (BOARD_SEARCH_EDGES as readonly string[]).includes(value);
}

/**
 * `edges=spawn,depends` (or a repeated query key) narrowed to known kinds, in
 * catalog order. Unknown values fall back to the default set, and so does an
 * empty result — an all-off graph would just look broken.
 *
 * Also the normalization boundary for callers that read `useSearch()`: the
 * router spreads raw URL values over the validated ones, so a value the
 * validator prunes (the default set) still reaches the component in its raw
 * form — `?edges=0` parses to the number `0`, `?edges=spawn` to a string.
 */
export function boardSearchEdges(value: unknown): BoardSearchEdge[] | undefined {
	const raw = Array.isArray(value)
		? value
		: typeof value === "string"
			? value.split(",")
			: [];
	const selected = new Set<BoardSearchEdge>();
	for (const entry of raw) {
		if (typeof entry !== "string") continue;
		const normalized = entry.trim().toLowerCase();
		if (isBoardSearchEdge(normalized)) selected.add(normalized);
	}
	if (selected.size === 0) return undefined;
	const ordered = BOARD_SEARCH_EDGES.filter((edge) => selected.has(edge));
	return ordered.length === BOARD_SEARCH_EDGES.length ? undefined : ordered;
}

/**
 * The DAG run id from a raw URL value. The router spreads raw values over the
 * validated ones, so anything that is not a non-empty string is no filter.
 */
export function boardSearchRun(value: unknown): string | undefined {
	if (typeof value !== "string") return undefined;
	const trimmed = value.trim();
	return trimmed === "" ? undefined : trimmed;
}

export function validateBoardSearch(search: Record<string, unknown>): BoardSearch {
	const result: BoardSearch = {};
	if (search.view === "graph") result.view = "graph";
	const archived = search.archived;
	if (archived === true || archived === 1 || archived === "1") result.archived = true;
	const run = boardSearchRun(search.run);
	if (run) result.run = run;
	const team = boardSearchRun(search.team);
	if (team) result.team = team;
	if (search.layout === "dependency") result.layout = "dependency";
	const edges = boardSearchEdges(search.edges);
	if (edges) result.edges = edges;
	return result;
}

/** Rebuild the search object in a stable key order, dropping defaults. */
function withBoardSearch(search: BoardSearch, patch: Partial<BoardSearch>): BoardSearch {
	const next: BoardSearch = { ...search, ...patch };
	return {
		...(next.view === "graph" ? { view: "graph" as const } : {}),
		...(next.archived ? { archived: true } : {}),
		...(next.run ? { run: next.run } : {}),
		...(next.team ? { team: next.team } : {}),
		...(next.layout === "dependency" ? { layout: "dependency" as const } : {}),
		...(next.edges ? { edges: next.edges } : {}),
	};
}

export function boardSearchWithView(
	search: BoardSearch,
	view: "board" | "graph",
): BoardSearch {
	// The board view has no graph layout or edge filters to keep. A team filter
	// applies to both views, so it survives the switch.
	if (view === "board") return search.team ? { team: search.team } : {};
	return withBoardSearch(search, { view: "graph" });
}

export function boardSearchWithArchived(
	search: BoardSearch,
	archived: boolean,
): BoardSearch {
	return withBoardSearch(search, { archived: archived || undefined });
}

export function boardSearchWithRun(
	search: BoardSearch,
	run: string | undefined,
): BoardSearch {
	const normalized = boardSearchRun(run);
	// A run filter is only meaningful in the graph, so naming one also selects it.
	return withBoardSearch(search, {
		run: normalized,
		...(normalized ? { view: "graph" as const } : {}),
	});
}

export function boardSearchWithTeam(
	search: BoardSearch,
	team: string | undefined,
): BoardSearch {
	return withBoardSearch(search, { team: boardSearchRun(team) });
}

export function boardSearchWithLayout(
	search: BoardSearch,
	layout: BoardSearchLayout,
): BoardSearch {
	return withBoardSearch(search, { layout: layout === "dependency" ? "dependency" : undefined });
}

export function boardSearchWithEdges(
	search: BoardSearch,
	edges: readonly BoardSearchEdge[],
): BoardSearch {
	return withBoardSearch(search, { edges: boardSearchEdges([...edges]) });
}
