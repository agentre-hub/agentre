/**
 * chat-panel-stop.test.tsx — 停止的两个入口（输入框提交位 + 顶带）由 ChatPanel 接到
 * 同一个 doStop 与同一份 stopping 态（C2）。
 *
 * 只桩掉 RPC、会话数据与重组件；顶带是真的，ChatComposer 换成记录 props 的桩 ——
 * 它自己的三种提交位形态由共享包的 chat-composer.test.tsx 覆盖。
 */

import {
  fireEvent,
  render as rtlRender,
  screen,
  waitFor,
} from "@testing-library/react";
import * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  GetCCUsage: vi.fn().mockResolvedValue({ reason: "" }),
  ListSessionProcesses: vi.fn().mockResolvedValue({ items: [] }),
  ListLLMProviders: vi.fn().mockResolvedValue({ items: [] }),
  ListLLMModels: vi.fn().mockResolvedValue({ items: [] }),
  RemoteDeviceFingerprint: vi.fn().mockResolvedValue("sha256:local-desktop"),
  RemoteDeviceList: vi.fn().mockResolvedValue([]),
  RemoteDeviceListProviders: vi.fn().mockResolvedValue([]),
  MarkChatSessionRead: vi.fn().mockResolvedValue({}),
  ResolveLocalCommandScope: vi.fn(),
  StopChatMessage: vi.fn(),
  GetSessionGitState: vi.fn().mockResolvedValue({
    state: {
      branch: "",
      worktree: "",
      dirty: 0,
      ahead: 0,
      behind: 0,
      hasUpstream: false,
      notARepo: true,
      updatedAt: 0,
    },
  }),
  WorkspaceFsWorkRoots: vi.fn().mockResolvedValue([]),
  WorkspaceFsGitState: vi.fn().mockResolvedValue({
    branch: "",
    worktree: "",
    dirty: 0,
    ahead: 0,
    behind: 0,
    hasUpstream: false,
    notARepo: true,
    commonDir: "",
  }),
  ProjectListTree: vi.fn().mockResolvedValue([]),
}));

vi.mock("../../../../wailsjs/go/app/App", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  ...appMocks,
}));
vi.mock("@/../wailsjs/go/app/App", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  ...appMocks,
}));
vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  BrowserOpenURL: vi.fn(),
  EventsOff: vi.fn(),
  EventsOn: vi.fn(() => vi.fn()),
}));

vi.mock("@/hooks/use-project-tree", () => ({
  useProjectTree: () => ({ tree: [], invalidate: () => {}, loaded: true }),
}));

const sessionStore = vi.hoisted(() => ({
  session: null as Record<string, unknown> | null,
}));
vi.mock("@/hooks/use-chat-session", () => ({
  useChatSession: () => ({
    session: sessionStore.session,
    messages: [],
    loading: false,
    error: null,
    reload: () => Promise.resolve(),
    setMessages: vi.fn(),
  }),
}));
vi.mock("@/hooks/use-cc-usage", () => ({ useCCUsage: () => undefined }));

const composerProps = vi.hoisted(() => [] as Array<Record<string, unknown>>);
vi.mock("../chat", async () => {
  const React = await import("react");
  return {
    QuotaMeter: () => null,
    ChatComposer: React.forwardRef((props: Record<string, unknown>) => {
      composerProps.push(props);
      return null;
    }),
    ChatTranscript: () => null,
  };
});
vi.mock("../project-merge-dialog", () => ({ ProjectMergeDialog: () => null }));
vi.mock("../session-exec-target", () => ({
  NewSessionExecTargetLine: () => null,
}));
vi.mock("../task-progress/task-progress-bar", () => ({
  TaskProgressBar: () => null,
}));
vi.mock("../task-progress/derive", () => ({
  deriveTaskProgress: () => ({ total: 0, done: 0 }),
}));
vi.mock("../capability/use-capabilities", () => {
  const caps = {
    has: (c: string) => c === "steer",
    permissionModeMeta: {
      allowedModes: [],
      defaultMode: "default",
      switchableDuringTurn: true,
      order: [],
    },
  };
  return {
    useSessionCapabilities: () => ({ caps }),
    useBackendCapabilities: () => ({ caps }),
  };
});
vi.mock("../permission-mode", () => ({
  usePermissionMode: () => ({
    mode: "default",
    modes: [],
    setMode: vi.fn(),
    cycleMode: vi.fn(),
    error: null,
    permissionModeAtLaunch: "",
    hasActiveSession: false,
  }),
}));
vi.mock("../chat-panel-context-usage", () => ({
  computeComposerContextUsage: () => ({ max: 0, used: 0 }),
}));

import { ChatPanel } from "../chat-panel";
import {
  LocalCommandsProvider,
  TerminalTransportProvider,
} from "@agentre-hub/agentre-ui";
import { useChatStreamsStore } from "@/stores/chat-streams-store";
import { desktopLocalCommandsAccess } from "../local-commands-access-desktop";
import { desktopTerminalTransport } from "../terminal/terminal-transport-desktop";

function render(ui: React.ReactElement) {
  return rtlRender(ui, {
    wrapper: ({ children }) => (
      <TerminalTransportProvider transport={desktopTerminalTransport}>
        <LocalCommandsProvider access={desktopLocalCommandsAccess}>
          {children}
        </LocalCommandsProvider>
      </TerminalTransportProvider>
    ),
  });
}

function makeSession(overrides: Record<string, unknown>) {
  return {
    agentColor: "agent-1",
    agentIcon: "",
    agentId: 7,
    agentName: "Eng",
    backendType: "builtin",
    createtime: 0,
    id: 42,
    lastMessageAt: 0,
    lastReadAt: 0,
    needsAttention: false,
    agentStatus: "idle",
    permissionMode: "",
    permissionModeAtLaunch: "",
    contextWindow: 0,
    llmProviderType: "",
    title: "Test session",
    workMode: "",
    worktreeBranch: "",
    projectId: 0,
    ...overrides,
  };
}

function lastComposer(): Record<string, unknown> {
  const props = composerProps.at(-1);
  if (!props) throw new Error("ChatComposer not rendered");
  return props;
}

describe("ChatPanel · stop entries", () => {
  beforeEach(() => {
    composerProps.length = 0;
    appMocks.StopChatMessage.mockReset();
    useChatStreamsStore.getState().streams.clear();
  });

  it("Given an idle session, When rendered, Then the composer is told no turn is running", () => {
    sessionStore.session = makeSession({ agentStatus: "idle" });

    render(<ChatPanel sessionId={42} />);

    expect(lastComposer().running).toBe(false);
  });

  it("Given a running turn, When stop is pressed in the composer, Then the turn is stopped and both stop entries show the same stopping state", async () => {
    // 停止 RPC 已回、aborted 事件还没到：会话仍是 running。
    appMocks.StopChatMessage.mockResolvedValue({ stopped: true });
    sessionStore.session = makeSession({ agentStatus: "running" });

    render(<ChatPanel sessionId={42} />);

    expect(lastComposer().running).toBe(true);
    expect(lastComposer().stopping).toBe(false);
    const header = screen.getByRole("button", { name: "Stop" });
    expect(header).toBeEnabled();

    (lastComposer().onStop as () => void)();

    await waitFor(() =>
      expect(appMocks.StopChatMessage).toHaveBeenCalledWith({ sessionId: 42 }),
    );
    await waitFor(() => expect(lastComposer().stopping).toBe(true));
    expect(header).toBeDisabled();
    expect(header.querySelector(".animate-spin")).not.toBeNull();
    expect(appMocks.StopChatMessage).toHaveBeenCalledTimes(1);
  });

  it("Given a running turn, When stop is pressed in the header, Then the composer's stop control also shows stopping", async () => {
    appMocks.StopChatMessage.mockResolvedValue({ stopped: true });
    sessionStore.session = makeSession({ agentStatus: "running" });

    render(<ChatPanel sessionId={42} />);
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));

    await waitFor(() => expect(lastComposer().stopping).toBe(true));
  });
});
