import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { useChatSession } from "./use-chat-session";
import type { ChatMessage } from "./use-chat-session";
import { deriveBackgroundTasks } from "@/components/agentre/background-tasks/derive";
import { deriveOutline } from "@/components/agentre/chat-context-sidebar/derive";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionReadStore } from "@/stores/session-read-store";
import { useSessionStatusStore } from "@/stores/session-status-store";
import {
  hasSessionStream,
  streamForMessage,
  useChatStreamsStore,
} from "@/stores/chat-streams-store";

vi.mock("../../wailsjs/go/app/App", () => ({
  LoadChatSession: vi.fn(),
  LoadChatMessageBlocks: vi.fn(),
  LoadChatSessionBlocksByType: vi.fn(),
  MarkChatSessionRead: vi.fn().mockResolvedValue(undefined),
}));
import {
  LoadChatMessageBlocks,
  LoadChatSession,
  LoadChatSessionBlocksByType,
  MarkChatSessionRead,
} from "../../wailsjs/go/app/App";
const loadChatSession = LoadChatSession as ReturnType<typeof vi.fn>;
const loadChatMessageBlocks = LoadChatMessageBlocks as ReturnType<typeof vi.fn>;
const loadChatSessionBlocksByType = LoadChatSessionBlocksByType as ReturnType<
  typeof vi.fn
>;
const markChatSessionRead = MarkChatSessionRead as ReturnType<typeof vi.fn>;

describe("useChatSession", () => {
  beforeEach(() => {
    loadChatSession.mockReset();
    loadChatMessageBlocks.mockReset();
    loadChatSessionBlocksByType.mockReset();
    markChatSessionRead.mockClear();
    useSessionStatusStore.getState().__reset();
    useSessionMetaStore.getState().__reset();
    // session-read-store 没有 __reset,直接重建 Map(单调推进语义保证不会被旧值污染,
    // 但跨用例 override 残留会影响 "no override" 类断言)。
    useSessionReadStore.setState({ overrides: new Map() });
    useChatStreamsStore.setState({ streams: new Map() });
  });

  // Bug: 自主轮/subagent 子轮(及任何非前端发起的 turn)在中途打开会话时,前端没有 per-turn
  // 流入口 → 看不到"生成中"和流式内容。修复:LoadSession 在有活跃 turn 时回传
  // activeStream;hook 据此 openStream 重挂实时流。
  it("reattaches live stream on load when activeStream is present", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        activeStream: "chat:event:9:42",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        { id: 40, sessionId: 9, role: "user", blocks: [], seq: 1 },
        { id: 42, sessionId: 9, role: "assistant", blocks: [], seq: 2 },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    const live = streamForMessage(useChatStreamsStore.getState(), 9, 42);
    expect(live?.name).toBe("chat:event:9:42");
    expect(live?.assistantMessageId).toBe(42);
  });

  // 「块全是 notice」不足以判定旁白行:回退 notice 是后端追加进**这一轮自己**的
  // assistant 消息(chat_svc runTurn finalize),零内容收尾时那条消息的块正好只剩它。
  // 判据必须是「独立落库的切换 notice」(noticeKind==="switch"),与后端
  // chat.go noticeOnlyMessage 同一口径 —— 否则这一轮被当旁白行跳过,压根不 openStream,
  // 用户看不到任何流式内容。
  it("reattaches to a turn whose only block is a provider fallback notice", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        activeStream: "chat:event:9:41",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        { id: 40, sessionId: 9, role: "user", blocks: [], seq: 1 },
        {
          id: 41,
          sessionId: 9,
          role: "assistant",
          blocks: [
            { type: "notice", level: "info", providerKey: "gone-provider" },
          ],
          seq: 2,
        },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    const live = streamForMessage(useChatStreamsStore.getState(), 9, 41);
    expect(live?.assistantMessageId).toBe(41);
  });

  // Bug: 中途重开运行中的会话时,pending tool_approval 卡来自 LoadSession overlay
  // (后端把内存 pending 块 overlay 进末条 assistant 消息投影 → 渲染走 messages 路径)。
  // 用户点批准/拒绝后 resolved 事件只反扫 liveBlocks → no-op → 卡片永远 pending。
  // 修复:reattach 时把 pending tool_approval 块从消息副本剥离并搬进 liveBlocks
  // (单一真相源),resolved 事件自然命中,且不与消息路径双卡。
  it("moves overlay pending tool_approval blocks into liveBlocks on reattach", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        activeStream: "chat:event:9:42",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        { id: 40, sessionId: 9, role: "user", blocks: [], seq: 1 },
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          blocks: [
            { type: "text", text: "creating department..." },
            {
              type: "tool_approval",
              toolApproval: {
                requestId: "org-1",
                toolName: "org_create_department",
                toolInput: { name: "研发部" },
                status: "pending",
              },
            },
          ],
          seq: 2,
        },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    // ① 消息路径不再含该 pending 块(防双卡);其余块保留。
    const lastMsg = result.current.messages.at(-1)!;
    expect((lastMsg.blocks ?? []).some((b) => b.type === "tool_approval")).toBe(
      false,
    );
    expect((lastMsg.blocks ?? []).some((b) => b.type === "text")).toBe(true);

    // ② live store 里出现该块,挂在重挂流上。
    const live = streamForMessage(useChatStreamsStore.getState(), 9, 42);
    expect(live?.assistantMessageId).toBe(42);
    expect(live?.liveBlocks).toHaveLength(1);
    expect(live?.liveBlocks[0]).toMatchObject({
      type: "tool_approval",
      toolApproval: { requestId: "org-1", status: "pending" },
    });

    // ③ resolved 事件现在命中 liveBlocks,卡片翻 approved。
    act(() => {
      useChatStreamsStore.getState().markToolApprovalResolved(9, 42, {
        toolKey: "org",
        requestId: "org-1",
        toolName: "org_create_department",
        status: "approved",
        result: "department created",
      });
    });
    const updated = streamForMessage(useChatStreamsStore.getState(), 9, 42)!
      .liveBlocks[0];
    expect(updated.toolApproval).toMatchObject({
      status: "approved",
      result: "department created",
    });
  });

  // 供应商切换 notice 是一条独立的 assistant 消息(只有一个 notice 块),排在在跑的
  // assistant 之后。轮中切换供应商 → chat-panel reloadSession() → 这条 notice 成了末条
  // assistant。若"末条 assistant"按行取,overlay 的 pending 审批就落在 notice 那条空
  // 消息上找不到 → 既没从 messages 剥离也没搬进 liveBlocks → 用户点批准后 resolved
  // 事件反扫 liveBlocks 落空 → 卡片永远 pending。找的必须是末条**真实** assistant。
  it("moves overlay pending tool_approval blocks into liveBlocks even when a provider notice trails", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        activeStream: "chat:event:9:42",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        { id: 40, sessionId: 9, role: "user", blocks: [], seq: 1 },
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          blocks: [
            { type: "text", text: "creating department..." },
            {
              type: "tool_approval",
              toolApproval: {
                requestId: "org-1",
                toolName: "org_create_department",
                toolInput: { name: "研发部" },
                status: "pending",
              },
            },
          ],
          seq: 2,
        },
        {
          id: 43,
          sessionId: 9,
          role: "assistant",
          blocks: [
            {
              type: "notice",
              level: "info",
              noticeKind: "switch",
              providerKey: "session-key",
              providerName: "中转 · GLM 5.2",
            },
          ],
          seq: 3,
        },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    // ① pending 块从真正持有它的那条消息(42)上剥离,notice 那条不受影响。
    const owner = result.current.messages.find((m) => m.id === 42)!;
    expect((owner.blocks ?? []).some((b) => b.type === "tool_approval")).toBe(
      false,
    );
    expect((owner.blocks ?? []).some((b) => b.type === "text")).toBe(true);

    // ② 搬进的是 42 的 liveBlocks —— resolved 事件按 assistantMessageId 反扫才命中。
    const live = streamForMessage(useChatStreamsStore.getState(), 9, 42);
    expect(live?.liveBlocks).toHaveLength(1);
    expect(live?.liveBlocks[0]).toMatchObject({
      type: "tool_approval",
      toolApproval: { requestId: "org-1", status: "pending" },
    });
  });

  // 已决议(resolved)的 tool_approval 块是持久化历史,留在 messages 路径,不搬不剥。
  it("leaves resolved tool_approval blocks in messages untouched on reattach", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        activeStream: "chat:event:9:42",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          blocks: [
            {
              type: "tool_approval",
              toolApproval: {
                requestId: "org-0",
                toolName: "org_delete_agent",
                status: "approved",
                result: "done",
              },
            },
          ],
          seq: 1,
        },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    const lastMsg = result.current.messages.at(-1)!;
    expect(
      (lastMsg.blocks ?? []).some(
        (b) =>
          b.type === "tool_approval" && b.toolApproval?.status === "approved",
      ),
    ).toBe(true);
    expect(
      streamForMessage(useChatStreamsStore.getState(), 9, 42)?.liveBlocks,
    ).toEqual([]);
  });

  // 同 tab 已有活跃流且 liveBlocks 已含同 requestId(流事件路径已写入)时,
  // mid-turn reload 返回的 overlay 块仍要从 messages 剥离,但不得重复注入。
  it("dedupes overlay pending tool_approval against an existing live block", async () => {
    act(() => {
      useChatStreamsStore.getState().openStream({
        name: "chat:event:9:42",
        sessionId: 9,
        assistantMessageId: 42,
        streamStartedAt: 123,
      });
      useChatStreamsStore.getState().appendLiveToolApproval(9, 42, {
        toolKey: "org",
        requestId: "org-1",
        toolName: "org_create_department",
        status: "pending",
      });
    });
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        activeStream: "chat:event:9:42",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          blocks: [
            {
              type: "tool_approval",
              toolApproval: {
                requestId: "org-1",
                toolName: "org_create_department",
                status: "pending",
              },
            },
          ],
          seq: 1,
        },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    const lastMsg = result.current.messages.at(-1)!;
    expect((lastMsg.blocks ?? []).some((b) => b.type === "tool_approval")).toBe(
      false,
    );
    const live = streamForMessage(useChatStreamsStore.getState(), 9, 42);
    expect(live?.liveBlocks).toHaveLength(1);
  });

  it("Given a reattached OpenClaw turn, When LoadSession overlays a pending exec approval, Then it moves to the live stream exactly once", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "waiting",
        activeStream: "chat:event:9:42",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          blocks: [
            { type: "text", text: "waiting for operator" },
            {
              type: "exec_approval",
              execApproval: {
                id: "exec-1",
                commandText: "pwd",
                allowedDecisions: ["allow-once", "deny"],
                status: "pending",
              },
            },
          ],
          seq: 1,
        },
      ],
    });

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(
      (result.current.messages.at(-1)?.blocks ?? []).some(
        (block) => block.type === "exec_approval",
      ),
    ).toBe(false);
    const live = streamForMessage(useChatStreamsStore.getState(), 9, 42);
    expect(live?.liveBlocks).toHaveLength(1);
    expect(live?.liveBlocks[0]).toMatchObject({
      type: "exec_approval",
      execApproval: { id: "exec-1", status: "pending" },
    });
  });

  it("Given resolved OpenClaw approval history, When the session reloads, Then it remains persisted and is not moved into live state", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        activeStream: "chat:event:9:42",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          blocks: [
            {
              type: "exec_approval",
              execApproval: {
                id: "exec-1",
                commandText: "pwd",
                status: "resolved",
                decision: "allow-once",
              },
            },
          ],
          seq: 1,
        },
      ],
    });

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.messages.at(-1)?.blocks?.[0]).toMatchObject({
      type: "exec_approval",
      execApproval: { id: "exec-1", status: "resolved" },
    });
    expect(
      streamForMessage(useChatStreamsStore.getState(), 9, 42)?.liveBlocks,
    ).toEqual([]);
  });

  // Bug(sess-3396):转录的契约是「持久化正文 ++ liveBlocks」,liveBlocks 只装还没
  // 落库的尾巴。但后端在轮内就把已发出的块整段 replaceBlocks 落库了,轮中途的一次
  // reload(切走会话再切回来、停子代理、旁路流收尾…)会把 liveBlocks 已经持有的那
  // 一段又当成持久化正文发回来 —— 整轮画两遍。重复的 tool_use 还会算出同一个
  // uiStateKey,虚拟行撞 key 后测量缓存互相覆盖,行位置错乱、中间空出一大片。
  // 修复:一条消息只要还有活跃 LiveStream,它的持久化正文就冻在开流那一刻。
  it("freezes a live message's persisted blocks while its stream is open", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        { id: 42, sessionId: 9, role: "assistant", blocks: [], seq: 1 },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    // 用户发起的那一轮:开流,内容一路进 liveBlocks。
    act(() => {
      useChatStreamsStore.getState().openStream({
        name: "chat:event:9:42",
        sessionId: 9,
        assistantMessageId: 42,
        streamStartedAt: 123,
      });
      useChatStreamsStore.getState().appendLiveToolUse(9, 42, {
        type: "tool_use",
        toolUseId: "toolu_live",
        toolName: "Bash",
      } as never);
    });

    // 轮内 reload:后端此刻已经把同一批块落库了。
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        activeStream: "chat:event:9:42",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          seq: 1,
          blocks: [
            { type: "text", text: "已经落库的正文" },
            { type: "tool_use", toolUseId: "toolu_live", toolName: "Bash" },
          ],
        },
      ],
    });
    await act(async () => {
      await result.current.reload();
    });

    expect(result.current.messages.find((m) => m.id === 42)?.blocks).toEqual(
      [],
    );
    expect(
      streamForMessage(useChatStreamsStore.getState(), 9, 42)?.liveBlocks,
    ).toHaveLength(1);
  });

  // 反向守卫:本次 load 才第一次挂上流的消息(reattach 分支),这份快照就是「开流
  // 那一刻」的样子 —— 必须原样收下,否则中途打开在跑的会话就只剩一片空白。
  it("keeps the fresh snapshot for a stream this load just reattached", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        activeStream: "chat:event:9:42",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          seq: 1,
          blocks: [{ type: "text", text: "轮内已落库的正文" }],
        },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(
      result.current.messages.find((m) => m.id === 42)?.blocks,
    ).toHaveLength(1);
  });

  it("does not reattach when activeStream is absent", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        { id: 42, sessionId: 9, role: "assistant", blocks: [], seq: 1 },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(hasSessionStream(useChatStreamsStore.getState(), 9)).toBe(false);
  });

  it("does not clobber an already-open live stream", async () => {
    // 模拟用户在自己会话里正常 Send 已经 openStream;reload 不得覆盖它。
    act(() => {
      useChatStreamsStore.getState().openStream({
        name: "chat:event:9:1",
        sessionId: 9,
        assistantMessageId: 1,
        streamStartedAt: 123,
      });
    });
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "running",
        activeStream: "chat:event:9:99",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        { id: 99, sessionId: 9, role: "assistant", blocks: [], seq: 1 },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    const live = streamForMessage(useChatStreamsStore.getState(), 9, 1);
    expect(live?.name).toBe("chat:event:9:1");
    expect(live?.assistantMessageId).toBe(1);
  });

  it("does not let an idle detail snapshot override running while a live stream is active", async () => {
    act(() => {
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
    });
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        { id: 42, sessionId: 9, role: "assistant", blocks: [], seq: 1 },
      ],
    });

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.session?.agentStatus).toBe("running");
    expect(useSessionStatusStore.getState().statuses.get(9)?.agentStatus).toBe(
      "running",
    );
  });

  it("Given a session load started before a fast turn finished, When the stale empty snapshot resolves after done, Then it reloads the persisted final blocks", async () => {
    let resolveStale!: (value: unknown) => void;
    const stale = new Promise((resolve) => {
      resolveStale = resolve;
    });
    loadChatSession.mockReturnValueOnce(stale).mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Pi",
        title: "read marker",
        agentStatus: "idle",
        lastMessageAt: 2,
        createtime: 0,
      },
      messages: [
        { id: 40, sessionId: 9, role: "user", blocks: [], seq: 1 },
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          blocks: [
            { type: "tool_use", toolUseId: "tool-1", toolName: "read" },
            {
              type: "tool_result",
              toolUseId: "tool-1",
              content: "AGENTRE_DEEPSEEK_V4_PI_20260805",
            },
          ],
          seq: 2,
        },
      ],
    });
    act(() => {
      useChatStreamsStore.getState().openStream({
        name: "chat:event:9:42",
        sessionId: 9,
        assistantMessageId: 42,
        streamStartedAt: 123,
      });
    });

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(loadChatSession).toHaveBeenCalledOnce());

    act(() => {
      useChatStreamsStore.getState().finishStream(9, 42, {
        kind: "done",
      });
      resolveStale({
        session: {
          id: 9,
          agentId: 1,
          agentName: "Pi",
          title: "read marker",
          agentStatus: "running",
          activeStream: "chat:event:9:42",
          lastMessageAt: 1,
          createtime: 0,
        },
        messages: [
          { id: 40, sessionId: 9, role: "user", blocks: [], seq: 1 },
          { id: 42, sessionId: 9, role: "assistant", blocks: [], seq: 2 },
        ],
      });
    });

    await waitFor(() => expect(loadChatSession).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(result.current.messages.at(-1)?.blocks).toEqual([
        expect.objectContaining({ type: "tool_use" }),
        expect.objectContaining({
          type: "tool_result",
          content: "AGENTRE_DEEPSEEK_V4_PI_20260805",
        }),
      ]),
    );
  });

  // Bug(sess-2916):LoadSession 是异步 DB 快照,自主续轮可能在它在途时起手 ——
  // chat-panel 的 onAutonomousEvent 收到 autonomous_started 后 setMessages 插入
  // 新 assistant 行,几十毫秒后那份**更旧**的快照 resolve,整表覆盖把行抹掉。
  // 行没了,挂在该 assistantMessageId 上的 liveBlocks 就再没有宿主(transcript-rows
  // 的 buildRows 只遍历 displayMessages),后续到达的审批卡 / 流式正文全部静默丢弃:
  // 会话胶囊翻「审批」(agentStatus 有 normalizeSessionSnapshot 挡旧快照),转录里
  // 却一张卡都没有。doneTick 守卫只认 turn **结束**,起手这一侧完全没护栏。
  it("Given a session load in flight, When an autonomous turn inserts its assistant row, Then the older snapshot does not drop it", async () => {
    let resolveStale!: (value: unknown) => void;
    const stale = new Promise((resolve) => {
      resolveStale = resolve;
    });
    loadChatSession.mockReturnValueOnce(stale);

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(loadChatSession).toHaveBeenCalledOnce());

    // 自主续轮起手:chat-panel 就地插入这一轮的 assistant 行并 openStream。
    act(() => {
      result.current.setMessages((prev) => [
        ...prev,
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          blocks: [],
          seq: 2,
        } as unknown as ChatMessage,
      ]);
    });

    // 这份快照是后端在 42 落库**之前**取走的,天然不含它。
    act(() => {
      resolveStale({
        session: {
          id: 9,
          agentId: 1,
          agentName: "Eng",
          title: "x",
          agentStatus: "running",
          activeStream: "chat:event:9:42",
          lastMessageAt: 0,
          createtime: 0,
        },
        messages: [{ id: 40, sessionId: 9, role: "user", blocks: [], seq: 1 }],
      });
    });

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.messages.map((m) => m.id)).toEqual([40, 42]);
  });

  // 边界:只保留「本次 load 在途期间新插入」的行。发起 load 时就已在表里、而快照
  // 不含的行是真被后端删了(编辑 / 重跑会截断后续消息),必须跟着快照消失 ——
  // 否则修复会把被截断的历史复活。
  it("Given rows the backend truncated, When the reload snapshot omits them, Then they are dropped", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        { id: 40, sessionId: 9, role: "user", blocks: [], seq: 1 },
        { id: 41, sessionId: 9, role: "assistant", blocks: [], seq: 2 },
        { id: 42, sessionId: 9, role: "user", blocks: [], seq: 3 },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.messages).toHaveLength(3));

    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [{ id: 40, sessionId: 9, role: "user", blocks: [], seq: 1 }],
    });
    await act(async () => {
      await result.current.reload();
    });

    expect(result.current.messages.map((m) => m.id)).toEqual([40]);
  });

  // 转录的行缓存是 WeakMap<消息对象, 行[]>(agentre-ui transcript-rows),键就是消息
  // 对象本身。每次 turn 收尾都 reload 全量历史,若整表换成新 JSON 对象,缓存全表
  // miss、行级 memo 全被击穿 —— 用户看到的就是「结束时整段转录重刷一遍」。
  // 内容没变的消息必须原样保留旧对象引用;整表都没变时连数组引用一起保留。
  it("Given a reload whose snapshot is unchanged, When it lands, Then every message keeps its object identity so the transcript row cache survives", async () => {
    const snapshot = () => ({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "user",
          blocks: [{ type: "text", text: "hi" }],
          seq: 1,
        },
        {
          id: 41,
          sessionId: 9,
          role: "assistant",
          blocks: [{ type: "text", text: "yo" }],
          seq: 2,
        },
      ],
    });
    // 每次 IPC 都是一份全新的对象树 —— mockResolvedValue 复用同一份会让测试假绿。
    loadChatSession.mockImplementation(() =>
      Promise.resolve(structuredClone(snapshot())),
    );

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.messages).toHaveLength(2));
    const before = result.current.messages;

    await act(async () => {
      await result.current.reload();
    });

    expect(result.current.messages[0]).toBe(before[0]);
    expect(result.current.messages[1]).toBe(before[1]);
    expect(result.current.messages).toBe(before);
  });

  // 反面:内容真变了的那条必须换成新对象,否则缓存会把旧行一直钉在屏幕上。
  it("Given one message whose blocks changed, When the reload lands, Then only that message gets a new object while its neighbours keep theirs", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "user",
          blocks: [{ type: "text", text: "hi" }],
          seq: 1,
        },
        { id: 41, sessionId: 9, role: "assistant", blocks: [], seq: 2 },
      ],
    });
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.messages).toHaveLength(2));
    const before = result.current.messages;

    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 9,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "user",
          blocks: [{ type: "text", text: "hi" }],
          seq: 1,
        },
        {
          id: 41,
          sessionId: 9,
          role: "assistant",
          blocks: [{ type: "text", text: "final" }],
          seq: 2,
        },
      ],
    });
    await act(async () => {
      await result.current.reload();
    });

    expect(result.current.messages[0]).toBe(before[0]);
    expect(result.current.messages[1]).not.toBe(before[1]);
    expect(result.current.messages[1].blocks?.[0]).toMatchObject({
      text: "final",
    });
  });

  it("returns null when sessionId is 0", async () => {
    const { result } = renderHook(() => useChatSession(0));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.session).toBeNull();
    expect(loadChatSession).not.toHaveBeenCalled();
  });

  it("loads session when sessionId changes", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 5,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [],
    });
    const { result } = renderHook(() => useChatSession(5));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.session?.id).toBe(5);
  });

  // 被动 ExitPlanMode 流程：CLI 自己切到 default 之后后端推
  // session_status 带 permissionMode，前端 hook 必须 overlay 到 session 上，
  // pill 才能跟着变。无 permissionMode 时不动 session.permissionMode（避免污染）。
  it("overlays session-status-store.permissionMode onto session when patch carries new mode", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 7,
        agentId: 1,
        agentName: "Claude",
        title: "x",
        agentStatus: "running",
        permissionMode: "plan",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [],
    });
    const { result } = renderHook(() => useChatSession(7));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.session?.permissionMode).toBe("plan");

    act(() => {
      useSessionStatusStore.getState().upsert(7, {
        agentStatus: "running",
        needsAttention: false,
        permissionMode: "default",
      });
    });

    await waitFor(() =>
      expect(result.current.session?.permissionMode).toBe("default"),
    );
  });

  // Bug 3: useChatSession.reload 的 setMeta 必须保留 resp.session.lastReadAt,
  // 否则会擦掉 chat-agents-store.bulkUpsert 之前写入的服务端 lastReadAt。
  it("writes resp.session.lastReadAt into session-meta-store", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 42,
        agentId: 1,
        agentName: "Eng",
        agentColor: "agent-1",
        projectId: 5,
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 2000,
        lastReadAt: 1500,
        createtime: 0,
      },
      messages: [],
    });
    const { result } = renderHook(() => useChatSession(42));
    await waitFor(() => expect(result.current.loading).toBe(false));

    const meta = useSessionMetaStore.getState().metas.get(42);
    expect(meta?.lastReadAt).toBe(1500);
  });

  // Bug 2: 加载会话不再自动 MarkRead — mark-read 的语义是"用户当前正在看",
  // 这个判断只能在 ChatPanel 层基于 active prop 决定;hook 层无条件 MarkRead
  // 会让 tab-strip 隐藏 tab / 启动时恢复的 tab 全部错误地被标记为已读。
  it("does not call MarkChatSessionRead on load", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 11,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 2000,
        lastReadAt: 0,
        createtime: 0,
      },
      messages: [],
    });
    const { result } = renderHook(() => useChatSession(11));
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(markChatSessionRead).not.toHaveBeenCalled();
    // 同步:也不再往 read overlay 写客户端 override。
    expect(useSessionReadStore.getState().overrides.get(11)).toBeUndefined();
  });

  // 兜底回归：现有 agentStatus/needsAttention 的 patch 不带 permissionMode 时，
  // session.permissionMode 保留 detail 上的值，不被空串覆盖。
  it("keeps detail.permissionMode when patch omits permissionMode", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: {
        id: 8,
        agentId: 1,
        agentName: "Claude",
        title: "x",
        agentStatus: "running",
        permissionMode: "acceptEdits",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: [],
    });
    const { result } = renderHook(() => useChatSession(8));
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      // 模拟 chat-streams-host 把 session_status 事件转写到 store 时, 不带
      // permissionMode 字段（生产代码会用前一次的值兜底, 这里测试 hook 侧
      // 的合并语义）。
      useSessionStatusStore.getState().upsert(8, {
        agentStatus: "waiting",
        needsAttention: true,
        permissionMode: "acceptEdits",
      });
    });

    await waitFor(() =>
      expect(result.current.session?.agentStatus).toBe("waiting"),
    );
    expect(result.current.session?.permissionMode).toBe("acceptEdits");
  });
});

// 读路径:元数据全量 + 块按需取(spec 2026-08-27 决策 6)。
//
// 后端只随 LoadChatSession 下发最近一个窗口的完整正文,更早的消息先只给元数据。
// 前端因此要做两件事:①派生视图(后台任务面板 / 大纲 / 变更)要的那几类块,改由后端
// 按类型点查补齐 —— 它们的数据集合必须与「整条转录都在本地」时逐条相同,否则就是
// 决策 6 点名的「静默算错」;②用户向上滚动时继续把更早那一段的完整正文取回来。
describe("useChatSession block windowing", () => {
  beforeEach(() => {
    loadChatSession.mockReset();
    loadChatMessageBlocks.mockReset();
    loadChatSessionBlocksByType.mockReset();
    markChatSessionRead.mockClear();
    useSessionStatusStore.getState().__reset();
    useSessionMetaStore.getState().__reset();
    useSessionReadStore.setState({ overrides: new Map() });
    useChatStreamsStore.setState({ streams: new Map() });
  });

  const windowedSession = {
    id: 9,
    agentId: 1,
    agentName: "Eng",
    title: "x",
    agentStatus: "idle",
    lastMessageAt: 0,
    createtime: 0,
  };

  // 回归 spec 2026-08-27 收尾评审 F4：用户往上翻回窗口外的那些行，正文被
  // retainLoadedBlocks 保住(并把 blocksLoaded 翻成 true)，而 mergeDerivedViewBlocks
  // 只处理 blocksLoaded===false —— 于是这些行被按类型点查跳过，里面还在跑的后台
  // subagent 卡片状态就冻在「翻上去那一刻」，再不更新。
  it("Given a pulled-back row holding a running subagent, When a later reload lands, Then its card takes the fresh state instead of freezing", async () => {
    const pulledBack = {
      id: 40,
      sessionId: 9,
      role: "assistant",
      seq: 1,
      blocks: [
        { type: "text", text: "older turn" },
        {
          type: "tool_use",
          toolUseId: "tu-bg",
          toolName: "Agent",
          subagent: { kind: "local_agent", status: "running" },
        },
      ],
      blocksLoaded: true,
    };
    const latest = {
      id: 41,
      sessionId: 9,
      role: "assistant",
      seq: 2,
      blocks: [{ type: "text", text: "latest" }],
      blocksLoaded: true,
    };

    // 首次打开：这一行已经是「用户取回来的完整正文」。
    loadChatSession.mockResolvedValueOnce({
      session: windowedSession,
      messages: [pulledBack, latest],
    });

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(
      deriveBackgroundTasks(result.current.messages, []).find(
        (t) => t.toolUseId === "tu-bg",
      )?.status,
    ).toBe("running");

    // 轮收尾的 reload：窗口只带最近一条，40 号退回「只有元数据」。
    loadChatSession.mockResolvedValueOnce({
      session: windowedSession,
      messages: [{ ...pulledBack, blocks: [], blocksLoaded: false }, latest],
    });
    // 按类型点查拿到的是新鲜状态：那个后台任务已经跑完了。
    loadChatSessionBlocksByType.mockResolvedValueOnce({
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "assistant",
          seq: 1,
          blocks: [
            { type: "text", text: "older turn" },
            {
              type: "tool_use",
              toolUseId: "tu-bg",
              toolName: "Agent",
              subagent: { kind: "local_agent", status: "completed" },
            },
          ],
          blocksLoaded: false,
        },
      ],
    });

    await act(async () => {
      await result.current.reload();
    });

    expect(
      deriveBackgroundTasks(result.current.messages, []).find(
        (t) => t.toolUseId === "tu-bg",
      )?.status,
    ).toBe("completed");
    // 保住正文这件事本身不能退化：转录仍要看得到那一行的 text 块。
    expect(
      result.current.messages
        .find((m) => m.id === 40)
        ?.blocks?.some((b) => b.type === "text"),
    ).toBe(true);
  });

  it("backfills derived-view blocks for messages outside the transcript window", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: windowedSession,
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "assistant",
          blocks: [],
          seq: 1,
          blocksLoaded: false,
        },
        {
          id: 41,
          sessionId: 9,
          role: "assistant",
          blocks: [{ type: "text", text: "latest" }],
          seq: 2,
          blocksLoaded: true,
        },
      ],
    });
    loadChatSessionBlocksByType.mockResolvedValueOnce({
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "assistant",
          blocks: [
            {
              type: "tool_use",
              toolUseId: "tu1",
              subagent: { kind: "local_agent", status: "running" },
            },
          ],
          seq: 1,
          blocksLoaded: false,
        },
        {
          id: 41,
          sessionId: 9,
          role: "assistant",
          blocks: [],
          seq: 2,
          blocksLoaded: false,
        },
      ],
    });

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));
    await waitFor(() =>
      expect(result.current.messages[0].blocks?.length).toBe(1),
    );

    expect(loadChatSessionBlocksByType).toHaveBeenCalledWith({
      sessionId: 9,
      types: ["text", "tool_use"],
    });
    // 窗口外那条:后台任务面板看得见它的 tool_use 卡,但它仍不是「正文已就绪」。
    expect(result.current.messages[0].blocks?.[0].toolUseId).toBe("tu1");
    expect(result.current.messages[0].blocksLoaded).toBe(false);
    // 窗口内那条的完整正文不被按类型取数覆盖成空。
    expect(result.current.messages[1].blocks?.[0].text).toBe("latest");
    expect(result.current.messages[1].blocksLoaded).toBe(true);
    expect(result.current.hasEarlierBlocks).toBe(true);
  });

  it("skips the per-type query when the whole transcript is already loaded", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: windowedSession,
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "assistant",
          blocks: [{ type: "text", text: "only" }],
          seq: 1,
          blocksLoaded: true,
        },
      ],
    });

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(loadChatSessionBlocksByType).not.toHaveBeenCalled();
    expect(result.current.hasEarlierBlocks).toBe(false);
  });

  it("loads earlier blocks before the earliest loaded message", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: windowedSession,
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "user",
          blocks: [],
          seq: 1,
          blocksLoaded: false,
        },
        {
          id: 41,
          sessionId: 9,
          role: "user",
          blocks: [],
          seq: 2,
          blocksLoaded: false,
        },
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          blocks: [{ type: "text", text: "latest" }],
          seq: 3,
          blocksLoaded: true,
        },
      ],
    });
    loadChatSessionBlocksByType.mockResolvedValueOnce({ messages: [] });
    loadChatMessageBlocks.mockResolvedValueOnce({
      messages: [
        {
          id: 41,
          sessionId: 9,
          role: "user",
          blocks: [{ type: "text", text: "older" }],
          seq: 2,
          blocksLoaded: true,
        },
      ],
      hasMore: true,
    });

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.hasEarlierBlocks).toBe(true);

    await act(async () => {
      await result.current.loadEarlierBlocks();
    });

    // 取的是「手上最早那条已就绪消息」之前的一段。
    expect(loadChatMessageBlocks).toHaveBeenCalledWith({
      sessionId: 9,
      beforeSeq: 3,
      limit: 0,
    });
    expect(result.current.messages[1].blocks?.[0].text).toBe("older");
    expect(result.current.messages[1].blocksLoaded).toBe(true);
    // 更早的 seq 1 还没取,继续往上滚还能取。
    expect(result.current.messages[0].blocksLoaded).toBe(false);
    expect(result.current.hasEarlierBlocks).toBe(true);
  });

  it("keeps bodies the user already scrolled back into view across a reload", async () => {
    const windowed = (blocksLoaded: boolean) => ({
      session: windowedSession,
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "user",
          blocks: blocksLoaded ? [{ type: "text", text: "older" }] : [],
          seq: 1,
          blocksLoaded,
        },
        {
          id: 41,
          sessionId: 9,
          role: "assistant",
          blocks: [{ type: "text", text: "latest" }],
          seq: 2,
          blocksLoaded: true,
        },
      ],
    });
    loadChatSession.mockResolvedValue(windowed(false));
    loadChatSessionBlocksByType.mockResolvedValue({ messages: [] });
    loadChatMessageBlocks.mockResolvedValueOnce({
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "user",
          blocks: [{ type: "text", text: "older" }],
          seq: 1,
          blocksLoaded: true,
        },
      ],
      hasMore: false,
    });

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));
    await act(async () => {
      await result.current.loadEarlierBlocks();
    });
    expect(result.current.messages[0].blocksLoaded).toBe(true);

    // 一轮收尾后的 reload:后端照旧只带最近一个窗口,取回来的那条不能又缩回去。
    await act(async () => {
      await result.current.reload();
    });
    expect(result.current.messages[0].blocksLoaded).toBe(true);
    expect(result.current.messages[0].blocks?.[0].text).toBe("older");
    expect(result.current.hasEarlierBlocks).toBe(false);
  });

  // 派生视图的等价性(决策 6 的落点):后台任务面板与大纲此前遍历「本地整条转录」得出
  // 结论,现在整条转录已不在本地。它们看得见的东西必须**一条不少** —— 派生视图算错
  // 是静默的(漏一个后台任务、大纲少一轮),不像少一段历史那样一眼看得出来。
  it("keeps the background-task panel and the outline complete across the window edge", async () => {
    loadChatSession.mockResolvedValueOnce({
      session: windowedSession,
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "user",
          blocks: [],
          seq: 1,
          createtime: 1000,
          blocksLoaded: false,
        },
        {
          id: 41,
          sessionId: 9,
          role: "assistant",
          blocks: [],
          seq: 2,
          createtime: 2000,
          blocksLoaded: false,
        },
        {
          id: 42,
          sessionId: 9,
          role: "assistant",
          blocks: [{ type: "text", text: "latest" }],
          seq: 3,
          createtime: 3000,
          blocksLoaded: true,
        },
      ],
    });
    loadChatSessionBlocksByType.mockResolvedValueOnce({
      messages: [
        {
          id: 40,
          sessionId: 9,
          role: "user",
          blocks: [{ type: "text", text: "跑个后台任务" }],
          seq: 1,
          createtime: 1000,
          blocksLoaded: false,
        },
        {
          id: 41,
          sessionId: 9,
          role: "assistant",
          blocks: [
            {
              type: "tool_use",
              toolName: "Agent",
              toolUseId: "toolu-bg",
              toolInput: { run_in_background: true },
              subagent: {
                kind: "local_agent",
                status: "running",
                taskDescription: "probe",
              },
            },
            {
              type: "tool_use",
              toolName: "Edit",
              toolUseId: "toolu-edit",
              toolInput: { file_path: "/repo/a.ts" },
            },
          ],
          seq: 2,
          createtime: 2000,
          blocksLoaded: false,
        },
      ],
    });

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));
    await waitFor(() =>
      expect(result.current.messages[1].blocks?.length).toBe(2),
    );

    // 后台任务面板:发起卡在窗口之外,面板仍然收得到这个任务。
    const tasks = deriveBackgroundTasks(result.current.messages, []);
    expect(tasks.map((task) => task.toolUseId)).toEqual(["toolu-bg"]);
    expect(tasks[0].status).toBe("running");
    // 大纲:窗口之外那一轮仍然成行,并数得出它那一轮里的编辑次数。
    const outline = deriveOutline(result.current.messages);
    expect(outline).toHaveLength(1);
    expect(outline[0].text).toBe("跑个后台任务");
    expect(outline[0].edits).toBe(1);
  });
});
