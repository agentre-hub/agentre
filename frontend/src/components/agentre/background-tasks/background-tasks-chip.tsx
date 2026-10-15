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
} from "@agentre-hub/agentre-ui";

import { BackgroundTasksPopoverContent } from "./background-tasks-popover";
import type { BackgroundTask } from "./types";

// 预览卡里最多列几条命令;其余折成「还有 N 个命令」。
const PEEK_COMMAND_LIMIT = 4;

type BackgroundTasksChipProps = {
  tasks: BackgroundTask[];
  onClearCompleted?: () => void;
  onStopTask?: (task: BackgroundTask) => void;
  onLocateTask?: (task: BackgroundTask) => void;
};

function PeekRow({ task, agent }: { task: BackgroundTask; agent: boolean }) {
  const Icon = agent ? Bot : Terminal;
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
      <span className="min-w-0 flex-1 truncate text-[12.5px]">
        {task.description}
      </span>
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

  const running = tasks.filter((task) => task.status === "running");
  const runningCount = running.length;
  const endedCount = tasks.length - runningCount;

  // 只在有任务在跑时出现;全部结束就收起,历史留在弹层的「已结束」页里。
  if (runningCount === 0) return null;

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
              variant="outline"
              size="icon-sm"
              aria-label={t("chatPanel.backgroundTasks.ariaRunning", {
                count: runningCount,
              })}
              className="relative"
            >
              <Bot data-icon="only" aria-hidden="true" />
              <span
                data-testid="background-tasks-dot"
                className="absolute top-1 right-1 size-1.5 rounded-full bg-status-running"
                aria-hidden="true"
              />
            </Button>
          </PopoverTrigger>
        </HoverCardTrigger>
        <HoverCardContent
          align="end"
          className="w-72 p-1.5"
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
                  <PeekRow key={task.toolUseId} task={task} agent />
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
                  <PeekRow key={task.toolUseId} task={task} agent={false} />
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
