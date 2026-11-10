import {
  Ban,
  Check,
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  LoaderCircle,
  Square,
  X,
} from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";

import { cn, shortenModelName } from "@agentre-hub/agentre-ui";

import type { BackgroundTask, SessionProcess } from "./types";

// formatElapsed 将毫秒数格式化为紧凑耗时字符串（computed — 不走 t()）。
// < 60s → "12s"  |  < 60m → "3m 05s"  |  else → "1h 02m"
// 标题栏按钮的悬停预览卡与弹层共用这一个格式。
export function formatElapsed(ms: number): string {
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

// 停止 / 结束这类回调:返回的 Promise 失败(宿主已报过错)时按钮复原;成功则保持
// 进行态,直到行随状态变化翻态或消失。
export type PendingAction<T> = (target: T) => void | Promise<void>;

// 第一次点「结束进程」变成红色「结束？」,这段时间内再点才执行,否则复原。
export const KILL_CONFIRM_WINDOW_MS = 2000;

type BackgroundTasksPopoverContentProps = {
  tasks: BackgroundTask[];
  onClearCompleted?: () => void;
  // onStopTask 停掉一个正在运行的后台任务/子 agent(仅当 backend 支持且该行有 taskId)。
  onStopTask?: PendingAction<BackgroundTask>;
  // onLocateTask 把对话滚到发起这个任务的那张卡片(仅当该任务的消息已落库)。
  onLocateTask?: (task: BackgroundTask) => void;
  // 会话留下的存活进程;按钮回调只在宿主提供时才渲染对应按钮。
  processes?: SessionProcess[];
  // port 是被点的那个端口:多端口时每个端口各有一颗按钮。
  onOpenProcess?: (process: SessionProcess, port: number) => void;
  onKillProcess?: PendingAction<SessionProcess>;
};

// usePendingAction 跑一次宿主回调并给出进行态:回调 reject(或同步抛错)才复原。
function usePendingAction(): [
  boolean,
  (call: () => void | Promise<void>) => void,
] {
  const [pending, setPending] = React.useState(false);
  const run = (call: () => void | Promise<void>) => {
    setPending(true);
    const settleFailed = () => setPending(false);
    try {
      Promise.resolve(call()).catch(settleFailed);
    } catch {
      settleFailed();
    }
  };
  return [pending, run];
}

// 弹层只回答「现在在跑什么」:运行中的按类型分节列出来,已结束的收成底部一行,
// 点开在同一个弹层里换页。没有运行中的任务时,已结束列表就是全部内容。
export function BackgroundTasksPopoverContent({
  tasks,
  onClearCompleted,
  onStopTask,
  onLocateTask,
  processes,
  onOpenProcess,
  onKillProcess,
}: BackgroundTasksPopoverContentProps) {
  const { t } = useTranslation();
  const liveProcesses = processes ?? [];

  const [now, setNow] = React.useState(() => Date.now());
  const hasLiveElapsed = tasks.some(
    (task) => task.status === "running" && task.startedAt != null,
  );
  const needsTick = hasLiveElapsed || liveProcesses.length > 0;
  React.useEffect(() => {
    if (!needsTick) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [needsTick]);

  const [endedOpen, setEndedOpen] = React.useState(false);

  const running = tasks.filter((tk) => tk.status === "running");
  // tasks 按发起顺序给进来;已结束页把最近的放最上面。
  const ended = tasks.filter((tk) => tk.status !== "running").reverse();
  const agents = running.filter((tk) => tk.kind === "local_agent");
  const commands = running.filter((tk) => tk.kind === "local_bash");

  const renderRow = (task: BackgroundTask) => (
    <TaskRow
      key={task.toolUseId}
      task={task}
      now={now}
      onStopTask={onStopTask}
      onLocateTask={onLocateTask}
    />
  );

  const endedOnly = running.length === 0 && liveProcesses.length === 0;
  // 清理之后已结束的没了,就回到运行中那一页,不停在一张空页上。
  if (endedOnly || (endedOpen && ended.length > 0)) {
    return (
      <div className="flex flex-col">
        <div className="flex items-center gap-1 px-2 py-1.5">
          {!endedOnly && (
            <button
              type="button"
              onClick={() => setEndedOpen(false)}
              aria-label={t("chatPanel.backgroundTasks.back")}
              className="inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground outline-none focus-visible:focus-outline"
            >
              <ChevronLeft className="size-3.5" aria-hidden="true" />
            </button>
          )}
          <span
            className={cn(
              "text-xs font-semibold text-foreground",
              endedOnly && "pl-1.5",
            )}
          >
            {t("chatPanel.backgroundTasks.ended")}
          </span>
          <span className="text-xs tabular-nums text-muted-foreground">
            {ended.length}
          </span>
          <span className="min-w-0 flex-1" />
          {onClearCompleted && (
            <button
              type="button"
              onClick={onClearCompleted}
              className="shrink-0 cursor-pointer rounded px-1.5 py-0.5 text-2xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground outline-none focus-visible:focus-outline"
            >
              {t("chatPanel.backgroundTasks.clear")}
            </button>
          )}
        </div>
        <ul className="flex max-h-80 flex-col overflow-y-auto overscroll-contain p-1.5">
          {ended.map(renderRow)}
        </ul>
      </div>
    );
  }

  return (
    <div className="flex flex-col">
      <div className="flex max-h-80 flex-col gap-1.5 overflow-y-auto overscroll-contain p-1.5">
        {agents.length > 0 && (
          <TaskSection
            label={t("chatPanel.backgroundTasks.subagents")}
            count={agents.length}
          >
            {agents.map(renderRow)}
          </TaskSection>
        )}
        {commands.length > 0 && (
          <TaskSection
            label={t("chatPanel.backgroundTasks.commands")}
            count={commands.length}
          >
            {commands.map(renderRow)}
          </TaskSection>
        )}
        {liveProcesses.length > 0 && (
          <TaskSection
            label={t("chatPanel.backgroundTasks.processes")}
            count={liveProcesses.length}
          >
            {liveProcesses.map((process) => (
              <ProcessRow
                key={process.pid}
                process={process}
                now={now}
                onOpenProcess={onOpenProcess}
                onKillProcess={onKillProcess}
              />
            ))}
          </TaskSection>
        )}
      </div>
      {/* 钉在滚动区外面:运行中的再多,已结束这一行也够得着。 */}
      {ended.length > 0 && (
        <div className="p-1.5">
          <button
            type="button"
            onClick={() => setEndedOpen(true)}
            className="flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs text-muted-foreground outline-none transition-colors hover:bg-accent hover:text-foreground focus-visible:focus-outline"
          >
            <span className="flex-1">
              {t("chatPanel.backgroundTasks.ended")}
            </span>
            <span className="tabular-nums">{ended.length}</span>
            <ChevronRight className="size-3.5" aria-hidden="true" />
          </button>
        </div>
      )}
    </div>
  );
}

function TaskSection({
  label,
  count,
  children,
}: {
  label: string;
  count: number;
  children: React.ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <section aria-label={label}>
      <div className="flex items-center justify-between px-2 pb-1 pt-1.5">
        <span className="text-2xs font-semibold text-muted-foreground">
          {label}
        </span>
        <span className="text-2xs tabular-nums text-muted-foreground">
          {t("chatPanel.backgroundTasks.chip", { count })}
        </span>
      </div>
      <ul className="flex flex-col">{children}</ul>
    </section>
  );
}

function TaskRow({
  task,
  now,
  onStopTask,
  onLocateTask,
}: {
  task: BackgroundTask;
  now: number;
  onStopTask?: PendingAction<BackgroundTask>;
  onLocateTask?: (task: BackgroundTask) => void;
}) {
  const { t } = useTranslation();
  const isSubagent = task.kind === "local_agent";
  const [stopping, runStop] = usePendingAction();

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

  // meta 行:只放拿得出来的段,分隔符夹在真的存在的两段之间。
  // subagentType / taskId / model 是动态值,不走 t()。
  const meta: React.ReactNode[] = [];
  if (isSubagent) {
    meta.push(task.subagentType || t("chatPanel.backgroundTasks.subagent"));
  } else {
    meta.push(t("chatPanel.backgroundTasks.bash"));
    // 后台命令的 task id 与转录里那一步标的 id 对得上;子代理在转录里不标 id。
    if (task.taskId) {
      meta.push(<span data-testid="task-id">{task.taskId}</span>);
    }
  }
  if (elapsedLabel != null) {
    meta.push(
      <span className="tabular-nums" data-testid="elapsed">
        {elapsedLabel}
      </span>,
    );
  }
  if (isSubagent && task.toolUses != null && task.toolUses > 0) {
    meta.push(
      <span className="tabular-nums">
        {t("chatPanel.backgroundTasks.steps", { count: task.toolUses })}
      </span>,
    );
  }
  if (isSubagent && task.model) meta.push(shortenModelName(task.model));

  const body = (
    <>
      <StatusGlyph status={task.status} />
      <span className="min-w-0 flex-1">
        {/* description is dynamic agent output — do NOT pass through t()。
            只展示单行标题(truncate),完整提示词内容不再铺开,避免过长撑高。 */}
        <span className="block truncate text-xs leading-snug text-foreground">
          {task.description || " "}
        </span>
        <span className="mt-0.5 flex min-w-0 items-center gap-1.5 font-mono text-3xs text-muted-foreground">
          <StatusLabel status={task.status} />
          {meta.map((part, i) => (
            <React.Fragment key={i}>
              {i > 0 && (
                <span className="text-muted-foreground/50" aria-hidden="true">
                  ·
                </span>
              )}
              <span
                className={cn(
                  "whitespace-nowrap",
                  i === meta.length - 1 && "min-w-0 truncate",
                )}
              >
                {part}
              </span>
            </React.Fragment>
          ))}
        </span>
        {/* summary is dynamic agent text (exit-code text) — do NOT pass through t()。
            子代理的 summary 是整篇回报正文,会把弹层撑到失控,只展示标题;
            bash 的 summary 是一行 exit-code 文本,继续展示。 */}
        {!isSubagent && task.summary && (
          <span className="mt-0.5 block truncate text-3xs text-muted-foreground">
            {task.summary}
          </span>
        )}
      </span>
    </>
  );

  const bodyClassName = "flex min-w-0 flex-1 items-start gap-2.5 px-2 py-1.5";
  const canLocate = task.messageId != null && onLocateTask != null;

  return (
    <li
      className={cn(
        "flex items-center rounded-md",
        canLocate && "transition-colors hover:bg-accent",
      )}
    >
      {canLocate ? (
        <button
          type="button"
          onClick={() => onLocateTask(task)}
          title={t("chatPanel.backgroundTasks.locate")}
          className={cn(
            bodyClassName,
            "cursor-pointer rounded-md text-left outline-none focus-visible:focus-outline",
          )}
        >
          {body}
        </button>
      ) : (
        <div className={bodyClassName}>{body}</div>
      )}
      {task.status === "running" && task.taskId && onStopTask && (
        <button
          type="button"
          onClick={() => runStop(() => onStopTask(task))}
          disabled={stopping}
          aria-busy={stopping || undefined}
          aria-label={t("chatPanel.backgroundTasks.stop")}
          title={t("chatPanel.backgroundTasks.stop")}
          className={cn(rowIconButtonClassName, "mr-1.5")}
        >
          {stopping ? (
            <LoaderCircle className={spinnerClassName} aria-hidden="true" />
          ) : (
            <Square className="size-3 fill-current" aria-hidden="true" />
          )}
        </button>
      )}
    </li>
  );
}

// 行尾停止 / 结束键:24px 命中区;进行中禁用、不再有悬停反馈。
const rowIconButtonClassName =
  "inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive-text outline-none focus-visible:focus-outline disabled:pointer-events-none disabled:opacity-60";
const spinnerClassName = "size-3 animate-spin motion-reduce:animate-none";

// 进程行:没有定位动作,行本身不可点、没有悬停底;只有端口的「打开」和行尾的「结束」。
// 命令可换行、可选中;cwd 从头部截断(省略号落在开头,留住末尾的目录名)。
// command / 端口 / PID / cwd 都是动态值,不走 t()。
function ProcessRow({
  process,
  now,
  onOpenProcess,
  onKillProcess,
}: {
  process: SessionProcess;
  now: number;
  onOpenProcess?: (process: SessionProcess, port: number) => void;
  onKillProcess?: PendingAction<SessionProcess>;
}) {
  const { t } = useTranslation();
  const hasPorts = process.ports.length > 0;
  // 多端口时每个端口各自是一颗按钮;单端口沿用行尾的打开键。
  const portButtons = process.ports.length > 1 && onOpenProcess != null;
  const meta: React.ReactNode[] = [
    portButtons ? (
      <span className="inline-flex items-center gap-1">
        {process.ports.map((port) => (
          <button
            key={port}
            type="button"
            onClick={() => onOpenProcess(process, port)}
            aria-label={t("chatPanel.backgroundTasks.openPortNumber", { port })}
            title={t("chatPanel.backgroundTasks.openPortNumber", { port })}
            className="inline-flex cursor-pointer items-center gap-0.5 rounded bg-button-fill px-1 text-foreground transition-colors hover:bg-button-fill-hover outline-none focus-visible:focus-outline"
          >
            {`:${port}`}
            <ExternalLink className="size-2.5" aria-hidden="true" />
          </button>
        ))}
      </span>
    ) : hasPorts ? (
      <span className="text-foreground">
        {process.ports.map((port) => `:${port}`).join(" ")}
      </span>
    ) : (
      t("chatPanel.backgroundTasks.noPort")
    ),
    `PID ${process.pid}`,
    <span className="tabular-nums" data-testid="elapsed">
      {formatElapsed(Math.max(0, now - process.startedAt))}
    </span>,
  ];

  return (
    <li className="flex items-center rounded-md">
      <div className="flex min-w-0 flex-1 items-start gap-2.5 px-2 py-1.5">
        <span
          className="mt-[5px] size-1.5 shrink-0 rounded-full bg-status-running"
          aria-hidden="true"
        />
        <span className="min-w-0 flex-1">
          <span
            data-selectable-text="true"
            className="block whitespace-pre-wrap break-all font-mono text-xs leading-snug text-foreground"
          >
            {process.command}
          </span>
          <span className="mt-0.5 flex min-w-0 items-center gap-1.5 font-mono text-3xs text-muted-foreground">
            {meta.map((part, i) => (
              <React.Fragment key={i}>
                {i > 0 && <MetaDot />}
                <span
                  className={cn(
                    "whitespace-nowrap",
                    !process.cwd && i === meta.length - 1 && "min-w-0 truncate",
                  )}
                >
                  {part}
                </span>
              </React.Fragment>
            ))}
            {process.cwd && (
              <>
                <MetaDot />
                {/* rtl 让省略号落在开头;bdi 把路径本身隔成 ltr,开头的 / 或 ~ 不被挪到末尾。 */}
                <span
                  dir="rtl"
                  data-selectable-text="true"
                  title={process.cwd}
                  className="min-w-0 truncate text-left"
                >
                  <bdi>{process.cwd}</bdi>
                </span>
              </>
            )}
          </span>
        </span>
      </div>
      {process.ports.length === 1 && onOpenProcess && (
        <button
          type="button"
          onClick={() => onOpenProcess(process, process.ports[0])}
          aria-label={t("chatPanel.backgroundTasks.openPort")}
          title={t("chatPanel.backgroundTasks.openPort")}
          className="inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground outline-none focus-visible:focus-outline"
        >
          <ExternalLink className="size-3" aria-hidden="true" />
        </button>
      )}
      {onKillProcess && (
        <KillProcessButton process={process} onKillProcess={onKillProcess} />
      )}
    </li>
  );
}

function MetaDot() {
  return (
    <span className="text-muted-foreground/50" aria-hidden="true">
      ·
    </span>
  );
}

// 结束进程不可恢复,两段式:第一次点变成红色「结束？」,KILL_CONFIRM_WINDOW_MS 内再点
// 才执行,否则复原;执行中转圈并禁用,失败复原。
function KillProcessButton({
  process,
  onKillProcess,
}: {
  process: SessionProcess;
  onKillProcess: PendingAction<SessionProcess>;
}) {
  const { t } = useTranslation();
  const [armed, setArmed] = React.useState(false);
  const [killing, runKill] = usePendingAction();
  React.useEffect(() => {
    if (!armed) return;
    const id = setTimeout(() => setArmed(false), KILL_CONFIRM_WINDOW_MS);
    return () => clearTimeout(id);
  }, [armed]);

  if (armed) {
    return (
      <button
        type="button"
        onClick={() => {
          setArmed(false);
          runKill(() => onKillProcess(process));
        }}
        className="mr-1.5 inline-flex h-6 shrink-0 cursor-pointer items-center justify-center rounded bg-destructive px-1.5 text-2xs font-medium text-destructive-foreground transition-colors hover:bg-destructive/90 dark:bg-destructive/60 outline-none focus-visible:focus-outline"
      >
        {t("chatPanel.backgroundTasks.killProcessConfirm")}
      </button>
    );
  }
  return (
    <button
      type="button"
      onClick={() => setArmed(true)}
      disabled={killing}
      aria-busy={killing || undefined}
      aria-label={t("chatPanel.backgroundTasks.killProcess")}
      title={t("chatPanel.backgroundTasks.killProcess")}
      className={cn(rowIconButtonClassName, "mr-1.5")}
    >
      {killing ? (
        <LoaderCircle className={spinnerClassName} aria-hidden="true" />
      ) : (
        <X className="size-3.5" aria-hidden="true" />
      )}
    </button>
  );
}

// 状态由行首图形携带;失败 / 已停止另在 meta 行写出字来,运行中 / 已完成只留给读屏。
function StatusGlyph({ status }: { status: BackgroundTask["status"] }) {
  const base = "mt-px size-3.5 shrink-0";
  if (status === "running") {
    return (
      <LoaderCircle
        className={cn(
          base,
          "animate-spin text-status-running motion-reduce:animate-none",
        )}
        aria-hidden="true"
      />
    );
  }
  if (status === "failed") {
    return <X className={cn(base, "text-destructive")} aria-hidden="true" />;
  }
  if (status === "canceled") {
    return (
      <Ban className={cn(base, "text-muted-foreground")} aria-hidden="true" />
    );
  }
  return (
    <Check className={cn(base, "text-muted-foreground")} aria-hidden="true" />
  );
}

function StatusLabel({ status }: { status: BackgroundTask["status"] }) {
  const { t } = useTranslation();
  const dot = (
    <span className="text-muted-foreground/50" aria-hidden="true">
      ·
    </span>
  );
  // 静态 t() key:每条分支各用字面量 key,便于 i18n 静态校验(避免 t(cond?…))。
  if (status === "running") {
    return (
      <span className="sr-only">{t("chatPanel.backgroundTasks.running")}</span>
    );
  }
  if (status === "failed") {
    return (
      <>
        <span className="whitespace-nowrap text-destructive-text">
          {t("chatPanel.backgroundTasks.failed")}
        </span>
        {dot}
      </>
    );
  }
  if (status === "canceled") {
    return (
      <>
        <span className="whitespace-nowrap">
          {t("chatPanel.backgroundTasks.canceled")}
        </span>
        {dot}
      </>
    );
  }
  return (
    <span className="sr-only">{t("chatPanel.backgroundTasks.completed")}</span>
  );
}
