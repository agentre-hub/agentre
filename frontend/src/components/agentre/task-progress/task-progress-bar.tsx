import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  Check,
  ChevronDown,
  ChevronRight,
  Circle,
  ListChecks,
  LoaderCircle,
} from "lucide-react";

import { cn } from "@/lib/utils";

import type { Task, TaskProgress } from "./types";

type Props = {
  progress: TaskProgress;
};

function countCompleted(tasks: Task[]): number {
  return tasks.filter((t) => t.status === "completed").length;
}

function findRunning(tasks: Task[]): Task | undefined {
  return tasks.find((t) => t.status === "running");
}

const STATUS_FILL: Record<Task["status"], string> = {
  queued: "text-muted-foreground",
  running: "text-status-waiting",
  completed: "text-status-running",
  cancelled: "text-muted-foreground",
  failed: "text-status-error",
};

function TaskRow({ index, task }: { index: number; task: Task }) {
  const { t } = useTranslation();
  const isRunning = task.status === "running";
  const Icon =
    task.status === "completed"
      ? Check
      : task.status === "running"
        ? LoaderCircle
        : Circle;
  return (
    <li
      className={cn(
        "flex items-center gap-2 border-b border-border px-3 py-1.5 last:border-b-0 font-mono text-2xs",
        isRunning && "bg-primary-soft",
      )}
    >
      <Icon
        className={cn(
          "size-3 shrink-0",
          STATUS_FILL[task.status],
          isRunning && "animate-spin motion-reduce:animate-none",
        )}
        aria-hidden
      />
      <span className="w-5 shrink-0 text-right font-semibold text-muted-foreground">
        {index + 1}.
      </span>
      <span
        className={cn(
          "min-w-0 flex-1 truncate font-sans",
          task.status === "completed" && "text-muted-foreground",
          isRunning && "font-semibold text-primary-text",
        )}
      >
        {task.description}
      </span>
      <span
        className={cn(
          "shrink-0 text-[9px] font-bold tracking-wider",
          STATUS_FILL[task.status],
        )}
      >
        {t(`taskProgress.status.${task.status}`)}
      </span>
    </li>
  );
}

export function TaskProgressBar({ progress }: Props) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = React.useState(false);
  const { tasks } = progress;
  const total = tasks.length;
  const done = countCompleted(tasks);
  const allCompleted = total > 0 && done === total;

  React.useEffect(() => {
    if (!allCompleted) return;
    if (!expanded) return;
    const timer = setTimeout(() => setExpanded(false), 2000);
    return () => clearTimeout(timer);
  }, [allCompleted, expanded]);

  if (tasks.length === 0) return null;

  const pct = total === 0 ? 0 : Math.round((done / total) * 100);
  const running = findRunning(tasks);

  return (
    <div
      role="region"
      aria-label={t("taskProgress.title")}
      className="flex flex-col border-b border-border bg-card"
    >
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className="flex items-center gap-2 bg-primary-soft px-3 py-2 text-left hover:bg-primary-soft/80"
      >
        <ListChecks
          className="size-3.5 shrink-0 text-primary-text"
          aria-hidden
        />
        <span className="shrink-0 text-2xs font-bold text-primary-text">
          {t("taskProgress.title")}
        </span>
        <span className="inline-flex items-baseline gap-0.5 tabular-nums">
          <span className="text-aux font-bold text-status-running">{done}</span>
          <span className="text-xs text-muted-foreground">/</span>
          <span className="text-aux font-semibold text-foreground">
            {total}
          </span>
        </span>
        <span className="text-3xs text-muted-foreground">
          {allCompleted
            ? t("taskProgress.allCompleted")
            : t("taskProgress.completed")}
        </span>
        <span
          className="ml-2 h-1.5 w-28 overflow-hidden rounded-sm bg-border"
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={total}
          aria-valuenow={done}
        >
          <span
            className="block h-1.5 rounded-sm bg-status-running transition-[width]"
            style={{ width: `${pct}%` }}
          />
        </span>
        <span className="text-2xs font-semibold text-status-running">
          {pct}%
        </span>
        <span className="ml-auto inline-flex shrink-0 items-center text-muted-foreground">
          {expanded ? (
            <ChevronDown className="size-3.5" aria-hidden />
          ) : (
            <ChevronRight className="size-3.5" aria-hidden />
          )}
        </span>
      </button>
      {expanded ? (
        <ul className="flex max-h-64 flex-col overflow-y-auto overscroll-contain">
          {tasks.map((t, i) => (
            <TaskRow key={t.id} index={i} task={t} />
          ))}
        </ul>
      ) : running ? (
        <div className="flex items-center gap-2 px-3 py-1.5 font-mono text-2xs">
          <LoaderCircle
            className="size-3 shrink-0 animate-spin text-status-waiting motion-reduce:animate-none"
            aria-hidden
          />
          <span className="font-sans text-3xs font-bold text-muted-foreground">
            {t("taskProgress.current")}
          </span>
          <span className="text-muted-foreground">·</span>
          <span className="min-w-0 truncate text-foreground">
            {running.description}
          </span>
        </div>
      ) : null}
    </div>
  );
}
