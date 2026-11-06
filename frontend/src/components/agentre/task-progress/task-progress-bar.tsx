import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  Check,
  ChevronDown,
  ChevronRight,
  Circle,
  LoaderCircle,
} from "lucide-react";

import { cn } from "@agentre-hub/agentre-ui";

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

// 同一个状态在一行里出现两次：左边的图标是图形，留在饱和的填充色上；右边的
// 状态词是要读的字，用 -text 角色（docs/design.md §3.5）。分表而不是共用一张。
const STATUS_ICON_TONE: Record<Task["status"], string> = {
  queued: "text-muted-foreground",
  running: "text-status-running",
  completed: "text-status-running",
  cancelled: "text-muted-foreground",
  failed: "text-status-error",
};
const STATUS_TEXT_TONE: Record<Task["status"], string> = {
  queued: "text-muted-foreground",
  running: "text-status-running-text",
  completed: "text-status-running-text",
  cancelled: "text-muted-foreground",
  failed: "text-destructive-text",
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
        "flex items-center gap-2 px-3 py-1.5 font-mono text-2xs",
        isRunning && "bg-primary-soft",
      )}
    >
      <Icon
        className={cn(
          "size-3 shrink-0",
          STATUS_ICON_TONE[task.status],
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
          STATUS_TEXT_TONE[task.status],
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
  const recap = allCompleted ? t("taskProgress.allCompleted") : null;

  return (
    <div
      role="region"
      aria-label={t("taskProgress.title")}
      className="flex flex-col"
    >
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className="flex items-center gap-2 bg-primary-soft px-3 py-2 text-left hover:bg-primary-soft/80"
      >
        {!expanded && running ? (
          <LoaderCircle
            className="size-3 shrink-0 animate-spin text-status-running motion-reduce:animate-none"
            aria-hidden
          />
        ) : null}
        <span className="inline-flex shrink-0 items-baseline font-mono text-2xs tabular-nums">
          <span className="font-bold text-status-running-text">{done}</span>
          <span className="text-muted-foreground">/</span>
          <span className="font-semibold text-foreground">{total}</span>
        </span>
        <span
          className={cn(
            "min-w-0 flex-1 truncate",
            !expanded && !running && "text-3xs text-muted-foreground",
          )}
        >
          {expanded ? null : (running?.description ?? recap)}
        </span>
        <span
          className="h-1 w-16 shrink-0 overflow-hidden rounded-sm bg-border"
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={total}
          aria-valuenow={done}
        >
          <span
            className="block h-1 rounded-sm bg-status-running transition-[width]"
            style={{ width: `${pct}%` }}
          />
        </span>
        <span className="inline-flex shrink-0 items-center text-muted-foreground">
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
      ) : null}
    </div>
  );
}
