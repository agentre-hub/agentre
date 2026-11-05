/**
 * chat-panel.render-isolation.test.tsx — 流式输出期间的渲染隔离。
 *
 * 契约（spec 2026-11-05 perf-sweep「渲染」）：文字 delta 只重渲染转录区与依赖流状态
 * 的头部元素；composer 不因流更新重渲染；会话级派生（变更 / 后台任务 / 任务进度）
 * 只在块集合变化时重算。
 *
 * 观测点：
 *   - composer：宿主的 ChatComposer 适配层是真的（它的 memo 与回调稳定性正是被测
 *     对象），只把它往下递的共享包外壳换成计数桩 —— 外壳渲染一次就是 composer
 *     重渲染一次。
 *   - 派生：三个派生函数原样执行，外面包一层计数。
 */

import { act, render, waitFor } from "@testing-library/react";
import * as React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const probes = vi.hoisted(() => ({
  composerRenders: 0,
  lastLiveByMessageId: null as unknown,
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
  ListLLMProviders: vi.fn().mockResolvedValue({ items: [] }),
  ListLLMModels: vi.fn().mockResolvedValue({ items: [] }),
  LoadChatSession: vi.fn(),
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
  // 两个认领的工作根：侧栏按根各数一次变更，那条逐根派生也在被测范围内。
  WorkspaceFsWorkRoots: vi.fn().mockResolvedValue([
    { path: "/repo", isPrimary: true },
    { path: "/repo-wt", isPrimary: false },
  ]),
  WorkspaceFsGitState: vi.fn().mockResolvedValue({ notARepo: true }),
  WorkspaceFsGitChanges: vi.fn().mockResolvedValue({ notARepo: true }),
  ProjectListTree: vi.fn().mockResolvedValue([]),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);
vi.mock("@/../wailsjs/go/app/App", () => appMocks);

const runtimeMocks = vi.hoisted(() => ({
  BrowserOpenURL: vi.fn(),
  EventsOff: vi.fn(),
  EventsOn: vi.fn(() => () => {}),
}));
vi.mock("../../../../wailsjs/runtime/runtime", () => runtimeMocks);

// ── 会话数据：引用稳定，免得桩自己制造重渲染 ────────────────────────────────

const SESSION_ID = 42;
const ASSISTANT_ID = 1001;

const sessionData = vi.hoisted(() => {
  const session = {
    id: 42,
    agentId: 7,
    agentName: "Ada",
    agentColor: "agent-1",
    title: "Streaming session",
    backendType: "claudecode",
    cwd: "/repo",
    deviceID: "",
    projectId: 0,
    agentStatus: "running",
    lastMessageAt: 0,
    contextWindow: 0,
  };
  return {
    session,
    messages: [] as unknown[],
    reload: () => Promise.resolve(),
    setMessages: () => {},
    loadEarlier: () => Promise.resolve(),
  };
});

vi.mock("@/hooks/use-chat-session", () => ({
  useChatSession: () => ({
    session: sessionData.session,
    messages: sessionData.messages,
    loading: false,
    error: null,
    reload: sessionData.reload,
    setMessages: sessionData.setMessages,
    hasEarlierBlocks: false,
    loadingEarlierBlocks: false,
    loadEarlierBlocks: sessionData.loadEarlier,
  }),
}));

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

// 能力矩阵：引用稳定的一份（真实 hook 把它放在 state 里，同样稳定）。
const CAPS = vi.hoisted(() => ({
  has: (c: string) =>
    c === "steer" || c === "set_permission_mode" || c === "image_input",
  permissionModeMeta: {
    allowedModes: ["default", "plan"],
    defaultMode: "default",
    switchableDuringTurn: true,
    order: ["default", "plan"],
  },
}));
vi.mock("../capability/use-capabilities", () => ({
  useSessionCapabilities: () => ({ caps: CAPS }),
  useBackendCapabilities: () => ({ caps: null }),
}));

// 宿主 composer 适配层的数据源：引用稳定的空集合。
const COMPOSER_SOURCES = vi.hoisted(() => ({
  agents: { agents: [] as unknown[] },
  projects: { projects: [] as unknown[] },
  devices: [] as unknown[],
  skills: [] as unknown[],
}));
vi.mock("@/hooks/use-chat-agents", () => ({
  useChatAgents: () => COMPOSER_SOURCES.agents,
}));
vi.mock("@/hooks/use-project-list", () => ({
  useProjectList: () => COMPOSER_SOURCES.projects,
}));
vi.mock("../remote-devices/use-device-mentions", () => ({
  useDeviceMentionItems: () => COMPOSER_SOURCES.devices,
}));
vi.mock("../slash-commands", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../slash-commands")>();
  return {
    ...actual,
    useAgentSkillCommands: () => COMPOSER_SOURCES.skills,
  };
});

// ── 观测桩 ──────────────────────────────────────────────────────────────────

// 共享包外壳 → 计数桩；包的其余导出原样透传。
vi.mock("@agentre-hub/agentre-ui", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@agentre-hub/agentre-ui")>();
  const React = await import("react");
  return {
    ...actual,
    ChatComposer: React.forwardRef(function ChatComposerProbe() {
      probes.composerRenders += 1;
      return null;
    }),
  };
});

// 转录区是流式内容的正当消费者：桩只记下它最后收到的流式内容，证明 delta 确实到了面板。
vi.mock("../chat", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../chat")>();
  return {
    ...actual,
    ChatTranscript: (props: { liveByMessageId?: unknown }) => {
      probes.lastLiveByMessageId = props.liveByMessageId;
      return null;
    },
  };
});

vi.mock("../background-tasks/derive", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../background-tasks/derive")>();
  return {
    ...actual,
    deriveBackgroundTasks: vi.fn(actual.deriveBackgroundTasks),
  };
});
vi.mock("../task-progress/derive", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../task-progress/derive")>();
  return { ...actual, deriveTaskProgress: vi.fn(actual.deriveTaskProgress) };
});
vi.mock("../chat-context-sidebar/derive", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../chat-context-sidebar/derive")>();
  return {
    ...actual,
    deriveSessionChanges: vi.fn(actual.deriveSessionChanges),
  };
});

// ── import after mocks ─────────────────────────────────────────────────────

import {
  LocalCommandsProvider,
  TerminalTransportProvider,
} from "@agentre-hub/agentre-ui";
import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import { useChatStreamsStore } from "@/stores/chat-streams-store";

import { deriveBackgroundTasks } from "../background-tasks/derive";
import { ChatPanel } from "../chat-panel";
import { deriveSessionChanges } from "../chat-context-sidebar/derive";
import { desktopLocalCommandsAccess } from "../local-commands-access-desktop";
import { deriveTaskProgress } from "../task-progress/derive";
import { desktopTerminalTransport } from "../terminal/terminal-transport-desktop";

function Host({ children }: { children?: React.ReactNode }) {
  return (
    <TerminalTransportProvider transport={desktopTerminalTransport}>
      <LocalCommandsProvider access={desktopLocalCommandsAccess}>
        {children}
      </LocalCommandsProvider>
    </TerminalTransportProvider>
  );
}

type Counts = {
  composer: number;
  backgroundTasks: number;
  taskProgress: number;
  sessionChanges: number;
};

function counts(): Counts {
  return {
    composer: probes.composerRenders,
    backgroundTasks: vi.mocked(deriveBackgroundTasks).mock.calls.length,
    taskProgress: vi.mocked(deriveTaskProgress).mock.calls.length,
    sessionChanges: vi.mocked(deriveSessionChanges).mock.calls.length,
  };
}

// 挂载后的异步加载（供应商目录、工作根、能力……）会带来正当的重渲染；等到连续
// 两轮宏任务计数都不再变化，才算到了「只剩流式更新」的稳态。
async function settle(): Promise<void> {
  let prev = "";
  for (let i = 0; i < 20; i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    const now = JSON.stringify(counts());
    if (now === prev) return;
    prev = now;
  }
}

function liveTextOf(map: unknown): string {
  const entry = (map as Map<number, { liveTail?: string }> | null)?.get(
    ASSISTANT_ID,
  );
  return entry?.liveTail ?? "";
}

describe("ChatPanel 流式输出期间的渲染隔离", () => {
  beforeEach(() => {
    probes.composerRenders = 0;
    probes.lastLiveByMessageId = null;
    vi.mocked(deriveBackgroundTasks).mockClear();
    vi.mocked(deriveTaskProgress).mockClear();
    vi.mocked(deriveSessionChanges).mockClear();
    useChatStreamsStore.setState({ streams: new Map() });
    // 每个 delta 各自落一次 store —— 最坏情况，正好放大每次通知的重渲染。
    useChatStreamsStore.getState().__setDeltaFlushMs(0);
    useChatSidebarStore.setState({ open: true });
    useChatStreamsStore.getState().openStream({
      sessionId: SESSION_ID,
      assistantMessageId: ASSISTANT_ID,
      name: `chat:event:${SESSION_ID}:${ASSISTANT_ID}`,
      streamStartedAt: Date.now(),
    });
  });

  afterEach(() => {
    useChatStreamsStore.setState({ streams: new Map() });
  });

  async function streamFiveDeltas(): Promise<{ before: Counts }> {
    render(<ChatPanel sessionId={SESSION_ID} />, { wrapper: Host });
    await settle();
    const before = counts();
    expect(before.composer).toBeGreaterThan(0);
    expect(before.sessionChanges).toBeGreaterThan(0);

    for (const piece of ["a", "b", "c", "d", "e"]) {
      act(() => {
        useChatStreamsStore
          .getState()
          .appendLiveText(SESSION_ID, ASSISTANT_ID, piece);
      });
    }

    // delta 确实到了面板：转录区拿到了最新的流式正文。
    expect(liveTextOf(probes.lastLiveByMessageId)).toBe("abcde");
    return { before };
  }

  it("Given 正在流式输出的会话 When 连续到达 5 个文字 delta Then 会话级派生（变更含逐根计数 / 后台任务 / 任务进度）不重算", async () => {
    const { before } = await streamFiveDeltas();
    const after = counts();
    expect(after.backgroundTasks).toBe(before.backgroundTasks);
    expect(after.taskProgress).toBe(before.taskProgress);
    expect(after.sessionChanges).toBe(before.sessionChanges);
  });

  it("Given 正在流式输出的会话 When 连续到达 5 个文字 delta Then composer 不重渲染", async () => {
    const { before } = await streamFiveDeltas();
    expect(counts().composer).toBe(before.composer);
  });

  it("Given 正在流式输出的会话 When 到达一个工具块 Then 会话级派生重算", async () => {
    render(<ChatPanel sessionId={SESSION_ID} />, { wrapper: Host });
    await settle();
    const before = counts();

    act(() => {
      useChatStreamsStore
        .getState()
        .appendLiveToolUse(SESSION_ID, ASSISTANT_ID, {
          toolUseId: "toolu_edit",
          toolName: "Edit",
          toolInput: JSON.stringify({ file_path: "/repo/a.go" }),
        } as never);
    });

    await waitFor(() => {
      const after = counts();
      expect(after.backgroundTasks).toBeGreaterThan(before.backgroundTasks);
      expect(after.taskProgress).toBeGreaterThan(before.taskProgress);
      expect(after.sessionChanges).toBeGreaterThan(before.sessionChanges);
    });
  });
});
