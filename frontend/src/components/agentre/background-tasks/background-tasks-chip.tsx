import { Bot, Terminal } from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";

import {
  Button,
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
  Popover,
  PopoverContent,
  PopoverTrigger,
  shortenModelName,
} from "@agentre-hub/agentre-ui";

import {
  BackgroundTasksPopoverContent,
  formatElapsed,
} from "./background-tasks-popover";
import type { BackgroundTask } from "./types";

// 预览卡里最多列几条命令;其余折成「还有 N 个命令」。
const PEEK_COMMAND_LIMIT = 4;

type BackgroundTasksChipProps = {
  tasks: BackgroundTask[];
  onClearCompleted?: () => void;
  onStopTask?: (task: BackgroundTask) => void;
  onLocateTask?: (task: BackgroundTask) => void;
};

function PeekRow({
  task,
  agent,
  now,
}: {
  task: BackgroundTask;
  agent: boolean;
  now: number;
}) {
  const { t } = useTranslation();
  const Icon = agent ? Bot : Terminal;
  // 子代理一行写明类型、步数、模型；类型与模型是动态值，不走 t()。
  const meta: string[] = [];
  if (agent) {
    meta.push(task.subagentType || t("chatPanel.backgroundTasks.subagent"));
    if (task.toolUses != null && task.toolUses > 0) {
      meta.push(t("chatPanel.backgroundTasks.steps", { count: task.toolUses }));
    }
    if (task.model) meta.push(shortenModelName(task.model));
  }
  const elapsed =
    task.startedAt != null ? formatElapsed(now - task.startedAt) : null;
  return (
    <li className="flex items-start gap-2 rounded-lg px-2 py-1.5">
      <Icon
        className={
          agent
            ? "mt-0.5 size-3.5 shrink-0 text-status-running"
            : "mt-0.5 size-3.5 shrink-0 text-muted-foreground"
        }
        aria-hidden="true"
      />
      <span className="min-w-0 flex-1">
        {/* description 是 agent 的动态输出，不走 t()。 */}
        <span className="block truncate text-[12.5px]">{task.description}</span>
        {meta.length > 0 ? (
          <span className="block truncate font-mono text-3xs text-muted-foreground">
            {meta.join(" · ")}
          </span>
        ) : null}
      </span>
      {elapsed != null ? (
        <span className="mt-0.5 shrink-0 font-mono text-3xs whitespace-nowrap text-muted-foreground tabular-nums">
          {elapsed}
        </span>
      ) : null}
    </li>
  );
}

export function BackgroundTasksChip({
  tasks,
  onClearCompleted,
  onStopTask,
  onLocateTask,
}: BackgroundTasksChipProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [peekOpen, setPeekOpen] = React.useState(false);
  // 预览卡开着时每秒走一次已运行时长；关着不起计时器。
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    if (!peekOpen) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [peekOpen]);

  const running = tasks.filter((task) => task.status === "running");
  const runningCount = running.length;
  const endedCount = tasks.length - runningCount;
  const hidden = runningCount === 0;

  // 按钮消失时弹层与预览卡随之卸载，Radix 不会回调 onOpenChange(false)：不在这里
  // 收起，下次有任务开跑时弹层会自己开着回来，预览卡的秒表也一直空转。
  React.useEffect(() => {
    if (!hidden) return;
    setOpen(false);
    setPeekOpen(false);
  }, [hidden]);

  // 只在有任务在跑时出现;全部结束就收起,历史留在弹层的「已结束」页里。
  if (hidden) return null;

  const agents = running.filter((task) => task.kind === "local_agent");
  const commands = running.filter((task) => task.kind !== "local_agent");

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) setPeekOpen(false);
      }}
    >
      <HoverCard
        open={peekOpen && !open}
        onOpenChange={setPeekOpen}
        openDelay={150}
        closeDelay={100}
      >
        <HoverCardTrigger asChild>
          <PopoverTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={t("chatPanel.backgroundTasks.ariaRunning", {
                count: runningCount,
              })}
            >
              {/* 绿点挂在图标右上角外沿、不压图形，缓慢呼吸；减少动态效果时静止。 */}
              <span className="relative inline-flex">
                <Bot data-icon="only" aria-hidden="true" />
                <span
                  data-testid="background-tasks-dot"
                  className="absolute -top-0.5 -right-[3px] size-1.5 animate-pulse rounded-full bg-status-running motion-reduce:animate-none"
                  aria-hidden="true"
                />
              </span>
            </Button>
          </PopoverTrigger>
        </HoverCardTrigger>
        <HoverCardContent
          align="end"
          className="w-[340px] p-1.5"
          data-testid="background-tasks-peek"
        >
          {agents.length > 0 ? (
            <section>
              <div className="px-2 pt-2 pb-1 text-2xs text-muted-foreground">
                {t("chatPanel.backgroundTasks.peekAgents", {
                  count: agents.length,
                })}
              </div>
              <ul>
                {agents.map((task) => (
                  <PeekRow key={task.toolUseId} task={task} agent now={now} />
                ))}
              </ul>
            </section>
          ) : null}
          {commands.length > 0 ? (
            <section>
              <div className="px-2 pt-2 pb-1 text-2xs text-muted-foreground">
                {t("chatPanel.backgroundTasks.peekCommands", {
                  count: commands.length,
                })}
              </div>
              <ul>
                {commands.slice(0, PEEK_COMMAND_LIMIT).map((task) => (
                  <PeekRow
                    key={task.toolUseId}
                    task={task}
                    agent={false}
                    now={now}
                  />
                ))}
              </ul>
              {commands.length > PEEK_COMMAND_LIMIT ? (
                <div className="px-2 pb-1.5 pl-7 text-2xs text-muted-foreground">
                  {t("chatPanel.backgroundTasks.peekMore", {
                    count: commands.length - PEEK_COMMAND_LIMIT,
                  })}
                </div>
              ) : null}
            </section>
          ) : null}
          <div className="mt-1 rounded-lg bg-field-fill px-2 py-2 text-2xs text-muted-foreground">
            {endedCount > 0
              ? t("chatPanel.backgroundTasks.peekFootEnded", {
                  count: endedCount,
                })
              : t("chatPanel.backgroundTasks.peekFoot")}
          </div>
        </HoverCardContent>
      </HoverCard>
      {/* 定宽:主页与「已结束」页来回切换时弹层不跳宽。 */}
      <PopoverContent align="end" className="w-[340px] p-0">
        <BackgroundTasksPopoverContent
          tasks={tasks}
          onClearCompleted={onClearCompleted}
          onStopTask={onStopTask}
          onLocateTask={
            onLocateTask &&
            ((task) => {
              // 对话滚过去之后弹层不该还盖在上面。
              setOpen(false);
              onLocateTask(task);
            })
          }
        />
      </PopoverContent>
    </Popover>
  );
}
