import { createFileRoute } from "@tanstack/react-router";
import { SessionsBoard } from "../components/SessionsBoard";
import {
	boardSearchEdges,
	boardSearchWithArchived,
	boardSearchWithEdges,
	boardSearchWithLayout,
	boardSearchWithView,
	validateBoardSearch,
} from "../lib/board-search";

export const Route = createFileRoute("/_shell/projects/$projectId")({
	validateSearch: validateBoardSearch,
	component: ProjectBoardRoute,
});

function ProjectBoardRoute() {
	const { projectId } = Route.useParams();
	const { view, archived, layout, edges } = Route.useSearch();
	const navigate = Route.useNavigate();
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
			layout={layout ?? "spawn"}
			onLayoutChange={(next) =>
				navigate({ search: (prev) => boardSearchWithLayout(prev, next), replace: true })
			}
			edges={boardSearchEdges(edges)}
			onEdgesChange={(next) =>
				navigate({ search: (prev) => boardSearchWithEdges(prev, next), replace: true })
			}
		/>
	);
}
