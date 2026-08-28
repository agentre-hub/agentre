import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentSlim } from "@/hooks/use-chat-agents";
import {
  clearLastAgentId,
  readLastAgentId,
} from "@/stores/last-agent-persistence";
import { useNewChatContextStore } from "@/stores/new-chat-context-store";

import {
  flattenAgents,
  newProjectChatSource,
  type NewProjectChatItem,
} from "./new-project-chat-source";
import type { OnSelectCtx } from "../types";

function mkAgent(over: Partial<AgentSlim> = {}): AgentSlim {
  return {
    id: 1,
    name: "Agent",
    avatarColor: "agent-1",
    avatarIcon: "",
    avatarDataUrl: "",
    backendType: "claudecode",
    chattable: true,
    pinned: false,
    chattableHint: "",
    activeCount: 0,
    recentCount: 0,
    totalSessions: 0,
    sessions: [],
    attentionSessions: [],
    ...over,
  } as AgentSlim;
}

function mkItem(over: Partial<NewProjectChatItem> = {}): NewProjectChatItem {
  const agent = over.agent ?? mkAgent({ id: 7, name: "X" });
  return {
    key: `new-project-chat-agent-${agent.id}`,
    agentId: agent.id,
    agent,
    isMember: true,
    ...over,
  };
}

function mkCtx() {
  return {
    navigate: vi.fn(),
    close: vi.fn(),
    openSession: vi.fn(),
    openNewSession: vi.fn(),
  } as unknown as OnSelectCtx & {
    navigate: ReturnType<typeof vi.fn>;
    close: ReturnType<typeof vi.fn>;
    openSession: ReturnType<typeof vi.fn>;
    openNewSession: ReturnType<typeof vi.fn>;
  };
}

describe("flattenAgents (NewProjectChatSource · 项目分组)", () => {
  it("only chattable agents are kept", () => {
    const agents: AgentSlim[] = [
      mkAgent({ id: 1, name: "Yes", chattable: true }),
      mkAgent({
        id: 2,
        name: "No",
        chattable: false,
        chattableHint: "未绑后端",
      }),
    ];
    const items = flattenAgents(agents, null);
    expect(items.map((i) => i.agent.id)).toEqual([1]);
  });

  it("with no members set: all marked isMember=true (项目页未选项目时的退化形态)", () => {
    const agents: AgentSlim[] = [
      mkAgent({ id: 1, name: "A" }),
      mkAgent({ id: 2, name: "B" }),
    ];
    const items = flattenAgents(agents, null);
    expect(items.every((i) => i.isMember)).toBe(true);
  });

  it("pinned-first holds within the 'no members' branch", () => {
    const agents: AgentSlim[] = [
      mkAgent({ id: 1, name: "A", pinned: false }),
      mkAgent({ id: 2, name: "B", pinned: true }),
    ];
    const items = flattenAgents(agents, null);
    expect(items.map((i) => i.agent.id)).toEqual([2, 1]);
  });

  it("with members set: groups members first, non-members after; non-members marked isMember=false", () => {
    const agents: AgentSlim[] = [
      mkAgent({ id: 1, name: "A" }),
      mkAgent({ id: 2, name: "B" }),
      mkAgent({ id: 3, name: "C" }),
    ];
    const members = new Set([2, 3]);
    const items = flattenAgents(agents, members);
    expect(items.map((i) => ({ id: i.agent.id, m: i.isMember }))).toEqual([
      { id: 2, m: true },
      { id: 3, m: true },
      { id: 1, m: false },
    ]);
  });

  it("with members set: pinned-non-member still goes to non-member group", () => {
    const agents: AgentSlim[] = [
      mkAgent({ id: 1, name: "A", pinned: true }),
      mkAgent({ id: 2, name: "B", pinned: false }),
    ];
    const members = new Set([2]);
    const items = flattenAgents(agents, members);
    expect(items.map((i) => ({ id: i.agent.id, m: i.isMember }))).toEqual([
      { id: 2, m: true },
      { id: 1, m: false },
    ]);
  });

  it("item key is stable and namespaced (new-project-chat-agent-N)", () => {
    const items = flattenAgents([mkAgent({ id: 42 })], null);
    expect(items[0]?.key).toBe("new-project-chat-agent-42");
  });

  it("无 members（退化模式）：lastAgentId 命中冒泡到组首", () => {
    const agents: AgentSlim[] = [
      mkAgent({ id: 1, name: "A", pinned: true }),
      mkAgent({ id: 2, name: "B", pinned: false }),
      mkAgent({ id: 3, name: "C", pinned: false }),
    ];
    const items = flattenAgents(agents, null, 3);
    expect(items.map((i) => i.agent.id)).toEqual([3, 1, 2]);
  });

  it("有 members：lastAgentId 是 member 时冒泡到 member 组首", () => {
    const agents: AgentSlim[] = [
      mkAgent({ id: 1, name: "A", pinned: true }),
      mkAgent({ id: 2, name: "B" }),
      mkAgent({ id: 3, name: "C" }),
    ];
    const members = new Set([1, 2]);
    const items = flattenAgents(agents, members, 2);
    expect(items.map((i) => ({ id: i.agent.id, m: i.isMember }))).toEqual([
      { id: 2, m: true },
      { id: 1, m: true },
      { id: 3, m: false },
    ]);
  });

  it("有 members：lastAgentId 不是 member 时不冒泡（members-first 是更强语义）", () => {
    const agents: AgentSlim[] = [
      mkAgent({ id: 1, name: "A" }),
      mkAgent({ id: 2, name: "B" }),
      mkAgent({ id: 3, name: "C" }),
    ];
    const members = new Set([1, 2]);
    const items = flattenAgents(agents, members, 3);
    expect(items.map((i) => ({ id: i.agent.id, m: i.isMember }))).toEqual([
      { id: 1, m: true },
      { id: 2, m: true },
      { id: 3, m: false },
    ]);
  });

  describe("paths arg (device-aware path preview)", () => {
    it("local agent · resolves to paths.localPath", () => {
      const agents: AgentSlim[] = [mkAgent({ id: 1, name: "L", deviceID: "" })];
      const items = flattenAgents(agents, null, null, {
        localPath: "/Code/foo",
      });
      expect(items[0]?.locationPath).toBe("/Code/foo");
    });

    it("remote agent · resolves to paths.byDeviceID[deviceID]", () => {
      const agents: AgentSlim[] = [
        mkAgent({ id: 1, name: "R", deviceID: "7", deviceName: "linux-srv" }),
      ];
      const items = flattenAgents(agents, null, null, {
        byDeviceID: { "7": "/home/me/foo" },
      });
      expect(items[0]?.locationPath).toBe("/home/me/foo");
    });

    it("remote agent · missing location maps to undefined (UI 渲染时跳过 cwd 预览)", () => {
      const agents: AgentSlim[] = [
        mkAgent({ id: 1, name: "R", deviceID: "7" }),
      ];
      const items = flattenAgents(agents, null, null, { byDeviceID: {} });
      expect(items[0]?.locationPath).toBeUndefined();
    });

    it("paths=undefined · 全部不带 locationPath(退化/无 project context)", () => {
      const agents: AgentSlim[] = [
        mkAgent({ id: 1, name: "L", deviceID: "" }),
        mkAgent({ id: 2, name: "R", deviceID: "7" }),
      ];
      const items = flattenAgents(agents, null);
      expect(items.every((i) => i.locationPath === undefined)).toBe(true);
    });
  });

  describe("subHeading (两段分组)", () => {
    it("projectName 提供时:成员的 subHeading 是 '在 {N} 中新建 chat',非成员是 '其它 Agent'", () => {
      const agents: AgentSlim[] = [
        mkAgent({ id: 1, name: "A" }),
        mkAgent({ id: 2, name: "B" }),
      ];
      const members = new Set([1]);
      const items = flattenAgents(agents, members, null, undefined, "foo");
      expect(items.map((i) => i.subHeading)).toEqual([
        "New chat in foo",
        "Other Agents",
      ]);
    });

    it("projectName=undefined 时所有 item 不带 subHeading(单组)", () => {
      const agents: AgentSlim[] = [
        mkAgent({ id: 1, name: "A" }),
        mkAgent({ id: 2, name: "B" }),
      ];
      const members = new Set([1]);
      const items = flattenAgents(agents, members);
      expect(items.every((i) => i.subHeading === undefined)).toBe(true);
    });
  });
});

describe("newProjectChatSource — metadata", () => {
  it("declares modes=['command']", () => {
    expect(newProjectChatSource.modes).toEqual(["command"]);
  });

  it("activeFor 只认项目上下文，不认路由", () => {
    expect(newProjectChatSource.activeFor?.({ hasProjectContext: true })).toBe(
      true,
    );
    expect(newProjectChatSource.activeFor?.({ hasProjectContext: false })).toBe(
      false,
    );
  });

  it("getScore matches 'New project chat with X' full title", () => {
    const item = mkItem({ agent: mkAgent({ id: 7, name: "CEO 助手" }) });
    expect(newProjectChatSource.getScore("CEO", item)).toBeGreaterThan(0);
    expect(
      newProjectChatSource.getScore("New project chat", item),
    ).toBeGreaterThan(0);
    expect(newProjectChatSource.getScore("xyz-nope", item)).toBe(0);
    expect(newProjectChatSource.getScore("", item)).toBe(1);
  });

  it("'new chat' also matches because 'new chat' is substring of title 'New project chat with X'", () => {
    const item = mkItem({ agent: mkAgent({ id: 7, name: "CEO" }) });
    expect(newProjectChatSource.getScore("new chat", item)).toBeGreaterThan(0);
  });
});

describe("newProjectChatSource.renderItem — shows 'New project chat with <name>'", () => {
  it("renders the project-scoped command name", () => {
    const item = mkItem({ agent: mkAgent({ id: 1, name: "CEO 助手" }) });
    const { container } = render(
      <>{newProjectChatSource.renderItem(item, { active: false })}</>,
    );
    expect(container.textContent).toContain("New project chat with");
    expect(container.textContent).toContain("CEO 助手");
  });

  it("non-member rows show 'Not in this project' badge", () => {
    const item = mkItem({
      agent: mkAgent({ id: 5, name: "Designer" }),
      isMember: false,
    });
    const { container } = render(
      <>{newProjectChatSource.renderItem(item, { active: false })}</>,
    );
    expect(container.textContent).toContain("Not in this project");
  });
});

describe("newProjectChatSource.isDisabled — 非成员（其它 Agent）不可选", () => {
  it("returns true for non-members, false for members", () => {
    expect(newProjectChatSource.isDisabled?.(mkItem({ isMember: false }))).toBe(
      true,
    );
    expect(newProjectChatSource.isDisabled?.(mkItem({ isMember: true }))).toBe(
      false,
    );
  });
});

describe("newProjectChatSource.onSelect — 项目作用域分发", () => {
  beforeEach(() => {
    useNewChatContextStore.getState().clear();
    clearLastAgentId();
    vi.spyOn(console, "info").mockImplementation(() => {});
  });
  afterEach(() => {
    clearLastAgentId();
  });

  it("写入 lastAgentId（成员走项目路径 / 退化自由会话都写）", () => {
    // 项目路径：handler 注册 + member
    const handler = vi.fn();
    useNewChatContextStore
      .getState()
      .setContext({ projectID: 42, projectName: "X" });
    useNewChatContextStore.getState().setNewSelectionHandler(handler);
    const agent = mkAgent({ id: 77 });
    newProjectChatSource.onSelect(mkItem({ agent, isMember: true }), mkCtx());
    expect(readLastAgentId()).toBe(77);

    // 退化自由会话（member, 无 project context）也写
    useNewChatContextStore.getState().clear();
    clearLastAgentId();
    newProjectChatSource.onSelect(
      mkItem({ agent: mkAgent({ id: 88 }), isMember: true }),
      mkCtx(),
    );
    expect(readLastAgentId()).toBe(88);
  });

  it("非成员（其它 Agent）→ no-op：不选中、不兜底自由会话、不写 lastAgentId", () => {
    useNewChatContextStore.getState().setContext({
      projectID: 42,
      projectName: "后端重构",
    });
    useNewChatContextStore.getState().setNewSelectionHandler(vi.fn());

    const agent = mkAgent({ id: 9, name: "设计师" });
    const item = mkItem({ agent, isMember: false });
    const ctx = mkCtx();
    newProjectChatSource.onSelect(item, ctx);

    expect(ctx.close).not.toHaveBeenCalled();
    expect(ctx.openNewSession).not.toHaveBeenCalled();
    expect(ctx.navigate).not.toHaveBeenCalled();
    expect(readLastAgentId()).toBeNull();
    // 项目上下文未被清
    expect(useNewChatContextStore.getState().projectContext).not.toBeNull();
  });

  it("上下文在选中与回车之间被清掉 → 退化成自由会话", () => {
    const item = mkItem({ agent: mkAgent({ id: 7 }) });
    const ctx = mkCtx();
    newProjectChatSource.onSelect(item, ctx);

    expect(ctx.openNewSession).toHaveBeenCalledWith(7);
    expect(ctx.navigate).toHaveBeenCalledWith("/chat");
  });

  it("with projectContext + isMember=true → handler called, no navigate", () => {
    const handler = vi.fn();
    useNewChatContextStore.getState().setContext({
      projectID: 42,
      projectName: "后端重构",
    });
    useNewChatContextStore.getState().setNewSelectionHandler(handler);

    const agent = mkAgent({ id: 7, name: "CEO" });
    const item = mkItem({ agent, isMember: true });
    const ctx = mkCtx();
    newProjectChatSource.onSelect(item, ctx);

    expect(ctx.close).toHaveBeenCalledTimes(1);
    expect(handler).toHaveBeenCalledWith(42, agent);
    expect(ctx.navigate).not.toHaveBeenCalled();
    expect(ctx.openNewSession).not.toHaveBeenCalled();
    expect(ctx.openSession).not.toHaveBeenCalled();
  });

  it("isMember=true but handler not registered (init race) → falls back to free chat", () => {
    useNewChatContextStore.getState().setContext({
      projectID: 42,
      projectName: "X",
    });
    // handler intentionally not set

    const item = mkItem({ agent: mkAgent({ id: 7 }), isMember: true });
    const ctx = mkCtx();
    newProjectChatSource.onSelect(item, ctx);

    expect(ctx.openNewSession).toHaveBeenCalledWith(7);
    expect(ctx.navigate).toHaveBeenCalledWith("/chat");
  });

  it("nested project route /projects/42/foo also dispatches as projects", () => {
    const handler = vi.fn();
    useNewChatContextStore.getState().setContext({
      projectID: 42,
      projectName: "X",
    });
    useNewChatContextStore.getState().setNewSelectionHandler(handler);

    const agent = mkAgent({ id: 7 });
    newProjectChatSource.onSelect(mkItem({ agent, isMember: true }), mkCtx());
    expect(handler).toHaveBeenCalledWith(42, agent);
  });

  // R15: 这一行的 chip 只反映 Agent 有序执行目标列表第一档的设备/路径；离线或
  // 没配路径的档现在由 chat_svc.PickExecTarget 在首发 Send 时自动跳过、落到下一个
  // 可用的档，不再由命令面板静默拦截。之前这里 offline / 无 locationPath 会
  // console.warn 后直接 return，用户点了完全没反应；改成始终照常派发。
  it("member agent whose first target is offline still dispatches (R15 falls through to the next target, no silent return)", () => {
    const handler = vi.fn();
    useNewChatContextStore.getState().setContext({
      projectID: 42,
      projectName: "后端重构",
    });
    useNewChatContextStore.getState().setNewSelectionHandler(handler);

    const agent = mkAgent({
      id: 7,
      deviceID: "3",
      deviceName: "构建机",
      online: false,
    });
    const item = mkItem({ agent, isMember: true });
    const ctx = mkCtx();
    newProjectChatSource.onSelect(item, ctx);

    expect(handler).toHaveBeenCalledWith(42, agent);
    expect(readLastAgentId()).toBe(7);
  });

  it("member agent whose first (remote) target has no configured location still dispatches (R15 falls through)", () => {
    const handler = vi.fn();
    useNewChatContextStore.getState().setContext({
      projectID: 42,
      projectName: "后端重构",
    });
    useNewChatContextStore.getState().setNewSelectionHandler(handler);

    const agent = mkAgent({
      id: 7,
      deviceID: "3",
      deviceName: "构建机",
      online: true,
    });
    // locationPath 缺失 = 这台机器没配这个项目的路径。
    const item = mkItem({ agent, isMember: true, locationPath: undefined });
    const ctx = mkCtx();
    newProjectChatSource.onSelect(item, ctx);

    expect(handler).toHaveBeenCalledWith(42, agent);
  });
});
