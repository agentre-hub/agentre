import { Bot } from "lucide-react";
import React from "react";
import { useTranslation } from "react-i18next";

import {
  Button,
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@agentre-hub/agentre-ui";

import { usePanelShown } from "../chat-panel/panel-shown";
import { BackgroundTasksPopoverContent } from "./background-tasks-popover";
import type { BackgroundTask } from "./types";

// 指针停在按钮上多久才打开；离开按钮、又没进弹层多久才收起（留出跨过两者
// 之间那道缝的时间）。
const HOVER_OPEN_DELAY_MS = 150;
const HOVER_CLOSE_DELAY_MS = 120;

type BackgroundTasksChipProps = {
  tasks: BackgroundTask[];
  onClearCompleted?: () => void;
  onStopTask?: (task: BackgroundTask) => void;
  onLocateTask?: (task: BackgroundTask) => void;
};

// hover:指针悬停打开，离开就收；pinned:点开（含 Enter / Space），只有再点一次、
// Esc 或点外面才收。
type OpenMode = "closed" | "hover" | "pinned";

export function BackgroundTasksChip({
  tasks,
  onClearCompleted,
  onStopTask,
  onLocateTask,
}: BackgroundTasksChipProps) {
  const { t } = useTranslation();
  const [mode, setMode] = React.useState<OpenMode>("closed");
  const open = mode !== "closed";
  // 最近一次打开的方式。弹层挂载 / 卸载时的焦点回调读它：悬停打开的弹层
  // 既不把焦点拉进来，收起时也不把焦点塞回按钮。
  const openedByHoverRef = React.useRef(false);
  const openTimerRef = React.useRef<ReturnType<typeof setTimeout>>(undefined);
  const closeTimerRef = React.useRef<ReturnType<typeof setTimeout>>(undefined);

  const clearTimers = React.useCallback(() => {
    clearTimeout(openTimerRef.current);
    clearTimeout(closeTimerRef.current);
  }, []);
  React.useEffect(() => clearTimers, [clearTimers]);

  const runningCount = tasks.filter((task) => task.status === "running").length;
  const hidden = runningCount === 0;
  const panelShown = usePanelShown();

  // 两种情况 Radix 都不会回调 onOpenChange(false)，得自己收起：
  // - 按钮消失，弹层随之卸载：若不收，下次有任务开跑时弹层会自己开着回来；
  // - 宿主把所在面板藏起来（切 Tab / 离开对话页）：按钮仍挂载、不一定失焦，浮层会
  //   脱离触发器漂在窗口里。
  // 还在等的悬停打开一并作废：指针没动就切走时收不到离开，延迟到点照样会打开，
  // 而那时弹层没了锚点，会被挪到窗口左上角、漂在别的页面上。
  const dismissed = hidden || !panelShown;
  React.useEffect(() => {
    if (!dismissed) return;
    clearTimers();
    setMode("closed");
  }, [dismissed, clearTimers]);

  // 只在有任务在跑时出现;全部结束就收起,历史留在弹层的「已结束」页里。
  if (hidden) return null;

  const scheduleClose = () => {
    clearTimeout(closeTimerRef.current);
    closeTimerRef.current = setTimeout(() => {
      setMode((current) => (current === "hover" ? "closed" : current));
    }, HOVER_CLOSE_DELAY_MS);
  };

  const onTriggerPointerEnter = (event: React.PointerEvent) => {
    if (event.pointerType === "touch" || !panelShown) return;
    clearTimeout(closeTimerRef.current);
    if (open) return;
    clearTimeout(openTimerRef.current);
    openTimerRef.current = setTimeout(() => {
      openedByHoverRef.current = true;
      setMode((current) => (current === "closed" ? "hover" : current));
    }, HOVER_OPEN_DELAY_MS);
  };

  const onTriggerPointerLeave = (event: React.PointerEvent) => {
    if (event.pointerType === "touch") return;
    clearTimeout(openTimerRef.current);
    if (mode === "hover") scheduleClose();
  };

  // 开合由这里自己管，不走 PopoverTrigger 的开关切换：悬停开着时点一下是「钉住」，
  // 不是「关掉」。preventDefault 让 Radix 跳过它的切换。
  const onTriggerClick = (event: React.MouseEvent) => {
    event.preventDefault();
    clearTimers();
    if (mode === "hover") {
      // 钉住之后它就是点开的弹层：收起时焦点照常交还按钮。
      openedByHoverRef.current = false;
      setMode("pinned");
      return;
    }
    if (mode === "pinned") {
      setMode("closed");
      return;
    }
    openedByHoverRef.current = false;
    setMode("pinned");
  };

  return (
    <Popover
      open={open}
      // 只剩 Radix 自己的关闭：Esc、点外面、焦点移出。
      onOpenChange={(next) => {
        clearTimers();
        if (!next) setMode("closed");
      }}
    >
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={t("chatPanel.backgroundTasks.ariaRunning", {
            count: runningCount,
          })}
          onPointerEnter={onTriggerPointerEnter}
          onPointerLeave={onTriggerPointerLeave}
          onClick={onTriggerClick}
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
      {/* 定宽:主页与「已结束」页来回切换时弹层不跳宽。 */}
      <PopoverContent
        align="end"
        className="w-[340px] p-0"
        onPointerEnter={() => clearTimeout(closeTimerRef.current)}
        onPointerLeave={(event) => {
          if (event.pointerType === "touch") return;
          if (mode === "hover") scheduleClose();
        }}
        onOpenAutoFocus={(event) => {
          if (openedByHoverRef.current) event.preventDefault();
        }}
        onCloseAutoFocus={(event) => {
          if (openedByHoverRef.current) event.preventDefault();
        }}
      >
        <BackgroundTasksPopoverContent
          tasks={tasks}
          onClearCompleted={onClearCompleted}
          onStopTask={onStopTask}
          onLocateTask={
            onLocateTask &&
            ((task) => {
              // 对话滚过去之后弹层不该还盖在上面。
              clearTimers();
              setMode("closed");
              onLocateTask(task);
            })
          }
        />
      </PopoverContent>
    </Popover>
  );
}
