// frontend/src/components/agentre/__tests__/session-index-page.test.tsx
//
// 单一会话索引的**页面级**测试（规格 docs/specs/2026-08-16-unified-chat-index.md）。
// 合并自已删除的 chat-page.test.tsx + project-page.test.tsx，两份里对同一行为的重复
// 断言在这里只剩一份。
//
// **这里不重复测的东西**（各自有更窄的家，别再搬回来）：
//   - 行投影（标题退化 / 尾标 / rank 键）      → index-row-model.test.ts
//   - 组装配、agent 排序、按需拉取的 scope      → use-index-groups.test.tsx
//   - 组内行、attention 气泡排序、选中锚点      → use-group-rows.test.tsx
//   - 项目组头六项菜单 / 右键四项 / 运行中高亮  → project-group-header.test.tsx
//   - AxisPicker / FreeGroupHeader / 行首槽位   → session-index-chrome.test.tsx
//   - 气泡去重 / 折叠动画 / 行右键菜单渲染      → packages/agentre-ui/src/session-index/*
//
// 留在这里的是**只有整页装起来才成立**的事：三个轴换出来的列表结构、搜索与 chips 的
// 组合、拖拽只在项目轴开、对话框接线、`?focus=` 深链、命令面板上下文桥接，以及三条
// 规格明写的守卫（无轮询 / 只剩一处 computeAttention / 320px 下 chips 不换行）。
import { readFileSync, readdirSync } from "node:fs";
import { join, relative, resolve } from "node:path";

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type * as React from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  DeleteChatSession: vi.fn(),
  ListChatAgents: vi.fn(),
  ListChatIndexSessions: vi.fn(),
  ProjectCreate: vi.fn(),
  ProjectDelete: vi.fn(),
  ProjectDetectGitRepo: vi.fn(),
  ProjectGet: vi.fn(),
  ProjectListTree: vi.fn(),
  ProjectLocationList: vi.fn(),
  ProjectMerge: vi.fn(),
  ProjectReorder: vi.fn(),
  ProjectSetLocalPath: vi.fn(),
  ProjectUpdate: vi.fn(),
  RemoteDeviceList: vi.fn(),
  RenameChatSession: vi.fn(),
  SelectDirectory: vi.fn(),
  ServerListDevices: vi.fn(),
  SetAgentPinned: vi.fn(),
}));

// 组件（经 use-remote-devices）间接 import wails runtime：per-file mock，
// importActual 后只覆盖用到的两个事件 API，别加全局 alias。
vi.mock("../../../../wailsjs/runtime/runtime", async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  EventsOn: vi.fn(() => () => {}),
  EventsOff: vi.fn(),
}));
vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

// dnd-kit 的指针拖拽在 happy-dom 里没有布局可言。沿用合并前项目页的做法：把
// DndContext 换成「记住 onDragEnd 的透明容器」，于是既能直接驱动落点回调，
// 又能用「回调有没有被登记」判断这一档到底挂没挂拖拽上下文。
const dndMocks = vi.hoisted(() => ({
  onDragEnd: null as null | ((event: unknown) => void),
}));

vi.mock("@dnd-kit/core", () => ({
  DndContext: ({
    children,
    onDragEnd,
  }: {
    children: React.ReactNode;
    onDragEnd: (event: unknown) => void;
  }) => {
    dndMocks.onDragEnd = onDragEnd;
    return children;
  },
  KeyboardSensor: vi.fn(),
  PointerSensor: vi.fn(),
  useSensor: vi.fn(() => ({})),
  useSensors: vi.fn(() => []),
}));
vi.mock("@dnd-kit/sortable", () => ({
  SortableContext: ({ children }: { children: React.ReactNode }) => children,
  sortableKeyboardCoordinates: vi.fn(),
  useSortable: vi.fn(() => ({
    attributes: {},
    isDragging: false,
    listeners: {},
    setActivatorNodeRef: vi.fn(),
    setNodeRef: vi.fn(),
    transform: null,
    transition: undefined,
  })),
  verticalListSortingStrategy: {},
}));

import { __resetProjectTreeForTesting } from "@/hooks/use-project-tree";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useCommandPaletteStore } from "@/stores/command-palette-store";
import { consumeNewAgentDialogIntent } from "@/stores/new-agent-intent-store";
import { useNewChatContextStore } from "@/stores/new-chat-context-store";
import { useSessionIndexStore } from "@/stores/session-index-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionReadStore } from "@/stores/session-read-store";
import { useSessionStatusStore } from "@/stores/session-status-store";
import { useSidebarAxisStore } from "@/stores/sidebar-axis-store";
import { reloadSidebarSources } from "@/stores/sidebar-reload";

import { SessionIndexPage } from "../session-index/index-page";
import type { app } from "../../../../wailsjs/go/models";

// ── 种子与渲染 ──────────────────────────────────────────────────────────────

type SeedSession = {
  id: number;
  title: string;
  agentId?: number;
  projectId?: number;
  status?: string;
  needsAttention?: boolean;
  bgRunning?: boolean;
  lastMessageAt?: number;
  lastReadAt?: number;
};

type SeedAgent = {
  id: number;
  name: string;
  avatarColor?: string;
  chattable?: boolean;
  blockReason?: string;
  pinned?: boolean;
  sessions?: SeedSession[];
  attentionSessions?: SeedSession[];
};

/**
 * ListChatIndexSessions 的假后端：一份会话表，按 scope 切三种视图 ——
 * 与真实实现同一口径（recent = 全部按活动倒序，free = project_id 0，
 * project = 该项目），这样「哪个轴看得到哪条」是被真的分出来的，而不是喂给测试的。
 */
function seedIndexSessions(sessions: SeedSession[]) {
  const byActivity = [...sessions].sort(
    (a, b) => (b.lastMessageAt ?? 0) - (a.lastMessageAt ?? 0),
  );
  appMocks.ListChatIndexSessions.mockImplementation(
    async (req: { scope: string; projectId: number }) => {
      const rows =
        req.scope === "recent"
          ? byActivity
          : req.scope === "free"
            ? byActivity.filter((s) => (s.projectId ?? 0) === 0)
            : byActivity.filter((s) => (s.projectId ?? 0) === req.projectId);
      return {
        sessions: rows.map((s) => ({
          bgRunning: false,
          lastMessageAt: 0,
          lastReadAt: 0,
          needsAttention: false,
          projectId: 0,
          status: "idle",
          ...s,
        })),
        total: rows.length,
        hasMore: false,
      };
    },
  );
}

function toChatSessionLite(s: SeedSession) {
  return {
    lastMessageAt: 0,
    lastReadAt: 0,
    needsAttention: false,
    projectId: 0,
    status: "idle",
    ...s,
  };
}

function seedAgents(agents: SeedAgent[]) {
  appMocks.ListChatAgents.mockResolvedValue({
    agents: agents.map((a) => ({
      activeCount: 0,
      avatarColor: "agent-1",
      backendType: "builtin",
      blockReason: "",
      chattable: true,
      pinned: false,
      recentCount: a.sessions?.length ?? 0,
      totalSessions: a.sessions?.length ?? 0,
      ...a,
      sessions: (a.sessions ?? []).map(toChatSessionLite),
      attentionSessions: (a.attentionSessions ?? []).map(toChatSessionLite),
    })),
  });
}

function projectNode(
  project: { id: number; name: string } & Partial<app.ProjectItem>,
  children: app.ProjectTreeNode[] = [],
): app.ProjectTreeNode {
  return {
    project: {
      color: "agent-1",
      icon: "folder",
      localPathMissing: false,
      parentID: 0,
      path: `/tmp/${project.name}`,
      ...project,
    },
    children,
  } as unknown as app.ProjectTreeNode;
}

function seedTree(nodes: app.ProjectTreeNode[]) {
  appMocks.ProjectListTree.mockResolvedValue(nodes);
}

/** 组默认折叠（项目组尤其）；行要可见就得先把展开偏好写进 localStorage。 */
function expandGroups(...keys: string[]) {
  for (const key of keys) {
    localStorage.setItem(`agentre.agentExpanded.${key}`, "1");
  }
}

function selectSessionTab(sessionId: number) {
  const tab = {
    id: `seed-${sessionId}`,
    meta: { kind: "session" as const, sessionId },
    isPreview: false,
    isPinned: false,
    pinAt: 0,
    openedAt: 0,
  };
  useChatTabsStore.setState({ tabs: [tab], activeTabId: tab.id });
}

function LocationProbe() {
  return <output data-testid="location">{useLocation().pathname}</output>;
}

function renderIndex(path = "/chat") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <LocationProbe />
      <SessionIndexPage />
    </MemoryRouter>,
  );
}

// Radix 菜单在 happy-dom 中需要关闭 pointerEvents 检查。
function setupUser() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

/** 会话行的可访问名里混着状态与尾标，用标题子串定位就够稳。 */
function sessionRow(title: string) {
  return screen.getByRole("button", { name: new RegExp(title) });
}

function querySessionRow(title: string) {
  return screen.queryByRole("button", { name: new RegExp(title) });
}

function resetAll() {
  vi.clearAllMocks();
  dndMocks.onDragEnd = null;
  localStorage.clear();
  __resetProjectTreeForTesting();
  // axis 是模块级 store，清 localStorage 并不会把它拨回默认 —— 不重置的话上一条
  // 用例点过的轴会一路漏给后面所有用例。
  useSidebarAxisStore.getState().__reset();
  useChatAgentsStore.getState().__reset();
  useSessionIndexStore.getState().__reset();
  useSessionMetaStore.getState().__reset();
  useSessionStatusStore.getState().__reset();
  useSessionReadStore.setState({ overrides: new Map() });
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
  useCommandPaletteStore.setState({ open: false, initialQuery: "" });
  useNewChatContextStore.getState().clear();
  consumeNewAgentDialogIntent();

  seedTree([]);
  seedAgents([]);
  seedIndexSessions([]);
  appMocks.ProjectGet.mockResolvedValue({
    project: null,
    directMembers: [],
    inheritedMembers: [],
  });
  appMocks.ProjectLocationList.mockResolvedValue([]);
  appMocks.RemoteDeviceList.mockResolvedValue([]);
  appMocks.ServerListDevices.mockResolvedValue([]);
  appMocks.ProjectReorder.mockResolvedValue(undefined);
  appMocks.SetAgentPinned.mockResolvedValue({ id: 0, pinned: false });
  appMocks.RenameChatSession.mockResolvedValue({});
  appMocks.DeleteChatSession.mockResolvedValue({});
}

beforeEach(resetAll);
afterEach(() => {
  localStorage.clear();
});

// ── 三个轴 ──────────────────────────────────────────────────────────────────

describe("SessionIndexPage grouping axes", () => {
  const tree = [
    projectNode({ id: 1, name: "Agentre" }, [
      projectNode({ id: 2, name: "backend", parentID: 1 }),
    ]),
  ];
  const sessions: SeedSession[] = [
    {
      id: 11,
      title: "Root work",
      agentId: 7,
      projectId: 1,
      lastMessageAt: 3000,
      lastReadAt: 3000,
    },
    {
      id: 22,
      title: "Backend work",
      agentId: 7,
      projectId: 2,
      lastMessageAt: 2000,
      lastReadAt: 2000,
    },
    {
      id: 33,
      title: "Quick question",
      agentId: 8,
      projectId: 0,
      lastMessageAt: 1000,
      lastReadAt: 1000,
    },
  ];

  beforeEach(() => {
    seedTree(tree);
    seedIndexSessions(sessions);
    seedAgents([
      { id: 7, name: "Eng", sessions: sessions.slice(0, 2) },
      { id: 8, name: "Designer", sessions: sessions.slice(2) },
    ]);
  });

  it("Given the project axis, When the index mounts, Then the tree becomes the groups, each project keeps its own sessions and 随手对话 sits last", async () => {
    expandGroups("project:1", "project:2", "free");
    renderIndex();

    expect(await screen.findByText("Agentre")).toBeInTheDocument();
    await screen.findByRole("button", { name: /Root work/ });

    // 组头顺序 = 深度优先的项目树，末尾常驻「随手对话」（决策 6）。
    const headers = screen
      .getAllByText(/^(Agentre|backend|Quick chats)$/)
      .map((el) => el.textContent);
    expect(headers).toEqual(["Agentre", "backend", "Quick chats"]);

    // 每条会话只落在自己项目的组里 —— 自由会话不混进项目组。
    expect(sessionRow("Backend work")).toBeInTheDocument();
    expect(sessionRow("Quick question")).toBeInTheDocument();
  });

  it("Given the project axis, When the picker switches to Agent, Then the list regroups by agent and the project chrome disappears", async () => {
    const user = setupUser();
    renderIndex();
    await screen.findByText("Agentre");

    await user.click(screen.getByTestId("axis-picker"));
    await user.click(await screen.findByTestId("axis-option-agent"));

    expect(await screen.findByText("Eng")).toBeInTheDocument();
    expect(screen.getByText("Designer")).toBeInTheDocument();
    expect(screen.queryByText("Agentre")).toBeNull();
    expect(screen.queryByTestId("free-group-header")).toBeNull();
  });

  it("Given an axis was chosen, When the index is mounted again, Then it comes back on that axis instead of the default", async () => {
    const user = setupUser();
    const first = renderIndex();
    await screen.findByText("Agentre");

    await user.click(screen.getByTestId("axis-picker"));
    await user.click(await screen.findByTestId("axis-option-agent"));
    await screen.findByText("Eng");
    expect(localStorage.getItem("agentre.sidebarAxis")).toBe("agent");

    first.unmount();
    renderIndex();

    expect(await screen.findByTestId("axis-picker")).toHaveAttribute(
      "data-axis",
      "agent",
    );
    expect(screen.queryByText("Agentre")).toBeNull();
  });

  it("Given the time axis, When the index renders, Then it is one flat list with no group header and each row carries both dimensions", async () => {
    useSidebarAxisStore.getState().setAxis("time");
    renderIndex();

    const row = await screen.findByRole("button", { name: /Root work/ });

    // 没有组头可点，两维只能落在行的第二行（决策 5）。
    expect(screen.queryByTestId("project-group-header")).toBeNull();
    expect(screen.queryByTestId("free-group-header")).toBeNull();
    expect(row).toHaveTextContent("Eng");
    expect(row).toHaveTextContent("Agentre");
    // 自由会话报的是「随手对话」——它有去处，只是那个去处不是项目（决策 7）。
    expect(sessionRow("Quick question")).toHaveTextContent("Designer");
    expect(sessionRow("Quick question")).toHaveTextContent("Quick chats");
  });
});

// ── 项目树的呈现（页面把 depth / 全局路径缺失态喂给组头） ─────────────────────

describe("SessionIndexPage project tree rendering", () => {
  it("Given nested projects, When they render, Then depth reaches the header: the root stays sans and the sub-project is the mono section label", async () => {
    seedTree([
      projectNode({ id: 1, name: "Agentre" }, [
        projectNode({ id: 2, name: "backend", parentID: 1 }),
      ]),
    ]);
    renderIndex();

    const root = await screen.findByText("Agentre");
    const sub = screen.getByText("backend");

    expect(root.className).not.toMatch(/font-mono/);
    expect(root.className).not.toMatch(/uppercase/);
    expect(sub.className).toMatch(/font-mono/);
    expect(sub.className).toMatch(/uppercase/);
  });

  it("Given only some projects miss a local path, When the tree renders, Then just that row is badged and no name is greyed", async () => {
    seedTree([
      projectNode({ id: 1, name: "agentre" }),
      projectNode({
        id: 2,
        name: "agentre-hub",
        path: "",
        localPathMissing: true,
      }),
    ]);
    renderIndex();

    const missing = await screen.findByText("agentre-hub");
    expect(
      await screen.findAllByTestId("project-local-path-missing-badge"),
    ).toHaveLength(1);
    expect(missing.className).not.toContain("text-muted-foreground");
    expect(screen.getByText("agentre").className).not.toContain(
      "text-muted-foreground",
    );
  });

  it("Given every project misses a local path, When the tree renders, Then per-row badges give way to greyed names", async () => {
    seedTree([
      projectNode({ id: 1, name: "agentre", path: "", localPathMissing: true }),
      projectNode({
        id: 2,
        name: "agentre-hub",
        path: "",
        localPathMissing: true,
      }),
    ]);
    renderIndex();

    await screen.findByText("agentre-hub");
    expect(screen.queryByTestId("project-local-path-missing-badge")).toBeNull();
    expect(screen.getByText("agentre").className).toContain(
      "text-muted-foreground",
    );
    expect(screen.getByText("agentre-hub").className).toContain(
      "text-muted-foreground",
    );
  });

  it("Given a persisted sidebar width, When the index mounts, Then it reads the single merged key (decision 8)", async () => {
    localStorage.setItem("agentre.sidebarWidth.chat", "420");
    const { container } = renderIndex();

    const sidebar = await screen.findByRole("complementary", {
      name: "Session index",
    });
    expect(sidebar).toHaveStyle({ width: "420px" });
    // 侧栏就是 outlet 的根，右栏才能紧挨着它。
    expect(sidebar.parentElement).toBe(container);
  });
});

// ── 搜索与筛选 chips ────────────────────────────────────────────────────────

describe("SessionIndexPage search and filter chips", () => {
  const sessions: SeedSession[] = [
    {
      id: 4,
      title: "Running one",
      agentId: 7,
      projectId: 1,
      status: "running",
      lastMessageAt: 3000,
      lastReadAt: 3000,
    },
    {
      id: 5,
      title: "Background done",
      agentId: 7,
      projectId: 1,
      lastMessageAt: 2000,
      lastReadAt: 0,
    },
    {
      id: 6,
      title: "Visual pass",
      agentId: 8,
      projectId: 1,
      lastMessageAt: 1000,
      lastReadAt: 1000,
    },
  ];

  beforeEach(() => {
    expandGroups("project:1", "free");
    seedTree([projectNode({ id: 1, name: "Agentre" })]);
    seedIndexSessions(sessions);
    seedAgents([
      { id: 7, name: "Eng", sessions: sessions.slice(0, 2) },
      { id: 8, name: "Designer", sessions: sessions.slice(2) },
    ]);
  });

  it("Given a query matching one session title, When it is typed, Then only that row survives and clearing brings the rest back", async () => {
    const user = setupUser();
    renderIndex();
    await screen.findByRole("button", { name: /Visual pass/ });

    await user.type(
      screen.getByLabelText("Search sessions, projects or agents"),
      "Visual",
    );

    expect(sessionRow("Visual pass")).toBeInTheDocument();
    expect(querySessionRow("Running one")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Clear search" }));
    expect(
      await screen.findByRole("button", { name: /Running one/ }),
    ).toBeInTheDocument();
  });

  it("Given a query matching an agent name, When it is typed, Then every session of that agent stays (decision 8 searches all three)", async () => {
    const user = setupUser();
    renderIndex();
    await screen.findByRole("button", { name: /Visual pass/ });

    await user.type(
      screen.getByLabelText("Search sessions, projects or agents"),
      "designer",
    );

    expect(sessionRow("Visual pass")).toBeInTheDocument();
    expect(querySessionRow("Running one")).toBeNull();
    expect(querySessionRow("Background done")).toBeNull();
  });

  it("Given mixed statuses, When Running then Unread then All are picked, Then the rows narrow to that class and come back", async () => {
    const user = setupUser();
    renderIndex();
    await screen.findByRole("button", { name: /Visual pass/ });

    await user.click(screen.getByTestId("filter-chip-running"));
    expect(sessionRow("Running one")).toBeInTheDocument();
    expect(querySessionRow("Visual pass")).toBeNull();

    await user.click(screen.getByTestId("filter-chip-unread"));
    expect(sessionRow("Background done")).toBeInTheDocument();
    expect(querySessionRow("Running one")).toBeNull();

    await user.click(screen.getByTestId("filter-chip-all"));
    expect(sessionRow("Running one")).toBeInTheDocument();
    expect(sessionRow("Visual pass")).toBeInTheDocument();
  });

  it("Given unread sessions exist, When the chips render, Then the unread chip carries their count", async () => {
    renderIndex();
    await screen.findByRole("button", { name: /Background done/ });

    expect(screen.getByTestId("filter-chip-unread")).toHaveTextContent(
      /Unread\s*1/,
    );
  });

  it("Given a status chip and a query together, When both are active, Then only rows satisfying both survive", async () => {
    const user = setupUser();
    renderIndex();
    await screen.findByRole("button", { name: /Visual pass/ });

    await user.click(screen.getByTestId("filter-chip-running"));
    await user.type(
      screen.getByLabelText("Search sessions, projects or agents"),
      "Visual",
    );

    // Visual pass 命中搜索但不是 running；Running one 是 running 但不命中搜索。
    expect(querySessionRow("Visual pass")).toBeNull();
    expect(querySessionRow("Running one")).toBeNull();
  });

  it("Given the 320px sidebar, When the filter row renders, Then the picker and three chips share one non-wrapping line (decision 3)", async () => {
    renderIndex();

    const row = (await screen.findByTestId("filter-chip-all")).parentElement!;
    // happy-dom 不做布局，所以守的是「能不能换行」这两个结构事实：一行装不下时
    // flex 默认是压缩/溢出而不是折行 —— 只要没人加上 flex-wrap，也没人往这行里
    // 塞第五个控件，决策 3 说的那条第二行就不会出现。
    expect(row.className).not.toMatch(/flex-wrap/);
    expect(row).toContainElement(screen.getByTestId("axis-picker"));
    expect(row).toContainElement(screen.getByTestId("filter-chip-running"));
    expect(row).toContainElement(screen.getByTestId("filter-chip-unread"));
    expect(row.childElementCount).toBe(4);
  });
});

// ── 拖拽排序（决策 9：只在项目轴，且筛选生效时禁用） ─────────────────────────

describe("SessionIndexPage drag reorder", () => {
  beforeEach(() => {
    seedTree([
      projectNode({ id: 1, name: "Alpha" }),
      projectNode({ id: 2, name: "Beta" }),
      projectNode({ id: 3, name: "Gamma" }, [
        projectNode({ id: 4, name: "Child A", parentID: 3 }),
        projectNode({ id: 5, name: "Child B", parentID: 3 }),
      ]),
    ]);
  });

  it("Given root projects, When one is dropped before another, Then the new root order is persisted", async () => {
    renderIndex();
    await screen.findByText("Gamma");

    dndMocks.onDragEnd?.({
      active: { id: "project-3" },
      over: { id: "project-1" },
    });

    await waitFor(() => {
      expect(appMocks.ProjectReorder).toHaveBeenCalledWith({
        parentID: 0,
        orderedIDs: [3, 1, 2],
      });
    });
  });

  it("Given sub-projects, When one is dropped before its sibling, Then the order is persisted under their parent", async () => {
    renderIndex();
    await screen.findByText("Child B");

    dndMocks.onDragEnd?.({
      active: { id: "project-5" },
      over: { id: "project-4" },
    });

    await waitFor(() => {
      expect(appMocks.ProjectReorder).toHaveBeenCalledWith({
        parentID: 3,
        orderedIDs: [5, 4],
      });
    });
  });

  it("Given a search is active, When a drop lands, Then nothing is persisted — order has no meaning in a filtered list", async () => {
    const user = setupUser();
    renderIndex();
    await screen.findByText("Gamma");

    await user.type(
      screen.getByLabelText("Search sessions, projects or agents"),
      "Al",
    );
    dndMocks.onDragEnd?.({
      active: { id: "project-2" },
      over: { id: "project-1" },
    });

    expect(appMocks.ProjectReorder).not.toHaveBeenCalled();
  });

  it("Given the agent axis, When the list renders, Then there is no drag context at all (decision 9)", async () => {
    useSidebarAxisStore.getState().setAxis("agent");
    seedAgents([{ id: 7, name: "Eng" }]);
    renderIndex();

    await screen.findByText("Eng");
    expect(dndMocks.onDragEnd).toBeNull();
  });
});

// ── 项目组头挂出来的对话框 / 副作用 ─────────────────────────────────────────

describe("SessionIndexPage project group actions", () => {
  beforeEach(() => {
    seedTree([projectNode({ id: 1, name: "Agentre" })]);
    appMocks.ProjectGet.mockResolvedValue({
      project: {
        color: "agent-1",
        description: "",
        icon: "folder",
        id: 1,
        name: "Agentre",
        parentID: 0,
        path: "/tmp/Agentre",
      },
      directMembers: [],
      inheritedMembers: [],
    });
  });

  async function openMore(user: ReturnType<typeof userEvent.setup>) {
    await user.click(
      await screen.findByRole("button", { name: "More actions for Agentre" }),
    );
  }

  it("Given the ⋮ menu, When Project Settings is picked, Then that project's settings drawer opens", async () => {
    const user = setupUser();
    renderIndex();
    await screen.findByText("Agentre");

    await openMore(user);
    await user.click(
      await screen.findByRole("menuitem", { name: "Project Settings" }),
    );

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("Given the ⋮ menu, When New Terminal → Local is picked, Then a local terminal tab opens for that project", async () => {
    const user = setupUser();
    renderIndex();
    await screen.findByText("Agentre");

    await openMore(user);
    await user.hover(await screen.findByText("New Terminal"));
    // fireEvent 直接触发 Radix item 的 onSelect：userEvent.click 的 pointer-leave
    // 会先把子菜单关掉。
    fireEvent.click(await screen.findByText("Local"));

    await waitFor(() => {
      const state = useChatTabsStore.getState();
      const active = state.tabs.find((t) => t.id === state.activeTabId);
      expect(active?.meta).toMatchObject({
        kind: "terminal",
        projectId: 1,
        deviceId: "",
      });
    });
  });

  it("Given an unconfigured project, When Specify path… picks a directory, Then it is saved and the tree reloads", async () => {
    const user = setupUser();
    seedTree([
      projectNode({
        id: 1,
        name: "Agentre",
        path: "",
        localPathMissing: true,
      }),
    ]);
    appMocks.SelectDirectory.mockResolvedValue("/Users/me/Code/agentre");
    appMocks.ProjectSetLocalPath.mockResolvedValue({
      id: 1,
      localPathMissing: false,
    });
    renderIndex();
    await screen.findByText("Agentre");

    await openMore(user);
    await user.click(await screen.findByText("Specify path…"));

    await waitFor(() => {
      expect(appMocks.ProjectSetLocalPath).toHaveBeenCalledWith({
        id: 1,
        path: "/Users/me/Code/agentre",
      });
    });
    await waitFor(() => {
      expect(appMocks.ProjectListTree.mock.calls.length).toBeGreaterThan(1);
    });
  });

  it("Given an unconfigured project, When Merge into existing project… is picked, Then the dialog opens carrying the other projects as candidates", async () => {
    const user = setupUser();
    seedTree([
      projectNode({ id: 1, name: "Agentre" }),
      projectNode({
        id: 2,
        name: "agentre-hub",
        path: "",
        localPathMissing: true,
      }),
    ]);
    renderIndex();
    await screen.findByText("agentre-hub");

    await user.click(
      await screen.findByRole("button", {
        name: "More actions for agentre-hub",
      }),
    );
    await user.click(await screen.findByText("Merge into existing project…"));

    expect(
      await screen.findByText("Merge into existing project"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("combobox", { name: /merge into/i }),
    ).toBeInTheDocument();
  });

  it("Given the ⋮ menu, When Delete Project is picked, Then the delete confirmation opens", async () => {
    const user = setupUser();
    renderIndex();
    await screen.findByText("Agentre");

    await openMore(user);
    await user.click(
      await screen.findByRole("menuitem", { name: "Delete Project" }),
    );

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("Given the group ＋, When a member agent is chosen, Then a project-scoped new-session tab opens", async () => {
    const user = setupUser();
    appMocks.ProjectGet.mockResolvedValue({
      project: { id: 1, name: "Agentre" },
      directMembers: [
        { agentID: 5, agentName: "Builder", inherited: false },
        { agentID: 6, agentName: "Reviewer", inherited: false },
      ],
      inheritedMembers: [],
    });
    renderIndex();

    await user.click(
      await screen.findByRole("button", { name: "New session in Agentre" }),
    );
    await user.click(await screen.findByText("Builder"));

    await waitFor(() => {
      const state = useChatTabsStore.getState();
      const active = state.tabs.find((t) => t.id === state.activeTabId);
      expect(active?.meta).toMatchObject({
        kind: "new",
        projectId: 1,
        agentId: 5,
        workMode: "",
      });
    });
  });

  it("Given the 随手对话 group ＋, When it is clicked, Then the command palette picks the agent instead of guessing one (decision 6)", async () => {
    const user = setupUser();
    renderIndex();

    await user.click(
      await screen.findByRole("button", { name: "New quick chat" }),
    );

    expect(useCommandPaletteStore.getState()).toMatchObject({
      open: true,
      initialQuery: "> ",
    });
    expect(useChatTabsStore.getState().tabs).toHaveLength(0);
  });
});

// ── 头部 ＋ 菜单（决策 11） ─────────────────────────────────────────────────

describe("SessionIndexPage top + menu", () => {
  it("Given the top ＋, When New chat is picked, Then the command palette opens seeded for a new chat", async () => {
    const user = setupUser();
    renderIndex();

    await user.click(await screen.findByRole("button", { name: "New" }));
    const item = await screen.findByTestId("new-agent-chat-item");
    // 副标题说的是「在面板里按 Tab 换项目」——决策 6 的第二条路全靠它被人看见。
    expect(item).toHaveTextContent("Tab for project");
    await user.click(item);

    expect(useCommandPaletteStore.getState()).toMatchObject({
      open: true,
      initialQuery: "> ",
    });
  });

  it("Given the top ＋, When New project is picked, Then the create-project dialog opens as the secondary path (decision 11)", async () => {
    const user = setupUser();
    renderIndex();

    await user.click(await screen.findByRole("button", { name: "New" }));
    await user.click(await screen.findByTestId("project-create-trigger"));

    expect(await screen.findByTestId("project-create-name")).toBeInTheDocument();
  });

  it("Given the top ＋, When New agent is picked, Then it navigates to the org chart and asks for the dialog", async () => {
    const user = setupUser();
    renderIndex();

    await user.click(await screen.findByRole("button", { name: "New" }));
    const item = await screen.findByTestId("new-agent-item");
    expect(item).toHaveTextContent("Create it in the org chart");
    await user.click(item);

    expect(screen.getByTestId("location")).toHaveTextContent("/org");
    expect(consumeNewAgentDialogIntent()).toBe(true);
  });
});

// ── Agent 轴的组头接线 ─────────────────────────────────────────────────────

describe("SessionIndexPage agent groups", () => {
  beforeEach(() => {
    useSidebarAxisStore.getState().setAxis("agent");
  });

  it("Given an agent group, When its ＋ is clicked, Then a new-session tab opens for that agent", async () => {
    const user = setupUser();
    seedAgents([{ id: 7, name: "Eng" }]);
    renderIndex();

    await user.click(
      await screen.findByRole("button", { name: "New Eng session" }),
    );

    await waitFor(() => {
      const { tabs, activeTabId } = useChatTabsStore.getState();
      expect(tabs).toHaveLength(1);
      expect(tabs[0].id).toBe(activeTabId);
      expect(tabs[0].meta).toMatchObject({ kind: "new", agentId: 7 });
    });
  });

  it("Given a read error session that only the attention pool knows about, When the agent group is expanded, Then it stays out of the bubble and the conversation list (regression: read errors leaked into the regular list)", async () => {
    expandGroups("agent:7");
    seedAgents([
      {
        id: 7,
        name: "Eng",
        sessions: [
          {
            id: 11,
            title: "Recent one",
            lastMessageAt: 3000,
            lastReadAt: 3000,
          },
          {
            id: 12,
            title: "Recent two",
            lastMessageAt: 2000,
            lastReadAt: 2000,
          },
        ],
        // 只出现在 attention 池里、且已读的 error —— 类似 sess-1819：
        // 后端因为 agent_status='error' 把它塞进 attentionSessions，
        // 但已读让它不该在任何地方冒泡。
        attentionSessions: [
          {
            id: 1819,
            title: "Stale error",
            status: "error",
            lastMessageAt: 1000,
            lastReadAt: 1000,
          },
        ],
      },
    ]);
    renderIndex();

    // 前 5 条常规列表正常渲染。
    expect(await screen.findByText("Recent one")).toBeInTheDocument();

    // 已读 error：既不进气泡，也不进常规列表（它不是 unread / running / waiting）。
    expect(querySessionRow("Stale error")).toBeNull();
    expect(
      document.querySelector('[data-slot="agent-attention-bubble"]'),
    ).toBeNull();
  });

  it("Given a non-chattable agent, When its ＋ is clicked, Then the reason dialog explains it and no session tab is created", async () => {
    const user = setupUser();
    seedAgents([
      { id: 7, name: "CEO", chattable: false, blockReason: "no-backend" },
      { id: 8, name: "Eng" },
    ]);
    renderIndex();

    // 未配置徽标只挂在不可对话那一行。
    expect(
      await screen.findByText("Backend not configured"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Not configured", { exact: true })).toBeNull();

    await user.click(
      await screen.findByRole("button", { name: "New CEO session" }),
    );

    expect(
      await screen.findByRole("dialog", { name: "CEO cannot chat yet" }),
    ).toBeInTheDocument();
    expect(useChatTabsStore.getState().tabs).toHaveLength(0);
  });

  it("Given pinned and unpinned agents, When their pin toggles are clicked, Then each writes the opposite state and the list is refreshed", async () => {
    const user = setupUser();
    seedAgents([
      { id: 7, name: "Eng" },
      { id: 8, name: "Designer", pinned: true },
    ]);
    renderIndex();

    const before = appMocks.ListChatAgents.mock.calls.length;
    await user.click(await screen.findByRole("button", { name: "Pin Eng" }));
    expect(appMocks.SetAgentPinned).toHaveBeenCalledWith({
      id: 7,
      pinned: true,
    });
    // 「写库后又拉了一次 ListChatAgents」证明置顶后浮顶即时生效。
    await waitFor(() =>
      expect(appMocks.ListChatAgents.mock.calls.length).toBeGreaterThan(before),
    );

    await user.click(screen.getByRole("button", { name: "Unpin Designer" }));
    expect(appMocks.SetAgentPinned).toHaveBeenCalledWith({
      id: 8,
      pinned: false,
    });
  });
});

// ── 会话行：选中、打开、右键菜单 ───────────────────────────────────────────

describe("SessionIndexPage session rows", () => {
  const sessions: SeedSession[] = [
    {
      id: 1,
      title: "Debug session",
      agentId: 7,
      projectId: 1,
      lastMessageAt: 3000,
      lastReadAt: 3000,
    },
    {
      id: 2,
      title: "Other session",
      agentId: 7,
      projectId: 1,
      lastMessageAt: 2000,
      lastReadAt: 2000,
    },
  ];

  beforeEach(() => {
    expandGroups("project:1", "free");
    seedTree([projectNode({ id: 1, name: "Agentre" })]);
    seedIndexSessions(sessions);
    seedAgents([{ id: 7, name: "Eng", sessions }]);
  });

  it("Given a session row, When it is clicked, Then it opens in the active tab and becomes the highlighted row", async () => {
    const user = setupUser();
    renderIndex();

    await user.click(
      await screen.findByRole("button", { name: /Other session/ }),
    );

    await waitFor(() => {
      const state = useChatTabsStore.getState();
      const active = state.tabs.find((t) => t.id === state.activeTabId);
      expect(active?.meta).toMatchObject({ kind: "session", sessionId: 2 });
    });
    await waitFor(() => {
      expect(sessionRow("Other session")).toHaveAttribute(
        "aria-current",
        "true",
      );
    });
    expect(sessionRow("Debug session")).not.toHaveAttribute("aria-current");
  });

  it("Given the active tab is an unsaved new chat, When the index renders, Then no row claims to be the current one", async () => {
    useChatTabsStore.setState({
      tabs: [
        {
          id: "seed-new-tab",
          meta: { kind: "new", projectId: 1, agentId: 7, workMode: "" },
          isPreview: false,
          isPinned: false,
          pinAt: 0,
          openedAt: 0,
        },
      ],
      activeTabId: "seed-new-tab",
    });
    renderIndex();

    await screen.findByRole("button", { name: /Debug session/ });
    expect(
      screen
        .getAllByRole("button")
        .filter((el) => el.getAttribute("aria-current") === "true"),
    ).toHaveLength(0);
  });

  it("Given a session row, When rename is confirmed from its context menu, Then the new title is persisted", async () => {
    const user = setupUser();
    renderIndex();

    fireEvent.contextMenu(
      await screen.findByRole("button", { name: /Debug session/ }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Rename" }));

    const dialog = await screen.findByRole("dialog", {
      name: "Rename Session",
    });
    const input = within(dialog).getByLabelText("Session name");
    await user.clear(input);
    await user.type(input, "Renamed session");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(appMocks.RenameChatSession).toHaveBeenCalledWith({
        sessionId: 1,
        title: "Renamed session",
      });
    });
  });

  it("Given a session row, When open-in-new-tab is picked, Then that session gets its own active tab", async () => {
    const user = setupUser();
    renderIndex();

    fireEvent.contextMenu(
      await screen.findByRole("button", { name: /Other session/ }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Open in new tab" }),
    );

    await waitFor(() => {
      const { tabs, activeTabId } = useChatTabsStore.getState();
      const opened = tabs.filter(
        (t) => t.meta.kind === "session" && t.meta.sessionId === 2,
      );
      expect(opened).toHaveLength(1);
      expect(opened[0].id).toBe(activeTabId);
    });
  });

  it("Given an open session, When delete is confirmed from its context menu, Then it is deleted and its tab is closed", async () => {
    const user = setupUser();
    selectSessionTab(1);
    renderIndex();

    fireEvent.contextMenu(
      await screen.findByRole("button", { name: /Debug session/ }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));

    const dialog = await screen.findByRole("dialog", {
      name: "Delete Session",
    });
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => {
      expect(appMocks.DeleteChatSession).toHaveBeenCalledWith({ sessionId: 1 });
    });
    expect(useChatTabsStore.getState().tabs).toHaveLength(0);
  });
});

// ── 命令面板的「新会话上下文」桥接 ─────────────────────────────────────────

describe("SessionIndexPage command palette bridge", () => {
  const sessions: SeedSession[] = [
    {
      id: 11,
      title: "In project",
      agentId: 7,
      projectId: 1,
      lastMessageAt: 3000,
      lastReadAt: 3000,
    },
    {
      id: 12,
      title: "Free chat",
      agentId: 7,
      projectId: 0,
      lastMessageAt: 2000,
      lastReadAt: 2000,
    },
  ];

  beforeEach(() => {
    seedTree([projectNode({ id: 1, name: "Agentre" })]);
    seedIndexSessions(sessions);
    seedAgents([{ id: 7, name: "Eng", sessions }]);
  });

  it("Given the open tab belongs to a project, When the index renders, Then the palette learns that project — and forgets it when a free session takes over", async () => {
    selectSessionTab(11);
    const { rerender } = renderIndex();

    await waitFor(() => {
      expect(useNewChatContextStore.getState().projectContext).toEqual({
        projectID: 1,
        projectName: "Agentre",
      });
    });

    selectSessionTab(12);
    rerender(
      <MemoryRouter initialEntries={["/chat"]}>
        <LocationProbe />
        <SessionIndexPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(useNewChatContextStore.getState().projectContext).toBeNull();
    });
  });

  it("Given the palette picks an agent inside the current project, When its handler fires, Then a project-scoped new tab opens", async () => {
    selectSessionTab(11);
    renderIndex();

    await waitFor(() => {
      expect(
        useNewChatContextStore.getState().newSelectionHandler,
      ).not.toBeNull();
    });

    act(() => {
      useNewChatContextStore.getState().newSelectionHandler!(1, {
        id: 9,
      } as never);
    });

    const state = useChatTabsStore.getState();
    const active = state.tabs.find((t) => t.id === state.activeTabId);
    expect(active?.meta).toMatchObject({
      kind: "new",
      projectId: 1,
      agentId: 9,
    });
  });
});

// ── `/chat?focus=<id>` 深链（决策 1：会话设置页的「项目」入口） ──────────────

describe("SessionIndexPage focus deep link", () => {
  beforeEach(() => {
    seedTree([projectNode({ id: 1, name: "Agentre" })]);
    appMocks.ProjectGet.mockResolvedValue({
      project: {
        color: "agent-1",
        description: "",
        icon: "folder",
        id: 1,
        name: "Agentre",
        parentID: 0,
        path: "/tmp/Agentre",
      },
      directMembers: [],
      inheritedMembers: [],
    });
  });

  it("Given /chat?focus=<existing> before the tree has loaded, When it arrives, Then that project's settings drawer still opens", async () => {
    // 回归：清 query 是无条件的，而项目要等 ProjectListTree 回来才认得出 ——
    // 谁先谁后决定了深链开不开抽屉，而树一定比首屏渲染晚。
    renderIndex("/chat?focus=1");

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("Given /chat?focus=<missing>, When the index loads, Then nothing opens — the project may simply be gone", async () => {
    renderIndex("/chat?focus=999");

    await screen.findByText("Agentre");
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

// ── 数据通路与刷新纪律（规格问题 3/4，决策 10） ─────────────────────────────

describe("SessionIndexPage data path", () => {
  it("Given an unread session delivered by the index RPC, When the group renders, Then attention is on it — the project axis used to miss this entirely", async () => {
    expandGroups("project:1");
    seedTree([projectNode({ id: 1, name: "Agentre" })]);
    seedIndexSessions([
      {
        id: 11,
        title: "Unread project session",
        agentId: 7,
        projectId: 1,
        lastMessageAt: 3000,
        lastReadAt: 2000,
      },
    ]);
    renderIndex();

    const bubble = await waitFor(() => {
      const node = document.querySelector(
        '[data-slot="agent-attention-bubble"]',
      );
      expect(node).not.toBeNull();
      return node!;
    });
    expect(bubble).toHaveTextContent("Unread project session");
    expect(bubble).toHaveTextContent("Unread");
  });

  it("Given the index is mounted, When seconds pass without interaction, Then nothing is polled (decision 10 deleted the 1s full refresh)", async () => {
    seedTree([projectNode({ id: 1, name: "Agentre" })]);
    seedAgents([{ id: 7, name: "Eng" }]);
    vi.useFakeTimers();
    try {
      renderIndex();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10);
      });

      const before = [
        appMocks.ProjectListTree.mock.calls.length,
        appMocks.ListChatAgents.mock.calls.length,
        appMocks.ListChatIndexSessions.mock.calls.length,
      ];
      expect(before[0]).toBeGreaterThan(0);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });

      expect([
        appMocks.ProjectListTree.mock.calls.length,
        appMocks.ListChatAgents.mock.calls.length,
        appMocks.ListChatIndexSessions.mock.calls.length,
      ]).toEqual(before);
    } finally {
      vi.useRealTimers();
    }
  });

  it("Given the whole front end, When sources are scanned, Then attention is computed in exactly one place (problem 3)", () => {
    // 项目页那份内联 computeAttention 是「同一条会话两个页面两种答案」的根因
    // （规格问题 3）。收敛之后它必须只剩 store 里那一处 —— 扫源码文本，因为第二处
    // import 在类型上完全合法，编译期与 ESLint 都不会红。
    //
    // 只认**代码位置**：注释里提这个名字的地方（types.ts 的类型说明、
    // use-group-rows.ts 与 session-index-store.ts 解释「那条绕行没有了」的段落）
    // 是在讲这条约束本身，不该被自己的守卫判违规。
    const hits: string[] = [];

    for (const root of ["src", "packages/agentre-ui/src"]) {
      for (const file of walkSources(resolve(process.cwd(), root))) {
        const code = stripComments(readFileSync(file, "utf8"));
        if (!/\bcomputeAttention\b/.test(code)) continue;
        hits.push(relative(process.cwd(), file));
      }
    }

    expect(hits).toEqual(["src/stores/attention-store.ts"]);
  });
});

/** 生产源码（测试文件不算）里的 .ts/.tsx 全表。 */
function walkSources(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "__tests__" || entry.name === "node_modules") continue;
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      walkSources(full, out);
      continue;
    }
    if (/\.tsx?$/.test(entry.name) && !/\.test\.tsx?$/.test(entry.name)) {
      out.push(full);
    }
  }
  return out;
}

function stripComments(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");
}

// ── 刷新是「校准」不是「重载」 ──────────────────────────────────────────────
//
// 侧栏的统一刷新入口 reloadSidebarSources() 在每轮对话起手 / 落定各被调一次(自主续轮、
// 后台活动轮还会再加)。这条路必须做到: 数据没变就一个 DOM 节点都别动, 数据变了也只
// 原地改那几个字节。
//
// 之前不是这样 —— 项目树在发 RPC 前先把自己清空(`cache = {tree: [], loaded: false}`),
// 而同一个 tick 里 chat-agents-store / session-index-store 的 loading 写入必然触发一次
// 重渲染, 那一帧读到空树: 整个项目轴塌成只剩「随手对话」, RPC 回来再重建。会话行因此
// 每轮都被拆掉重挂载 —— 肉眼就是「列表刷新了一遍」。
describe("SessionIndexPage refresh churn", () => {
  const tree = [projectNode({ id: 1, name: "Agentre" })];
  const seed: SeedSession[] = [
    {
      id: 11,
      title: "Root work",
      agentId: 7,
      projectId: 1,
      lastMessageAt: 100,
      status: "idle",
    },
  ];

  async function bootIndex(sessions: SeedSession[] = seed) {
    seedTree(tree);
    seedIndexSessions(sessions);
    seedAgents([{ id: 7, name: "Eng", sessions }]);
    expandGroups("project:1");
    renderIndex();
    return screen.findByRole("button", { name: /Root work/ });
  }

  async function refreshWith(sessions: SeedSession[]) {
    seedIndexSessions(sessions);
    seedAgents([{ id: 7, name: "Eng", sessions }]);
    await act(async () => {
      reloadSidebarSources();
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
  }

  it("Given a turn ends without changing anything the sidebar shows, When it refreshes, Then not a single DOM node is touched", async () => {
    await bootIndex();
    const records: MutationRecord[] = [];
    const observer = new MutationObserver((list) => records.push(...list));
    observer.observe(document.body, {
      attributes: true,
      characterData: true,
      childList: true,
      subtree: true,
    });

    await refreshWith(seed);

    observer.disconnect();
    expect(records).toEqual([]);
  });

  it("Given a session started running, When the sidebar refreshes, Then the row is updated in place instead of rebuilt", async () => {
    const row = await bootIndex();
    const search = screen.getByPlaceholderText(/Search sessions/i);
    search.focus();
    fireEvent.change(search, { target: { value: "Root" } });

    await refreshWith([{ ...seed[0], status: "running", lastMessageAt: 200 }]);

    // 同一个 DOM 节点 = 没被拆掉重建: CSS 过渡不重放、hover / 焦点不丢, 正在输入的
    // 搜索框也不会被打断。行内容该变的照变(状态点 / 相对时间), 那是数据真的变了。
    expect(screen.getByRole("button", { name: /Root work/ })).toBe(row);
    expect(screen.getByPlaceholderText(/Search sessions/i)).toBe(search);
    expect((search as HTMLInputElement).value).toBe("Root");
    expect(document.activeElement).toBe(search);
  });
});
