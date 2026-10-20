import type * as React from "react";
import {
  MoreHorizontal,
  PanelRight,
  PanelRightClose,
  Square,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  SessionHeaderBand,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  type SessionHeaderMetaPart,
} from "@agentre-hub/agentre-ui";

import type { ChatSessionDetail } from "@/hooks/use-chat-session";
import { cn } from "@agentre-hub/agentre-ui";

import { BackgroundTasksChip } from "../background-tasks/background-tasks-chip";
import type { BackgroundTask } from "../background-tasks/types";
import { AgentAvatar, DeviceTag, StatusDot } from "../primitives";
import type { AgentColor, AgentStatus } from "../types";
import { statusConfig } from "../types";

import type { chat_svc } from "../../../../wailsjs/go/models";

type ChatAgentItem = chat_svc.ChatAgentItem;

type ChatPanelHeaderProps = {
  session: ChatSessionDetail | null;
  newSessionAgent?: ChatAgentItem | null;
  /** 新建未首发态(!sessionId && newSessionAgent)。头部四态之一。*/
  showNewSessionPrompt: boolean;
  title: string;
  status: AgentStatus;
  topline: React.ReactNode;
  agentName: string;
  metaTail: string;
  backgroundTasks: BackgroundTask[];
  onClearCompletedTasks: () => void;
  onStopTask?: (task: BackgroundTask) => void;
  onLocateTask?: (task: BackgroundTask) => void;
  canStop: boolean;
  onStop: (sessionId: number) => void;
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
  onRename: (session: ChatSessionDetail) => void;
  onCopyLaunchCommand: (sessionId: number) => void;
  onDelete: (sessionId: number) => void;
};

/* ── Toolbar / Header ── */
/* 四态同一副外壳：固定两行标题的高度 + 整块垂直居中，所以新建会话首发、
   切 tab、标题长短都不改变它（决策 2/3）。
   单行 meta：breadcrumb · dot+agent · relativeTime · device(tooltip cwd)；
   状态文案 (RUNNING/WAITING) 由裸 dot 与 agent 名的颜色携带（决策 1）。
   cwd 太长不适合常驻一行，挪到 DeviceTag 的 tooltip。
   窄档不折行，改按 @container/header 分档收起：先机器名（DeviceTag 本来
   就有 tooltip 兜底），再项目/分支（标题与侧栏都还说得出它在哪）（决策 4）。 */
function ChatPanelHeader({
  session,
  newSessionAgent,
  showNewSessionPrompt,
  title,
  status,
  topline,
  agentName,
  metaTail,
  backgroundTasks,
  onClearCompletedTasks,
  onStopTask,
  onLocateTask,
  canStop,
  onStop,
  sidebarOpen,
  onToggleSidebar,
  onRename,
  onCopyLaunchCommand,
  onDelete,
}: ChatPanelHeaderProps) {
  const { t } = useTranslation();
  /* meta 行的各段。只有拿得出来的才进来，分隔符由顶带夹在真的存在的两段之间。 */
  const metaParts: SessionHeaderMetaPart[] = [];
  if (topline) {
    metaParts.push({
      key: "topline",
      testId: "chat-header-topline",
      // 窄档后收：项目/分支在标题与侧栏都还说得出（决策 4）。
      hideAt: "@max-[420px]/header:hidden",
      node: (
        <span className="inline-flex min-w-0 items-center text-primary-text">
          {topline}
        </span>
      ),
    });
  }
  /* 身份缩成 meta 行里的一枚小头像（顶带不再有 32px 方块，那副样子和转录里每条消息
     一模一样）；状态点挂在它右下角，空闲维持「没有记号」。 */
  const identityColor =
    (session?.agentColor as AgentColor) ||
    (showNewSessionPrompt
      ? (newSessionAgent?.avatarColor as AgentColor)
      : undefined) ||
    "agent-1";
  if (agentName) {
    metaParts.push({
      key: "agent",
      node: (
        <span className="inline-flex shrink-0 items-center gap-1.5">
          <span className="relative inline-flex shrink-0">
            <AgentAvatar
              name={agentName}
              initials={agentName.charAt(0)}
              color={identityColor}
              size="xs"
            />
            {status === "idle" ? null : (
              <StatusDot
                data-testid="chat-header-status-badge"
                status={status}
                size="xs"
                className="absolute -right-[3px] -bottom-[3px] ring-[1.5px] ring-sheet-header"
              />
            )}
          </span>
          <span className={cn(statusConfig[status].textClassName)}>
            {agentName}
          </span>
        </span>
      ),
    });
  }
  if (metaTail) {
    metaParts.push({
      key: "tail",
      node: <span className="shrink-0">{metaTail}</span>,
    });
  }
  /* 机器 chip 守卫（R15/R20）：远端会话今天就显示；
     多档 Agent（execTargetCount > 1）的本机会话也总是显示——本机走
     DeviceTag 既有的 deviceId === "" 分支（组件本来就实现了这一支，
     只是这里以前没调用）。单档 Agent 的本机会话维持今天"什么都不
     显示"的行为。 */
  if (session && (session.deviceID || session.execTargetCount > 1)) {
    const tag = (
      <DeviceTag
        deviceId={session.deviceID}
        deviceName={session.deviceName || session.deviceID}
        online={session.online ?? true}
      />
    );
    metaParts.push({
      key: "device",
      testId: "chat-header-device",
      // 最先收：机器名在 DeviceTag 的 tooltip 与设备页里都还在。
      hideAt: "@max-[560px]/header:hidden",
      node: session.cwd ? (
        <Tooltip>
          <TooltipTrigger asChild>{tag}</TooltipTrigger>
          <TooltipContent
            side="bottom"
            className="max-w-[480px] break-all font-mono text-2xs"
          >
            {session.cwd}
          </TooltipContent>
        </Tooltip>
      ) : (
        tag
      ),
    });
  }

  return (
    <SessionHeaderBand
      testId="chat-header"
      metaTestId="chat-header-meta"
      className="px-5"
      title={title}
      meta={metaParts}
      actions={
        /* role="toolbar" 只包这一组控件；标题与 meta 是内容，不在其中（决策 9 之外
           的可及性修正：工具栏语义此前罩住了标题文本）。 */
        <div
          role="toolbar"
          aria-label={t("chatPanel.toolbar.aria")}
          className="flex shrink-0 items-center gap-3"
        >
          {session ? (
            /* 后台任务胶囊：有后台任务时显示，点击展开弹层 */
            <BackgroundTasksChip
              tasks={backgroundTasks}
              onClearCompleted={onClearCompletedTasks}
              onStopTask={onStopTask}
              onLocateTask={onLocateTask}
            />
          ) : null}
          {/* 停止按需渲染（决策 8）：停不下来的时候不摆一个空转的禁用控件。 */}
          {session && canStop ? (
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={() => onStop(session.id)}
              aria-label={t("chatPanel.toolbar.stop")}
              title={t("chatPanel.toolbar.stopActiveTitle")}
              className="hover:bg-destructive/15 hover:text-destructive-text dark:hover:bg-destructive/20"
            >
              <Square
                data-icon="only"
                className="size-3.5 fill-current"
                aria-hidden="true"
              />
            </Button>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={t("chatPanel.toolbar.contextSidebar")}
            onClick={onToggleSidebar}
            title={
              sidebarOpen
                ? t("chatPanel.toolbar.hideContextSidebar")
                : t("chatPanel.toolbar.showContextSidebar")
            }
          >
            {sidebarOpen ? (
              <PanelRightClose data-icon="only" aria-hidden="true" />
            ) : (
              <PanelRight data-icon="only" aria-hidden="true" />
            )}
          </Button>
          {/* 更多操作：改名 / 复制启动命令 / 删除三项都要一条真的会话，
            新建未首发与加载中因此不摆这颗。 */}
          {session ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t("common.moreActions")}
                  title={t("common.moreActions")}
                >
                  <MoreHorizontal data-icon="only" aria-hidden="true" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={() => onRename(session)}>
                  {t("chatPanel.actions.rename")}
                </DropdownMenuItem>
                {(session.backendType === "claudecode" ||
                  session.backendType === "codex" ||
                  session.backendType === "piagent") && (
                  <DropdownMenuItem
                    onClick={() => onCopyLaunchCommand(session.id)}
                  >
                    {t("chatPanel.launchCommand.copy")}
                  </DropdownMenuItem>
                )}
                <DropdownMenuItem
                  className="text-destructive-text focus:text-destructive-text"
                  onClick={() => onDelete(session.id)}
                >
                  {t("common.delete")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null}
        </div>
      }
    />
  );
}

export { ChatPanelHeader };
export type { ChatPanelHeaderProps };
