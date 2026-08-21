// frontend/src/components/agentre/chat-tabs/tab.tsx
import * as React from "react";
import { Loader2, MonitorUp, Pin, TerminalSquare, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

export type TabStatus = "idle" | "running" | "waiting" | "error";

export type TabProps = {
  title: string;
  kind?: "session" | "new" | "terminal" | "run" | "peer";
  avatar: { letter: string; color: string };
  active: boolean;
  isPreview: boolean;
  isPinned: boolean;
  status: TabStatus;
  projectColor: string | null;
  worktree: boolean;
  pillText: string | null;
  onActivate: () => void;
  onClose: () => void;
  onDoublePromote: () => void;
};

const statusDotColor: Record<TabStatus, string | null> = {
  idle: null,
  running: "bg-status-running",
  waiting: "bg-status-waiting",
  error: "bg-destructive",
};

// forwardRef + 透传剩余 props,让 Radix Tooltip 的 `asChild`(及未来其它 Slot 风格
// 包装器)能把 onPointerEnter / onPointerLeave / data-state / ref 等挂到 Tab 的根
// div 上。原先 plain function component 会把这些事件吞掉,导致 hover 浮窗不出现。
export const Tab = React.forwardRef<
  HTMLDivElement,
  TabProps &
    Omit<React.HTMLAttributes<HTMLDivElement>, "onClick" | "onDoubleClick">
>(function Tab(
  {
    title,
    kind,
    avatar,
    active,
    isPreview,
    isPinned,
    status,
    projectColor,
    worktree,
    pillText,
    onActivate,
    onClose,
    onDoublePromote,
    className,
    ...rest
  },
  ref,
) {
  const { t } = useTranslation();

  return (
    <div
      ref={ref}
      {...rest}
      role="tab"
      data-active={active}
      data-preview={isPreview}
      data-project-color={projectColor ?? undefined}
      className={cn(
        "relative flex h-[38px] min-w-[120px] max-w-[240px] flex-shrink items-center gap-1.5 border-r border-border px-2.5 text-xs",
        active ? "bg-background" : "hover:bg-card",
        isPreview && "italic",
        className,
      )}
      onClick={onActivate}
      onDoubleClick={onDoublePromote}
    >
      {active ? (
        <span className="absolute left-0 top-0 h-[2px] w-full bg-primary" />
      ) : null}
      {projectColor ? (
        <span
          className="absolute bottom-0 left-0 h-[2px] w-full"
          style={{
            backgroundColor: projectColor,
            opacity: worktree ? 0.4 : 1,
          }}
        />
      ) : null}
      {statusDotColor[status] ? (
        <span className={cn("size-1.5 rounded-full", statusDotColor[status])} />
      ) : null}
      {kind === "terminal" ? (
        <TerminalSquare
          className="size-4 text-muted-foreground"
          aria-hidden="true"
        />
      ) : kind === "peer" ? (
        <MonitorUp
          className="size-4 text-muted-foreground"
          aria-hidden="true"
        />
      ) : (
        <span
          className="inline-flex size-4 items-center justify-center rounded-sm"
          style={{ backgroundColor: avatar.color }}
        >
          <span className="text-[9px] font-semibold text-agent-foreground">
            {avatar.letter}
          </span>
        </span>
      )}
      {isPinned ? (
        <Pin
          data-testid="tab-pin-icon"
          className="size-2.5 rotate-[30deg] text-primary"
          aria-hidden="true"
        />
      ) : null}
      <span
        className={cn(
          "min-w-0 flex-1 truncate",
          active ? "font-medium text-foreground" : "text-muted-foreground",
        )}
      >
        {title}
      </span>
      {pillText ? (
        <span className="inline-flex items-center rounded-sm border border-status-waiting bg-status-waiting-bg px-1.5 text-[9px] font-semibold text-status-waiting">
          {pillText}
        </span>
      ) : null}
      {status === "running" ? (
        <span className="inline-flex items-center gap-0.5">
          <Loader2
            data-testid="tab-spinner"
            className="size-3 animate-spin text-status-running"
            aria-hidden="true"
          />
          <button
            type="button"
            aria-label={t("chatTabs.actions.closeTab")}
            className="inline-flex size-4 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground"
            onClick={(e) => {
              e.stopPropagation();
              onClose();
            }}
          >
            <X className="size-2.5" aria-hidden="true" />
          </button>
        </span>
      ) : (
        <button
          type="button"
          aria-label={t("chatTabs.actions.closeTab")}
          className="inline-flex size-4 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground"
          onClick={(e) => {
            e.stopPropagation();
            onClose();
          }}
        >
          <X className="size-2.5" aria-hidden="true" />
        </button>
      )}
    </div>
  );
});
