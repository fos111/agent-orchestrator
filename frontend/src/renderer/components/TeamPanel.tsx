import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import type { HostId } from "../lib/hosts";
import { useSendTeamMessage, useTeamQuery } from "../hooks/useTeam";

/**
 * The roster and composer for one derived team. Broadcasts are best-effort:
 * the response reports per-member delivery counts, so a failure names the
 * count here and never rolls back the attempt. The composer is only reachable
 * from the team header, which is never shown for standalone or cloud work.
 */
export function TeamPanel({
	rootSessionId,
	hostId,
	onClose,
	onOpenSession,
}: {
	rootSessionId: string;
	hostId?: HostId;
	onClose: () => void;
	onOpenSession?: (sessionId: string) => void;
}) {
	const { t } = useTranslation();
	const teamQuery = useTeamQuery(rootSessionId, hostId);
	const send = useSendTeamMessage(rootSessionId, hostId);
	const composerId = useId();
	const [body, setBody] = useState("");
	const team = teamQuery.data;
	const members = team?.members ?? [];
	const empty = body.trim() === "";
	const error = send.error
		? send.error instanceof Error
			? send.error.message
			: t("shell.teamMessageFailed")
		: undefined;

	function submit(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		if (empty || send.isPending) return;
		send.mutate(body, { onSuccess: () => setBody("") });
	}

	return (
		<section
			data-testid="team-panel"
			aria-label={t("shell.teamPanelAria")}
			className="border-t border-border px-3 py-2 text-xs"
		>
			<div className="flex items-center justify-between gap-2">
				<h3 className="font-medium text-foreground">{t("shell.teamPanelTitle")}</h3>
				<button
					type="button"
					aria-label={t("shell.teamPanelClose")}
					className="rounded p-1 text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					onClick={onClose}
				>
					<X className="size-3.5" aria-hidden="true" />
				</button>
			</div>
			{members.length === 0 ? (
				<p className="mt-1 text-muted-foreground">{t("shell.teamMembersEmpty")}</p>
			) : (
				<ul className="mt-1 flex flex-wrap gap-1.5" aria-label={t("shell.teamMembersAria")}>
					{members.map((member) => {
						const name = member.displayName || member.sessionId;
						const isLead = member.sessionId === rootSessionId;
						const content = (
							<>
								<span className="truncate">{name}</span>
								{isLead ? (
									<span className="rounded border border-border px-1 text-[10px] text-muted-foreground">
										{t("shell.teamLead")}
									</span>
								) : null}
							</>
						);
						return (
							<li key={member.sessionId}>
								{onOpenSession ? (
									<button
										type="button"
										className="flex max-w-40 items-center gap-1 rounded border border-border px-1.5 py-0.5 text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
										onClick={() => onOpenSession(member.sessionId)}
									>
										{content}
									</button>
								) : (
									<span className="flex max-w-40 items-center gap-1 rounded border border-border px-1.5 py-0.5 text-muted-foreground">
										{content}
									</span>
								)}
							</li>
						);
					})}
				</ul>
			)}
			<form className="mt-2 flex flex-col gap-1.5" onSubmit={submit}>
				<label className="text-muted-foreground" htmlFor={composerId}>
					{t("shell.teamComposerLabel")}
				</label>
				<textarea
					id={composerId}
					className="min-h-16 resize-y rounded border border-border bg-background px-2 py-1 text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					placeholder={t("shell.teamComposerPlaceholder")}
					value={body}
					onChange={(event) => {
						setBody(event.target.value);
						// The previous outcome belongs to the previous message.
						if (send.data || send.error) send.reset();
					}}
				/>
				<div className="flex items-center gap-2">
					<button
						type="submit"
						className="rounded border border-border px-2 py-0.5 text-foreground hover:bg-interactive-hover disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						disabled={empty || send.isPending}
					>
						{send.isPending ? t("shell.teamComposerSending") : t("shell.teamComposerSend")}
					</button>
					{send.data ? (
						<span
							data-testid="team-message-result"
							role="status"
							className="text-muted-foreground"
						>
							{t("shell.teamMessageSent", {
								delivered: send.data.delivered,
								failed: send.data.failed,
							})}
						</span>
					) : null}
					{error ? (
						<span role="alert" className="text-destructive">
							{error}
						</span>
					) : null}
				</div>
			</form>
		</section>
	);
}
