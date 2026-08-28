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
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@agentre-hub/agentre-ui";

import type { ChatSessionDetail } from "@/hooks/use-chat-session";
import { cn } from "@/lib/utils";

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
  canStop,
  onStop,
  sidebarOpen,
  onToggleSidebar,
  onRename,
  onCopyLaunchCommand,
  onDelete,
}: ChatPanelHeaderProps) {
  const { t } = useTranslation();
  return (
    <div
      data-testid="chat-header"
      className="@container/header flex h-[68px] shrink-0 items-center gap-3 border-b border-border px-5"
    >
      {session ? (
        <AgentAvatar
          name={session.agentName}
          initials={session.agentName.charAt(0)}
          color={(session.agentColor as AgentColor) || "agent-1"}
          size="md"
        />
      ) : showNewSessionPrompt && newSessionAgent ? (
        <AgentAvatar
          name={newSessionAgent.name}
          initials={newSessionAgent.name.charAt(0)}
          color={(newSessionAgent.avatarColor as AgentColor) || "agent-1"}
          size="md"
        />
      ) : (
        // 加载中 / 加载失败也占住身份方块那一格，否则标题会横向跳一格。
        <div
          aria-hidden="true"
          className="size-8 shrink-0 rounded-lg bg-muted"
        />
      )}
      <div className="min-w-0 flex-1">
        <h2
          className="line-clamp-2 break-words text-sm font-semibold leading-snug"
          title={title}
        >
          {title}
        </h2>
        <div
          data-testid="chat-header-meta"
          className="mt-0.5 flex min-w-0 items-center gap-x-1.5 overflow-hidden font-mono text-2xs whitespace-nowrap text-muted-foreground"
        >
          {topline ? (
            <span
              data-testid="chat-header-topline"
              className="inline-flex min-w-0 items-center gap-1.5 @max-[420px]/header:hidden"
            >
              <span className="inline-flex min-w-0 items-center text-primary-text">
                {topline}
              </span>
              <span className="text-border-strong">·</span>
            </span>
          ) : null}
          {agentName ? (
            <span className="inline-flex shrink-0 items-center gap-1">
              <StatusDot status={status} size="xs" />
              <span className={cn(statusConfig[status].textClassName)}>
                {agentName}
              </span>
            </span>
          ) : null}
          {agentName && metaTail ? (
            <span className="text-border-strong">·</span>
          ) : null}
          {metaTail ? <span className="shrink-0">{metaTail}</span> : null}
          {/* 机器 chip 守卫（R15/R20）：远端会话今天就显示；
              多档 Agent（execTargetCount > 1）的本机会话也总是显示——本机走
              DeviceTag 既有的 deviceId === "" 分支（组件本来就实现了这一支，
              只是这里以前没调用）。单档 Agent 的本机会话维持今天"什么都不
              显示"的行为。 */}
          {session && (session.deviceID || session.execTargetCount > 1) ? (
            <span
              data-testid="chat-header-device"
              className="inline-flex shrink-0 items-center gap-1.5 @max-[560px]/header:hidden"
            >
              <span className="text-border-strong">·</span>
              {session.cwd ? (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <DeviceTag
                      deviceId={session.deviceID}
                      deviceName={session.deviceName || session.deviceID}
                      online={session.online ?? true}
                    />
                  </TooltipTrigger>
                  <TooltipContent
                    side="bottom"
                    className="max-w-[480px] break-all font-mono text-2xs"
                  >
                    {session.cwd}
                  </TooltipContent>
                </Tooltip>
              ) : (
                <DeviceTag
                  deviceId={session.deviceID}
                  deviceName={session.deviceName || session.deviceID}
                  online={session.online ?? true}
                />
              )}
            </span>
          ) : null}
        </div>
      </div>
      {/* role="toolbar" 只包这一组控件；标题与 meta 是内容，不在其中（决策 9 之外
          的可及性修正：工具栏语义此前罩住了标题文本）。 */}
      <div
        role="toolbar"
        aria-label={t("chatPanel.toolbar.aria")}
        className="flex shrink-0 items-center gap-3"
      >
        {session ? (
          /* 后台任务胶囊：有运行中任务时显示，点击展开只读弹层 */
          <BackgroundTasksChip
            tasks={backgroundTasks}
            onClearCompleted={onClearCompletedTasks}
            onStopTask={onStopTask}
          />
        ) : null}
        {/* 停止按需渲染（决策 8）：停不下来的时候不摆一个空转的禁用控件。 */}
        {session && canStop ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => onStop(session.id)}
            title={t("chatPanel.toolbar.stopActiveTitle")}
          >
            <Square data-icon="inline-start" aria-hidden="true" />
            {t("chatPanel.toolbar.stop")}
          </Button>
        ) : null}
        <Button
          type="button"
          variant="outline"
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
                variant="outline"
                size="icon-sm"
                aria-label={t("common.moreActions")}
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
                className="text-destructive focus:text-destructive"
                onClick={() => onDelete(session.id)}
              >
                {t("common.delete")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        ) : null}
      </div>
    </div>
  );
}

export { ChatPanelHeader };
export type { ChatPanelHeaderProps };
