/**
 * chat-panel-pending-below.test.tsx — 回到底部药丸的「N 项待处理」由宿主按活状态算
 * （视口下沿之后、未决的审批 / 提问），与补齐（catch-up）无关（C4）。
 *
 * 滚动几何由 useTranscriptScroll 桩出（bottomVisibleId / showBackToBottom），
 * 药丸本身是真的。
 */

import { render as rtlRender, screen } from "@testing-library/react";
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
const scrollState = vi.hoisted(() => ({
  showBackToBottom: true,
  bottomVisibleId: 1 as number | null,
}));
vi.mock("@agentre-hub/agentre-ui", async (importOriginal) => {
  const React = await import("react");
  const actual = await importOriginal<object>();
  return {
    ...actual,
    useTranscriptScroll: () => ({
      armCollapsedRestore: vi.fn(),
      bottomVisibleId: scrollState.bottomVisibleId,
      followBottom: vi.fn(),
      onScroll: vi.fn(),
      scrollElement: null,
      scrollRef: React.createRef(),
      scrollToBottom: vi.fn(),
      setScrollElement: vi.fn(),
      showBackToBottom: scrollState.showBackToBottom,
      turnsBelow: 0,
    }),
  };
});
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

function perm(requestId: string, resolved: boolean) {
  return {
    type: "tool_use",
    toolUseId: `tu-${requestId}`,
    canonical: {
      kind: "tool.permission",
      toolPermission: { requestId, toolName: "Bash", resolved },
    },
  };
}
function ask(requestId: string, answered: boolean) {
  return {
    type: "ask_user_question",
    canonical: {
      kind: "user.ask",
      userAsk: { requestId, questions: [], answered },
    },
  };
}
function msg(id: number, blocks: Array<Record<string, unknown>> = []) {
  return { id, sessionId: 42, role: "assistant", blocks };
}
function setLive(
  messageId: number,
  liveBlocks: Array<Record<string, unknown>>,
) {
  useChatStreamsStore.setState({
    streams: new Map([
      [42, new Map([[messageId, { liveBlocks, streamStartedAt: 1 } as never]])],
    ]),
  });
}

describe("ChatPanel · pendingBelow", () => {
  beforeEach(() => {
    composerProps.length = 0;
    scrollState.showBackToBottom = true;
    scrollState.bottomVisibleId = 1;
    sessionStore.session = makeSession({ agentStatus: "idle" });
    sessionStore.messages = [];
    useChatStreamsStore.getState().streams.clear();
  });

  it("Given 视口下方有未决审批与提问且没有 catch-up, When 药丸显示, Then 写出待处理项数", () => {
    sessionStore.messages = [
      msg(1, [perm("above", false)]),
      msg(2, [perm("p1", false), perm("p2", true)]),
      msg(3, [ask("q1", false), ask("q2", true)]),
    ];

    render(<ChatPanel sessionId={42} />);

    expect(screen.getByTestId("transcript-jump-pending")).toHaveTextContent(
      "2 pending",
    );
  });

  it("Given 未决审批挂在在流消息的 liveBlocks 上, When 药丸显示, Then 也计入", () => {
    sessionStore.messages = [msg(1), msg(2)];
    setLive(2, [perm("live1", false)]);

    render(<ChatPanel sessionId={42} />);

    expect(screen.getByTestId("transcript-jump-pending")).toHaveTextContent(
      "1 pending",
    );
  });

  it("Given 视口下方的审批已解决, When 药丸显示, Then 没有待处理段", () => {
    sessionStore.messages = [msg(1), msg(2, [perm("p1", true)])];

    render(<ChatPanel sessionId={42} />);

    expect(screen.getByTestId("transcript-jump-control")).toBeInTheDocument();
    expect(screen.queryByTestId("transcript-jump-pending")).toBeNull();
  });

  it("Given 视口下方有未决的 exec 审批（OpenClaw）, When 药丸显示, Then 也计入", () => {
    sessionStore.messages = [
      msg(1),
      msg(2, [
        {
          type: "exec_approval",
          execApproval: {
            id: "ex1",
            commandText: "rm -rf x",
            status: "pending",
          },
        },
        {
          type: "exec_approval",
          execApproval: { id: "ex2", commandText: "ls", status: "approved" },
        },
      ]),
    ];

    render(<ChatPanel sessionId={42} />);

    expect(screen.getByTestId("transcript-jump-pending")).toHaveTextContent(
      "1 pending",
    );
  });

  it("Given 未决项只在视口上方, When 药丸显示, Then 不计入", () => {
    sessionStore.messages = [msg(1, [perm("above", false)]), msg(2)];

    render(<ChatPanel sessionId={42} />);

    expect(screen.queryByTestId("transcript-jump-pending")).toBeNull();
  });
});
