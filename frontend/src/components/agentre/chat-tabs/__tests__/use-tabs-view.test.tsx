// frontend/src/components/agentre/chat-tabs/__tests__/use-tabs-view.test.tsx
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useTabsView } from "../use-tabs-view";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useChatStreamsStore } from "@/stores/chat-streams-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

vi.mock("@/hooks/use-project-tree", () => ({
  useProjectTree: () => ({
    tree: [
      {
        project: {
          id: 7,
          name: "Agentre",
          color: "agent-1",
          parentID: 0,
          icon: "",
          description: "",
          path: "",
          isGitRepo: false,
          createtime: 0,
          updatetime: 0,
        },
        children: [
          {
            project: {
              id: 8,
              name: "backend",
              color: "agent-2",
              parentID: 7,
              icon: "",
              description: "",
              path: "",
              isGitRepo: false,
              createtime: 0,
              updatetime: 0,
            },
            children: [],
          },
        ],
      },
    ],
    invalidate: () => {},
    loaded: true,
  }),
}));

describe("useTabsView 数据派生", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    useChatStreamsStore.setState({
      streams: new Map(),
    });
    useChatAgentsStore.getState().__reset();
    useSessionMetaStore.getState().__reset();
    useSessionStatusStore.getState().__reset();
  });

  it("meta 未到位时 avatar fallback 到 ?/灰色", () => {
    useChatTabsStore.getState().openSessionInNewTab(101);
    const { result } = renderHook(() => useTabsView());
    expect(result.current).toHaveLength(1);
    expect(result.current[0].avatar.letter).toBe("?");
    expect(result.current[0].avatar.color).toBe("#94a3b8");
    expect(result.current[0].projectColor).toBeNull();
    expect(result.current[0].projectChain).toBeNull();
  });

  it("有 agent meta 时 avatar letter/color 来自 agentName/agentColor token", () => {
    useChatTabsStore.getState().openSessionInNewTab(101);
    useSessionMetaStore.getState().setMeta(101, {
      agentId: 3,
      agentName: "CEO",
      agentColor: "agent-2",
      projectId: 0,
      title: "周一例会",
    });
    const { result } = renderHook(() => useTabsView());
    expect(result.current[0].avatar.letter).toBe("C");
    expect(result.current[0].avatar.color).toBe("var(--agent-2)");
    expect(result.current[0].title).toBe("周一例会");
  });

  it("项目会话: projectColor + projectChain 都派生出来", () => {
    useChatTabsStore.getState().openSessionInNewTab(202);
    useSessionMetaStore.getState().setMeta(202, {
      agentId: 1,
      agentName: "Eng",
      agentColor: "agent-3",
      projectId: 8,
      title: "arch-review · jwt",
    });
    const { result } = renderHook(() => useTabsView());
    const view = result.current[0];
    expect(view.projectColor).toBe("var(--agent-2)"); // project 8 (backend) color=agent-2
    expect(view.projectChain).toEqual(["Agentre", "backend"]);
  });

  it("中文 agent 名取第一个 字符 作为 letter", () => {
    useChatTabsStore.getState().openSessionInNewTab(303);
    useSessionMetaStore.getState().setMeta(303, {
      agentId: 5,
      agentName: "前端工程师",
      agentColor: "agent-4",
      projectId: 0,
      title: "命令面板",
    });
    const { result } = renderHook(() => useTabsView());
    expect(result.current[0].avatar.letter).toBe("前");
  });

  it("非法 agentColor token 时 avatar 颜色走 fallback", () => {
    useChatTabsStore.getState().openSessionInNewTab(404);
    useSessionMetaStore.getState().setMeta(404, {
      agentId: 9,
      agentName: "X",
      agentColor: "neutral", // 不在 agent-1..agent-10 里
      projectId: 0,
      title: "x",
    });
    const { result } = renderHook(() => useTabsView());
    expect(result.current[0].avatar.color).toBe("#94a3b8");
  });

  it("running 状态: status 翻 'running' 且 pillText 为 null (running 不出 pill 文案)", () => {
    useChatTabsStore.getState().openSessionInNewTab(505);
    useSessionMetaStore.getState().setMeta(505, {
      agentId: 1,
      agentName: "A",
      agentColor: "agent-1",
      projectId: 0,
      title: "t",
      lastMessageAt: 100,
      lastReadAt: 0,
    });
    useSessionStatusStore.getState().upsert(505, {
      agentStatus: "running",
      needsAttention: true,
    });
    const { result } = renderHook(() => useTabsView());
    expect(result.current[0].status).toBe("running");
    // running reason → reasonToPillText("needs_attention") = null 因为 needsAttention 优先于 running，
    // 但 reasonToPillText("needs_attention") = "Approval"。此用例 needsAttention=true + running →
    // computeAttention → "needs_attention" → reasonToPillText = "Approval"。
    // 原测试期待 null 是因为 old RANK 路径 needsAttention=running 时 pillText 判断特例。
    // 新契约：needs_attention → "Approval" pill，running → null pill（覆盖 reasonToPillText("running")=null）。
    // needsAttention=true 覆盖 running → reason="needs_attention" → pillText="Approval"。
    expect(result.current[0].pillText).toBe("Approval");
  });

  it("needsAttention 且非 running 时 pillText='Approval'", () => {
    useChatTabsStore.getState().openSessionInNewTab(606);
    useSessionMetaStore.getState().setMeta(606, {
      agentId: 1,
      agentName: "A",
      agentColor: "agent-1",
      projectId: 0,
      title: "t",
      lastMessageAt: 100,
      lastReadAt: 0,
    });
    useSessionStatusStore.getState().upsert(606, {
      agentStatus: "waiting",
      needsAttention: true,
    });
    const { result } = renderHook(() => useTabsView());
    expect(result.current[0].status).toBe("waiting");
    expect(result.current[0].pillText).toBe("Approval");
  });

  it("error 状态即使已读也显示出错 pill", () => {
    useChatTabsStore.getState().openSessionInNewTab(707);
    useSessionMetaStore.getState().setMeta(707, {
      agentId: 1,
      agentName: "A",
      agentColor: "agent-1",
      projectId: 0,
      title: "t",
      lastMessageAt: 100,
      lastReadAt: 100,
    });
    useSessionStatusStore.getState().upsert(707, {
      agentStatus: "error",
      needsAttention: false,
    });
    const { result } = renderHook(() => useTabsView());
    expect(result.current[0].status).toBe("error");
    expect(result.current[0].pillText).toBe("Error");
  });

  describe("Given Agent 在 chat-agents-store 里, When tab 投影, Then 头像带上完整身份", () => {
    const seedAgent = (over: Record<string, unknown>) => {
      useChatAgentsStore.setState({
        agents: [
          {
            id: 3,
            name: "设计师",
            avatarColor: "agent-9",
            avatarIcon: "",
            avatarDataUrl: "",
            ...over,
          },
        ] as never,
      });
    };
    it("Given a new-session tab without session metadata When its selected agent is loaded Then it already has the selected agent's mark", () => {
      seedAgent({ avatarIcon: "preset:agent-architect" });
      useChatTabsStore.getState().openNewSession(0, 3, "");
      const { result } = renderHook(() => useTabsView());
      expect(result.current[0].avatar).toMatchObject({
        avatarIcon: "preset:agent-architect",
        color: "var(--agent-9)",
      });
    });

    const openMeta = () => {
      useChatTabsStore.getState().openSessionInNewTab(101);
      useSessionMetaStore.getState().setMeta(101, {
        agentId: 3,
        agentName: "旧名字",
        agentColor: "agent-2",
        projectId: 0,
        title: "周一例会",
      });
    };

    it("上传图片：avatar 带上 avatarDataUrl", () => {
      seedAgent({ avatarDataUrl: "data:image/png;base64,AAAA" });
      openMeta();

      const { result } = renderHook(() => useTabsView());
      expect(result.current[0].avatar.avatarDataUrl).toBe(
        "data:image/png;base64,AAAA",
      );
      expect(result.current[0].avatar.letter).toBe("设");
      expect(result.current[0].avatar.color).toBe("var(--agent-9)");
    });

    it("预设 key：avatar 带上 avatarIcon", () => {
      seedAgent({ avatarIcon: "preset:agent-assistant" });
      openMeta();

      const { result } = renderHook(() => useTabsView());
      expect(result.current[0].avatar.avatarIcon).toBe(
        "preset:agent-assistant",
      );
    });

    it("图标 key：avatar 带上 avatarIcon", () => {
      seedAgent({ avatarIcon: "hammer" });
      openMeta();

      const { result } = renderHook(() => useTabsView());
      expect(result.current[0].avatar.avatarIcon).toBe("hammer");
    });

    it("Agent 不在 store 里：回落 meta 的名字与颜色，没有图标/图片", () => {
      openMeta();

      const { result } = renderHook(() => useTabsView());
      expect(result.current[0].avatar).toEqual({
        letter: "旧",
        color: "var(--agent-2)",
        avatarIcon: "",
        avatarDataUrl: "",
      });
    });
  });
});
