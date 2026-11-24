/**
 * ChatPanel 在「文件占满」时把对话缩成左侧窄栏：最后一条用户消息、AI 正在执行的
 * 动作（只在有进行中的工具时出现）和同一个输入框——发出去的就是这个会话的消息。
 *
 * 只桩掉 RPC、会话数据与重组件；分屏、预览面板与窄栏是真的。ChatComposer 换成记录
 * props 的桩（它自己的行为由共享包测试覆盖）。
 */

import "@testing-library/jest-dom/vitest";

import {
  act,
  render as rtlRender,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
  WorkspaceFsReadFile: vi.fn().mockResolvedValue({
    content: "file body",
    contentType: "",
    binary: false,
    tooLarge: false,
  }),
  WorkspaceFsGitFileContent: vi.fn(),
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

vi.mock("../../../../../wailsjs/go/app/App", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  ...appMocks,
}));
vi.mock("@/../wailsjs/go/app/App", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  ...appMocks,
}));
vi.mock("../../../../../wailsjs/runtime/runtime", () => ({
  BrowserOpenURL: vi.fn(),
  EventsOff: vi.fn(),
  EventsOn: vi.fn(() => vi.fn()),
}));
vi.mock("@/lib/file-preview/monaco-loader", () => ({
  loadMonaco: vi.fn(async () => ({
    editor: {
      create: () => ({ setValue() {}, dispose() {} }),
      createDiffEditor: () => ({ setModel() {}, dispose() {} }),
      createModel: () => ({ dispose() {} }),
      setTheme() {},
    },
  })),
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

const composerProps = vi.hoisted(() => [] as Array<Record<string, unknown>>);
vi.mock("../../chat", async () => {
  const React = await import("react");
  return {
    QuotaMeter: () => null,
    ChatComposer: React.forwardRef((props: Record<string, unknown>) => {
      composerProps.push(props);
      return React.createElement("div", { "data-testid": "composer" });
    }),
    ChatTranscript: (props: { active?: boolean }) =>
      React.createElement("div", {
        "data-testid": "chat-transcript",
        "data-active": String(props.active),
      }),
  };
});
vi.mock("../../project-merge-dialog", () => ({
  ProjectMergeDialog: () => null,
}));
vi.mock("../../session-exec-target", () => ({
  NewSessionExecTargetLine: () => null,
}));
vi.mock("../../task-progress/task-progress-bar", () => ({
  TaskProgressBar: () => null,
}));
vi.mock("../../task-progress/derive", () => ({
  deriveTaskProgress: () => ({ total: 0, done: 0 }),
}));
vi.mock("../../capability/use-capabilities", () => {
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
vi.mock("../../permission-mode", () => ({
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
vi.mock("../../chat-panel-context-usage", () => ({
  computeComposerContextUsage: () => ({ max: 0, used: 0 }),
}));

import {
  LocalCommandsProvider,
  TerminalTransportProvider,
} from "@agentre-hub/agentre-ui";
import { useChatStreamsStore } from "@/stores/chat-streams-store";
import {
  DEFAULT_FILE_RATIO,
  useEditorSplitStore,
} from "@/stores/editor-split-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";

import { ChatPanel } from "../../chat-panel";
import { desktopLocalCommandsAccess } from "../../local-commands-access-desktop";
import { desktopTerminalTransport } from "../../terminal/terminal-transport-desktop";

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

function makeSession(overrides: Record<string, unknown> = {}) {
  return {
    agentColor: "agent-1",
    agentIcon: "",
    agentId: 7,
    agentName: "Eng",
    backendType: "builtin",
    createtime: 0,
    id: 42,
    cwd: "/repo",
    lastMessageAt: 0,
    lastReadAt: 0,
    needsAttention: false,
    agentStatus: "idle",
    permissionMode: "",
    permissionModeAtLaunch: "",
    contextWindow: 0,
    llmProviderType: "",
    title: "Dark mode",
    workMode: "",
    worktreeBranch: "",
    projectId: 0,
    ...overrides,
  };
}

const USER_ASK = "开关的位置往上挪，放到「外观」分组第一个";

function seedConversation() {
  sessionStore.messages = [
    {
      id: 1,
      role: "user",
      createtime: 1,
      blocks: [{ type: "text", text: "先给设置页加深色模式" }],
    },
    { id: 2, role: "assistant", createtime: 2, blocks: [] },
    {
      id: 3,
      role: "user",
      createtime: 3,
      blocks: [{ type: "text", text: USER_ASK }],
    },
    { id: 4, role: "assistant", createtime: 4, blocks: [] },
  ];
}

function startTurnRunning(tool?: {
  name: string;
  input: Record<string, unknown>;
}) {
  act(() => {
    const store = useChatStreamsStore.getState();
    store.openStream({
      assistantMessageId: 4,
      name: "chat:event:42:4",
      sessionId: 42,
      streamStartedAt: Date.now(),
    });
    if (tool) {
      store.appendLiveToolUse(42, 4, {
        toolName: tool.name,
        toolUseId: "tu-1",
        toolInput: tool.input,
      });
    }
  });
}

function openFileFull() {
  act(() => {
    useFilePreviewTabsStore
      .getState()
      .openPreviewInNewTab(42, "src/settings/theme.ts", "directory");
    useEditorSplitStore.getState().toggleFileFull(42);
  });
}

const rail = () => screen.getByRole("main", { name: "Chat" });

beforeEach(() => {
  localStorage.clear();
  composerProps.length = 0;
  sessionStore.session = makeSession();
  sessionStore.messages = [];
  appMocks.SendChatMessage.mockReset();
  appMocks.SendChatMessage.mockResolvedValue({
    assistantMessageId: 10,
    stream: "chat:event:42:10",
    userMessageId: 9,
    sessionId: 42,
  });
  useChatStreamsStore.getState().streams.clear();
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useEditorSplitStore.setState({
    fileRatio: DEFAULT_FILE_RATIO,
    fileFullBySession: {},
  });
});

describe("ChatPanel · 编辑区分屏", () => {
  it("Given a file is open, Then the preview sits beside the chat inside the editor split", () => {
    seedConversation();
    act(() => {
      useFilePreviewTabsStore
        .getState()
        .openPreviewInNewTab(42, "src/a.ts", "directory");
    });
    render(<ChatPanel sessionId={42} />);

    const split = screen.getByTestId("editor-split");
    expect(split).toHaveAttribute("data-layout", "split");
    expect(
      within(split).getByRole("complementary", { name: "File preview" }),
    ).toBeInTheDocument();
    expect(within(split).getByTestId("chat-transcript")).toBeInTheDocument();
  });
});

describe("ChatPanel · 文件占满时的对话窄栏", () => {
  it("Given file-full and a running Edit, Then the rail shows the last user message, the current action and the composer — not the transcript", () => {
    seedConversation();
    render(<ChatPanel sessionId={42} />);
    startTurnRunning({
      name: "Edit",
      input: { file_path: "/repo/src/settings/theme.ts" },
    });
    openFileFull();

    expect(screen.getByTestId("editor-split")).toHaveAttribute(
      "data-layout",
      "file-full",
    );
    expect(within(rail()).getByText(USER_ASK)).toBeInTheDocument();
    expect(
      within(rail()).queryByText("先给设置页加深色模式"),
    ).not.toBeInTheDocument();
    expect(within(rail()).getByTestId("chat-rail-action")).toHaveTextContent(
      "Edit · src/settings/theme.ts",
    );
    expect(within(rail()).getByTestId("composer")).toBeInTheDocument();
    // 转录收起而不卸载（滚动位置与虚拟器状态留着），按「不在前台」冻住。
    expect(screen.getByTestId("chat-transcript-scroll")).not.toBeVisible();
    expect(screen.getByTestId("chat-transcript")).toHaveAttribute(
      "data-active",
      "false",
    );
  });

  it("Given file-full and no tool in progress, Then the rail has no action line", () => {
    seedConversation();
    render(<ChatPanel sessionId={42} />);
    startTurnRunning();
    openFileFull();

    expect(within(rail()).getByText(USER_ASK)).toBeInTheDocument();
    expect(
      within(rail()).queryByTestId("chat-rail-action"),
    ).not.toBeInTheDocument();
  });

  it("Given the tool finished, Then the action line disappears", () => {
    seedConversation();
    render(<ChatPanel sessionId={42} />);
    startTurnRunning({ name: "Bash", input: { command: "pnpm test" } });
    openFileFull();
    expect(within(rail()).getByTestId("chat-rail-action")).toHaveTextContent(
      "Bash · pnpm test",
    );

    act(() => {
      useChatStreamsStore
        .getState()
        .appendLiveToolResult(42, 4, { toolUseId: "tu-1", text: "ok" });
    });
    expect(
      within(rail()).queryByTestId("chat-rail-action"),
    ).not.toBeInTheDocument();
  });

  it("When the user types in the rail composer and sends, Then the message goes to this session", async () => {
    seedConversation();
    render(<ChatPanel sessionId={42} />);
    openFileFull();

    const submit = composerProps.at(-1)?.onSubmit as
      | ((payload: { text: string }) => void)
      | undefined;
    expect(submit).toBeDefined();
    act(() => submit?.({ text: "再挪一下" }));

    await waitFor(() =>
      expect(appMocks.SendChatMessage).toHaveBeenCalledWith(
        expect.objectContaining({ sessionId: 42, text: "再挪一下" }),
      ),
    );
  });

  it("When 还原 is clicked, Then the transcript comes back beside the file", async () => {
    seedConversation();
    render(<ChatPanel sessionId={42} />);
    openFileFull();

    await userEvent.click(screen.getByRole("button", { name: "Restore" }));

    expect(screen.getByTestId("editor-split")).toHaveAttribute(
      "data-layout",
      "split",
    );
    expect(screen.getByTestId("chat-transcript-scroll")).toBeVisible();
    expect(screen.getByTestId("chat-transcript")).toHaveAttribute(
      "data-active",
      "true",
    );
    expect(
      screen.queryByRole("main", { name: "Chat" }),
    ).not.toBeInTheDocument();
  });
});
