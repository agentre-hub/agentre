// frontend/src/components/agentre/session-index/index-group-row.tsx
//
// 索引里的**一个组**。四种组头（项目 / Agent / 随手对话 / 时间轴的无头平铺）共用同一个
// SessionGroup 容器 —— 折叠动画、attention 气泡、「查看全部 N」溢出、与气泡去重、空态
// 都在包里，这里只负责挑组头、投影行、接线。
import * as React from "react";
import { useTranslation } from "react-i18next";
import { SessionGroup } from "@agentre-ai/agentre-ui";

import type { IndexAxis } from "@/lib/session-axis";
import { useSessionMetaStore } from "@/stores/session-meta-store";

import { AgentGroup } from "../agent-list";
import type { AgentSession } from "../agent-list";
import type { ChatAgentItem } from "@/hooks/use-chat-agents";
import type { app } from "../../../../wailsjs/go/models";

import { FreeGroupHeader } from "./free-group-header";
import { OwnSessionsHeader } from "./own-sessions-header";
import { ProjectGroupHeader } from "./project-group-header";
import { RowLeadingSlot } from "./row-leading-slot";
import type { IndexGroup } from "./use-index-groups";
import { useGroupRows } from "./use-group-rows";

/** 稳定的空列表：会话下沉进内层组时外层传它，别每次渲染新建一个数组。 */
const NO_SESSIONS: AgentSession[] = [];

export type IndexGroupHandlers = {
  onSessionSelect: (sessionID: number, opts?: { newTab?: boolean }) => void;
  onOpenInNewTab: (sessionID: number) => void;
  onRenameSession: (sessionID: number, title: string) => void;
  onDeleteSession: (sessionID: number) => void;
  /** 组头 `＋`：分组已经填好了一维，直接开这一维下的新会话。 */
  onNewSession: (projectID: number, agentID: number) => void;
  onOpenNotChattable: (agent: ChatAgentItem) => void;
  onTogglePin: (agentID: number, pinned: boolean) => void;
  onOpenSettings: (projectID: number) => void;
  onAddSubProject: (parentID: number) => void;
  onOpenTerminal: (
    projectID: number,
    deviceID: string,
    deviceName: string,
  ) => void;
  onSpecifyPath: (projectID: number) => void;
  onMergeInto: (projectID: number, name: string) => void;
  onDeleteProject: (projectID: number, name: string) => void;
  renderSessionsPopover?: (
    group: IndexGroup,
    close: () => void,
  ) => React.ReactNode;
};

export type IndexGroupRowProps = {
  group: IndexGroup;
  axis: IndexAxis;
  selectedSessionID: number;
  /** 命中筛选 / 搜索的会话 id；null = 当前没有筛选，全放行。 */
  visibleSessionIDs: ReadonlySet<number> | null;
  /**
   * 本组**连同后代**的会话 id（仅项目轴有意义，其余传 group.sessionIDs 即可）。
   * 折叠父项目时它是唯一的出口：气泡要冒后代的 running/审批/未读，组头的计数也要
   * 含后代，否则「折起来就看不见了」。
   */
  subtreeSessionIDs: readonly number[];
  project?: app.ProjectItem;
  agent?: ChatAgentItem;
  hasRunning: boolean;
  allLocalPathsMissing: boolean;
  dragListeners?: Record<string, unknown>;
  projectColorOf: (projectID: number) => string | null;
  projectNameOf: (projectID: number) => string;
  /**
   * agent id → 名称 / 颜色。**不能只读 meta.agentName** —— 索引 RPC 的载荷里没有
   * 这两个字段（只有 agentId），只有 ListChatAgents 那条路会顺带写。凡是落在某个
   * agent 前 5 条窗口之外的会话，读 meta 会拿到空名字 + 默认色，
   * 正好把决策 4 的行首字形与决策 5 的第二行打空。
   */
  agentInfoOf: (agentID: number) => { name: string; color: string };
  handlers: IndexGroupHandlers;
  /** 子项目递归插槽（仅项目轴）。 */
  children?: React.ReactNode;
};

export function IndexGroupRow({
  group,
  axis,
  selectedSessionID,
  visibleSessionIDs,
  subtreeSessionIDs,
  project,
  agent,
  hasRunning,
  allLocalPathsMissing,
  dragListeners,
  projectColorOf,
  projectNameOf,
  agentInfoOf,
  handlers,
  children,
}: IndexGroupRowProps) {
  const { t } = useTranslation();
  const metas = useSessionMetaStore((s) => s.metas);

  const sessionIDs = React.useMemo(() => {
    if (!visibleSessionIDs) return group.sessionIDs;
    return group.sessionIDs.filter((id) => visibleSessionIDs.has(id));
  }, [group.sessionIDs, visibleSessionIDs]);

  const { sessions, attentionSessions } = useGroupRows(
    sessionIDs,
    selectedSessionID,
    t,
  );
  // 折叠态专用气泡：整棵子树的 attention。展开时包会改用上面那份（只有自己的），
  // 避免与下方递归渲染出来的子项目行重复。
  const subtreeIDs = React.useMemo(() => {
    if (!visibleSessionIDs) return subtreeSessionIDs;
    return subtreeSessionIDs.filter((id) => visibleSessionIDs.has(id));
  }, [subtreeSessionIDs, visibleSessionIDs]);
  const subtree = useGroupRows(subtreeIDs, selectedSessionID, t);

  // 行首那一维（决策 4）与时间轴的第二行（决策 5）。两者都只是「分组没说的那一维」，
  // 所以在同一处按 axis 决定，避免两套投影再次分叉。
  const decorate = React.useCallback(
    (row: AgentSession): AgentSession => {
      const meta = metas.get(Number(row.id));
      if (!meta) return row;
      const projectID = meta.projectId ?? 0;
      const resolved = agentInfoOf(meta.agentId ?? 0);
      const agentName = resolved.name || (meta.agentName ?? "");
      const agentColor = resolved.color || meta.agentColor || "agent-1";
      if (axis === "time") {
        const projectName = projectID > 0 ? projectNameOf(projectID) : "";
        return {
          ...row,
          secondaryLabel: [agentName, projectName].filter(Boolean).join(" · "),
        };
      }
      return {
        ...row,
        leading: (
          <RowLeadingSlot
            axis={axis}
            agentName={agentName}
            agentColor={agentColor}
            projectColor={projectID > 0 ? projectColorOf(projectID) : null}
          />
        ),
      };
    },
    [axis, metas, projectColorOf, projectNameOf, agentInfoOf],
  );

  const decoratedSessions = React.useMemo(
    () => sessions.map(decorate),
    [sessions, decorate],
  );
  const decoratedAttention = React.useMemo(
    () => attentionSessions.map(decorate),
    [attentionSessions, decorate],
  );

  const collapsedAttention = React.useMemo(
    () => subtree.attentionSessions.map(decorate),
    [subtree.attentionSessions, decorate],
  );
  // 计数含后代 —— 折叠的父项目也要显示子项目里有几条在等你。
  const attentionCount = subtree.attentionSessions.filter(
    (s) => s.attentionRank !== "selected",
  ).length;
  // 「查看全部 N」只在真的还有没加载的会话时出现；筛选生效时不出现 —— 翻页拉回来的
  // 是未过滤的下一页，混进过滤后的列表只会让人以为筛选漏了。
  const overflow =
    !visibleSessionIDs && group.total > group.sessionIDs.length
      ? group.total
      : undefined;

  const rowHandlers = {
    onSessionSelect: (id: string, opts?: { newTab?: boolean }) =>
      handlers.onSessionSelect(Number(id), opts),
    onOpenInNewTab: handlers.onOpenInNewTab,
    onRenameSession: handlers.onRenameSession,
    onDeleteSession: handlers.onDeleteSession,
  };

  const selectedSessionIDStr =
    selectedSessionID > 0 ? String(selectedSessionID) : undefined;

  const sessionsPopover = handlers.renderSessionsPopover
    ? (close: () => void) => handlers.renderSessionsPopover!(group, close)
    : undefined;

  if (group.kind === "agent") {
    if (!agent) return null;
    return (
      <AgentGroup
        persistenceKey={group.key}
        name={agent.name}
        color={(agent.avatarColor || "agent-1") as never}
        activeCount={attentionCount}
        notChattable={!agent.chattable}
        blockReason={agent.blockReason}
        pinned={agent.pinned}
        pinToggleLabel={t(agent.pinned ? "agentList.unpin" : "agentList.pin", {
          name: agent.name,
        })}
        onTogglePin={() => handlers.onTogglePin(agent.id, !agent.pinned)}
        // 组头点击 = 打开最近活动的那条会话，把「选 agent → 选会话」压成一步。
        // 没有会话时退化成开新会话；不可聊则弹原因（与 ＋ 同一分支）。
        onHeaderClick={() => {
          if (!agent.chattable) {
            handlers.onOpenNotChattable(agent);
            return;
          }
          const recent = sessionIDs[0];
          if (recent) handlers.onSessionSelect(recent);
          else handlers.onNewSession(0, agent.id);
        }}
        onNewSession={() => {
          if (!agent.chattable) {
            handlers.onOpenNotChattable(agent);
            return;
          }
          handlers.onNewSession(0, agent.id);
        }}
        selectedSessionId={selectedSessionIDStr}
        sessions={decoratedSessions}
        attentionSessions={decoratedAttention}
        totalSessions={overflow}
        renderSessionsPopover={sessionsPopover}
        {...rowHandlers}
      />
    );
  }

  const renderHeader = ({
    expanded,
    toggle,
  }: {
    expanded: boolean;
    toggle: () => void;
  }): React.ReactNode => {
    if (group.kind === "free") {
      return (
        <FreeGroupHeader
          expanded={expanded}
          onToggle={toggle}
          count={sessions.length}
          attentionCount={attentionCount}
          // 随手对话的 `＋` 不带项目上下文：projectID 传 0，agent 由命令面板挑。
          onNewSession={() => handlers.onNewSession(0, 0)}
        />
      );
    }
    if (group.kind === "project" && project) {
      return (
        <ProjectGroupHeader
          project={project}
          depth={group.depth}
          expanded={expanded}
          onToggle={toggle}
          attentionCount={attentionCount}
          hasRunning={hasRunning}
          allLocalPathsMissing={allLocalPathsMissing}
          dragListeners={dragListeners}
          onOpenSettings={handlers.onOpenSettings}
          onAddSubProject={handlers.onAddSubProject}
          onNewSession={handlers.onNewSession}
          onOpenTerminal={handlers.onOpenTerminal}
          onSpecifyPath={handlers.onSpecifyPath}
          onMergeInto={handlers.onMergeInto}
          onDelete={handlers.onDeleteProject}
        />
      );
    }
    // 时间轴：无组头（决策 5）。两维已经落在行的第二行里。
    return null;
  };

  // 父项目同时有自己的会话和子项目时，把自己的会话下沉进一个可独立折叠的内层组
  // （`project:<id>:sessions`）。共用父级那一个箭头会把两者绑死 —— 会话多的父项目
  // 会把子项目整个挤出视野，而那正是这个子分组存在的理由。
  const nestOwnSessions =
    group.kind === "project" &&
    children != null &&
    decoratedSessions.length > 0;

  return (
    <SessionGroup
      persistenceKey={group.kind === "flat" ? undefined : group.key}
      // 项目 / 随手对话 / 时间轴默认展开 —— 合并前项目页就是展开的，改成折叠等于
      // 首次启动看到一棵全关的文件夹树。只有 Agent 组保持折叠（对话页的旧行为）。
      defaultExpanded
      renderHeader={renderHeader}
      selectedSessionId={selectedSessionIDStr}
      sessions={nestOwnSessions ? NO_SESSIONS : decoratedSessions}
      attentionSessions={nestOwnSessions ? NO_SESSIONS : decoratedAttention}
      // 折叠态气泡留在**外层**：把父项目整卡收起来时，要冒的是整棵子树的
      // attention，内层那个组头这时候根本不在屏幕上。
      collapsedAttentionSessions={collapsedAttention}
      totalSessions={nestOwnSessions ? undefined : overflow}
      renderSessionsPopover={nestOwnSessions ? undefined : sessionsPopover}
      renderAfterSessions={
        nestOwnSessions ? (
          <>
            <SessionGroup
              persistenceKey={`${group.key}:sessions`}
              defaultExpanded
              renderHeader={({ expanded, toggle }) => (
                <OwnSessionsHeader
                  name={project?.name ?? ""}
                  count={sessions.length}
                  expanded={expanded}
                  onToggle={toggle}
                />
              )}
              selectedSessionId={selectedSessionIDStr}
              sessions={decoratedSessions}
              attentionSessions={decoratedAttention}
              totalSessions={overflow}
              renderSessionsPopover={sessionsPopover}
              {...rowHandlers}
            />
            {children}
          </>
        ) : (
          children
        )
      }
      emptyLabel={
        group.kind === "free" ? t("sessionIndex.free.empty") : undefined
      }
      {...rowHandlers}
    />
  );
}
