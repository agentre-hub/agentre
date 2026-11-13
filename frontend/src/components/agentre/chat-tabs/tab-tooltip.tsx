// frontend/src/components/agentre/chat-tabs/tab-tooltip.tsx
import * as React from "react";
import { Folder, GitBranch } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  StatusDot,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  cn,
  formatRelativeTime,
  statusConfig,
} from "@agentre-hub/agentre-ui";

type Props = {
  title: string;
  projectChain: string[] | null;
  projectColor: string | null;
  status: "idle" | "running" | "waiting" | "error";
  worktreeBranch: string | null;
  keyboardIndex: number | null;
  /** 最近一次消息时间, ms epoch; 0/未知时跳过时间那一段。*/
  lastMessageAt?: number;
  /**
   * 首次悬停的延迟；不给就用 Tab 条那个 TooltipProvider 的（400ms，卡片开过之后在
   * Tab 间滑动不再等）。测试传 0 跳过等待。
   */
  delayDuration?: number;
  /** Tab 条拖拽排序期间关着的闸：不弹出，已开的立即收起。 */
  gate?: HoverCardGate;
  children: React.ReactNode;
};

/**
 * 悬停卡的闸。拖拽开始 / 结束时开关它不会让任何组件重渲染——拿起 Tab 的那一帧若要
 * 整条 Tab 条重来，Tab 一多就掉帧；只有正开着的那张卡会收到通知收起自己。
 */
export type HoverCardGate = {
  readonly closed: boolean;
  setClosed: (closed: boolean) => void;
  subscribe: (listener: () => void) => () => void;
};

export function createHoverCardGate(): HoverCardGate {
  let closed = false;
  const listeners = new Set<() => void>();
  return {
    get closed() {
      return closed;
    },
    setClosed(next) {
      if (next === closed) return;
      closed = next;
      listeners.forEach((l) => l());
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}

/**
 * Tab 的悬停卡：项目链 + ⌘N 一行；标题**全文**（最多 3 行 —— 悬停最想看的就是它，
 * Tab 上只剩两三个字）；状态、时间、worktree 分支一行。
 *
 * 必须放在 Tab 条的 TooltipProvider 之下：延迟由它统一管，卡片开过之后在相邻 Tab 间
 * 横向滑动才能立刻换卡（每个 Tab 各包一个 Provider 时每格都要重新等）。
 */
export function TabTooltip({
  title,
  projectChain,
  projectColor,
  status,
  worktreeBranch,
  keyboardIndex,
  lastMessageAt,
  delayDuration,
  gate,
  children,
}: Props) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);

  React.useEffect(
    () =>
      gate?.subscribe(() => {
        if (gate.closed) setOpen(false);
      }),
    [gate],
  );

  return (
    <Tooltip
      delayDuration={delayDuration}
      open={open}
      onOpenChange={(next) => setOpen(next && !gate?.closed)}
    >
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent
        data-slot="tab-hover-card"
        side="bottom"
        sideOffset={6}
        align="start"
        className="w-[300px] max-w-[300px] rounded-lg px-3 pt-2.5 pb-3"
      >
        <div className="flex min-w-0 items-center gap-1.5">
          {projectChain && projectChain.length > 0 ? (
            <>
              <Folder
                data-testid="tooltip-folder-icon"
                className="size-3 shrink-0"
                style={projectColor ? { color: projectColor } : undefined}
                aria-hidden="true"
              />
              <span className="min-w-0 flex-1 truncate text-muted-foreground">
                {projectChain.join(" / ")}
              </span>
            </>
          ) : (
            <span className="min-w-0 flex-1 truncate text-muted-foreground">
              {t("chatTabs.tooltip.noProject")}
            </span>
          )}
          {keyboardIndex !== null ? (
            <kbd className="shrink-0 rounded-sm bg-button-fill px-1.5 font-mono text-2xs text-muted-foreground">
              ⌘{keyboardIndex}
            </kbd>
          ) : null}
        </div>
        {/* 标题是用户 / agent 的动态内容，不走 t()。 */}
        <div className="mt-1.5 line-clamp-3 text-aux font-medium leading-normal break-all text-foreground">
          {title}
        </div>
        <div className="mt-2 flex min-w-0 items-center gap-1.5 whitespace-nowrap text-muted-foreground">
          <span
            className={cn(
              "inline-flex shrink-0 items-center gap-1.5",
              statusConfig[status].textClassName,
            )}
          >
            <StatusDot
              data-testid="tab-tooltip-status-dot"
              status={status}
              size="xs"
            />
            {t(`chatTabs.status.${status}`)}
          </span>
          {lastMessageAt && lastMessageAt > 0 ? (
            <>
              <span aria-hidden="true" className="text-border-strong">
                ·
              </span>
              <Ago at={lastMessageAt} />
            </>
          ) : null}
          {worktreeBranch ? (
            <>
              <span aria-hidden="true" className="text-border-strong">
                ·
              </span>
              <span
                data-testid="tab-tooltip-branch"
                className="inline-flex min-w-0 items-center gap-1"
              >
                <GitBranch className="size-3 shrink-0" aria-hidden="true" />
                <span className="truncate font-mono">{worktreeBranch}</span>
              </span>
            </>
          ) : null}
        </div>
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * 「N 分钟前」。卡片内容只在打开时挂载，所以「现在」取在这一刻：每次打开都是新的，
 * 而不是 Tab 挂载那一刻的旧时间。
 */
function Ago({ at }: { at: number }) {
  const { t } = useTranslation();
  const [now] = React.useState(() => Date.now());
  return <span className="shrink-0">{formatRelativeTime(at, now, t)}</span>;
}
