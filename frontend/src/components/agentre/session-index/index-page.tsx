// frontend/src/components/agentre/session-index/index-page.tsx
//
// 单一会话索引 —— 合并前的「对话」与「项目」两个页面
// （规格：docs/specs/2026-08-16-unified-chat-index.md）。
//
// 右栏一行不改：TabStrip + ChatPanelHost 本来就挂在 AppLayout 上、两条路由共用同一份
// 实例。本页只是左栏 + 它的数据通路。
import * as React from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { DndContext } from "@dnd-kit/core";
import {
  SortableContext,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import {
  ResizableSidebar,
  type ImportDialogPrefill,
} from "@agentre-hub/agentre-ui";

import { useChatAgents, type AgentSlim } from "@/hooks/use-chat-agents";
import { useProjectTree } from "@/hooks/use-project-tree";
import { NEW_CHAT_INITIAL_QUERY } from "@/components/agentre/shortcuts/registry";
import { useSidebarAxisStore } from "@/stores/sidebar-axis-store";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useCommandPaletteStore } from "@/stores/command-palette-store";
import { useNewChatContextStore } from "@/stores/new-chat-context-store";
import { requestNewAgentDialog } from "@/stores/new-agent-intent-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";

import { SessionsPopover } from "../sessions-popover";
import * as WailsApp from "../../../../wailsjs/go/app/App";
import type { app } from "../../../../wailsjs/go/models";

import { IndexDialogs } from "./index-dialogs";
import { IndexGroupRow, type IndexGroupHandlers } from "./index-group-row";
import { IndexToolbar } from "./index-toolbar";
import { useSessionImportPorts } from "./import-ports-desktop";
import type { ProjectGlyphInfo } from "./project-glyph";
import { useSessionActions } from "./session-actions";
import { useMachineRoster } from "./machine-roster";
import { useIndexDialogs } from "./use-index-dialogs";
import { useIndexFilter, useIndexSearch } from "./use-index-filter";
import { useIndexGroups, type IndexGroup } from "./use-index-groups";
import { useProjectReorder } from "./use-project-reorder";

function projectDragID(id: number): string {
  return `project-${id}`;
}

function collectProjects(
  nodes: app.ProjectTreeNode[],
  out: Map<number, app.ProjectItem> = new Map(),
): Map<number, app.ProjectItem> {
  for (const n of nodes) {
    if (n.project?.id) out.set(n.project.id, n.project);
    if (n.children?.length) collectProjects(n.children, out);
  }
  return out;
}

export function SessionIndexPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { tree, invalidate, loaded } = useProjectTree();
  const { agents } = useChatAgents();
  const openCommandPalette = useCommandPaletteStore((s) => s.openWith);
  const metas = useSessionMetaStore((s) => s.metas);

  // axis 住在 store 里而不是这里的 useState：⌘D 从 ShortcutsProvider 切轴，
  // 那里不在本组件树上。
  const axis = useSidebarAxisStore((s) => s.axis);
  const setAxis = useSidebarAxisStore((s) => s.setAxis);

  // 机器名单只在机器轴上拉 —— 别的轴不需要这份清单，不该为它多发一个 RPC。
  const machines = useMachineRoster(
    axis === "machine",
    t("sessionIndex.machine.local"),
  );
  // 搜索词在组装组之前就要有：它是取数 scope 的一部分，不是摆好之后再过一遍的
  // 前端过滤 —— 后者只看得见首屏那一页，正是「搜 happy 搜不出来」的根。
  const { query, setQuery, needle, keyword, searching } = useIndexSearch();
  const groups = useIndexGroups(axis, tree, machines, keyword);
  const machineByID = React.useMemo(
    () => new Map(machines.map((m) => [m.deviceId, m])),
    [machines],
  );
  const groupByKey = React.useMemo(
    () => new Map(groups.map((g) => [g.key, g])),
    [groups],
  );
  const projectByID = React.useMemo(() => collectProjects(tree), [tree]);
  const agentByID = React.useMemo(
    () => new Map(agents.map((a) => [a.id, a])),
    [agents],
  );

  /**
   * 项目 id → 该项目**连同后代**的会话 id。折叠父项目时的 attention 冒泡、组头计数、
   * 运行中高亮、以及筛选时「祖先要不要留下」都靠它。
   */
  const subtreeSessionIDs = React.useMemo(() => {
    const map = new Map<number, number[]>();
    const walk = (node: app.ProjectTreeNode): number[] => {
      const id = node.project?.id ?? 0;
      const own = groupByKey.get(`project:${id}`)?.sessionIDs ?? [];
      const kids = (node.children ?? []).flatMap(walk);
      const all = [...own, ...kids];
      if (id > 0) map.set(id, all);
      return all;
    };
    tree.forEach(walk);
    return map;
  }, [tree, groupByKey]);

  // ── 选中态 ────────────────────────────────────────────────────────────────
  const activeTab = useChatTabsStore((s) =>
    s.tabs.find((tab) => tab.id === s.activeTabId),
  );
  const selectedSessionID =
    activeTab?.meta.kind === "session" ? activeTab.meta.sessionId : 0;

  const openSession = useChatTabsStore((s) => s.openSession);
  const openSessionInNewTab = useChatTabsStore((s) => s.openSessionInNewTab);
  const openNewSession = useChatTabsStore((s) => s.openNewSession);
  const openTerminal = useChatTabsStore((s) => s.openTerminal);

  // ── 导入本地会话（规格 2026-08-26）─────────────────────────────────────────
  // null = 对话框没开。入口按自己那一维预填，所以状态本身就是那份预填。
  const [importPrefill, setImportPrefill] =
    React.useState<ImportDialogPrefill | null>(null);
  const importPorts = useSessionImportPorts(
    importPrefill !== null,
    openSession,
  );

  // ── 状态 chips ────────────────────────────────────────────────────────────
  const allSessionIDs = React.useMemo(
    () => [...new Set(groups.flatMap((g) => g.sessionIDs))],
    [groups],
  );
  const { statusFilter, setStatusFilter, unreadCount, visibleSessionIDs } =
    useIndexFilter({ sessionIDs: allSessionIDs });

  // ── 命令面板的「新会话上下文」桥接 ─────────────────────────────────────────
  //
  // 合并前这段挂在项目页上：selection 切到某项目时把 {projectID, projectName} 写进
  // new-chat-context-store，命令面板据此把成员/非成员分组并显示项目 chip；再注册一个
  // newSelectionHandler，让面板选中项目内成员时能开出带项目上下文的新会话。
  //
  // 合并后「当前在哪个项目」不再是页面自己的 selection，而是**当前标签页那条会话的
  // 项目归属** —— 这也是它一直想表达的意思，只是过去只有项目页知道。
  const currentProjectID = React.useMemo(() => {
    if (activeTab?.meta.kind === "new") return activeTab.meta.projectId;
    if (selectedSessionID > 0)
      return metas.get(selectedSessionID)?.projectId ?? 0;
    return 0;
  }, [activeTab, selectedSessionID, metas]);

  React.useEffect(() => {
    const store = useNewChatContextStore.getState();
    if (currentProjectID > 0) {
      store.setContext({
        projectID: currentProjectID,
        projectName: projectByID.get(currentProjectID)?.name ?? "",
      });
    } else {
      store.setContext(null);
    }
    // ownership check：只清自己写的那条，避免切走后又把别人写的覆盖掉。
    return () => {
      const cur = useNewChatContextStore.getState().projectContext;
      if (cur && cur.projectID === currentProjectID) {
        useNewChatContextStore.getState().setContext(null);
      }
    };
  }, [currentProjectID, projectByID]);

  React.useEffect(() => {
    const handler = (projectID: number, agent: AgentSlim) => {
      openNewSession(projectID, agent.id, "");
    };
    useNewChatContextStore.getState().setNewSelectionHandler(handler);
    return () => {
      const cur = useNewChatContextStore.getState().newSelectionHandler;
      if (cur === handler) {
        useNewChatContextStore.getState().setNewSelectionHandler(null);
      }
    };
  }, [openNewSession]);

  // ── 项目相关的对话框 ──────────────────────────────────────────────────────
  const dialogs = useIndexDialogs({ invalidate, projectByID, loaded });
  const {
    openCreateDialog,
    refreshProjectData,
    setDeleteTarget,
    setMergeSource,
    setNotChattableAgent,
    setSettingsFocus,
    setSettingsProjectID,
  } = dialogs;

  // ── 会话行的改名 / 删除 ───────────────────────────────────────────────────
  const sessionActions = useSessionActions();

  // ── 拖拽排序：只在「按项目」下开启，且筛选生效时禁用（决策 9） ─────────────
  const { dragDisabled, sensors, handleDragEnd, reorderError } =
    useProjectReorder({
      axis,
      // 搜索与状态 chip 都让顺序失去意义（决策 9）—— 前者现在是取数级的过滤，
      // 不再体现在 visibleSessionIDs 上，得单独报给拖拽。
      filtering: searching || visibleSessionIDs !== null,
      projectByID,
      refreshProjectData,
    });

  // ── 组的公共 handlers ─────────────────────────────────────────────────────
  const handlers = React.useMemo<IndexGroupHandlers>(
    () => ({
      onSessionSelect: (sid, opts) =>
        opts?.newTab ? openSessionInNewTab(sid) : openSession(sid),
      onOpenInNewTab: openSessionInNewTab,
      onRenameSession: sessionActions.requestRename,
      onDeleteSession: sessionActions.requestDelete,
      onNewSession: (projectID, agentID) => {
        // agentID 为 0 = 组头没填这一维（随手对话），交给命令面板挑 agent。
        if (agentID <= 0) {
          openCommandPalette(NEW_CHAT_INITIAL_QUERY);
          return;
        }
        openNewSession(projectID, agentID, "");
      },
      onOpenNotChattable: setNotChattableAgent,
      onTogglePin: async (agentID, pinned) => {
        // 字段名是 `id`（agent_svc.SetPinnedRequest）。`as never` 会把写错的键
        // 一路放行到运行时，所以这一行由页面级测试断言载荷本身。
        await WailsApp.SetAgentPinned({ id: agentID, pinned } as never);
        void useChatAgentsStore.getState().reload();
      },
      onOpenSettings: (projectID: number, focus?: "members" | "paths") => {
        setSettingsProjectID(projectID);
        setSettingsFocus(focus);
      },
      onAddSubProject: openCreateDialog,
      onOpenTerminal: (projectID, deviceID, deviceName) =>
        openTerminal(projectID, deviceID, deviceName || undefined),
      onImportLocalSession: setImportPrefill,
      onMergeInto: (projectID, name) => setMergeSource({ id: projectID, name }),
      onDeleteProject: (projectID, name) =>
        // 子项目数从树上数出来递进去：确认弹窗要逐条写清后果，「一并删掉几个」是
        // 宿主的数据，包不去猜。
        setDeleteTarget({
          id: projectID,
          name,
          childCount: countDescendants(tree, projectID),
        }),
      // 「查看全部 N」：组已经把一维填好了，翻页就沿着那一维继续拉。
      // Agent 组走既有的 ListChatAgentSessions，项目 / 随手对话走索引的同名 scope。
      renderSessionsPopover: (group, close) => (
        <SessionsPopover
          header={{
            name:
              group.kind === "agent"
                ? (agentByID.get(group.refID)?.name ?? "")
                : group.kind === "free"
                  ? t("sessionIndex.free.name")
                  : (projectByID.get(group.refID)?.name ?? ""),
            avatarColor:
              group.kind === "agent"
                ? agentByID.get(group.refID)?.avatarColor
                : undefined,
          }}
          loader={async ({ offset, limit }) => {
            // 不搜索时 agent 组走它自己那条列表接口；一开搜就改走索引查询的
            // agent scope —— 关键词只有那条路认得，弹层里翻出未过滤的下一页会让人
            // 以为搜索漏了。
            if (group.kind === "agent" && !keyword) {
              const resp = await WailsApp.ListChatAgentSessions({
                agentId: group.refID,
                offset,
                limit,
              } as never);
              return {
                sessions: (resp?.sessions ?? []).map((x) => ({
                  id: x.id,
                  title: x.title ?? "",
                  status: x.status ?? "idle",
                  lastMessageAt: x.lastMessageAt ?? 0,
                })),
                total: resp?.total ?? 0,
                hasMore: resp?.hasMore ?? false,
              };
            }
            // 机器组此前落进了 project 这一支，projectId 被填成 deviceID ——
            // 本机的 0 会被服务端当成漏传直接拒掉，那一组的「查看全部 N」点不开。
            const resp = await WailsApp.ListChatIndexSessions({
              scope:
                group.kind === "free"
                  ? "free"
                  : group.kind === "machine"
                    ? "machine"
                    : group.kind === "agent"
                      ? "agent"
                      : "project",
              projectId:
                group.kind === "project" || group.kind === "flat"
                  ? group.refID
                  : 0,
              deviceId: group.kind === "machine" ? group.refID : 0,
              agentId: group.kind === "agent" ? group.refID : 0,
              keyword,
              offset,
              limit,
            } as never);
            return {
              sessions: (resp?.sessions ?? []).map((x) => ({
                id: x.id,
                title: x.title ?? "",
                status: x.status ?? "idle",
                lastMessageAt: x.lastMessageAt ?? 0,
              })),
              total: resp?.total ?? 0,
              hasMore: resp?.hasMore ?? false,
            };
          }}
          onClose={close}
          onSelectSession={(sid, opts) =>
            opts?.newTab ? openSessionInNewTab(sid) : openSession(sid)
          }
        />
      ),
    }),
    [
      openSession,
      openSessionInNewTab,
      openNewSession,
      openTerminal,
      openCommandPalette,
      openCreateDialog,
      sessionActions.requestRename,
      sessionActions.requestDelete,
      setDeleteTarget,
      setMergeSource,
      setNotChattableAgent,
      setSettingsFocus,
      setSettingsProjectID,
      agentByID,
      projectByID,
      tree,
      keyword,
      t,
    ],
  );

  // 画一枚项目字形要的三件事一起取：组头与行里的字形从此只有一个来源
  // （project-glyph.tsx），不会再各画各的。
  const projectInfoOf = React.useCallback(
    (projectID: number): ProjectGlyphInfo | null => {
      const p = projectByID.get(projectID);
      if (!p) return null;
      return { name: p.name, color: p.color, icon: p.icon };
    },
    [projectByID],
  );
  const agentInfoOf = React.useCallback(
    (agentID: number) => {
      const a = agentByID.get(agentID);
      return { name: a?.name ?? "", color: a?.avatarColor || "" };
    },
    [agentByID],
  );

  const allLocalPathsMissing = React.useMemo(() => {
    const all = [...projectByID.values()];
    return all.length > 0 && all.every((p) => p.localPathMissing);
  }, [projectByID]);

  /** 筛选生效时这个项目还该不该出现（含后代命中与项目名自身命中）。 */
  const projectVisible = React.useCallback(
    (projectID: number): boolean => {
      if (!searching && !visibleSessionIDs) return true;
      const ids = subtreeSessionIDs.get(projectID) ?? [];
      // 搜索时列表本身已是过滤后的，所以「子树里还剩下行」就等于命中；状态 chip 是
      // 前端派生态，得逐条比。
      const hit = visibleSessionIDs
        ? ids.some((sid) => visibleSessionIDs.has(sid))
        : ids.length > 0;
      if (hit) return true;
      // 一条会话都没有、但名字命中的空项目也要留下 —— 否则搜自己刚建的项目搜不到。
      return (
        needle.length > 0 &&
        (projectByID.get(projectID)?.name ?? "").toLowerCase().includes(needle)
      );
    },
    [searching, visibleSessionIDs, subtreeSessionIDs, needle, projectByID],
  );

  const renderGroup = React.useCallback(
    (group: IndexGroup, children?: React.ReactNode, drag?: SortableDrag) => (
      <IndexGroupRow
        key={group.key}
        group={group}
        axis={axis}
        selectedSessionID={selectedSessionID}
        visibleSessionIDs={visibleSessionIDs}
        subtreeSessionIDs={
          group.kind === "project"
            ? (subtreeSessionIDs.get(group.refID) ?? group.sessionIDs)
            : group.sessionIDs
        }
        project={
          group.kind === "project" ? projectByID.get(group.refID) : undefined
        }
        machine={
          group.kind === "machine" ? machineByID.get(group.refID) : undefined
        }
        agent={group.kind === "agent" ? agentByID.get(group.refID) : undefined}
        allLocalPathsMissing={allLocalPathsMissing}
        dragListeners={drag?.listeners}
        projectInfoOf={projectInfoOf}
        agentInfoOf={agentInfoOf}
        handlers={handlers}
      >
        {children}
      </IndexGroupRow>
    ),
    [
      axis,
      selectedSessionID,
      visibleSessionIDs,
      projectByID,
      machineByID,
      agentByID,
      allLocalPathsMissing,
      projectInfoOf,
      agentInfoOf,
      subtreeSessionIDs,
      handlers,
    ],
  );

  const projectLevelProps = React.useMemo(
    () => ({ groupByKey, dragDisabled, renderGroup, projectVisible }),
    [groupByKey, dragDisabled, renderGroup, projectVisible],
  );

  const list = React.useMemo(() => {
    if (axis === "project") {
      const free = groupByKey.get("free");
      return (
        <DndContext sensors={sensors} onDragEnd={handleDragEnd}>
          <ProjectLevel nodes={tree} {...projectLevelProps} />
          {free ? renderGroup(free) : null}
        </DndContext>
      );
    }
    return groups.map((group) => renderGroup(group));
  }, [
    axis,
    groups,
    groupByKey,
    tree,
    sensors,
    handleDragEnd,
    projectLevelProps,
    renderGroup,
  ]);

  // 空态：三个轴各自「一条都渲染不出来」时给一句话，而不是一片空白。
  // 项目轴永远有「随手对话」组，所以只有它自己空、且没有项目时才算空。
  const isEmpty =
    axis === "agent"
      ? groups.length === 0
      : axis === "time"
        ? groups[0]?.sessionIDs.length === 0
        : tree.length === 0 &&
          (groupByKey.get("free")?.sessionIDs.length ?? 0) === 0;

  return (
    <>
      <IndexDialogs
        dialogs={dialogs}
        sessionActions={sessionActions}
        tree={tree}
        projectByID={projectByID}
        importPrefill={importPrefill}
        setImportPrefill={setImportPrefill}
        importPorts={importPorts}
        openSession={openSession}
      />

      <ResizableSidebar
        persistenceKey="chat"
        ariaLabel={t("sessionIndex.sidebar")}
      >
        <IndexToolbar
          query={query}
          setQuery={setQuery}
          axis={axis}
          setAxis={setAxis}
          statusFilter={statusFilter}
          setStatusFilter={setStatusFilter}
          unreadCount={unreadCount}
          onOpenCommandPalette={() =>
            openCommandPalette(NEW_CHAT_INITIAL_QUERY)
          }
          onCreateProject={() => openCreateDialog(0)}
          onNewAgent={() => {
            requestNewAgentDialog();
            navigate("/org");
          }}
        />

        {axis === "project" && allLocalPathsMissing ? (
          <div
            data-testid="all-local-paths-missing"
            className="border-b border-border bg-status-waiting-bg px-4 py-2"
          >
            <p className="text-2xs font-medium text-foreground">
              {t("projects.localPath.allMissingTitle")}
            </p>
            <p className="mt-0.5 text-2xs text-muted-foreground">
              {t("projects.localPath.allMissingDescription", {
                count: projectByID.size,
              })}
            </p>
          </div>
        ) : null}

        {reorderError ? (
          <p
            data-testid="reorder-error"
            role="alert"
            className="border-b border-border bg-destructive-soft px-4 py-2 text-2xs text-status-error"
          >
            {reorderError}
          </p>
        ) : null}

        <div className="min-h-0 flex-1 overflow-auto px-2 py-3">
          {isEmpty ? (
            <p className="px-2 py-6 text-center text-xs text-muted-foreground">
              {searching || visibleSessionIDs
                ? t("sessionIndex.empty.noMatch")
                : t("sessionIndex.empty.nothing")}
            </p>
          ) : (
            list
          )}
        </div>
      </ResizableSidebar>
    </>
  );
}

type SortableDrag = { listeners?: Record<string, unknown> };

type ProjectLevelProps = {
  nodes: app.ProjectTreeNode[];
  groupByKey: Map<string, IndexGroup>;
  dragDisabled: boolean;
  renderGroup: (
    group: IndexGroup,
    children?: React.ReactNode,
    drag?: SortableDrag,
  ) => React.ReactNode;
  projectVisible: (projectID: number) => boolean;
};

/**
 * 项目轴按树递归渲染，而不是把 depth 摊平成缩进：折叠父项目要真的把子项目一起收起来，
 * 同级拖拽也要按层建各自的 SortableContext。
 *
 * 写成组件而不是自递归的 useCallback —— 后者在依赖数组里引用自己，读起来像闭包陷阱，
 * lint 也会拦（「accessed before it is declared」）。
 */
function ProjectLevel({
  nodes,
  groupByKey,
  dragDisabled,
  renderGroup,
  projectVisible,
}: ProjectLevelProps): React.ReactNode {
  if (nodes.length === 0) return null;
  return (
    <SortableContext
      items={nodes.map((n) => projectDragID(n.project?.id ?? 0))}
      strategy={verticalListSortingStrategy}
    >
      {nodes.map((node) => {
        const id = node.project?.id ?? 0;
        const group = groupByKey.get(`project:${id}`);
        if (!group) return null;
        // 筛选生效时，子树里一条都没命中的项目整个不渲染 —— 否则满屏是空组头。
        // 子树包含后代，所以命中项的祖先自然留下（这正是要的：路径要看得见）。
        if (!projectVisible(id)) return null;
        // 先算出这一层真正会渲染出来的子项目：一个都没有时 children 传 undefined，
        // 而不是一个只会返回 null 的 <ProjectLevel>。IndexGroupRow 靠这个区分
        // 「有子项目、会话要下沉进子分组」和「没有子项目、别多出一行组头」。
        const kids = (node.children ?? []).filter((n) =>
          projectVisible(n.project?.id ?? 0),
        );
        return (
          <SortableProjectGroup
            key={group.key}
            projectID={id}
            disabled={dragDisabled}
            render={(drag) =>
              renderGroup(
                group,
                kids.length > 0 ? (
                  <ProjectLevel
                    nodes={kids}
                    groupByKey={groupByKey}
                    dragDisabled={dragDisabled}
                    renderGroup={renderGroup}
                    projectVisible={projectVisible}
                  />
                ) : undefined,
                drag,
              )
            }
          />
        );
      })}
    </SortableContext>
  );
}

function SortableProjectGroup({
  projectID,
  disabled,
  render,
}: {
  projectID: number;
  disabled: boolean;
  render: (drag?: SortableDrag) => React.ReactNode;
}) {
  const { listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: projectDragID(projectID), disabled });
  return (
    <div
      ref={setNodeRef}
      style={{
        // 与合并前项目页逐字相同：@dnd-kit/utilities 不是本仓库的依赖，手写 transform。
        transform: transform
          ? `translate3d(${transform.x}px, ${transform.y}px, 0) scaleX(${transform.scaleX}) scaleY(${transform.scaleY})`
          : undefined,
        transition,
        opacity: isDragging ? 0.6 : undefined,
      }}
    >
      {render(disabled ? undefined : { listeners })}
    </div>
  );
}

/**
 * 这棵子树下还有几个项目（不含它自己）。
 *
 * 数的是**整棵子树**而不是直接子项目：删除是递归的，只说直接子项目会漏报。
 */
function countDescendants(
  nodes: app.ProjectTreeNode[],
  projectID: number,
): number {
  const sizeOf = (node: app.ProjectTreeNode): number =>
    (node.children ?? []).reduce((n, c) => n + 1 + sizeOf(c), 0);
  const find = (list: app.ProjectTreeNode[]): number | null => {
    for (const node of list) {
      if (node.project?.id === projectID) return sizeOf(node);
      const hit = find(node.children ?? []);
      if (hit !== null) return hit;
    }
    return null;
  };
  return find(nodes) ?? 0;
}
