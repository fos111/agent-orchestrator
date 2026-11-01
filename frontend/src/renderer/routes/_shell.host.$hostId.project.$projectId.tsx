import { createFileRoute } from "@tanstack/react-router";
import { SessionsBoard } from "../components/SessionsBoard";
import {
	boardSearchEdges,
	boardSearchWithArchived,
	boardSearchWithEdges,
	boardSearchWithLayout,
	boardSearchWithTeam,
	boardSearchWithRun,
	boardSearchWithView,
	validateBoardSearch,
} from "../lib/board-search";
import { refKey } from "../lib/hosts";

export const Route = createFileRoute("/_shell/host/$hostId/project/$projectId")({
	validateSearch: validateBoardSearch,
	component: HostProjectBoardRoute,
});

function HostProjectBoardRoute() {
	const { hostId, projectId } = Route.useParams();
	const { view, archived, layout, edges, run, team } = Route.useSearch();
	const navigate = Route.useNavigate();
	// A run filter reads best as the layered DAG; the user can still switch.
	const effectiveLayout = layout ?? (run ? "dependency" : "spawn");
	return (
		<SessionsBoard
			key={refKey({ host: hostId, id: projectId })}
			hostId={hostId}
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
			team={team}
			onTeamChange={(next) =>
				navigate({ search: (prev) => boardSearchWithTeam(prev, next), replace: true })
			}
		/>
	);
}
