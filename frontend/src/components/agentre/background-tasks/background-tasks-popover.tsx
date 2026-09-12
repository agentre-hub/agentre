import { Bot, Square, Terminal } from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";

import type { BackgroundTask } from "./types";

// formatElapsed 将毫秒数格式化为紧凑耗时字符串（computed — 不走 t()）。
// < 60s → "12s"  |  < 60m → "3m 05s"  |  else → "1h 02m"
function formatElapsed(ms: number): string {
  const totalSec = Math.floor(ms / 1000);
  if (totalSec < 60) return `${totalSec}s`;
  const totalMin = Math.floor(totalSec / 60);
  if (totalMin < 60) {
    const remSec = totalSec % 60;
    return `${totalMin}m ${remSec.toString().padStart(2, "0")}s`;
  }
  const hours = Math.floor(totalMin / 60);
  const remMin = totalMin % 60;
  return `${hours}h ${remMin.toString().padStart(2, "0")}m`;
}

type BackgroundTasksPopoverContentProps = {
  tasks: BackgroundTask[];
  onClearCompleted?: () => void;
  // onStopTask 停掉一个正在运行的后台任务/子 agent(仅当 backend 支持且该行有 taskId)。
  onStopTask?: (task: BackgroundTask) => void;
};

export function BackgroundTasksPopoverContent({
  tasks,
  onClearCompleted,
  onStopTask,
}: BackgroundTasksPopoverContentProps) {
  const { t } = useTranslation();

  const [now, setNow] = React.useState(() => Date.now());
  const hasLiveElapsed = tasks.some(
    (task) => task.status === "running" && task.startedAt != null,
  );
  React.useEffect(() => {
    if (!hasLiveElapsed) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [hasLiveElapsed]);

  const runningCount = tasks.filter((tk) => tk.status === "running").length;
  const hasCompleted = tasks.some((tk) => tk.status !== "running");

  return (
    <div className="flex min-w-[260px] max-w-[400px] flex-col gap-2">
      <div className="flex items-center gap-2">
        <span className="text-xs font-semibold text-foreground">
          {t("chatPanel.backgroundTasks.title")}
        </span>
        {runningCount > 0 && (
          <span className="inline-flex items-center gap-1 rounded-full bg-status-running-bg px-1.5 py-0.5 font-mono text-3xs font-medium text-status-running">
            <span
              className="inline-block size-1.5 rounded-full bg-status-running"
              aria-hidden="true"
            />
            {t("chatPanel.backgroundTasks.chip", { count: runningCount })}
          </span>
        )}
        <span className="min-w-0 flex-1" />
        {hasCompleted && onClearCompleted && (
          <button
            type="button"
            onClick={onClearCompleted}
            className="shrink-0 text-2xs text-muted-foreground transition-colors hover:text-foreground"
          >
            {t("chatPanel.backgroundTasks.clearCompleted")}
          </button>
        )}
      </div>
      {tasks.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {t("chatPanel.backgroundTasks.empty")}
        </p>
      ) : (
        <ul className="flex max-h-72 flex-col gap-1.5 overflow-y-auto overscroll-contain pr-0.5">
          {tasks.map((task) => {
            let elapsedLabel: string | undefined;
            if (task.status === "running" && task.startedAt != null) {
              elapsedLabel = formatElapsed(now - task.startedAt);
            } else if (
              (task.status === "completed" || task.status === "failed") &&
              task.durationMs != null &&
              task.durationMs > 0
            ) {
              elapsedLabel = formatElapsed(task.durationMs);
            }

            const isSubagent = task.kind === "local_agent";
            // 静态 t() key:两条分支各用字面量 key,便于 i18n 静态校验(避免 t(cond?…)) 。
            const kindLabel = isSubagent
              ? t("chatPanel.backgroundTasks.subagent")
              : t("chatPanel.backgroundTasks.bash");

            return (
              <li key={task.toolUseId} className="flex items-center gap-2.5">
                <span
                  className="inline-flex size-6 shrink-0 items-center justify-center rounded-md bg-primary-soft text-primary-text"
                  aria-hidden="true"
                >
                  {isSubagent ? (
                    <Bot className="size-3.5" />
                  ) : (
                    <Terminal className="size-3.5" />
                  )}
                </span>
                <div className="min-w-0 flex-1">
                  {/* description is dynamic agent output — do NOT pass through t()。
                      只展示单行标题(truncate),完整提示词内容不再铺开,避免过长撑高。 */}
                  <p className="truncate text-xs leading-snug text-foreground">
                    {task.description || " "}
                  </p>
                  <div className="mt-0.5 flex items-center gap-1.5">
                    <span className="font-mono text-3xs text-muted-foreground">
                      {kindLabel}
                    </span>
                    {task.taskId && (
                      <>
                        <span className="font-mono text-3xs text-muted-foreground/50">
                          ·
                        </span>
                        <span
                          className="font-mono text-3xs text-muted-foreground"
                          data-testid="task-id"
                        >
                          {task.taskId}
                        </span>
                      </>
                    )}
                    {elapsedLabel != null && (
                      <>
                        <span className="font-mono text-3xs text-muted-foreground/50">
                          ·
                        </span>
                        <span
                          className="font-mono text-3xs tabular-nums text-muted-foreground"
                          data-testid="elapsed"
                        >
                          {elapsedLabel}
                        </span>
                      </>
                    )}
                  </div>
                  {/* summary is dynamic agent text (exit-code text) — do NOT pass through t()。
                      子代理的 summary 是整篇回报正文,会把弹层撑到失控,只展示标题;
                      bash 的 summary 是一行 exit-code 文本,继续展示。 */}
                  {!isSubagent && task.summary && (
                    <p className="mt-0.5 break-words text-3xs text-muted-foreground">
                      {task.summary}
                    </p>
                  )}
                </div>
                {task.status === "running" && task.taskId && onStopTask && (
                  <button
                    type="button"
                    onClick={() => onStopTask(task)}
                    aria-label={t("chatPanel.backgroundTasks.stop")}
                    title={t("chatPanel.backgroundTasks.stop")}
                    className="inline-flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                  >
                    <Square className="size-3 fill-current" />
                  </button>
                )}
                <StatusPill status={task.status} />
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function StatusPill({ status }: { status: BackgroundTask["status"] }) {
  const { t } = useTranslation();

  if (status === "running") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-status-running-bg px-1.5 py-0.5 font-mono text-3xs text-status-running">
        <span
          className="inline-block size-1.5 rounded-full bg-status-running"
          aria-hidden="true"
        />
        {t("chatPanel.backgroundTasks.running")}
      </span>
    );
  }
  if (status === "failed") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-destructive/10 px-1.5 py-0.5 font-mono text-3xs text-destructive">
        <span
          className="inline-block size-1.5 rounded-full bg-destructive"
          aria-hidden="true"
        />
        {t("chatPanel.backgroundTasks.failed")}
      </span>
    );
  }
  if (status === "canceled") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-muted px-1.5 py-0.5 font-mono text-3xs text-muted-foreground">
        <span
          className="inline-block size-1.5 rounded-full bg-muted-foreground"
          aria-hidden="true"
        />
        {t("chatPanel.backgroundTasks.canceled")}
      </span>
    );
  }
  // completed
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-muted px-1.5 py-0.5 font-mono text-3xs text-muted-foreground">
      <span
        className="inline-block size-1.5 rounded-full bg-muted-foreground"
        aria-hidden="true"
      />
      {t("chatPanel.backgroundTasks.completed")}
    </span>
  );
}
