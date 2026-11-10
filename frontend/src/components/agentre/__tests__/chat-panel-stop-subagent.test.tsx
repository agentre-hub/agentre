/**
 * chat-panel-stop-subagent.test.tsx — 转录里子代理卡的停止键由 ChatPanel 接到
 * StopBackgroundTask（A4）。卡片的进行态只认回调返回的 Promise：失败必须 reject
 * 才能让按钮复原，成功 resolve 则保持转圈直到状态变化。
 *
 * 只桩掉 RPC、会话数据与重组件；ChatTranscript 换成记录 props 的桩 —— 卡片自身的
 * 进行态由共享包的 agent-spawn/card.test.tsx 覆盖。
 */

import { render as rtlRender, screen, waitFor } from "@testing-library/react";
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
  StopBackgroundTask: vi.fn(),
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

const transcriptProps = vi.hoisted(() => [] as Array<Record<string, unknown>>);
vi.mock("../chat", async () => {
  const React = await import("react");
  return {
    QuotaMeter: () => null,
    ChatComposer: React.forwardRef(() => null),
    ChatTranscript: (props: Record<string, unknown>) => {
      transcriptProps.push(props);
      return null;
    },
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
    has: (c: string) => c === "steer" || c === "stop_background_task",
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

function lastStopSubagent(): (toolUseId: string) => void | Promise<void> {
  const props = transcriptProps.at(-1);
  if (!props) throw new Error("ChatTranscript not rendered");
  const fn = props.onStopSubagent;
  if (typeof fn !== "function") throw new Error("onStopSubagent not wired");
  return fn as (toolUseId: string) => void | Promise<void>;
}

describe("ChatPanel · sub-agent card stop", () => {
  beforeEach(() => {
    transcriptProps.length = 0;
    appMocks.StopBackgroundTask.mockReset();
    sessionStore.session = makeSession({ agentStatus: "running" });
  });

  it("Given the stop RPC succeeds, When the card stops a sub-agent, Then the callback resolves after calling StopBackgroundTask", async () => {
    appMocks.StopBackgroundTask.mockResolvedValue({});
    render(<ChatPanel sessionId={42} />);

    await expect(lastStopSubagent()("toolu-1")).resolves.toBeUndefined();
    expect(appMocks.StopBackgroundTask).toHaveBeenCalledWith({
      sessionId: 42,
      toolUseId: "toolu-1",
    });
  });

  it("Given the stop RPC fails, When the card stops a sub-agent, Then the callback rejects so the card can recover, and the failure is still reported", async () => {
    appMocks.StopBackgroundTask.mockRejectedValue(new Error("missing task id"));
    vi.spyOn(console, "error").mockImplementation(() => {});
    render(<ChatPanel sessionId={42} />);

    await expect(lastStopSubagent()("toolu-1")).rejects.toThrow(
      "missing task id",
    );
    await waitFor(() =>
      expect(
        screen.getByText(/Failed to stop background task/),
      ).toBeInTheDocument(),
    );
  });
});
