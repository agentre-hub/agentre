/**
 * chat-panel-host.lazy-mount.test.tsx — 恢复的 tab 按需挂载 + 非活动 tab 的渲染隔离。
 *
 * 契约（spec 2026-11-05 perf-sweep「启动与切换会话」「渲染」，决策 10 / 11）：
 *   - 恢复的 tab 只有活动的那个立即挂载并发请求；其它 tab 首次被激活时才挂载，之后保持挂载。
 *   - tab 条状态 / 后台会话的流式状态不依赖面板挂载。
 *   - 非活动 tab 不随自己会话的文字 delta 重渲染；切回时内容是最新的。
 *
 * 宿主与 ChatPanel 都是真的；只把会话加载 hook 换成按 sid 稳定的桩（它在挂载时照真
 * hook 的样子发一次 LoadChatSession），技能目录走真的 useAgentSkillCommands →
 * ListAgentSkillCommands，转录区换成按会话计数的桩。
 */

import { act, fireEvent, render, renderHook } from "@testing-library/react";
import * as React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const probes = vi.hoisted(() => ({
  transcriptRenders: new Map<number, number>(),
  lastLive: new Map<number, unknown>(),
  handlers: new Map<string, Set<(ev: unknown) => void>>(),
}));

// ── wailsjs ─────────────────────────────────────────────────────────────────

const appMocks = vi.hoisted(() => ({
  CancelQueuedChatMessage: vi.fn(),
  CompactChatSession: vi.fn(),
  DeleteChatSession: vi.fn(),
  EditChatMessage: vi.fn(),
  EnqueueChatMessage: vi.fn(),
  EnsureChatSession: vi.fn(),
  GetCCUsage: vi.fn().mockResolvedValue({ reason: "" }),
  GetChatLaunchCommand: vi.fn(),
  ListSessionProcesses: vi.fn().mockResolvedValue([]),
  KillSessionProcess: vi.fn(),
  OpenSessionProcessPort: vi.fn(),
  GetChatGoal: vi.fn(),
  ListAgentSkillCommands: vi.fn().mockResolvedValue({ commands: [] }),
  ListLLMProviders: vi.fn().mockResolvedValue({ items: [] }),
  ListLLMModels: vi.fn().mockResolvedValue({ items: [] }),
  LoadChatSession: vi.fn().mockResolvedValue({}),
  PeerRunFresh: vi.fn(),
  RemoteDeviceFingerprint: vi.fn().mockResolvedValue("sha256:local-desktop"),
  RemoteDeviceList: vi.fn().mockResolvedValue([]),
  RemoteDeviceListProviders: vi.fn().mockResolvedValue([]),
  SetChatSessionProvider: vi.fn(),
  SetChatSessionModelTarget: vi.fn(),
  SetChatSessionReasoningEffort: vi.fn(),
  MarkChatSessionRead: vi.fn().mockResolvedValue({}),
  RegenerateChatMessage: vi.fn(),
  RenameChatSession: vi.fn(),
  ResolveLocalCommandScope: vi
    .fn()
    .mockResolvedValue({ deviceId: "", cwd: "/repo" }),
  SendChatMessage: vi.fn(),
  SetChatGoal: vi.fn(),
  StartChatGoal: vi.fn(),
  StopChatMessage: vi.fn(),
  TerminalClose: vi.fn(),
  TerminalRunCommand: vi.fn(),
  ClearChatGoal: vi.fn(),
  GetSessionGitState: vi.fn().mockResolvedValue({
    state: { notARepo: true },
  }),
  WorkspaceFsWorkRoots: vi
    .fn()
    .mockResolvedValue([{ path: "/repo", isPrimary: true }]),
  WorkspaceFsGitState: vi.fn().mockResolvedValue({ notARepo: true }),
  WorkspaceFsGitChanges: vi.fn().mockResolvedValue({ notARepo: true }),
  ProjectListTree: vi.fn().mockResolvedValue([]),
}));

vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);
vi.mock("@/../wailsjs/go/app/App", () => appMocks);

// EventsOn 按事件名登记处理器，测试据此模拟后端推帧。
const runtimeMocks = vi.hoisted(() => ({
  BrowserOpenURL: vi.fn(),
  EventsOff: vi.fn(),
  EventsOn: vi.fn((name: string, handler: (ev: unknown) => void) => {
    let set = probes.handlers.get(name);
    if (!set) {
      set = new Set();
      probes.handlers.set(name, set);
    }
    set.add(handler);
    return () => {
      set.delete(handler);
    };
  }),
}));
vi.mock("../../../../../wailsjs/runtime/runtime", () => runtimeMocks);

// ── 会话数据：按 sid 稳定的引用，免得桩自己制造重渲染 ─────────────────────

const sessionData = vi.hoisted(() => {
  const bySid = new Map<number, unknown>();
  const sessionFor = (sid: number) => {
    let s = bySid.get(sid);
    if (!s) {
      s = {
        id: sid,
        agentId: 7,
        agentName: "Ada",
        agentColor: "agent-1",
        title: `Session ${sid}`,
        backendType: "claudecode",
        cwd: "/repo",
        deviceID: "",
        projectId: 0,
        agentStatus: "idle",
        lastMessageAt: 0,
        contextWindow: 0,
      };
      bySid.set(sid, s);
    }
    return s;
  };
  return {
    bySid,
    sessionFor,
    messages: [] as unknown[],
    reload: () => Promise.resolve(),
    setMessages: () => {},
    loadEarlier: () => Promise.resolve(),
  };
});

vi.mock("@/hooks/use-chat-session", async () => {
  const React = await import("react");
  return {
    // 真 hook 挂载即发 LoadChatSession；桩照做一次，其余返回稳定引用。
    useChatSession: (sessionId: number) => {
      React.useEffect(() => {
        if (sessionId > 0) void appMocks.LoadChatSession({ sessionId });
      }, [sessionId]);
      return {
        session: sessionId > 0 ? sessionData.sessionFor(sessionId) : null,
        messages: sessionData.messages,
        loading: false,
        error: null,
        reload: sessionData.reload,
        setMessages: sessionData.setMessages,
        hasEarlierBlocks: false,
        loadingEarlierBlocks: false,
        loadEarlierBlocks: sessionData.loadEarlier,
      };
    },
  };
});

const EMPTY_TREE = vi.hoisted(() => ({
  tree: [] as unknown[],
  invalidate: () => {},
  loaded: true,
}));
vi.mock("@/hooks/use-project-tree", () => ({
  useProjectTree: () => EMPTY_TREE,
}));

vi.mock("@/hooks/use-cc-usage", () => ({
  useCCUsage: () => undefined,
}));

const CAPS = vi.hoisted(() => ({
  has: (c: string) => c === "steer" || c === "set_permission_mode",
  permissionModeMeta: {
    allowedModes: ["default", "plan"],
    defaultMode: "default",
    switchableDuringTurn: true,
    order: ["default", "plan"],
  },
}));
vi.mock("../../capability/use-capabilities", () => ({
  useSessionCapabilities: () => ({ caps: CAPS }),
  useBackendCapabilities: () => ({ caps: null }),
}));

const COMPOSER_SOURCES = vi.hoisted(() => ({
  agents: { agents: [] as unknown[] },
  projects: { projects: [] as unknown[] },
  devices: [] as unknown[],
}));
vi.mock("@/hooks/use-chat-agents", () => ({
  useChatAgents: () => COMPOSER_SOURCES.agents,
}));
vi.mock("@/hooks/use-project-list", () => ({
  useProjectList: () => COMPOSER_SOURCES.projects,
}));
vi.mock("../../remote-devices/use-device-mentions", () => ({
  useDeviceMentionItems: () => COMPOSER_SOURCES.devices,
}));

vi.mock("@agentre-hub/agentre-ui", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@agentre-hub/agentre-ui")>();
  const React = await import("react");
  return {
    ...actual,
    ChatComposer: React.forwardRef(function ChatComposerProbe() {
      return null;
    }),
  };
});

// 转录区按会话计数：渲染一次 = 该面板重渲染一次。
vi.mock("../../chat", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../chat")>();
  return {
    ...actual,
    ChatTranscript: (props: {
      sessionId: number;
      liveByMessageId?: unknown;
    }) => {
      probes.transcriptRenders.set(
        props.sessionId,
        (probes.transcriptRenders.get(props.sessionId) ?? 0) + 1,
      );
      probes.lastLive.set(props.sessionId, props.liveByMessageId);
      return null;
    },
  };
});

// ── import after mocks ─────────────────────────────────────────────────────

import {
  LocalCommandsProvider,
  TerminalTransportProvider,
} from "@agentre-hub/agentre-ui";
import type { ChatTab } from "@/stores/chat-tabs-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useChatStreamsStore } from "@/stores/chat-streams-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { desktopLocalCommandsAccess } from "../../local-commands-access-desktop";
import { desktopTerminalTransport } from "../../terminal/terminal-transport-desktop";
import { ChatPanelHost } from "../chat-panel-host";
import { useTabsView } from "../use-tabs-view";

function Host({ children }: { children?: React.ReactNode }) {
  return (
    <TerminalTransportProvider transport={desktopTerminalTransport}>
      <LocalCommandsProvider access={desktopLocalCommandsAccess}>
        {children}
      </LocalCommandsProvider>
    </TerminalTransportProvider>
  );
}

function sessionTab(sid: number): ChatTab {
  return {
    id: `tab-${sid}`,
    meta: { kind: "session", sessionId: sid },
    isPreview: false,
    isPinned: false,
    pinAt: 0,
    openedAt: sid,
  };
}

// 模拟启动时从 localStorage 同步恢复：store 在宿主首次渲染前就已经有 tab。
function restoreTabs(sids: number[], activeSid: number) {
  useChatTabsStore.setState({
    tabs: sids.map(sessionTab),
    activeTabId: `tab-${activeSid}`,
  });
}

async function settle(): Promise<void> {
  let prev = "";
  for (let i = 0; i < 20; i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    const now = JSON.stringify([...probes.transcriptRenders.entries()]);
    if (now === prev) return;
    prev = now;
  }
}

function loadedSessionIds(): number[] {
  return appMocks.LoadChatSession.mock.calls
    .map((c) => (c[0] as { sessionId: number }).sessionId)
    .sort((a, b) => a - b);
}

function liveTextOf(sid: number, assistantId: number): string {
  const map = probes.lastLive.get(sid) as
    | Map<number, { liveTail?: string }>
    | null
    | undefined;
  return map?.get(assistantId)?.liveTail ?? "";
}

function emit(name: string, ev: unknown) {
  for (const h of probes.handlers.get(name) ?? []) h(ev);
}

describe("ChatPanelHost 恢复 tab 按需挂载", () => {
  beforeEach(() => {
    probes.transcriptRenders.clear();
    probes.lastLive.clear();
    probes.handlers.clear();
    appMocks.LoadChatSession.mockClear();
    appMocks.ListAgentSkillCommands.mockClear();
    useChatStreamsStore.setState({ streams: new Map() });
    useChatStreamsStore.getState().__setDeltaFlushMs(0);
    useSessionStatusStore.getState().__reset();
  });

  afterEach(() => {
    useChatStreamsStore.setState({ streams: new Map() });
  });

  it("Given 5 个恢复的 tab When 宿主首次渲染 Then 只有活动 tab 发 LoadChatSession / ListAgentSkillCommands；激活另一个 tab 时它才加载，且之前的 tab 保持挂载", async () => {
    restoreTabs([11, 12, 13, 14, 15], 13);
    render(<ChatPanelHost />, { wrapper: Host });
    await settle();

    expect(loadedSessionIds()).toEqual([13]);
    expect(appMocks.ListAgentSkillCommands).toHaveBeenCalledTimes(1);

    act(() => {
      useChatTabsStore.getState().setActive("tab-15");
    });
    await settle();

    expect(loadedSessionIds()).toEqual([13, 15]);
    expect(appMocks.ListAgentSkillCommands).toHaveBeenCalledTimes(2);

    // 切回 13：已挂载的面板不重挂、不重发请求。
    act(() => {
      useChatTabsStore.getState().setActive("tab-13");
    });
    await settle();
    expect(loadedSessionIds()).toEqual([13, 15]);
    expect(appMocks.ListAgentSkillCommands).toHaveBeenCalledTimes(2);
  });

  it("Given 两个已挂载的 tab、A 活动 When B 的会话连续到达 5 个文字 delta Then B 的转录区至多渲染一次；切到 B 时显示全部文字", async () => {
    const A = 21;
    const B = 22;
    const B_ASSISTANT = 2201;
    restoreTabs([A, B], B);
    const { container } = render(<ChatPanelHost />, { wrapper: Host });
    await settle();
    // 用户在 B 里往上翻到中途再离开：切回时要还是同一个滚动容器、同一个位置。
    const bScroll = container.querySelector<HTMLElement>(
      `[data-tab-id="tab-${B}"] [data-testid="chat-transcript-scroll"]`,
    )!;
    Object.defineProperty(bScroll, "scrollHeight", {
      configurable: true,
      value: 1000,
    });
    Object.defineProperty(bScroll, "clientHeight", {
      configurable: true,
      value: 300,
    });
    // 先在底部，再往上翻（往上翻才解除贴底跟随）。
    bScroll.scrollTop = 700;
    fireEvent.scroll(bScroll);
    bScroll.scrollTop = 200;
    fireEvent.scroll(bScroll);
    act(() => {
      useChatTabsStore.getState().setActive(`tab-${A}`);
    });
    await settle();

    act(() => {
      useChatStreamsStore.getState().openStream({
        sessionId: B,
        assistantMessageId: B_ASSISTANT,
        name: `chat:event:${B}:${B_ASSISTANT}`,
        streamStartedAt: Date.now(),
      });
    });
    await settle();
    const before = probes.transcriptRenders.get(B) ?? 0;

    for (const piece of ["a", "b", "c", "d", "e"]) {
      act(() => {
        useChatStreamsStore.getState().appendLiveText(B, B_ASSISTANT, piece);
      });
    }

    expect((probes.transcriptRenders.get(B) ?? 0) - before).toBeLessThanOrEqual(
      1,
    );

    act(() => {
      useChatTabsStore.getState().setActive(`tab-${B}`);
    });

    expect(liveTextOf(B, B_ASSISTANT)).toBe("abcde");
    expect(
      container.querySelector(
        `[data-tab-id="tab-${B}"] [data-testid="chat-transcript-scroll"]`,
      ),
    ).toBe(bScroll);
    expect(bScroll.scrollTop).toBe(200);
  });

  it("Given 未挂载的后台 tab When 它的会话起了一轮自主续轮 Then tab 条显示运行中，且该会话的流进入 store", async () => {
    const A = 31;
    const B = 32;
    restoreTabs([A, B], A);
    render(<ChatPanelHost />, { wrapper: Host });
    await settle();
    const { result } = renderHook(() => useTabsView(), { wrapper: Host });

    act(() => {
      emit(`chat:autonomous:${B}`, {
        kind: "autonomous_started",
        stream: `chat:event:${B}:3201`,
        assistantMessage: { id: 3201, sessionId: B, role: "assistant" },
      });
    });

    expect(result.current.find((v) => v.sessionId === B)?.status).toBe(
      "running",
    );
    expect(useChatStreamsStore.getState().streams.get(B)?.has(3201)).toBe(true);
  });
});
