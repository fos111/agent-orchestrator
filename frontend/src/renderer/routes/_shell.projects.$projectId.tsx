import { createFileRoute } from "@tanstack/react-router";
import { SessionsBoard } from "../components/SessionsBoard";
import {
	boardSearchWithArchived,
	boardSearchWithView,
	validateBoardSearch,
} from "../lib/board-search";

export const Route = createFileRoute("/_shell/projects/$projectId")({
	validateSearch: validateBoardSearch,
	component: ProjectBoardRoute,
});

function ProjectBoardRoute() {
	const { projectId } = Route.useParams();
	const { view, archived } = Route.useSearch();
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
		/>
	);
}
