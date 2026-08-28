import * as React from "react";
import { ChevronDown, Pin, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Badge,
  Button,
  SessionGroup,
  SessionRow,
  type SessionRowModel,
} from "@agentre-hub/agentre-ui";

import { cn } from "@/lib/utils";
import type { AttentionReason } from "@/stores/attention-store";

import { AgentAvatar, StatusDot } from "./primitives";
import type { AgentColor } from "./types";

type AgentPanelSectionProps = React.ComponentProps<"div"> & {
  label: string;
  icon?: "pin";
};

function AgentPanelSection({
  className,
  label,
  icon,
  ...props
}: AgentPanelSectionProps) {
  return (
    <div
      className={cn(
        "flex items-center gap-1 px-2 pb-0.5 font-mono text-2xs font-semibold text-muted-foreground",
        className,
      )}
      {...props}
    >
      {icon === "pin" ? (
        <Pin className="size-2.5 -rotate-[30deg]" aria-hidden="true" />
      ) : null}
      <span>{label}</span>
    </div>
  );
}

/**
 * 会话行的展示模型。定义随 `SessionRow` / `SessionGroup` 一起搬进
 * `@agentre-hub/agentre-ui`（agentre-server 的会话列表要用同一件东西）；
 * 这里保留本地别名，仓库内 40 多个引用点不必逐个改指包。
 *
 * `attentionRank` 在包里是**不透明 token**（包只解释 `"selected"` 那一个值），
 * 桌面端在这里把它收窄回 `AttentionReason | "selected"` —— 不能因为搬了包
 * 就把宿主侧的类型放松成任意字符串。
 */
type AgentSession = Omit<SessionRowModel, "attentionRank"> & {
  attentionRank?: AttentionReason | "selected";
};

type AgentGroupProps = React.ComponentProps<"article"> & {
  activeCount?: number;
  blockReason?: string;
  color?: AgentColor;
  notChattable?: boolean;
  expanded?: boolean;
  initials?: string;
  name: string;
  onNewSession?: () => void;
  // 头部 ＋ 之后那一格：宿主自己的动作（会话索引的「导入本地会话…」⋮ 就挂在这里）。
  // 与 ＋ 并排，不替换它。
  headerActions?: React.ReactNode;
  // 头部 (avatar + 名称区) 被点击：父组件常用这个钩子直接打开「最近活动的会话」，
  // 把"选 agent → 选会话"压成一步。不影响 chevron / + 按钮（这俩 stopPropagation）。
  onHeaderClick?: () => void;
  onSessionSelect?: (sessionId: string, opts?: { newTab?: boolean }) => void;
  // 置顶切换:父组件提供时,头部常驻一个 pin 开关按钮。pinToggleLabel 由父组件按
  // 当前 pinned 态算好(Pin/Unpin {{name}}),同时用作 aria-label 与 title,
  // 让 agent-list 不必反向依赖 chat 的 i18n 命名空间。
  onTogglePin?: () => void;
  pinToggleLabel?: string;
  persistenceKey?: string;
  pinned?: boolean;
  selectedSessionId?: string;
  sessions?: AgentSession[];
  totalSessions?: number;
  // 折叠态下「冒泡」展示的 attention 行：父组件已按 rank 排序、过滤过非 attention。
  // expanded === false 时永远渲染；expanded === true 时隐藏，避免与下方 5 行列表重复。
  attentionSessions?: AgentSession[];
  // 渲染「查看全部 N 个会话」按钮关联的 popover content；
  // 由父组件提供，避免 agent-list 依赖 chat 业务（依赖反转）。
  renderSessionsPopover?: (close: () => void) => React.ReactNode;
  // 会话行右键菜单（可选）：任一 handler 提供才渲染 ContextMenu；
  // 不传时 SessionGroup / SessionRow 保持旧行为（项目页等）。
  onOpenInNewTab?: (sessionId: number) => void;
  onRenameSession?: (sessionId: number, title: string) => void;
  onDeleteSession?: (sessionId: number) => void;
};

function AgentGroup({
  activeCount = 0,
  blockReason = "",
  className,
  color = "agent-1",
  notChattable = false,
  expanded: expandedProp,
  initials,
  name,
  onHeaderClick,
  onNewSession,
  headerActions,
  onSessionSelect,
  onTogglePin,
  pinToggleLabel,
  persistenceKey,
  pinned,
  selectedSessionId,
  sessions = [],
  totalSessions,
  attentionSessions = [],
  renderSessionsPopover,
  onOpenInNewTab,
  onRenameSession,
  onDeleteSession,
  ...props
}: AgentGroupProps) {
  const { t } = useTranslation();
  const hasActiveSessions = activeCount > 0;
  // expandedProp 仅作为「无持久化时的初始默认值」使用：
  // 之前会在 mount 后根据 expandedProp → true 强制展开，但用户明确希望
  // 选中 agent 时**展开/收起状态保持不变**，所以那个 useEffect 移除。
  // 用户的展开偏好通过 chevron 触发 → 走 readSidebarExpanded 持久化。
  return (
    <SessionGroup
      className={className}
      persistenceKey={persistenceKey}
      defaultExpanded={expandedProp}
      sessions={sessions}
      selectedSessionId={selectedSessionId}
      onSessionSelect={onSessionSelect}
      totalSessions={totalSessions}
      renderSessionsPopover={renderSessionsPopover}
      attentionSessions={attentionSessions}
      attentionAriaLabel={t("agentList.attentionAria", { name })}
      onOpenInNewTab={onOpenInNewTab}
      onRenameSession={onRenameSession}
      onDeleteSession={onDeleteSession}
      {...props}
      renderHeader={({ expanded, toggle }) => (
        <div
          className={cn(
            "flex items-center gap-2 rounded-md px-2 py-1 transition-colors",
            onHeaderClick &&
              "cursor-pointer hover:bg-sidebar-active-bg focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none",
          )}
          role={onHeaderClick ? "button" : undefined}
          tabIndex={onHeaderClick ? 0 : undefined}
          aria-label={
            onHeaderClick ? t("agentList.openRecent", { name }) : undefined
          }
          onClick={onHeaderClick}
          onKeyDown={
            onHeaderClick
              ? (e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    onHeaderClick();
                  }
                }
              : undefined
          }
        >
          <AgentAvatar
            name={name}
            initials={initials}
            color={color}
            size="sm"
          />
          <span className="min-w-0 truncate text-sm font-semibold">{name}</span>
          {notChattable ? (
            <Badge
              variant="outline"
              className="border-status-waiting/40 bg-status-waiting-bg px-1.5 py-0 text-2xs text-foreground"
            >
              {blockReason === "no-backend"
                ? t("agentList.backendNotConfigured")
                : t("agentList.notConfigured")}
            </Badge>
          ) : null}
          {pinned ? (
            <Pin
              className="size-3 -rotate-[30deg] text-primary-text"
              aria-label={t("agentList.pinned")}
            />
          ) : null}
          <span className="min-w-0 flex-1" />
          {hasActiveSessions ? (
            <span
              className="flex shrink-0 items-center"
              title={t("agentList.running")}
              aria-label={t("agentList.runningAria", { name })}
            >
              <StatusDot
                status="running"
                size="xs"
                className="motion-safe:animate-pulse"
              />
            </span>
          ) : null}
          {onTogglePin ? (
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={pinToggleLabel}
              title={pinToggleLabel}
              className={cn(
                "text-muted-foreground",
                pinned && "text-primary-text",
              )}
              onClick={(e) => {
                e.stopPropagation();
                onTogglePin();
              }}
            >
              <Pin
                data-icon="only"
                aria-hidden="true"
                className="-rotate-[30deg]"
              />
            </Button>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t("agentList.newSession", { name })}
            title={t("agentList.newSession", { name })}
            className="text-muted-foreground"
            onClick={(e) => {
              e.stopPropagation();
              onNewSession?.();
            }}
          >
            <Plus data-icon="only" aria-hidden="true" />
          </Button>
          {headerActions}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-expanded={expanded}
            aria-label={
              expanded
                ? t("agentList.collapse", { name })
                : t("agentList.expand", { name })
            }
            title={
              expanded
                ? t("agentList.collapse", { name })
                : t("agentList.expand", { name })
            }
            className={cn(
              "text-muted-foreground transition-colors",
              expanded && "bg-sidebar-active-bg text-foreground",
            )}
            onClick={(e) => {
              e.stopPropagation();
              toggle();
            }}
          >
            <ChevronDown
              data-icon="only"
              aria-hidden="true"
              className={cn(
                "transition-transform duration-150 ease-out motion-reduce:transition-none",
                expanded && "rotate-180",
              )}
            />
          </Button>
        </div>
      )}
    />
  );
}

export { AgentGroup, AgentPanelSection, SessionRow };
export type { AgentSession };
