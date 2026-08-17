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
  onSpecifyPath: vi.fn(),
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
    hasRunning: false,
    allLocalPathsMissing: false,
    projectColorOf: () => "agent-3",
    projectNameOf: () => "Agentre",
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
    // 决策 5 的两行行式在 mockup 里是 `〔头像〕agent · 〔文件夹〕项目`：字形和
    // 「按项目」的行首头像、「按 Agent」的行首文件夹**同一个**。退成纯文字的话，
    // 同一条会话在三个档之间长出三种样子，切档时读者要重新找一遍锚点。
    seed(1, { agentId: 7, projectId: 4, lastMessageAt: 100 });

    renderRow({
      axis: "time",
      group: group({ key: "flat", kind: "flat", refID: 0, sessionIDs: [1] }),
      subtreeSessionIDs: [1],
      project: undefined,
      projectNameOf: () => "Agentre",
      projectColorOf: () => "agent-3",
      agentInfoOf: () => ({ name: "设计师", color: "agent-9" }),
    });

    const line = screen.getByTestId("row-secondary-line");
    expect(line).toHaveTextContent("设计师");
    expect(line).toHaveTextContent("Agentre");
    expect(line.querySelector("[data-kind='agent-avatar']")).not.toBeNull();
    expect(line.querySelector("[data-kind='project-folder']")).not.toBeNull();
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
    expect(
      line.querySelector("[data-kind='project-folder-muted']"),
    ).not.toBeNull();
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
