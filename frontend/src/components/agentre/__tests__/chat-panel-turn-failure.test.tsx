/**
 * chat-panel-turn-failure.test.tsx — 回合失败卡的出口由 ChatPanel 接到哪里（C3）。
 *
 * 只桩掉 RPC、会话数据与输入框；转录是真的 ChatTranscript（关掉虚拟化，happy-dom
 * 量不出行高），所以 ChatPanel → ChatTranscript → 行渲染上下文 → ErrorCard 这条宿主
 * 接线整条走生产路径。按类别出哪些按钮由共享包的 transcript-row-view-error.test 覆盖。
 */

import {
  fireEvent,
  render as rtlRender,
  screen,
  waitFor,
  within,
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
  RegenerateChatMessage: vi.fn(),
  ResolveLocalCommandScope: vi.fn(),
  SendChatMessage: vi.fn(),
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
  OnFileDrop: vi.fn(),
  OnFileDropOff: vi.fn(),
}));

vi.mock("@/hooks/use-project-tree", () => ({
  useProjectTree: () => ({ tree: [], invalidate: () => {}, loaded: true }),
}));

const sessionStore = vi.hoisted(() => ({
  session: null as Record<string, unknown> | null,
  messages: [] as Array<Record<string, unknown>>,
}));
vi.mock("@/hooks/use-chat-session", () => ({
  useChatSession: () => ({
    session: sessionStore.session,
    messages: sessionStore.messages,
    loading: false,
    error: null,
    reload: () => Promise.resolve(),
    setMessages: vi.fn(),
  }),
}));
vi.mock("@/hooks/use-cc-usage", () => ({ useCCUsage: () => undefined }));

// 输入框桩掉；转录换成真的 ChatTranscript，只关掉虚拟化。
vi.mock("../chat", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../chat")>();
  const React = await import("react");
  return {
    ...actual,
    QuotaMeter: () => null,
    ChatComposer: React.forwardRef(() => null),
    ChatTranscript: (
      props: React.ComponentProps<typeof actual.ChatTranscript>,
    ) =>
      React.createElement(actual.ChatTranscript, {
        ...props,
        virtualize: false,
        scrollElement: undefined,
      }),
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
    has: () => false,
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
  AGENTRE_UI_NAMESPACE,
  LocalCommandsProvider,
  TerminalTransportProvider,
} from "@agentre-hub/agentre-ui";
import i18n from "@/i18n";
import { desktopLocalCommandsAccess } from "../local-commands-access-desktop";
import { desktopTerminalTransport } from "../terminal/terminal-transport-desktop";

const uiT = (key: string) => i18n.t(key, { ns: AGENTRE_UI_NAMESPACE });

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
    agentId: 9,
    agentName: "Eng",
    backendType: "claudecode",
    createtime: 0,
    id: 42,
    lastMessageAt: 0,
    lastReadAt: 0,
    needsAttention: false,
    agentStatus: "error",
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

function message(
  id: number,
  role: "user" | "assistant",
  overrides: Record<string, unknown> = {},
) {
  return {
    blocks: role === "user" ? [{ type: "text", text: "try it" }] : [],
    cachedTokens: 0,
    cacheCreationTokens: 0,
    completionTokens: 0,
    createtime: 0,
    durationMs: 0,
    errorText: "",
    id,
    model: "",
    promptTokens: 0,
    reasoningTokens: 0,
    role,
    seq: id,
    sessionId: 42,
    ...overrides,
  };
}

function failedTurn(errorText: string) {
  sessionStore.messages = [
    message(1, "user"),
    message(2, "assistant", { errorText }),
  ];
}

describe("ChatPanel · 回合失败卡的出口", () => {
  beforeEach(() => {
    appMocks.RegenerateChatMessage.mockReset();
    appMocks.SendChatMessage.mockReset();
    sessionStore.session = makeSession({});
  });

  it("Given an overloaded turn, When 重试这一轮 is clicked, Then the same message is regenerated right away without the permanent-loss confirmation", async () => {
    appMocks.RegenerateChatMessage.mockResolvedValue({
      assistantMessageId: 4,
      sessionId: 42,
      stream: "chat:42:4",
      userMessageId: 3,
    });
    failedTurn(
      'API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}',
    );

    render(<ChatPanel active sessionId={42} />);
    const card = await screen.findByTestId("turn-error-card");
    fireEvent.click(
      within(card).getByRole("button", {
        name: uiT("chat.errorCard.retryTurn"),
      }),
    );

    await waitFor(() =>
      expect(appMocks.RegenerateChatMessage).toHaveBeenCalledOnce(),
    );
    expect(appMocks.RegenerateChatMessage.mock.calls[0]?.[0]).toMatchObject({
      sessionId: 42,
      messageId: 2,
    });
    expect(
      screen.queryByText(i18n.t("chatPanel.regenerateDialog.description")),
    ).toBeNull();
  });

  it("Given an overloaded turn that later turns followed, When 重试这一轮 is clicked, Then it still asks before discarding the later conversation", async () => {
    sessionStore.messages = [
      message(1, "user"),
      message(2, "assistant", {
        errorText:
          'API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}',
      }),
      message(3, "user"),
      message(4, "assistant", { blocks: [{ type: "text", text: "done" }] }),
    ];

    render(<ChatPanel active sessionId={42} />);
    const card = await screen.findByTestId("turn-error-card");
    fireEvent.click(
      within(card).getByRole("button", {
        name: uiT("chat.errorCard.retryTurn"),
      }),
    );

    expect(
      await screen.findByText(i18n.t("chatPanel.regenerateDialog.description")),
    ).toBeInTheDocument();
    expect(appMocks.RegenerateChatMessage).not.toHaveBeenCalled();
  });

  it("Given a Claude Code session whose login expired, When the failure renders, Then the card carries the claude /login command and no retry", async () => {
    failedTurn("Failed to authenticate. API Error: 403");

    render(<ChatPanel active sessionId={42} />);
    const card = await screen.findByTestId("turn-error-card");

    expect(within(card).getByText("claude /login")).toBeInTheDocument();
    expect(
      within(card).queryByRole("button", {
        name: uiT("chat.errorCard.retryTurn"),
      }),
    ).toBeNull();
  });

  it("Given a turn whose process exited, When 继续 is clicked, Then the localized continue text is sent instead of the English literal", async () => {
    appMocks.SendChatMessage.mockResolvedValue({
      assistantMessageId: 4,
      sessionId: 42,
      stream: "chat:42:4",
      userMessageId: 3,
    });
    failedTurn("codex: process exited unexpectedly");

    render(<ChatPanel active sessionId={42} />);
    const card = await screen.findByTestId("turn-error-card");
    fireEvent.click(
      within(card).getByRole("button", {
        name: uiT("chat.errorCard.continue"),
      }),
    );

    await waitFor(() =>
      expect(appMocks.SendChatMessage).toHaveBeenCalledOnce(),
    );
    expect(appMocks.SendChatMessage.mock.calls[0]?.[0]).toMatchObject({
      sessionId: 42,
      text: i18n.t("chatPanel.errorCard.continueMessage"),
    });
    // spec C3：中文界面发的是「继续」。
    expect(i18n.getFixedT("zh-CN")("chatPanel.errorCard.continueMessage")).toBe(
      "继续",
    );
  });
});
