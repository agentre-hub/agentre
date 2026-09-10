import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../../wailsjs/go/app/App", () => ({
  ListChatAgents: vi.fn(),
  ListChatIndexSessions: vi.fn(),
  ProjectGet: vi.fn().mockResolvedValue({
    directMembers: [],
    inheritedMembers: [],
  }),
  ProjectLocationList: vi.fn().mockResolvedValue([]),
  RemoteDeviceList: vi.fn().mockResolvedValue([]),
}));

// 项目组头的 ⋮ 会挂上「新建终端」子菜单 → useRemoteDevices → 订阅 wails 事件。
// 桌面 runtime 在 happy-dom 里不存在（`window.runtime` 为空），所以这一处按仓库
// 既定做法给个 per-file mock，而不是往全局 setup 里塞别名。
vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn(() => () => {}),
  EventsOff: vi.fn(),
  EventsEmit: vi.fn(),
}));

import {
  IndexGroupRow,
  type IndexGroupHandlers,
} from "../session-index/index-group-row";
import type { IndexGroup } from "../session-index/use-index-groups";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionStatusStore } from "@/stores/session-status-store";
import type { AgentStatus } from "@/stores/types";

/**
 * 组渲染层的回归守卫。这里的每一条都对应一次**实现回归** —— 合并前的两个页面各自
 * 有这些行为，重写时掉了，代码复核抓了出来。
 */

const noopHandlers: IndexGroupHandlers = {
  onSessionSelect: vi.fn(),
  onOpenInNewTab: vi.fn(),
  onRenameSession: vi.fn(),
  onDeleteSession: vi.fn(),
  onNewSession: vi.fn(),
  onOpenNotChattable: vi.fn(),
  onTogglePin: vi.fn(),
  onOpenSettings: vi.fn(),
  onAddSubProject: vi.fn(),
  onOpenTerminal: vi.fn(),
  onMergeInto: vi.fn(),
  onDeleteProject: vi.fn(),
};

function group(over: Partial<IndexGroup> = {}): IndexGroup {
  return {
    key: "project:1",
    kind: "project",
    refID: 1,
    depth: 0,
    sessionIDs: [],
    total: 0,
    ...over,
  };
}

function seed(
  sid: number,
  meta: { agentId?: number; projectId?: number; lastMessageAt?: number },
  status?: { agentStatus?: AgentStatus; needsAttention?: boolean },
) {
  useSessionMetaStore
    .getState()
    .bulkUpsert([
      [sid, { title: `s-${sid}`, lastMessageAt: 0, lastReadAt: 0, ...meta }],
    ]);
  if (status) {
    useSessionStatusStore
      .getState()
      .upsert(sid, { agentStatus: "idle", needsAttention: false, ...status });
  }
}

function renderRow(over: Partial<Parameters<typeof IndexGroupRow>[0]> = {}) {
  const props = {
    group: group(),
    axis: "project" as const,
    selectedSessionID: 0,
    visibleSessionIDs: null,
    subtreeSessionIDs: [] as number[],
    project: { id: 1, name: "Agentre" } as never,
    allLocalPathsMissing: false,
    projectInfoOf: () => ({ name: "Agentre", color: "agent-3", icon: "" }),
    agentInfoOf: () => ({ name: "", color: "" }),
    handlers: noopHandlers,
    ...over,
  };
  return render(<IndexGroupRow {...props} />);
}

describe("IndexGroupRow regressions", () => {
  beforeEach(() => {
    useSessionMetaStore.getState().__reset();
    useSessionStatusStore.getState().__reset();
    localStorage.clear();
  });

  it("Given a project group, When it first renders, Then it is expanded — a tree of closed folders on first launch is a regression from the old project page", () => {
    seed(1, { projectId: 1, lastMessageAt: 100 });

    renderRow({ group: group({ sessionIDs: [1] }), subtreeSessionIDs: [1] });

    expect(screen.getByRole("button", { name: /s-1/ })).not.toBeDisabled();
  });

  it("Given a subtree whose strongest attention is an error, When the group header renders, Then the mark is red even though other sessions are running", () => {
    // 组头的档位由子树里最强的一档决定，而不是「有没有在跑」——一条出错被三条在跑
    // 盖成绿色，正是此前那根绿条与写死绿计数一起造成的读误。
    seed(1, { projectId: 1, lastMessageAt: 100 }, { agentStatus: "running" });
    seed(2, { projectId: 1, lastMessageAt: 90 }, { agentStatus: "running" });
    seed(3, { projectId: 1, lastMessageAt: 80 }, { agentStatus: "error" });

    renderRow({
      group: group({ sessionIDs: [1, 2, 3] }),
      subtreeSessionIDs: [1, 2, 3],
    });

    const mark = screen.getByTestId("project-attention-mark");
    expect(mark.className).toMatch(/text-status-error/);
    expect(mark.textContent).toContain("3");
  });

  it("Given a subtree that only has unread sessions, When the group header renders, Then the mark is amber, matching the rows below it", () => {
    seed(1, { projectId: 1, lastMessageAt: 100 });

    renderRow({
      group: group({ sessionIDs: [1] }),
      subtreeSessionIDs: [1],
    });

    expect(screen.getByTestId("project-attention-mark").className).toMatch(
      /text-status-waiting-text/,
    );
  });

  it("Given a session outside its agent's loaded window, When the row is decorated, Then the agent identity is resolved through the agents list, not through meta", () => {
    // 索引 RPC 的载荷只有 agentId —— agentName / agentColor 只有 ListChatAgents
    // 那条路会写。凡是落在某个 agent 前 5 条之外的会话，读 meta 会拿到空名字，
    // 正好把决策 4 的行首字形与决策 5 的第二行打空。
    seed(1, { agentId: 7, projectId: 0, lastMessageAt: 100 });

    renderRow({
      axis: "time",
      group: group({ key: "flat", kind: "flat", refID: 0, sessionIDs: [1] }),
      subtreeSessionIDs: [1],
      project: undefined,
      agentInfoOf: (id) =>
        id === 7
          ? { name: "设计师", color: "agent-9" }
          : { name: "", color: "" },
    });

    expect(screen.getByText("设计师")).toBeInTheDocument();
  });

  it("Given the time axis, When a row renders its second line, Then each dimension keeps the glyph it wears on the other two axes", () => {
    // 决策 5 的两行行式是 `〔agent 头像〕agent · 〔项目头像〕项目`：字形和
    // 「按项目」的行首头像、「按 Agent」的行首项目头像**同一个**。退成纯文字的话，
    // 同一条会话在三个档之间长出三种样子，切档时读者要重新找一遍锚点。
    seed(1, { agentId: 7, projectId: 4, lastMessageAt: 100 });

    renderRow({
      axis: "time",
      group: group({ key: "flat", kind: "flat", refID: 0, sessionIDs: [1] }),
      subtreeSessionIDs: [1],
      project: undefined,
      projectInfoOf: () => ({
        name: "Agentre",
        color: "agent-3",
        icon: "rocket",
      }),
      agentInfoOf: () => ({ name: "设计师", color: "agent-9" }),
    });

    const line = screen.getByTestId("row-secondary-line");
    expect(line).toHaveTextContent("设计师");
    expect(line).toHaveTextContent("Agentre");
    expect(line.querySelector("[data-kind='agent-avatar']")).not.toBeNull();
    // 项目那一维带的是**这个项目自己的**图标 + 项目色，与它在项目轴组头上的
    // 那一枚同源；通用文件夹字形分不出是哪个项目。
    const glyph = line.querySelector("[data-kind='project-avatar']");
    expect(glyph).not.toBeNull();
    expect(glyph?.querySelector("svg")).not.toBeNull();
    // 上色走 css 变量而不是 bg-agent-* 类名：类名要靠宿主的 Tailwind 扫到包源码
    // 才生成得出来，消费方少配一条 content 路径字形就静默变透明。
    expect(
      glyph?.querySelector<HTMLElement>("[role='img']")?.style.backgroundColor,
    ).toBe("var(--agent-3)");
  });

  it("Given a free session on the time axis, When its second line renders, Then the project half says 随手对话 with the muted glyph instead of going blank", () => {
    // 决策 7：「随手对话」是一个正当的去处，不是分类失败的残留。留半行空白会让
    // 自由会话看起来像「项目丢了」，而它本来就不该有项目。
    seed(1, { agentId: 7, projectId: 0, lastMessageAt: 100 });

    renderRow({
      axis: "time",
      group: group({ key: "flat", kind: "flat", refID: 0, sessionIDs: [1] }),
      subtreeSessionIDs: [1],
      project: undefined,
      agentInfoOf: () => ({ name: "设计师", color: "agent-9" }),
    });

    const line = screen.getByTestId("row-secondary-line");
    expect(line).toHaveTextContent("Quick chats");
    expect(line.querySelector("[data-kind='free-glyph']")).not.toBeNull();
  });

  it("Given a collapsed parent whose descendant needs attention, When it renders, Then the bubble rolls the descendant up instead of hiding it", () => {
    seed(1, { projectId: 1, lastMessageAt: 100 });
    seed(2, { projectId: 2, lastMessageAt: 200 }, { needsAttention: true });
    localStorage.setItem("agentre.agentExpanded.project:1", "0");

    const { container } = renderRow({
      group: group({ sessionIDs: [1] }),
      subtreeSessionIDs: [1, 2], // 子项目 2 的那条
    });

    const bubble = container.querySelector(
      '[data-slot="agent-attention-bubble"]',
    );
    expect(bubble).not.toBeNull();
    expect(bubble!.textContent).toContain("s-2");
  });

  it("Given descendants needing attention, When the header count renders, Then it counts the whole subtree", () => {
    seed(1, { projectId: 1, lastMessageAt: 100 }, { needsAttention: true });
    seed(2, { projectId: 2, lastMessageAt: 200 }, { needsAttention: true });

    renderRow({ group: group({ sessionIDs: [1] }), subtreeSessionIDs: [1, 2] });

    expect(screen.getByText("2")).toBeInTheDocument();
  });

  it("Given more sessions than are loaded, When the group renders, Then the overflow entry appears so the rest is reachable", () => {
    seed(1, { projectId: 1, lastMessageAt: 100 });

    renderRow({
      group: group({ sessionIDs: [1], total: 9 }),
      subtreeSessionIDs: [1],
      handlers: {
        ...noopHandlers,
        renderSessionsPopover: () => null,
      },
    });

    expect(screen.getByText(/View all 9 sessions/)).toBeInTheDocument();
  });

  it("Given a project has both its own sessions and sub-projects, When it renders, Then its own sessions sit in a separately collapsible sub-group", async () => {
    // 合并前的行为：父项目的会话与子项目共用一个折叠箭头会把两者绑死 —— 会话多的
    // 父项目会把子项目挤出视野。自己的会话下沉进 `project:<id>:sessions`，父级箭头
    // 仍收整卡，但会话这一段可以单独收起来。
    // 已读会话：待处理的会话会被提到组头的气泡里（在折叠容器之外），那样就测不到
    // 折叠本身了。
    seed(1, { projectId: 1 });

    renderRow({
      group: group({ sessionIDs: [1] }),
      subtreeSessionIDs: [1],
      children: <div data-testid="sub-projects" />,
    });

    expect(screen.getByRole("button", { name: /s-1/ })).toBeInTheDocument();

    const toggle = screen.getByRole("button", {
      name: "Toggle Agentre sessions",
    });
    await userEvent.click(toggle);

    expect(screen.queryByRole("button", { name: /s-1/ })).toBeNull();
    // 子项目不跟着走 —— 这正是拆出这个子分组的理由。
    expect(screen.getByTestId("sub-projects")).toBeInTheDocument();
  });

  it("Given a project whose first page is only a window over its sessions, When the own-sessions sub-group renders, Then its count is the project total, not the loaded page size", () => {
    // 首屏只拉 GROUP_PAGE_SIZE 条，组头却把「已加载几条」当成「有几条」写出来：
    // 44 条会话的项目显示「会话 5」，与紧挨着它的「查看全部 44」自相矛盾，
    // 读起来像是会话丢了。已读会话（lastMessageAt 缺省 0），免得被提进气泡。
    for (const sid of [1, 2, 3, 4, 5]) seed(sid, { projectId: 1 });

    renderRow({
      group: group({ sessionIDs: [1, 2, 3, 4, 5], total: 44 }),
      subtreeSessionIDs: [1, 2, 3, 4, 5],
      children: <div data-testid="sub-projects" />,
      handlers: { ...noopHandlers, renderSessionsPopover: () => null },
    });

    expect(
      screen.getByRole("button", { name: "Toggle Agentre sessions" }),
    ).toHaveTextContent("44");
  });

  it("Given a project has no sub-projects, When it renders, Then its sessions stay directly under the project header — no pointless extra row", () => {
    seed(1, { projectId: 1, lastMessageAt: 100 });

    renderRow({ group: group({ sessionIDs: [1] }), subtreeSessionIDs: [1] });

    expect(screen.getByRole("button", { name: /s-1/ })).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Toggle Agentre sessions" }),
    ).toBeNull();
  });

  it("Given a filter is active, When the group renders, Then no overflow entry appears — the next page is unfiltered and would look like the filter leaked", () => {
    seed(1, { projectId: 1, lastMessageAt: 100 });

    renderRow({
      group: group({ sessionIDs: [1], total: 9 }),
      subtreeSessionIDs: [1],
      visibleSessionIDs: new Set([1]),
      handlers: { ...noopHandlers, renderSessionsPopover: () => null },
    });

    expect(screen.queryByText(/View all/)).toBeNull();
  });
});

/**
 * 「导入本地会话…」的入口（规格 2026-08-26，决策 13）：四条轴的组头各挂一条，
 * 各按自己那一维预填；宿主没声明这个能力时**整条不出现**（不置灰）。
 *
 * 时间轴没有组头（决策 5），所以那一条轴天然没有入口 —— 这里钉的是有组头的四种。
 */
describe("组头上的导入入口", () => {
  const importable: IndexGroupHandlers = {
    ...noopHandlers,
    onImportLocalSession: vi.fn(),
  };

  beforeEach(() => vi.clearAllMocks());

  async function openImportMenu(triggerTestId: string) {
    const user = userEvent.setup();
    await user.click(screen.getByTestId(triggerTestId));
    return screen.findByTestId(
      `${triggerTestId.replace(/-trigger$/, "")}-item`,
    );
  }

  it("Given 机器组头, When 从 ⋮ 选「导入本地会话…」, Then 预填的是那台机器（导的是那台机器磁盘上的会话）", async () => {
    renderRow({
      group: group({ key: "machine:3", kind: "machine", refID: 3 }),
      axis: "machine",
      machine: { deviceId: 3, name: "Build box", online: true },
      handlers: importable,
    });

    const item = await openImportMenu("import-menu-machine-3-trigger");
    await userEvent.setup().click(item);
    expect(importable.onImportLocalSession).toHaveBeenCalledWith({
      scopeLabel: "Build box",
      deviceId: "3",
    });
  });

  it("Given 项目组头, When 从既有的 ⋮ 选那一条, Then 预填该项目的路径与归属", async () => {
    renderRow({
      project: {
        id: 1,
        name: "Agentre",
        path: "/Code/agentre",
      } as never,
      handlers: importable,
    });

    const user = userEvent.setup();
    await user.click(screen.getByTestId("project-menu-1"));
    await user.click(
      await screen.findByTestId("project-menu-item-import-local-session"),
    );
    expect(importable.onImportLocalSession).toHaveBeenCalledWith({
      scopeLabel: "Agentre",
      cwdPrefix: "/Code/agentre",
      projectId: "1",
    });
  });

  it("Given 随手对话组头, When 从 ⋮ 选那一条, Then 不预填任何一维，且 ＋ 照样在（新入口不挤掉旧入口）", async () => {
    renderRow({
      group: group({ key: "free", kind: "free", refID: 0 }),
      handlers: importable,
    });

    expect(screen.getByTestId("free-group-header-plus")).toBeTruthy();
    const item = await openImportMenu("import-menu-free-trigger");
    await userEvent.setup().click(item);
    expect(importable.onImportLocalSession).toHaveBeenCalledWith({});
  });

  it("Given Agent 组头, When 从 ⋮ 选那一条, Then 预选那个 agent（backend / provider / model 跟着它定）", async () => {
    renderRow({
      group: group({ key: "agent:9", kind: "agent", refID: 9 }),
      axis: "agent",
      agent: { id: 9, name: "Backend dev", chattable: true } as never,
      handlers: importable,
    });

    const item = await openImportMenu("import-menu-agent-9-trigger");
    await userEvent.setup().click(item);
    expect(importable.onImportLocalSession).toHaveBeenCalledWith({
      scopeLabel: "Backend dev",
      agentId: "9",
    });
  });

  it("Given 宿主没有声明这个能力, When 四种组头渲染, Then 那条 ⋮ / 那条菜单条目整条不出现", async () => {
    const { unmount } = renderRow({
      group: group({ key: "machine:3", kind: "machine", refID: 3 }),
      axis: "machine",
      machine: { deviceId: 3, name: "Build box", online: true },
    });
    expect(screen.queryByTestId("import-menu-machine-3-trigger")).toBeNull();
    unmount();

    const free = renderRow({
      group: group({ key: "free", kind: "free", refID: 0 }),
    });
    expect(screen.queryByTestId("import-menu-free-trigger")).toBeNull();
    free.unmount();

    const agent = renderRow({
      group: group({ key: "agent:9", kind: "agent", refID: 9 }),
      axis: "agent",
      agent: { id: 9, name: "Backend dev", chattable: true } as never,
    });
    expect(screen.queryByTestId("import-menu-agent-9-trigger")).toBeNull();
    agent.unmount();

    renderRow({ project: { id: 1, name: "Agentre" } as never });
    await userEvent.setup().click(screen.getByTestId("project-menu-1"));
    expect(
      screen.queryByTestId("project-menu-item-import-local-session"),
    ).toBeNull();
  });
});
