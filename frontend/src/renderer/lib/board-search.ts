export type BoardSearch = { view?: "graph"; archived?: boolean };

export function validateBoardSearch(search: Record<string, unknown>): BoardSearch {
	const result: BoardSearch = {};
	if (search.view === "graph") result.view = "graph";
	const archived = search.archived;
	if (archived === true || archived === 1 || archived === "1") result.archived = true;
	return result;
}

export function boardSearchWithView(
	search: BoardSearch,
	view: "board" | "graph",
): BoardSearch {
	if (view === "board") return {};
	return {
		view: "graph",
		...(search.archived ? { archived: true } : {}),
	};
}

export function boardSearchWithArchived(
	search: BoardSearch,
	archived: boolean,
): BoardSearch {
	return {
		...(search.view === "graph" ? { view: "graph" as const } : {}),
		...(archived ? { archived: true } : {}),
	};
}
