import * as React from "react";
import { ArrowRight, Inbox } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { isOpenInNewTabModifier } from "../lib/keyboard";
import { cn } from "../lib/utils";
import { Popover, PopoverTrigger } from "../ui/popover";

import { readSidebarExpanded, writeSidebarExpanded } from "./expanded-state";
import { SessionRow } from "./session-row";
import type { SessionRowLinkRenderer } from "./session-row";
import type { SessionRowModel } from "./types";

// SessionGroup —— 通用的「会话分组」侧栏容器：
// 头部由调用方通过 renderHeader 完全自定义（agent / project / 状态 / 其它都可以塞自己的
// chrome），body 部分负责标准能力：折叠态可见的 attention bubble、grid 平滑展开动画、
// 常规会话列表（自动跟 attention 去重）、「查看全部 N」溢出 Popover、空态、子节点插槽。
//
// **分组本身不在这里**：桌面端按项目 / Agent / 时间三轴（用户选），agentre-server 按
// Agent / 状态（视口决定）。两边都对，塞进包就得二选一 —— 所以包收已经建好的组，
// `buildGroups` 留在各自宿主。
type SessionGroupProps = React.ComponentProps<"article"> & {
  // 头部 slot：完全由调用方控制 chrome（avatar / 名称 / 各种 badge / chevron 位置），
  // SessionGroup 把 expanded / toggle 回传给它使用。
  renderHeader: (state: {
    expanded: boolean;
    toggle: () => void;
  }) => React.ReactNode;

  // 展开 / 持久化
  persistenceKey?: string; // localStorage key (e.g. "agent:7" / "project:7")；不传 = 不落盘
  defaultExpanded?: boolean; // 未持久化时的初始默认值

  // 主列表
  sessions?: SessionRowModel[];
  selectedSessionId?: string;
  onSessionSelect?: (id: string, opts?: { newTab?: boolean }) => void;
  // 带 href 的行用宿主的路由链接渲染（见 SessionRow 的导航接缝）。整组一份，
  // 因为「用什么元素跳」是宿主外壳的性质，不是某一行的性质。
  renderLink?: SessionRowLinkRenderer;

  // 溢出 Popover：超过常规列表上限时显示「查看全部 N」入口，content 由调用方提供。
  // 只在打开时渲染 content —— 见下方渲染处的注释（常驻会把那一页钉死）。
  totalSessions?: number;
  renderSessionsPopover?: (
    close: () => void,
    trigger: HTMLButtonElement | null,
  ) => React.ReactNode;

  // Attention 气泡（折叠态也始终可见；展开态过滤掉 unread/selected 这类「软」rank）
  attentionSessions?: SessionRowModel[];
  // 折叠态专用 attention 气泡。项目树用它把后代项目的 attention 汇总到
  // 折叠父级；不传时保持旧行为。
  collapsedAttentionSessions?: SessionRowModel[];

  // 子节点插槽：在 sessions 列表之后渲染（仍在 grid 展开动画容器内），
  // 主要给项目树的子项目递归用。
  renderAfterSessions?: React.ReactNode;

  // 会话行右键菜单（可选）：任一 handler 提供才在 SessionRow 上渲染对应菜单项。
  // 项目页（ProjectCard）不传 → 保持旧行为（无右键菜单）。
  //
  // ID 是**原始字符串身份**：desktop 在自己的 adapter 里转本地数字主键，server
  // 直接用复合键。包内不再做身份强转（规格 2026-09-16 决策 3）。
  onOpenInNewTab?: (sessionId: string) => void;
  onRenameSession?: (sessionId: string, title: string) => void;
  onDeleteSession?: (sessionId: string) => void;
  // 逐行限制 Delete 能力；仅与 onDeleteSession 组合生效。不传时所有行保持可删除。
  canDeleteSession?: (sessionId: string) => boolean;

  // Pending 内容槽：宿主还在取这个组的会话时用它顶位（共享行骨架，或骨架+
  // 重试动作）。有它时它**替代**组内空态——“暂无会话”在数据在路上时是假话。
  // aria-busy / 连接状态 / 重试动作仍由宿主决定。
  pending?: React.ReactNode;

  // 空态可定制（无会话时显示），传 null 关闭空态渲染。默认「暂无会话」。
  emptyLabel?: React.ReactNode;

  // 头部下方 aria-label，用于读屏说明该 attention 区块归属。
  attentionAriaLabel?: string;
};

function SessionGroup({
  className,
  renderHeader,
  persistenceKey,
  defaultExpanded,
  sessions = [],
  selectedSessionId,
  onSessionSelect,
  renderLink,
  totalSessions,
  renderSessionsPopover,
  attentionSessions = [],
  collapsedAttentionSessions,
  renderAfterSessions,
  pending,
  emptyLabel,
  onOpenInNewTab,
  onRenameSession,
  onDeleteSession,
  canDeleteSession,
  attentionAriaLabel,
  ...props
}: SessionGroupProps) {
  const { t } = useUiTranslation();
  const resolvedEmptyLabel = emptyLabel ?? t("sessionGroup.empty");
  const [popoverOpen, setPopoverOpen] = React.useState(false);
  const [overflowTrigger, setOverflowTrigger] =
    React.useState<HTMLButtonElement | null>(null);
  const [expanded, setExpanded] = React.useState(
    () => readSidebarExpanded(persistenceKey ?? "") ?? defaultExpanded ?? false,
  );
  const handleToggle = React.useCallback(() => {
    setExpanded((value: boolean) => {
      const next = !value;
      if (persistenceKey) writeSidebarExpanded(persistenceKey, next);
      return next;
    });
  }, [persistenceKey]);

  const deleteHandlerFor = (sessionId: string) => {
    const deleteSession = onDeleteSession;
    if (!deleteSession || (canDeleteSession && !canDeleteSession(sessionId))) {
      return undefined;
    }
    return () => deleteSession(sessionId);
  };

  // 展开态下仅把 selected 从 bubble 过滤掉（让它回到常规列表它本来的时间序位置）。
  // unread 保留在 bubble 里 —— 这样按住 ⌘ 时未读会话也能拿到 ⌘N chip,
  // 与对话页折叠态体验对齐（项目页默认展开,过去这条路径下未读永远拿不到 chip）。
  // 下方常规列表通过 attentionIds 自动去重,unread 不会重复出现。
  // 折叠态下 bubble 是侧栏唯一可见入口,所有 rank 都保留。
  const visibleAttention = React.useMemo(() => {
    const base =
      !expanded && collapsedAttentionSessions
        ? collapsedAttentionSessions
        : attentionSessions;
    return expanded ? base.filter((s) => s.attentionRank !== "selected") : base;
  }, [attentionSessions, collapsedAttentionSessions, expanded]);
  // 下方常规列表对已在 bubble 出现的 sessionId 去重，避免视觉重复。
  const attentionIds = React.useMemo(
    () => new Set(visibleAttention.map((s) => s.id)),
    [visibleAttention],
  );
  const dedupedSessions = React.useMemo(
    () => sessions.filter((s) => !attentionIds.has(s.id)),
    [sessions, attentionIds],
  );

  const hasAfter =
    renderAfterSessions !== undefined && renderAfterSessions !== null;
  // 只把真有内容的节点当作 pending：宿主常见的写法是 `pending={loading && <…/>}`，
  // 取数结束时那是 `false` —— 它必须回到空态，而不是把空态一起吞掉却什么都不画。
  const hasPending = Boolean(pending);
  // isEmpty 仅决定空态文案是否渲染。有 totalSessions（「查看全部」）、renderAfterSessions
  // （子节点插槽）或 pending 内容（骨架顶位）时不算空 —— 避免项目树空 session 但有
  // 子项目时显示「暂无会话」，也避免数据还在路上时先说一句“没有”。
  const isEmpty =
    sessions.length === 0 &&
    !totalSessions &&
    !hasAfter &&
    !hasPending &&
    emptyLabel !== null;

  return (
    <article
      className={cn("flex w-full flex-col gap-0.5", className)}
      {...props}
    >
      {renderHeader({ expanded, toggle: handleToggle })}

      {/* attention bubble：始终可见（running / error / 审批 / unread 常驻）。
          折叠态下还额外保留 selected（让当前打开的会话钉在末尾可见）；
          展开态下仅剔除 selected —— 选中态回到下方常规列表它本来的时间序位置。
          下方常规列表对已经出现在 bubble 中的 sessionId 做去重，避免视觉重复。 */}
      {visibleAttention.length > 0 ? (
        <div
          data-slot="agent-attention-bubble"
          className="flex flex-col gap-px border-l-2 border-status-waiting/40 pl-1.5"
          aria-label={attentionAriaLabel}
        >
          {visibleAttention.map((session) => (
            <SessionRow
              key={`attn-${session.id}`}
              {...session}
              sessionId={session.id}
              renderLink={renderLink}
              selected={
                selectedSessionId
                  ? session.id === selectedSessionId
                  : session.selected
              }
              onClick={
                onSessionSelect
                  ? (e) =>
                      onSessionSelect(session.id, {
                        newTab: isOpenInNewTabModifier(e),
                      })
                  : undefined
              }
              onOpenInNewTab={
                onOpenInNewTab ? () => onOpenInNewTab(session.id) : undefined
              }
              onRenameSession={
                onRenameSession
                  ? () => onRenameSession(session.id, session.title)
                  : undefined
              }
              onDeleteSession={deleteHandlerFor(session.id)}
            />
          ))}
        </div>
      ) : null}

      <div
        data-slot="agent-group-content"
        aria-hidden={!expanded}
        className="grid transition-[grid-template-rows] duration-150 ease-out motion-reduce:transition-none"
        style={{ gridTemplateRows: expanded ? "1fr" : "0fr" }}
      >
        <div className="min-h-0 overflow-hidden">
          <div className="flex flex-col gap-0.5">
            {dedupedSessions.length > 0 ? (
              <div className="flex flex-col gap-px">
                {dedupedSessions.map((session) => (
                  <SessionRow
                    key={session.id}
                    {...session}
                    sessionId={session.id}
                    renderLink={renderLink}
                    aria-hidden={!expanded}
                    disabled={!expanded}
                    selected={
                      selectedSessionId
                        ? session.id === selectedSessionId
                        : session.selected
                    }
                    onClick={
                      onSessionSelect && expanded
                        ? (e) =>
                            onSessionSelect(session.id, {
                              newTab: isOpenInNewTabModifier(e),
                            })
                        : undefined
                    }
                    onOpenInNewTab={
                      onOpenInNewTab
                        ? () => onOpenInNewTab(session.id)
                        : undefined
                    }
                    onRenameSession={
                      onRenameSession
                        ? () => onRenameSession(session.id, session.title)
                        : undefined
                    }
                    onDeleteSession={deleteHandlerFor(session.id)}
                  />
                ))}
              </div>
            ) : null}

            {totalSessions ? (
              <Popover open={popoverOpen} onOpenChange={setPopoverOpen}>
                <PopoverTrigger asChild>
                  <button
                    ref={setOverflowTrigger}
                    type="button"
                    disabled={!expanded}
                    tabIndex={expanded ? undefined : -1}
                    className="flex cursor-pointer items-center gap-1 px-2 py-1.5 text-left text-2xs font-medium text-primary-text outline-none transition-colors hover:text-primary focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-default"
                  >
                    {t("sessionGroup.viewAll", { count: totalSessions })}
                    <ArrowRight className="size-3" aria-hidden="true" />
                  </button>
                </PopoverTrigger>
                {/* 内容只在打开时渲染：它挂载时才去拉那一页（桌面端的
                    SessionsPopover 在 mount effect 里 fetch）。常驻渲染的话，
                    这一页会在**组渲染的那一刻**拉一次并从此不再更新，之后每次
                    点开看到的都是那份旧快照（计数也一起旧）。 */}
                {popoverOpen && renderSessionsPopover
                  ? renderSessionsPopover(
                      () => setPopoverOpen(false),
                      overflowTrigger,
                    )
                  : null}
              </Popover>
            ) : null}

            {hasAfter ? renderAfterSessions : null}

            {hasPending ? pending : null}

            {isEmpty ? (
              <div
                aria-hidden={!expanded}
                className="flex items-center gap-1.5 px-2 py-2 text-2xs text-muted-foreground"
              >
                <Inbox
                  className="size-3 text-decorative-foreground"
                  aria-hidden="true"
                />
                <span>{resolvedEmptyLabel}</span>
              </div>
            ) : null}
          </div>
        </div>
      </div>
    </article>
  );
}

export { SessionGroup };
export type { SessionGroupProps };
