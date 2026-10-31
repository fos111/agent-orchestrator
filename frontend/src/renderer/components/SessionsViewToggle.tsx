import { useTranslation } from "react-i18next";
import { TopbarButton } from "./TopbarButton";
import { cn } from "../lib/utils";

export function shouldShowSessionsViewToggle(input: {
	projectId?: string;
	hasCallbacks: boolean;
	preview: boolean;
	cloudProject: boolean;
}): boolean {
	return Boolean(input.projectId) && input.hasCallbacks && !input.preview && !input.cloudProject;
}

export function SessionsViewToggle({
	view,
	onChange,
}: {
	view: "board" | "graph";
	onChange: (view: "board" | "graph") => void;
}) {
	const { t } = useTranslation();
	const options = [
		{ value: "board", label: t("shell.board") },
		{ value: "graph", label: t("shell.graph") },
	] as const;
	return (
		<div
			role="group"
			aria-label={t("shell.sessionsView")}
			className="inline-flex shrink-0 items-center gap-0 rounded-md transition-none hover:bg-interactive-hover"
		>
			{options.map(({ value, label }) => (
				<TopbarButton
					key={value}
					variant="secondary"
					aria-pressed={view === value}
					className={cn(
						"transition-none! duration-0",
						view === value
							? "bg-interactive-active text-foreground hover:bg-interactive-active"
							: "hover:bg-transparent",
					)}
					onClick={() => onChange(value)}
				>
					{label}
				</TopbarButton>
			))}
		</div>
	);
}
