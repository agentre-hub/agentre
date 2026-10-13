import { ChevronDown } from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";

import {
  Button,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@agentre-hub/agentre-ui";
import { cn } from "@agentre-hub/agentre-ui";

import { BackgroundTasksPopoverContent } from "./background-tasks-popover";
import type { BackgroundTask } from "./types";

type BackgroundTasksChipProps = {
  tasks: BackgroundTask[];
  onClearCompleted?: () => void;
  onStopTask?: (task: BackgroundTask) => void;
  onLocateTask?: (task: BackgroundTask) => void;
};

export function BackgroundTasksChip({
  tasks,
  onClearCompleted,
  onStopTask,
  onLocateTask,
}: BackgroundTasksChipProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);

  const runningCount = tasks.filter((task) => task.status === "running").length;
  const endedCount = tasks.length - runningCount;

  // Hidden only when there is nothing to show at all.
  if (tasks.length === 0) return null;

  const isRunning = runningCount > 0;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          size="sm"
          aria-label={t("chatPanel.backgroundTasks.aria")}
          className="gap-1.5"
        >
          <span
            className={cn(
              "inline-block size-2 rounded-full",
              isRunning ? "bg-status-running" : "bg-muted-foreground",
            )}
            aria-hidden="true"
          />
          {isRunning
            ? t("chatPanel.backgroundTasks.chip", { count: runningCount })
            : t("chatPanel.backgroundTasks.endedChip", { count: endedCount })}
          <ChevronDown className="size-3 opacity-60" aria-hidden="true" />
        </Button>
      </PopoverTrigger>
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
