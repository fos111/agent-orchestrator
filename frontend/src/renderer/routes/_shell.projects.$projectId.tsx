import { createFileRoute } from "@tanstack/react-router";
import { SessionsBoard } from "../components/SessionsBoard";
import {
	boardSearchEdges,
	boardSearchWithArchived,
	boardSearchWithEdges,
	boardSearchWithLayout,
	boardSearchWithRun,
	boardSearchWithView,
	validateBoardSearch,
} from "../lib/board-search";

export const Route = createFileRoute("/_shell/projects/$projectId")({
	validateSearch: validateBoardSearch,
	component: ProjectBoardRoute,
});

function ProjectBoardRoute() {
	const { projectId } = Route.useParams();
	const { view, archived, layout, edges, run } = Route.useSearch();
	const navigate = Route.useNavigate();
	const effectiveLayout = layout ?? (run ? "dependency" : "spawn");
	return (
		<SessionsBoard
			projectId={projectId}
			view={view}
			onViewChange={(next) =>
				navigate({ search: (prev) => boardSearchWithView(prev, next), replace: true })
			}
			showArchived={archived === true}
			onShowArchivedChange={(show) =>
				navigate({ search: (prev) => boardSearchWithArchived(prev, show), replace: true })
			}
			layout={effectiveLayout}
			onLayoutChange={(next) =>
				navigate({ search: (prev) => boardSearchWithLayout(prev, next), replace: true })
			}
			edges={boardSearchEdges(edges)}
			onEdgesChange={(next) =>
				navigate({ search: (prev) => boardSearchWithEdges(prev, next), replace: true })
			}
			run={run}
			onRunChange={(next) =>
				navigate({ search: (prev) => boardSearchWithRun(prev, next), replace: true })
			}
		/>
	);
}
