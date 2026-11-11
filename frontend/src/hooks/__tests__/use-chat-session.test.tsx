import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../wailsjs/go/app/App", () => ({
  LoadChatSession: vi.fn(),
  LoadChatSessionBlocksByType: vi.fn(),
}));

import {
  LoadChatSession,
  LoadChatSessionBlocksByType,
} from "../../../wailsjs/go/app/App";
import { useChatStreamsStore } from "@/stores/chat-streams-store";
import { useSessionConnStore } from "@/stores/session-conn-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionStatusStore } from "@/stores/session-status-store";
import type { SessionConnectionState } from "@/stores/types";
import { useChatSession } from "../use-chat-session";

const loadChatSession = LoadChatSession as ReturnType<typeof vi.fn>;
const loadBlocksByType = LoadChatSessionBlocksByType as ReturnType<
  typeof vi.fn
>;

// respondWith 造一份 LoadSession 响应:一条正在跑的远端会话(activeStream 非空 ——
// R4 之后断连不再终结会话,turn goroutine 活着,重挂的前端照旧拿得到流名),
// 外加后端随响应同步返回的 connectionState。
function respondWith(opts: {
  sessionId: number;
  connectionState: SessionConnectionState;
  activeStream?: string;
}): void {
  loadChatSession.mockResolvedValue({
    session: {
      id: opts.sessionId,
      agentId: 10,
      agentName: "Eng",
      agentColor: "agent-1",
      projectId: 0,
      title: "t",
      agentStatus: "running",
      needsAttention: false,
      bgRunning: false,
      permissionMode: "default",
      permissionModeAtLaunch: "default",
      lastMessageAt: 100,
      lastReadAt: 0,
      activeStream: opts.activeStream ?? `chat:stream:${opts.sessionId}:1`,
      connectionState: opts.connectionState,
    },
    messages: [
      {
        id: 1001,
        sessionId: opts.sessionId,
        role: "assistant",
        seq: 1,
        blocks: [],
      },
    ],
  });
}

// ─── R13:整页重载后仍在重连的会话立刻是断连形态 ─────────────────────────────
//
// 连接态的推送流 chat:conn:<sid> 只在跃迁时发一帧,而前端要在 LoadSession 返回
// ActiveStream **之后**才订阅它 —— 整页重载落在退避窗口中间时,那一帧早已发过,
// 补发也会落在订阅建立之前。所以连接态必须随 LoadSession 响应同步回来,由这里播种。
// 不播种的后果不是少一个提示,而是用户在整个退避窗口里只看到普通打字指示器,
// 分不清"agent 在想"与"网断了"(R13 明说这是本改动新引入的困惑)。
describe("useChatSession 播种连接态 (R13)", () => {
  beforeEach(() => {
    loadChatSession.mockReset();
    useChatStreamsStore.setState({ streams: new Map() });
    useSessionConnStore.getState().__reset();
    useSessionMetaStore.getState().__reset();
    useSessionStatusStore.getState().__reset();
  });

  it("重载落在重连窗口中间:载入即断连形态,不等下一次跃迁推送", async () => {
    respondWith({ sessionId: 42, connectionState: "reconnecting" });
    renderHook(() => useChatSession(42));
    await waitFor(() =>
      expect(useSessionConnStore.getState().stateOf(42)).toBe("reconnecting"),
    );
  });

  it("不覆盖推送流已经写下的更新值", async () => {
    // 推送流已经报过"补齐完成、回到实时";响应里的 reconnecting 是发请求那一刻的
    // 旧快照。播种若无条件写入,就会把界面倒回断连形态并一直挂到下一次跃迁。
    useSessionConnStore.getState().setConnState(42, "connected");
    useChatStreamsStore.getState().openStream({
      name: "chat:stream:42:1",
      sessionId: 42,
      assistantMessageId: 1001,
      streamStartedAt: Date.now(),
    });
    respondWith({ sessionId: 42, connectionState: "reconnecting" });
    const { result } = renderHook(() => useChatSession(42));
    await waitFor(() => expect(result.current.session).not.toBeNull());
    expect(useSessionConnStore.getState().stateOf(42)).toBe("connected");
  });

  it("连接正常的会话不留条目:缺省态本就按已连接看待", async () => {
    respondWith({ sessionId: 42, connectionState: "connected" });
    const { result } = renderHook(() => useChatSession(42));
    await waitFor(() => expect(result.current.session).not.toBeNull());
    expect(useSessionConnStore.getState().conns.size).toBe(0);
  });

  it("没有活跃流的会话不播种:没有活信号可修饰,也没人来清它", async () => {
    // 条目是由 chat:conn:<sid> 的订阅者卸载时清掉的,而订阅者只跟着活跃流挂载。
    // 给一条没有流的会话写条目 = 写一条永远清不掉的记录,它会在这条会话下一轮
    // 真的跑起来时顶着断连指示器,而那时连接完全正常。
    respondWith({
      sessionId: 42,
      connectionState: "reconnecting",
      activeStream: "",
    });
    const { result } = renderHook(() => useChatSession(42));
    await waitFor(() => expect(result.current.session).not.toBeNull());
    expect(useSessionConnStore.getState().conns.size).toBe(0);
  });
});

// ─── 首次打开:会话与消息一起落下 ─────────────────────────────────────────────
//
// 会话详情一落,面板就不再画骨架(骨架只在「还没有会话、也没有消息」时出现)。
// 窗口外消息的派生视图块要再点查一次,这次点查在真实大库上要几秒 —— 若详情先落、
// 消息后落,中间这几秒转录区既没有骨架也没有内容,只剩头部(2026-11-09 用户报
// 「切 tab 过去空白几秒才正常」,真 WKWebView 截帧复现)。
describe("useChatSession 首次打开:会话详情不先于消息落下", () => {
  const SID = 77;

  beforeEach(() => {
    loadChatSession.mockReset();
    loadBlocksByType.mockReset();
    useChatStreamsStore.setState({ streams: new Map() });
    useSessionConnStore.getState().__reset();
    useSessionMetaStore.getState().__reset();
    useSessionStatusStore.getState().__reset();
    // 一条窗口外消息(只有元数据)+ 一条窗口内消息 —— 前者触发派生块点查。
    loadChatSession.mockResolvedValue({
      session: {
        id: SID,
        agentId: 10,
        agentName: "Eng",
        title: "t",
        agentStatus: "idle",
        lastMessageAt: 100,
      },
      messages: [
        {
          id: 1,
          sessionId: SID,
          role: "user",
          seq: 1,
          blocks: [],
          blocksLoaded: false,
        },
        {
          id: 2,
          sessionId: SID,
          role: "assistant",
          seq: 2,
          blocks: [{ type: "text", text: "hi" }],
          blocksLoaded: true,
        },
      ],
    });
  });

  it("派生块点查未返回时不交出会话详情;返回后详情与消息同时就绪", async () => {
    let resolveByType!: (v: unknown) => void;
    loadBlocksByType.mockReturnValue(
      new Promise((resolve) => {
        resolveByType = resolve;
      }),
    );
    const { result } = renderHook(() => useChatSession(SID));

    await waitFor(() => expect(loadBlocksByType).toHaveBeenCalled());
    expect(result.current.loading).toBe(true);
    expect(result.current.session).toBeNull();
    expect(result.current.messages).toEqual([]);

    resolveByType({ messages: [] });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.session?.id).toBe(SID);
    expect(result.current.messages.map((m) => m.id)).toEqual([1, 2]);
  });

  it("派生块点查失败:会话详情与消息照样一起落下,不报错", async () => {
    loadBlocksByType.mockRejectedValue(new Error("db busy"));
    const { result } = renderHook(() => useChatSession(SID));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBeNull();
    expect(result.current.session?.id).toBe(SID);
    expect(result.current.messages.map((m) => m.id)).toEqual([1, 2]);
  });
});
