import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../wailsjs/go/app/App", () => ({
  ListChatAgents: vi.fn(),
}));

import { ListChatAgents } from "../../../wailsjs/go/app/App";
import { useChatAgentsStore } from "../chat-agents-store";
import { useSessionMetaStore } from "../session-meta-store";
import { useSessionStatusStore } from "../session-status-store";
import { useChatStreamsStore } from "../chat-streams-store";

const listChatAgents = ListChatAgents as ReturnType<typeof vi.fn>;

describe("chat-agents-store", () => {
  beforeEach(() => {
    listChatAgents.mockReset();
    useChatAgentsStore.getState().__reset();
    useSessionMetaStore.getState().__reset();
    useSessionStatusStore.getState().__reset();
    useChatStreamsStore.setState({ streams: new Map() });
  });

  it("does not let an idle list snapshot override running while a live stream is active", async () => {
    useSessionStatusStore.getState().upsert(9, {
      agentStatus: "running",
      needsAttention: false,
    });
    useChatStreamsStore.getState().openStream({
      name: "chat:event:9:42",
      sessionId: 9,
      assistantMessageId: 42,
      streamStartedAt: 123,
    });
    listChatAgents.mockResolvedValueOnce({
      agents: [
        {
          id: 1,
          name: "Eng",
          pinned: false,
          chattable: true,
          sessions: [{ id: 9, status: "idle", needsAttention: false }],
        },
      ],
    });

    await useChatAgentsStore.getState().reload();

    expect(useSessionStatusStore.getState().statuses.get(9)?.agentStatus).toBe(
      "running",
    );
  });

  it("reload 拉新数据并写入 agents", async () => {
    listChatAgents.mockResolvedValueOnce({
      agents: [
        { id: 1, name: "Eng", pinned: false, chattable: true, sessions: [] },
      ],
    });
    await useChatAgentsStore.getState().reload();
    const state = useChatAgentsStore.getState();
    expect(state.agents).toHaveLength(1);
    expect(state.agents[0].name).toBe("Eng");
    expect(state.loading).toBe(false);
    expect(state.error).toBeNull();
  });

  it("reload 失败把错误信息写到 error 上, 不抛出", async () => {
    listChatAgents.mockRejectedValueOnce(new Error("boom"));
    await useChatAgentsStore.getState().reload();
    const state = useChatAgentsStore.getState();
    expect(state.error).toBe("boom");
    expect(state.loading).toBe(false);
  });

  it("并发 reload dedupe: 只触发一次 ListChatAgents", async () => {
    let resolveFn: ((v: { agents: unknown[] }) => void) | null = null;
    listChatAgents.mockReturnValueOnce(
      new Promise((res) => {
        resolveFn = res;
      }),
    );
    const a = useChatAgentsStore.getState().reload();
    const b = useChatAgentsStore.getState().reload();
    expect(listChatAgents).toHaveBeenCalledTimes(1);
    resolveFn!({ agents: [] });
    await Promise.all([a, b]);
    expect(useChatAgentsStore.getState().loading).toBe(false);
  });

  it("reload 期间 loading=true, 完成后 loading=false", async () => {
    let resolveFn: ((v: { agents: unknown[] }) => void) | null = null;
    listChatAgents.mockReturnValueOnce(
      new Promise((res) => {
        resolveFn = res;
      }),
    );
    const p = useChatAgentsStore.getState().reload();
    expect(useChatAgentsStore.getState().loading).toBe(true);
    resolveFn!({ agents: [] });
    await p;
    expect(useChatAgentsStore.getState().loading).toBe(false);
  });

  // ── 刷新不churn：侧栏每轮对话起手 / 落定各刷一次 ──────────────────────────
  // 绝大多数轮次这份快照一个字节都没变。换新数组 / 翻 loading 会让所有订阅者
  // (索引页、命令面板三个 source、空态) 白重渲两趟。

  it("Given agents were already loaded, When a refresh returns the same payload, Then the agents array identity is kept so subscribers do not re-render", async () => {
    const payload = () => ({
      agents: [
        {
          id: 1,
          name: "Eng",
          pinned: false,
          chattable: true,
          sessions: [
            {
              id: 9,
              status: "idle",
              needsAttention: false,
              title: "s-9",
              lastMessageAt: 10,
            },
          ],
        },
      ],
    });
    listChatAgents.mockResolvedValueOnce(payload());
    await useChatAgentsStore.getState().reload();
    const first = useChatAgentsStore.getState().agents;

    listChatAgents.mockResolvedValueOnce(payload());
    await useChatAgentsStore.getState().reload();

    expect(useChatAgentsStore.getState().agents).toBe(first);
  });

  it("Given agents were already loaded, When the payload actually changes, Then the new snapshot still lands", async () => {
    listChatAgents.mockResolvedValueOnce({
      agents: [
        { id: 1, name: "Eng", pinned: false, chattable: true, sessions: [] },
      ],
    });
    await useChatAgentsStore.getState().reload();
    const first = useChatAgentsStore.getState().agents;

    listChatAgents.mockResolvedValueOnce({
      agents: [
        {
          id: 1,
          name: "Eng 改名",
          pinned: false,
          chattable: true,
          sessions: [],
        },
      ],
    });
    await useChatAgentsStore.getState().reload();

    expect(useChatAgentsStore.getState().agents).not.toBe(first);
    expect(useChatAgentsStore.getState().agents[0].name).toBe("Eng 改名");
  });

  it("Given agents were already loaded, When the sidebar refreshes, Then loading stays false instead of dragging every subscriber through a loading pass", async () => {
    listChatAgents.mockResolvedValueOnce({ agents: [] });
    await useChatAgentsStore.getState().reload();
    expect(useChatAgentsStore.getState().loading).toBe(false);

    let resolveFn: ((v: { agents: unknown[] }) => void) | null = null;
    listChatAgents.mockReturnValueOnce(
      new Promise((res) => {
        resolveFn = res;
      }),
    );
    const p = useChatAgentsStore.getState().reload();

    expect(useChatAgentsStore.getState().loading).toBe(false);

    resolveFn!({ agents: [] });
    await p;
  });

  it("reload 后再次 reload, 第二次拿到新的快照", async () => {
    listChatAgents.mockResolvedValueOnce({ agents: [] });
    await useChatAgentsStore.getState().reload();
    expect(useChatAgentsStore.getState().agents).toHaveLength(0);

    listChatAgents.mockResolvedValueOnce({
      agents: [
        { id: 2, name: "X", pinned: false, chattable: true, sessions: [] },
      ],
    });
    await useChatAgentsStore.getState().reload();
    expect(useChatAgentsStore.getState().agents).toHaveLength(1);
    expect(useChatAgentsStore.getState().agents[0].id).toBe(2);
  });

  it("响应 null/undefined 的 agents 字段, 兜底成 []", async () => {
    listChatAgents.mockResolvedValueOnce({});
    await useChatAgentsStore.getState().reload();
    expect(useChatAgentsStore.getState().agents).toEqual([]);
  });

  it("reload 后 agents[i].sessionIds 合并 sessions 与 attentionSessions, 避免运行中老会话被 reconcile 清掉", async () => {
    listChatAgents.mockResolvedValueOnce({
      agents: [
        {
          id: 10,
          name: "X",
          avatarColor: "agent-3",
          pinned: false,
          chattable: true,
          sessions: [
            { id: 1, status: "idle", needsAttention: false },
            { id: 2, status: "idle", needsAttention: false },
          ],
          attentionSessions: [
            {
              id: 3,
              title: "blocked",
              status: "running",
              needsAttention: false,
              lastMessageAt: 300,
            },
          ],
        },
      ],
    });
    await useChatAgentsStore.getState().reload();
    const a = useChatAgentsStore.getState().agents[0];
    expect(Array.isArray(a.sessionIds)).toBe(true);
    expect(new Set(a.sessionIds)).toEqual(new Set([1, 2, 3]));
    expect(useSessionStatusStore.getState().statuses.get(3)?.agentStatus).toBe(
      "running",
    );
    expect(useSessionMetaStore.getState().metas.get(3)).toMatchObject({
      agentId: 10,
      title: "blocked",
      lastMessageAt: 300,
    });
  });

  it("Given backend returns full sessionIds, When recent sessions are truncated, Then reload preserves all ids for tab reconcile", async () => {
    listChatAgents.mockResolvedValueOnce({
      agents: [
        {
          id: 10,
          name: "X",
          avatarColor: "agent-3",
          pinned: false,
          chattable: true,
          sessionIds: [1, 2, 3, 4, 5, 6],
          sessions: [
            { id: 1, status: "idle", needsAttention: false },
            { id: 2, status: "idle", needsAttention: false },
            { id: 3, status: "idle", needsAttention: false },
            { id: 4, status: "idle", needsAttention: false },
            { id: 5, status: "idle", needsAttention: false },
          ],
          attentionSessions: [],
        },
      ],
    });

    await useChatAgentsStore.getState().reload();

    expect(useChatAgentsStore.getState().agents[0].sessionIds).toEqual([
      1, 2, 3, 4, 5, 6,
    ]);
  });

  it("reload 把 sessions 的静态字段灌到 session-meta-store", async () => {
    // ChatSessionLite 不含 projectId, 所以这里不期望 meta.projectId 被设置。
    // projectId 由 useChatSession 在 LoadChatSession 后通过 setMeta 补全。
    listChatAgents.mockResolvedValueOnce({
      agents: [
        {
          id: 10,
          name: "X",
          avatarColor: "agent-3",
          pinned: false,
          chattable: true,
          sessions: [
            {
              id: 1,
              title: "hello",
              lastMessageAt: 1234,
              status: "idle",
              needsAttention: false,
            },
          ],
          attentionSessions: [],
        },
      ],
    });
    useSessionMetaStore.getState().__reset();
    await useChatAgentsStore.getState().reload();
    const meta = useSessionMetaStore.getState().metas.get(1);
    expect(meta).toMatchObject({
      agentId: 10,
      agentColor: "agent-3",
      agentName: "X",
      title: "hello",
      lastMessageAt: 1234,
    });
    expect(meta?.projectId).toBeUndefined();
  });

  it("Given the list payload carries projectId, When reload runs, Then it lands in the meta store", async () => {
    // ChatSessionLite 现在带 projectId（0 = 未挂项目）。单一会话索引按项目分组、以及
    // 按 Agent 分组时行首那个项目色字形（决策 4）都要它 —— 此前它只有会话详情才有，
    // 侧栏永远拿不到。
    listChatAgents.mockResolvedValueOnce({
      agents: [
        {
          id: 1,
          name: "Eng",
          pinned: false,
          chattable: true,
          sessions: [
            { id: 1, status: "idle", projectId: 7 },
            { id: 2, status: "idle", projectId: 0 },
          ],
        },
      ],
    });

    await useChatAgentsStore.getState().reload();

    const metas = useSessionMetaStore.getState().metas;
    expect(metas.get(1)?.projectId).toBe(7);
    expect(metas.get(2)?.projectId).toBe(0);
  });

  it("bulkUpsert merge 不会清掉 useChatSession 之前写入的 projectId", async () => {
    useSessionMetaStore.getState().__reset();
    // 模拟: useChatSession 先 setMeta(完整含 projectId=7)
    useSessionMetaStore.getState().setMeta(1, {
      agentId: 10,
      agentName: "X",
      agentColor: "agent-3",
      projectId: 7,
      title: "hello",
      lastMessageAt: 100,
    });
    // 然后 ListChatAgents reload, bulkUpsert 不带 projectId
    listChatAgents.mockResolvedValueOnce({
      agents: [
        {
          id: 10,
          name: "X",
          avatarColor: "agent-3",
          pinned: false,
          chattable: true,
          sessions: [
            {
              id: 1,
              title: "hello",
              lastMessageAt: 200,
              status: "idle",
              needsAttention: false,
            },
          ],
          attentionSessions: [],
        },
      ],
    });
    await useChatAgentsStore.getState().reload();
    const meta = useSessionMetaStore.getState().metas.get(1);
    expect(meta?.projectId).toBe(7); // 关键: 没被清掉
    expect(meta?.lastMessageAt).toBe(200); // reload 带来的新值仍然生效
  });
});
