import { OrchestratorStartingChat } from "./chat/OrchestratorStartingChat";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
	SessionsArchiveView,
	SessionsBoardGridView,
	archiveToggleOffsetClassName,
	chipTone,
	largestSession,
	type ChipTone,
} from "@aoagents/product-ui";
import { AlertTriangle, LayoutDashboard, RotateCw, Users } from "lucide-react";
import {
	CLOUD_PROJECT_KIND,
	STANDALONE_PROJECT_KIND,
	type WorkspaceSession,
	isOrchestratorSession,
	newestActiveOrchestrator,
	orchestratorHealth,
	sessionIsPreAdmission,
	workerSessions,
	hasConfiguredOrchestratorAgent,
} from "../types/workspace";
import {
	boardKanbanColumnOrder,
	getKanbanColumnView,
	type KanbanColumnView,
} from "../lib/session-presentation";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { useTerminateSession } from "../hooks/useTerminateSession";
import { useCloudCp } from "../hooks/useCloudCp";
import { useSessionParticipants } from "../hooks/useSessionParticipants";
import { useCloudProjectsQuery, useRemoteProjectQuery, useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { FREESTYLE_PROVIDER, projectSandboxProvider } from "../lib/cloud-offering";
import { NotificationCenter } from "./NotificationCenter";
import { BoardWelcome, ProjectBoardEmpty } from "./BoardEmptyStates";
import { TopbarButton, topbarProjectLabelClass } from "./TopbarButton";
import { restartProjectOrchestrator } from "../lib/restart-orchestrator";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { isArchivedSession } from "../lib/standalone-archive";
import { demoBoardSessions } from "../lib/demo-board-sessions";
import { isLinuxPlatform, isMacPlatform, usesBoardActionsInPanel } from "../lib/platform";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";
import { DaemonStartupLoader } from "./DaemonStartupLoader";
import { useBoardPresentation } from "../hooks/useBoardPresentation";
import { useProjectOrchestratorAction } from "../hooks/useProjectOrchestratorAction";
import { openRemoteOrchestrator } from "../lib/remote-orchestrator";
import { labelForHost } from "../lib/host-clients";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { LOCAL_HOST, refKey, sessionUiKey } from "../lib/hosts";
import { useShellMaybe } from "../lib/shell-context";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { ProjectTerminationFeedback } from "./ShellTopbar";
import { ProjectBoardActions } from "./ProjectBoardActions";
import { SessionsGraphView } from "./SessionsGraphView";
import { TeamHeader } from "./TeamHeader";
import { sessionIdsInTeam } from "../lib/team-membership";
import { SessionsQueueStrip } from "./SessionsQueueStrip";
import { SessionDependenciesPanel } from "./SessionDependenciesPanel";
import { SessionsViewToggle, shouldShowSessionsViewToggle } from "./SessionsViewToggle";
import { useSessionScheduling } from "../hooks/useSessionScheduling";
import type { BoardSearchEdge } from "../lib/board-search";
import { useDiagnosticsEnabled, usePressureState, useSessionMemory } from "../hooks/useSessionMemory";
import { AppMemoryIndicator, toSessionFacts, useHasAppMemory } from "./SessionMemoryPanel";
import { recordManualWorkerOpen } from "../lib/session-management-telemetry";
import {
	ArchivedSessionCardAdapter,
	BoardSessionCardAdapter,
	sessionsBoardLabels,
} from "./SessionsBoardAdapters";

type SessionsBoardProps = {
	/** When set, the board shows only this project's sessions. */
	projectId?: string;
	hostId?: string;
	view?: "board" | "graph";
	onViewChange?: (view: "board" | "graph") => void;
	showArchived?: boolean;
	onShowArchivedChange?: (show: boolean) => void;
	/** Graph layout from the URL; `spawn` is the default tree. */
	layout?: "spawn" | "dependency";
	onLayoutChange?: (layout: "spawn" | "dependency") => void;
	/** Which graph edges the URL asks for. */
	edges?: readonly BoardSearchEdge[];
	onEdgesChange?: (edges: readonly BoardSearchEdge[]) => void;
	/** A DAG run to filter the graph to. */
	run?: string;
	onRunChange?: (run: string | undefined) => void;
	/** A team root to filter the board and graph to. */
	team?: string;
	onTeamChange?: (team: string | undefined) => void;
};

const isMac = isMacPlatform();
const dragStyle = isMac ? ({ WebkitAppRegion: "drag" } as React.CSSProperties) : undefined;
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

export function SessionsBoard({
	projectId,
	hostId,
	view,
	onViewChange,
	showArchived,
	onShowArchivedChange,
	layout = "spawn",
	onLayoutChange,
	edges,
	onEdgesChange,
	run,
	onRunChange,
	team,
	onTeamChange,
}: SessionsBoardProps) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const shell = useShellMaybe();
	const connected = useConnectedHosts().includes(hostId ?? "");
	const scopeKey = refKey({ host: hostId ?? LOCAL_HOST, id: projectId ?? "all" });
	// Lanes follow the daemon's delivery order: building -> validating ->
	// in review -> ready. The middle two are one review-feedback loop, split by
	// whose turn it is.
	const columns: KanbanColumnView[] = boardKanbanColumnOrder.map((column) => getKanbanColumnView(column, t));
	const localWorkspaceQuery = useWorkspaceQuery();
	const remoteProjectQuery = useRemoteProjectQuery(hostId ?? "", projectId ?? "");
	// Evaluated at render so platform mocks in tests can flip the in-panel chrome.
	const boardActionsInPanel = usesBoardActionsInPanel();
	/** Bell lives in the board action row when the shell topbar does not host it. */
	const boardOwnsNotificationCenter = isLinuxPlatform() || boardActionsInPanel;
	const all = localWorkspaceQuery.data ?? [];
	const workspaces = hostId
		? remoteProjectQuery.data ? [remoteProjectQuery.data] : []
		: projectId ? all.filter((candidate) => candidate.id === projectId) : all;
	const workspace = projectId ? workspaces[0] : undefined;
	const canShowGraph = shouldShowSessionsViewToggle({
		projectId,
		hasCallbacks: onViewChange !== undefined,
		preview: usesPreviewWorkspaceData,
		cloudProject: workspace?.kind === CLOUD_PROJECT_KIND,
	});
	// A run filter only exists on the graph, so it implies that view.
	const activeView = canShowGraph && (view === "graph" || run !== undefined) ? "graph" : "board";
	// A Freestyle cloud project's orchestrator starts in place (see
	// OrchestratorStartingChat); its session does not exist yet, so the
	// project's provider decides, as the control plane's does.
	// Only this machine's cloud projects start in place: a remote host's board
	// never reads this machine's cloud settings (as useCloudCp below).
	const localCloudBoard = !hostId && workspace?.kind === CLOUD_PROJECT_KIND;
	const cloudProjects = useCloudProjectsQuery({ enabled: localCloudBoard });
	const offering = useCloudOffering(localCloudBoard);
	const cloudProject = cloudProjects.data?.find((candidate) => candidate.id === projectId);
	const startsInPlace = localCloudBoard && cloudProject !== undefined &&
		projectSandboxProvider(cloudProject.config, offering) === FREESTYLE_PROVIDER;
	// Board chrome stays route-oriented; project context remains in the sidebar.
	const boardLabel = t("shell.board");
	const liveSessions = workspaces.flatMap((workspace) => workerSessions(workspace.sessions));
	const demoWorkspaceId = projectId ?? workspaces[0]?.id;
	const unfilteredSessions = !hostId && usesPreviewWorkspaceData && demoWorkspaceId && liveSessions.length === 0
		? demoBoardSessions(demoWorkspaceId)
		: liveSessions;
	// A team filter narrows both views to one orchestrator subtree. Standalone
	// projects, desktop New Task work, and cloud projects have no derived team,
	// so the filter and its entry point only exist on a local/remote project
	// board. The root must be a real orchestrator in the project: a stale or
	// hand-edited `?team=` value is ignored rather than blanking the board.
	const allProjectSessions = workspaces.flatMap((candidate) => candidate.sessions);
	const teamSurfaceAllowed =
		projectId !== undefined &&
		workspace !== undefined &&
		workspace.kind !== CLOUD_PROJECT_KIND &&
		workspace.kind !== STANDALONE_PROJECT_KIND;
	const teamRootIds = new Set(
		allProjectSessions.filter((session) => session.kind === "orchestrator").map((session) => session.id),
	);
	const teamRoot = teamSurfaceAllowed && team && teamRootIds.has(team) ? team : undefined;
	const teamSessionIds = teamRoot
		? sessionIdsInTeam(allProjectSessions, allProjectSessions, teamRoot)
		: undefined;
	const inTeam = (session: WorkspaceSession) => !teamSessionIds || teamSessionIds.has(session.id);
	const sessions = teamSessionIds ? unfilteredSessions.filter(inTeam) : unfilteredSessions;
	const orchestrator = projectId ? newestActiveOrchestrator(workspaces[0]?.sessions ?? []) : undefined;
	const projectActions = useProjectOrchestratorAction({
		projectId,
		project: workspace,
		orchestrator,
		source: "board",
		hostId,
	});
	const { isProjectRestarting, isProvisioning } = projectActions;
	// A Freestyle cloud project just created starts its orchestrator on its own
	// (only those are flagged, see CreateProjectFlow), so it opens on the
	// orchestrator starting in place rather than on an empty board.
	const autoStartFlagged = useUiStore((state) => !hostId && projectId !== undefined && state.autoStartOrchestratorProjectIds.has(projectId));
	const autoStartPending = autoStartFlagged && !orchestrator;
	const { openOrchestrator } = projectActions;
	useEffect(() => {
		if (!autoStartFlagged || !projectId) return;
		if (orchestrator) {
			useUiStore.getState().setAutoStartOrchestrator(projectId, false);
			return;
		}
		if (workspace?.kind !== CLOUD_PROJECT_KIND || !startsInPlace) return;
		// Read the store, not this render: the flag is consumed exactly once.
		const ui = useUiStore.getState();
		if (!ui.autoStartOrchestratorProjectIds.has(projectId)) return;
		ui.setAutoStartOrchestrator(projectId, false);
		openOrchestrator();
	}, [autoStartFlagged, openOrchestrator, orchestrator, projectId, startsInPlace, workspace?.kind]);
	const setProjectRestarting = useUiStore((state) => state.setProjectRestarting);
	const setOrchestratorReplacementError = useUiStore((state) => state.setOrchestratorReplacementError);
	const setOrchestratorStartupError = useUiStore((state) => state.setOrchestratorStartupError);
	const health = workspace ? orchestratorHealth(workspace, isProjectRestarting) : { state: "ok" as const };
	const archived = sessions
		.filter(isArchivedSession)
		.sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
	const activeSessions = sessions.filter((candidate) => !isArchivedSession(candidate));
	// Who is in each shared cloud session, read without joining any of them.
	const participantsBySession = useSessionParticipants(activeSessions, !hostId);
	const { userId: cloudUserId } = useCloudCp(!hostId && activeSessions.some((session) => Boolean(session.cloud)));
	// Pre-delivery work belongs to the queue strip, not a lane: a queued or
	// blocked session has no workspace, terminal, or agent process yet, so a
	// kanban column would misreport it as work in progress.
	const boardSessions = activeSessions
		.filter((session) => !sessionIsPreAdmission(session))
		.map((session) =>
			session.status === "no_signal" || session.displayStatus === "No signal"
				? { ...session, kanbanColumn: "building" as const }
				: session,
		);
	const boardLabels = sessionsBoardLabels(t);
	const presentation = useBoardPresentation({
		projectId,
		isSuccess: localWorkspaceQuery.isSuccess,
		isError: localWorkspaceQuery.isError,
		hasProjects: workspaces.length > 0,
		hasWorkerSessions: liveSessions.length > 0,
	});
	const showStartup = !hostId && presentation.showStartup;
	const showWelcome = !hostId && presentation.showWelcome;
	const showProjectEmpty = hostId
		? connected && remoteProjectQuery.isSuccess && Boolean(workspace) && liveSessions.length === 0
		: presentation.showProjectEmpty;
	// Memory and CPU monitoring is a developer tool: the light, the card chips
	// and the window behind them only exist with Diagnostics on in Developer mode.
	// They read this machine, so another machine's board shows none of them.
	const diagnostics = useDiagnosticsEnabled();
	const hasMemory = useHasAppMemory(!hostId) && diagnostics && !hostId;
	// Per-session readings feed each card's resource chip. Chips are grey
	// unless the machine is tight and the card is part of the fix (idle, or
	// the single largest).
	const sessionMemory = useSessionMemory(projectId, !hostId).data;
	const memoryBySession = diagnostics ? sessionMemory : undefined;
	const pressure = usePressureState(!hostId);
	const chipToneOf = useMemo(() => {
		const now = Date.now();
		const facts = sessions
			.filter((session) => session.isTerminated !== true && !isOrchestratorSession(session))
			.map((session) => toSessionFacts(session, memoryBySession?.get(session.id), now));
		const largest = largestSession(facts);
		return (session: WorkspaceSession): ChipTone =>
			chipTone(pressure ?? "fine", facts.find((f) => f.id === session.id) ?? toSessionFacts(session, memoryBySession?.get(session.id), now), largest);
	}, [sessions, memoryBySession, pressure]);
	// The bar hosts the memory indicator too, so it stays up with an empty archive.
	const hasArchive = archived.length > 0 || hasMemory;
	const boardHasArchive = hasArchive && activeView === "board";
	const terminateSession = useTerminateSession();
	const scheduling = useSessionScheduling();
	// Pre-delivery work: queued and blocked sessions live in the queue strip
	// above the lanes, not in a kanban column of their own.
	const queueSessions = activeSessions.filter(sessionIsPreAdmission);
	// Dependency ids resolve against every session in scope, orchestrators and
	// archived ones included, so a panel row can still name what it waits on.
	const dependencyLookup = new Map<string, WorkspaceSession>();
	for (const candidate of workspaces) {
		for (const item of candidate.sessions) dependencyLookup.set(item.id, item);
	}
	const [dependenciesSessionId, setDependenciesSessionId] = useState<string | undefined>();
	const [dependenciesConfirm, setDependenciesConfirm] = useState(false);
	const dependenciesSession = dependenciesSessionId ? dependencyLookup.get(dependenciesSessionId) : undefined;
	const openDependencies = useCallback((session: WorkspaceSession, confirmAdmit = false) => {
		setDependenciesSessionId(session.id);
		setDependenciesConfirm(confirmAdmit);
	}, []);
	const closeDependencies = useCallback(() => {
		setDependenciesSessionId(undefined);
		setDependenciesConfirm(false);
	}, []);
	const dependencyEdgeShown = edges === undefined || edges.includes("depends");
	const onShowDependenciesChange = onEdgesChange
		? (show: boolean) => {
				const current = edges ?? (["spawn", "depends", "delivers"] as const);
				const next = show
					? [...new Set([...current, "depends" as const])]
					: current.filter((edge) => edge !== "depends");
				onEdgesChange(next);
			}
		: undefined;
	const activeScopeRef = useRef(scopeKey);
	activeScopeRef.current = scopeKey;

	const openSession = useCallback((session: WorkspaceSession) => {
		if (session.kind === "worker") recordManualWorkerOpen(session.id, hostId);
		void navigate(sessionNavigateTarget(session.workspaceId, session.id, hostId));
	}, [navigate, hostId]);
	const openSessionArtifact = useCallback((session: WorkspaceSession, path: string) => {
		useUiStore.getState().requestWorkspaceFileOpen(session.id, path, hostId, "artifact");
		openSession(session);
	}, [openSession, hostId]);
	const openSessionArtifacts = useCallback((session: WorkspaceSession) => {
		const uiKey = sessionUiKey(session.id, hostId);
		useUiStore.getState().initializeInspectorSession(uiKey, Boolean(session.previewUrl), true);
		useUiStore.getState().setInspectorView(uiKey, "artifacts");
		useUiStore.getState().setInspectorOpen(uiKey, true);
		openSession(session);
	}, [openSession, hostId]);

	const restartOrchestrator = async () => {
		if (!projectId || isProjectRestarting || isProvisioning) return;
		if (hostId) {
			if (!connected) return;
			setProjectRestarting(projectId, true, hostId);
			setOrchestratorStartupError(projectId, null, hostId);
			try {
				const sessionId = await openRemoteOrchestrator(hostId, projectId, orchestrator, undefined, true, "restart");
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				if (activeScopeRef.current === scopeKey) void navigate(sessionNavigateTarget(projectId, sessionId, hostId));
			} catch (error) {
				setOrchestratorStartupError(projectId, error instanceof Error ? error.message : t("shell.couldNotSpawn"), hostId);
			} finally {
				setProjectRestarting(projectId, false, hostId);
			}
			return;
		}
		await restartProjectOrchestrator({
			projectId,
			queryClient,
			navigate,
			setProjectRestarting,
			setOrchestratorReplacementError,
		});
	};

	const actions = projectId && (!hostId || connected) ? (
		<>
			<ProjectTerminationFeedback projectId={projectId} hostId={hostId} />
			<ProjectBoardActions actions={projectActions} placement="header" quiet={showProjectEmpty} cloud={workspace?.kind === CLOUD_PROJECT_KIND} />
			{boardOwnsNotificationCenter ? (
				<>
					<NotificationCenter />
				</>
			) : null}
		</>
	) : boardOwnsNotificationCenter ? (
		<NotificationCenter />
	) : undefined;

	if (projectId && (isProvisioning || projectActions.isSpawning || autoStartPending)) {
		return <OrchestratorStartingChat
			cloud={workspace?.kind === CLOUD_PROJECT_KIND || autoStartPending}
			freestyle={startsInPlace || autoStartPending}
			coderWorkspaceUrl={workspace?.sessions.find((session) => session.kind === "orchestrator")?.cloud?.coderWorkspaceUrl}
		/>;
	}

	return (
		<div className="relative flex h-full min-h-0 flex-col bg-background text-foreground" data-testid="board" data-host-id={hostId} data-project-id={projectId}>
			{/* macOS: shell topbar is hidden on board routes, so the project/"Board"
			    crumb + New task / Orchestrator / bell live in this in-panel row.
			    Win/Linux keep the crumb and actions in the framed ShellTopbar.
			    Welcome skips the row — a dangling "Board" above the import
			    chooser was review feedback on #2432. */}
			{!showWelcome && boardActionsInPanel && (boardLabel || actions) ? (
				<div
					className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center gap-2 border-b border-border-strong pr-1"
					style={dragStyle}
				>
					{boardLabel ? (
						<span
							className={cn(topbarProjectLabelClass, "inline-flex items-center gap-1.5")}
							data-testid="board-topbar-label"
						>
							<LayoutDashboard aria-hidden="true" className="size-icon-md" />
							{boardLabel}
							{hostId ? <span className="truncate text-muted-foreground">· {labelForHost(hostId) ?? hostId}</span> : null}
						</span>
					) : null}
					<div className="min-w-0 flex-1" />
					{actions ? (
						<div className="workspace-topbar-actions flex shrink-0 items-center" style={noDragStyle}>
							{actions}
						</div>
					) : null}
				</div>
			) : null}
			{hostId && !connected ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{t("remote.hostOffline")}</p> : null}
			{hostId && remoteProjectQuery.isError ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{t("shell.couldNotLoadProjects")}</p> : null}
			{hostId && remoteProjectQuery.isSuccess && !workspace ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{t("session.notFound")}</p> : null}
			{hostId && projectId && connected && workspace && !orchestrator && !hasConfiguredOrchestratorAgent(workspace) ? (
				<div className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground">
					<span className="min-w-0 flex-1">{t("remote.configureOrchestratorFirst", { label: labelForHost(hostId) ?? hostId, defaultValue: "Choose an orchestrator agent on {{label}} to start one." })}</span>
					<button type="button" className="shrink-0 rounded-md px-2 py-1 font-medium text-foreground hover:bg-interactive-hover focus-visible:outline-2 focus-visible:outline-ring" onClick={() => shell?.openRemoteProjectSettings(hostId, projectId)}>{t("restoreUnavailable.configureOrchestrator")}</button>
				</div>
			) : null}

			{/* Reserve only the collapsed archive bar. Expanded archive overlays the
			    board so lane height (and Needs You scrollbars) stay stable. */}
			<div
				className={cn("flex min-h-0 flex-1 flex-col overflow-hidden", boardHasArchive && archiveToggleOffsetClassName)}
				style={boardHasArchive ? { clipPath: "inset(0 0 var(--sessions-archive-height, 58px) 0)" } : undefined}
			>
				{projectId && health.state !== "ok" && (!hostId || connected && (health.state !== "missing" || hasConfiguredOrchestratorAgent(workspace))) ? (
					<div className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground">
						<AlertTriangle className="size-icon-base shrink-0 text-warning" aria-hidden="true" />
						<span className="min-w-0 flex-1">{health.message}</span>
						{health.state === "restart_needed" || health.state === "duplicates" ? (
							<TopbarButton disabled={isProjectRestarting} onClick={() => void restartOrchestrator()} variant="primary">
								<RotateCw className="size-3.5" aria-hidden="true" />
								{t("shell.restart")}
							</TopbarButton>
						) : null}
					</div>
				) : null}
			{workspace?.folderMissing ? (
				<div className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground">
					<AlertTriangle className="size-icon-base shrink-0 text-warning" aria-hidden="true" />
					<span className="min-w-0 flex-1">{t("home.folderMissing")}</span>
				</div>
			) : null}
			{projectId && isProvisioning ? (
				<div
					className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground"
					role="status"
				>
					<span
						className="size-icon-base shrink-0 animate-spin rounded-full border-2 border-current border-r-transparent"
						aria-hidden="true"
					/>
					<span className="min-w-0 flex-1">
						{t("shell.provisioning", { defaultValue: "Setting up the project — starting the orchestrator…" })}
					</span>
				</div>
			) : null}
			{canShowGraph ? (
				<div className="flex shrink-0 items-center gap-2 px-3 py-2">
					<SessionsViewToggle view={activeView} onChange={(next) => onViewChange?.(next)} />
					{teamSurfaceAllowed && orchestrator && onTeamChange && !teamRoot ? (
						<button
							type="button"
							data-testid="team-entry"
							className="inline-flex items-center gap-1.5 rounded-md border border-border px-1.5 py-0.5 text-xs font-medium text-foreground hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
							onClick={() => onTeamChange(orchestrator.id)}
						>
							<Users className="size-3.5 text-muted-foreground" aria-hidden="true" />
							{t("shell.teamView")}
						</button>
					) : null}
				</div>
			) : null}
			<SessionsQueueStrip
				sessions={queueSessions}
				onOpenDependencies={(session) => openDependencies(session)}
				onAdmitNow={(session) => openDependencies(session, true)}
			/>
			<div className="flex min-h-0 flex-1 flex-col">
				{teamRoot ? (
					<TeamHeader
						rootSessionId={teamRoot}
						hostId={hostId}
						onClear={onTeamChange ? () => onTeamChange(undefined) : undefined}
						onOpenSession={(sessionId) => {
							const target = allProjectSessions.find((candidate) => candidate.id === sessionId);
							if (target) openSession(target);
						}}
					/>
				) : null}
				{!hostId && (presentation.workspaceStartupState === "error" || localWorkspaceQuery.isError) ? (
					<p className="py-10 text-center text-xs text-passive">{t("shell.couldNotLoadSessions")}</p>
				) : hostId && !workspace ? null : showWelcome ? (
				<BoardWelcome />
				) : activeView === "graph" ? (
					<SessionsGraphView
						sessions={teamSessionIds ? allProjectSessions.filter(inTeam) : (workspace?.sessions ?? [])}
						showArchived={showArchived ?? false}
						onShowArchivedChange={onShowArchivedChange ?? (() => undefined)}
						onOpenSession={openSession}
						layout={layout}
						onLayoutChange={onLayoutChange}
						showDependencies={dependencyEdgeShown}
						onShowDependenciesChange={onShowDependenciesChange}
						dagRunId={run}
						onRunClear={onRunChange ? () => onRunChange(undefined) : undefined}
					/>
				) : showProjectEmpty ? (
					<ProjectBoardEmpty actions={<ProjectBoardActions actions={projectActions} placement="empty" />} />
				) : (
					<SessionsBoardGridView
						columns={columns}
						key={scopeKey}
						labels={boardLabels}
						renderSessionCard={(session) => (
							<BoardSessionCardAdapter
								participants={participantsBySession.get(session.id)}
								me={cloudUserId}
								memory={memoryBySession?.get(session.id)}
								memoryTone={chipToneOf(session)}
								onOpen={() => openSession(session)}
								onOpenArtifact={(path) => openSessionArtifact(session, path)}
								onOpenArtifacts={() => openSessionArtifacts(session)}
								onTerminate={!hostId || connected ? ({ deleteArtifacts }) => terminateSession.mutate({ ...session, deleteArtifacts }) : undefined}
								session={session}
							/>
						)}
						sessions={boardSessions}
					/>
				)}
			</div>
			</div>

			<SessionDependenciesPanel
				session={dependenciesSession}
				open={dependenciesSession !== undefined}
				onOpenChange={(next) => {
					if (!next) closeDependencies();
				}}
				resolveSession={(id) => dependencyLookup.get(id)}
				onOpenSession={openSession}
				onAdmit={(session, force) => {
					void scheduling.admit(session, force);
				}}
				onComplete={(session) => {
					void scheduling.complete(session);
				}}
				onCancel={(session) => {
					void scheduling.cancel(session);
				}}
				busy={scheduling.isPending && scheduling.pendingSessionId === dependenciesSession?.id}
				error={scheduling.error}
				startWithAdmitConfirm={dependenciesConfirm}
			/>

			{boardHasArchive ? (
				<BoardArchivePanel
					activeScopeRef={activeScopeRef}
					hostId={hostId}
					scopeKey={scopeKey}
					connected={!hostId || connected}
					sessions={archived}
				/>
			) : null}
			{showStartup ? <DaemonStartupLoader /> : null}
		</div>
	);
}

/**
 * Restore state lives here so expand/collapse in SessionsArchiveView does not
 * re-render the kanban columns. In-flight restores are invalidated on project
 * change or unmount so completion cannot navigate after the user left.
 */
const BoardArchivePanel = memo(function BoardArchivePanel({
	activeScopeRef,
	hostId,
	scopeKey,
	connected,
	sessions,
}: {
	activeScopeRef: React.MutableRefObject<string>;
	hostId?: string;
	scopeKey: string;
	connected: boolean;
	sessions: WorkspaceSession[];
}) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const diagnostics = useDiagnosticsEnabled();
	const restoreSessionById = useRestoreSession();
	const [restoringSessionId, setRestoringSessionId] = useState<string | undefined>();
	const [restoreErrors, setRestoreErrors] = useState<Record<string, string>>({});
	const [restoreUnavailableSession, setRestoreUnavailableSession] = useState<WorkspaceSession | undefined>();
	const restoreGenerationRef = useRef(0);

	useEffect(() => {
		setRestoringSessionId(undefined);
		setRestoreErrors({});
		setRestoreUnavailableSession(undefined);
		restoreGenerationRef.current += 1;
	}, [scopeKey]);

	useEffect(() => {
		const generation = restoreGenerationRef.current;
		return () => {
			// Invalidate in-flight restores if this panel unmounts (e.g. project with
			// no archive) so completion cannot navigate after the user left.
			if (restoreGenerationRef.current === generation) {
				restoreGenerationRef.current += 1;
			}
		};
	}, []);

	const restoreArchivedSession = async (event: MouseEvent<HTMLButtonElement>, session: WorkspaceSession) => {
		event.stopPropagation();
		if (restoringSessionId) return;
		const restoreScopeKey = scopeKey;
		const generation = restoreGenerationRef.current;
		const isStillActiveProject = () =>
			generation === restoreGenerationRef.current &&
			activeScopeRef.current === restoreScopeKey;
		setRestoringSessionId(session.id);
		setRestoreErrors((current) => {
			const next = { ...current };
			delete next[session.id];
			return next;
		});
		try {
			const result = await restoreSessionById(session.id, hostId);
			if (!isStillActiveProject()) return;
			if (result.status === "success") {
				void navigate(sessionNavigateTarget(session.workspaceId, session.id, hostId));
				return;
			}
			if (result.status === "not_resumable") {
				setRestoreUnavailableSession(session);
				return;
			}
			setRestoreErrors((current) => ({ ...current, [session.id]: result.message }));
		} finally {
			if (isStillActiveProject()) {
				setRestoringSessionId(undefined);
			}
		}
	};

	return (
		<>
			<SessionsArchiveView
				labels={{
					archive: t("shell.archive"),
					archiveAria: t("shell.archiveSessionsAria", { count: sessions.length }),
					archivedSessions: t("shell.archivedSessions"),
				}}
				trailing={diagnostics && !hostId ? <AppMemoryIndicator /> : undefined}
				renderSessionCard={(session) => (
					<ArchivedSessionCardAdapter
						isRestoreDisabled={!connected || restoringSessionId !== undefined}
						isRestoring={restoringSessionId === session.id}
						restoreAction={(event) => void restoreArchivedSession(event, session)}
						restoreError={restoreErrors[session.id]}
						session={session}
					/>
				)}
				resetKey={scopeKey}
				sessions={sessions}
			/>
			{restoreUnavailableSession ? (
				<RestoreUnavailableDialog
					open={true}
					session={restoreUnavailableSession}
					hostId={hostId}
					onOpenChange={(open) => {
						if (!open) setRestoreUnavailableSession(undefined);
					}}
					onRecreated={async () => {
						await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
					}}
				/>
			) : null}
		</>
	);
});
