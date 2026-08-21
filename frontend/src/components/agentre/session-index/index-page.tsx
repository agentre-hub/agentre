// frontend/src/components/agentre/session-index/index-page.tsx
//
// 单一会话索引 —— 合并前的「对话」与「项目」两个页面
// （规格：docs/specs/2026-08-16-unified-chat-index.md）。
//
// 右栏一行不改：TabStrip + ChatPanelHost 本来就挂在 AppLayout 上、两条路由共用同一份
// 实例。本页只是左栏 + 它的数据通路。
import * as React from "react";
import { Plus, Search, UserPlus, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { AxisPicker } from "@agentre-ai/agentre-ui";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { useChatAgents, type ChatAgentItem } from "@/hooks/use-chat-agents";
import { useProjectTree } from "@/hooks/use-project-tree";
import { NEW_CHAT_INITIAL_QUERY } from "@/components/agentre/shortcuts/registry";
import { INDEX_AXES, type IndexAxis } from "@/lib/session-axis";
import { useSidebarAxisStore } from "@/stores/sidebar-axis-store";
import { cn } from "@/lib/utils";
import { useSessionAttentionList } from "@/stores/attention-store";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useCommandPaletteStore } from "@/stores/command-palette-store";
import { useNewChatContextStore } from "@/stores/new-chat-context-store";
import { requestNewAgentDialog } from "@/stores/new-agent-intent-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { reloadSidebarSources } from "@/stores/sidebar-reload";

import { DeleteProjectDialog } from "../delete-project-dialog";
import type { DeleteProjectTarget } from "../delete-project-dialog";
import { NotChattableDialog } from "../not-chattable/not-chattable-dialog";
import { ProjectMergeDialog } from "../project-merge-dialog";
import type { ProjectMergeSource } from "../project-merge-dialog";
import { ProjectNewDialog } from "../project-new-dialog";
import { ProjectSettingsDrawer } from "../project-settings-drawer";
import { ResizableSidebar } from "../resizable-sidebar";
import { SessionsPopover } from "../sessions-popover";
import * as WailsApp from "../../../../wailsjs/go/app/App";
import type { app } from "../../../../wailsjs/go/models";

import { IndexGroupRow, type IndexGroupHandlers } from "./index-group-row";
import type { ProjectGlyphInfo } from "./project-glyph";
import { SessionActionDialogs, useSessionActions } from "./session-actions";
import { useMachineRoster } from "./machine-roster";
import { useIndexGroups, type IndexGroup } from "./use-index-groups";

/** 筛选 chips：单选。null = 全部（决策 8）。 */
type StatusFilter = "running" | "unread" | null;

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
  const groups = useIndexGroups(axis, tree, machines);
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

  // ── 搜索 / 筛选 ───────────────────────────────────────────────────────────
  const [query, setQuery] = React.useState("");
  const [statusFilter, setStatusFilter] = React.useState<StatusFilter>(null);
  const needle = query.trim().toLowerCase();

  const allSessionIDs = React.useMemo(
    () => [...new Set(groups.flatMap((g) => g.sessionIDs))],
    [groups],
  );
  const attentionItems = useSessionAttentionList(allSessionIDs);
  const reasonBySession = React.useMemo(
    () => new Map(attentionItems.map((i) => [i.sessionId, i.reason])),
    [attentionItems],
  );
  const unreadCount = React.useMemo(
    () => attentionItems.filter((i) => i.reason === "unread").length,
    [attentionItems],
  );

  /**
   * 命中集合。`null` = 没有任何筛选，整棵列表原样渲染。
   *
   * 搜索同时匹配**会话标题 / 项目名 / agent 名**（决策 8）。一条会话被留下的条件是
   * 「它自己命中」**或**「它所属的组命中」—— 搜 agent 名时该 agent 名下的会话应该
   * 全留（你在找那个 agent），搜某句标题时才收窄到那几条。
   */
  const visibleSessionIDs = React.useMemo<ReadonlySet<number> | null>(() => {
    if (!needle && statusFilter === null) return null;
    const hit = new Set<number>();
    for (const sid of allSessionIDs) {
      const meta = metas.get(sid);
      if (!meta) continue;
      if (statusFilter !== null && reasonBySession.get(sid) !== statusFilter) {
        continue;
      }
      if (!needle) {
        hit.add(sid);
        continue;
      }
      const projectName =
        meta.projectId && meta.projectId > 0
          ? (projectByID.get(meta.projectId)?.name ?? "")
          : "";
      const haystack = [meta.title ?? "", meta.agentName ?? "", projectName]
        .join("\n")
        .toLowerCase();
      if (haystack.includes(needle)) hit.add(sid);
    }
    return hit;
  }, [
    needle,
    statusFilter,
    allSessionIDs,
    metas,
    reasonBySession,
    projectByID,
  ]);

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
    const handler = (projectID: number, agent: ChatAgentItem) => {
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
  const [newDialogOpen, setNewDialogOpen] = React.useState(false);
  const [newDialogParent, setNewDialogParent] = React.useState(0);
  const [settingsProjectID, setSettingsProjectID] = React.useState(0);
  const [deleteTarget, setDeleteTarget] =
    React.useState<DeleteProjectTarget | null>(null);
  const [mergeSource, setMergeSource] =
    React.useState<ProjectMergeSource | null>(null);
  const [notChattableAgent, setNotChattableAgent] =
    React.useState<ChatAgentItem | null>(null);
  const [reorderError, setReorderError] = React.useState<string | null>(null);

  const refreshProjectData = React.useCallback(() => {
    invalidate();
    reloadSidebarSources();
  }, [invalidate]);

  const openCreateDialog = React.useCallback((parentID: number) => {
    setNewDialogParent(parentID);
    setNewDialogOpen(true);
  }, []);

  // `/chat?focus=<id>`：会话设置页点「项目」进来，打开该项目的设置抽屉，然后清掉
  // query 防重复。命中失败静默丢弃 —— 项目可能已经被删了。
  const [searchParams, setSearchParams] = useSearchParams();
  React.useEffect(() => {
    const raw = searchParams.get("focus");
    // 树还没到位就消费这条 query = 把深链吞掉：清 query 是无条件的，而项目要等
    // ProjectListTree 回来才认得出，冷缓存下抽屉就永远不开了。
    if (!raw || !loaded) return;
    const id = Number(raw);
    if (id > 0 && projectByID.has(id)) setSettingsProjectID(id);
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("focus");
        return next;
      },
      { replace: true },
    );
  }, [searchParams, setSearchParams, projectByID, loaded]);

  // ── 会话行的改名 / 删除 ───────────────────────────────────────────────────
  const sessionActions = useSessionActions();

  // ── 拖拽排序：只在「按项目」下开启，且筛选生效时禁用（决策 9） ─────────────
  const dragDisabled = axis !== "project" || visibleSessionIDs !== null;
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );
  const handleDragEnd = React.useCallback(
    async (event: DragEndEvent) => {
      if (dragDisabled) return;
      const activeID = Number(String(event.active.id).replace("project-", ""));
      const overID = Number(
        String(event.over?.id ?? "").replace("project-", ""),
      );
      if (!activeID || !overID || activeID === overID) return;
      const parentOf = (id: number) => projectByID.get(id)?.parentID ?? 0;
      if (parentOf(activeID) !== parentOf(overID)) return; // 跨父不允许
      const siblings = [...projectByID.values()]
        .filter((p) => (p.parentID ?? 0) === parentOf(activeID))
        .map((p) => p.id);
      const from = siblings.indexOf(activeID);
      const to = siblings.indexOf(overID);
      if (from < 0 || to < 0) return;
      siblings.splice(to, 0, ...siblings.splice(from, 1));
      // 顺序来自后端树，本地没有乐观重排可回滚 —— 但失败必须说出来，否则用户看到的
      // 是「拖了一下，松手弹回去，什么都没发生」。
      setReorderError(null);
      try {
        await WailsApp.ProjectReorder({
          parentID: parentOf(activeID),
          orderedIDs: siblings,
        } as never);
      } catch (e) {
        setReorderError(
          t("projects.errors.reorderFailed", { error: String(e) }),
        );
        return;
      }
      refreshProjectData();
    },
    [dragDisabled, projectByID, refreshProjectData, t],
  );

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
      onOpenSettings: setSettingsProjectID,
      onAddSubProject: openCreateDialog,
      onOpenTerminal: (projectID, deviceID, deviceName) =>
        openTerminal(projectID, deviceID, deviceName || undefined),
      onSpecifyPath: async (projectID) => {
        const path = await WailsApp.SelectDirectory(
          t("projectNew.selectDirectory"),
        );
        if (!path) return;
        await WailsApp.ProjectSetLocalPath({ id: projectID, path } as never);
        refreshProjectData();
      },
      onMergeInto: (projectID, name) => setMergeSource({ id: projectID, name }),
      onDeleteProject: (projectID, name) =>
        setDeleteTarget({ id: projectID, name }),
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
            if (group.kind === "agent") {
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
            const resp = await WailsApp.ListChatIndexSessions({
              scope: group.kind === "free" ? "free" : "project",
              projectId: group.kind === "free" ? 0 : group.refID,
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
      refreshProjectData,
      sessionActions.requestRename,
      sessionActions.requestDelete,
      agentByID,
      projectByID,
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
      if (!visibleSessionIDs) return true;
      const ids = subtreeSessionIDs.get(projectID) ?? [];
      if (ids.some((sid) => visibleSessionIDs.has(sid))) return true;
      // 一条会话都没有、但名字命中的空项目也要留下 —— 否则搜自己刚建的项目搜不到。
      return (
        needle.length > 0 &&
        (projectByID.get(projectID)?.name ?? "").toLowerCase().includes(needle)
      );
    },
    [visibleSessionIDs, subtreeSessionIDs, needle, projectByID],
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
      <SessionActionDialogs actions={sessionActions} />
      {notChattableAgent ? (
        <NotChattableDialog
          agent={notChattableAgent}
          open
          onOpenChange={(open) => {
            if (!open) setNotChattableAgent(null);
          }}
        />
      ) : null}
      <ProjectNewDialog
        open={newDialogOpen}
        onOpenChange={setNewDialogOpen}
        tree={tree}
        initialParentID={newDialogParent}
        onCreated={refreshProjectData}
      />
      <ProjectSettingsDrawer
        projectID={settingsProjectID}
        onClose={() => setSettingsProjectID(0)}
        onChanged={refreshProjectData}
        onDeleted={() => {
          setSettingsProjectID(0);
          refreshProjectData();
        }}
      />
      <DeleteProjectDialog
        target={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onDeleted={refreshProjectData}
      />
      <ProjectMergeDialog
        source={mergeSource}
        candidates={
          mergeSource
            ? [...projectByID.values()]
                .filter((p) => p.id !== mergeSource.id)
                .sort((a, b) => a.name.localeCompare(b.name))
            : []
        }
        onClose={() => setMergeSource(null)}
        onMerged={refreshProjectData}
      />

      <ResizableSidebar
        persistenceKey="chat"
        ariaLabel={t("sessionIndex.sidebar")}
      >
        <div className="border-b border-border px-4 py-3">
          <div className="flex items-center gap-2">
            <div className="relative min-w-0 flex-1">
              <Search
                className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
                aria-hidden="true"
              />
              <Input
                aria-label={t("sessionIndex.search.aria")}
                placeholder={t("sessionIndex.search.placeholder")}
                className="h-[30px] bg-background pl-8 pr-7 text-xs"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
              {query ? (
                <button
                  type="button"
                  aria-label={t("sessionIndex.search.clear")}
                  title={t("sessionIndex.search.clear")}
                  className="absolute right-1.5 top-1/2 inline-flex size-5 -translate-y-1/2 cursor-pointer items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                  onClick={() => setQuery("")}
                >
                  <X className="size-3" aria-hidden="true" />
                </button>
              ) : null}
            </div>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  type="button"
                  data-testid="new-chat-button"
                  variant="secondary"
                  size="icon-sm"
                  aria-label={t("sessionIndex.add.aria")}
                  title={t("sessionIndex.add.aria")}
                  className="size-[30px] bg-primary-soft text-primary-text hover:bg-primary-soft/80"
                >
                  <Plus data-icon="only" aria-hidden="true" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {/* 副标题不是装饰：这一项开的是命令面板，而「在面板里按 Tab 能换
                    项目」是这条路唯一能开出「不挂项目」的会话的办法（决策 6 的第二
                    条路），不写出来没人会去按。 */}
                <DropdownMenuItem
                  data-testid="new-agent-chat-item"
                  onSelect={() => openCommandPalette(NEW_CHAT_INITIAL_QUERY)}
                >
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <span>{t("sessionIndex.add.newChat")}</span>
                    <span className="text-2xs text-muted-foreground">
                      {t("sessionIndex.add.newChatHint")}
                    </span>
                  </div>
                </DropdownMenuItem>
                {/* 决策 11：「新建项目」降为次级项 —— ＋ 只有一种含义（开一条对话）。 */}
                <DropdownMenuItem
                  data-testid="project-create-trigger"
                  onSelect={() => openCreateDialog(0)}
                >
                  {t("sessionIndex.add.newProject")}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  data-testid="new-agent-item"
                  onSelect={() => {
                    requestNewAgentDialog();
                    navigate("/org");
                  }}
                >
                  <UserPlus className="size-4" aria-hidden="true" />
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <span>{t("sessionIndex.add.newAgent")}</span>
                    <span className="text-2xs text-muted-foreground">
                      {t("sessionIndex.add.newAgentHint")}
                    </span>
                  </div>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
          <div className="mt-2 flex items-center gap-1.5">
            <AxisPicker
              value={axis}
              axes={INDEX_AXES}
              // 包的词汇表比桌面端 offer 的多一档（machine）。选择器只摆得出
              // `axes` 里那几档，所以回来的一定是其中之一 —— 这里把它收回宿主
              // 的窄类型，而不是把窄类型放宽到包的全集。
              onChange={(next) => setAxis(next as IndexAxis)}
            />
            <Chip
              testID="filter-chip-all"
              active={statusFilter === null}
              onClick={() => setStatusFilter(null)}
            >
              {t("sessionIndex.filter.all")}
            </Chip>
            <Chip
              testID="filter-chip-running"
              active={statusFilter === "running"}
              onClick={() =>
                setStatusFilter((p) => (p === "running" ? null : "running"))
              }
            >
              {t("sessionIndex.filter.running")}
            </Chip>
            <Chip
              testID="filter-chip-unread"
              active={statusFilter === "unread"}
              onClick={() =>
                setStatusFilter((p) => (p === "unread" ? null : "unread"))
              }
            >
              {t("sessionIndex.filter.unread")}
              {unreadCount > 0 ? (
                <span className="rounded-full bg-status-waiting-bg px-1 font-medium text-status-waiting">
                  {unreadCount}
                </span>
              ) : null}
            </Chip>
          </div>
        </div>

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
              {visibleSessionIDs
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

function Chip({
  testID,
  active,
  onClick,
  children,
}: {
  testID: string;
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      data-testid={testID}
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "inline-flex cursor-pointer items-center gap-1 rounded-full px-2.5 py-1 text-2xs outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50",
        active
          ? "bg-primary-soft font-medium text-primary-text"
          : "bg-sidebar-active-bg text-muted-foreground hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}
