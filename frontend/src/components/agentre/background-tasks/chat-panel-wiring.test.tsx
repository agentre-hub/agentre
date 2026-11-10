/**
 * chat-panel-wiring.test.tsx — 后台任务弹层的三个动作由 ChatPanel 接到绑定（A4/A5）。
 * 弹层的进行态只认回调返回的 Promise：失败必须 reject 才能让按钮复原。
 *
 * 只桩掉 RPC、会话数据与重组件；ChatPanelHeader 换成记录 props 的桩 —— 弹层自身的
 * 进行态与两段式确认由 background-tasks-popover.test.tsx 覆盖。
 */

import { render as rtlRender } from "@testing-library/react";
import * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { BackgroundTask, SessionProcess } from "./types";

const runtimeMocks = vi.hoisted(() => ({ BrowserOpenURL: vi.fn() }));
const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const headerProps = vi.hoisted(() => [] as Array<Record<string, unknown>>);
vi.mock("../chat-panel/chat-panel-header", () => ({
  ChatPanelHeader: (props: Record<string, unknown>) => {
    headerProps.push(props);
    return null;
  },
}));

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
  KillSessionProcess: vi.fn(),
  OpenSessionProcessPort: vi.fn(),
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
  BrowserOpenURL: runtimeMocks.BrowserOpenURL,
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

function lastHeaderProp<T>(name: string): T {
  const props = headerProps.at(-1);
  if (!props) throw new Error("ChatPanelHeader not rendered");
  const fn = props[name];
  if (typeof fn !== "function") throw new Error(`${name} not wired`);
  return fn as T;
}

const proc: SessionProcess = {
  pid: 4242,
  command: "node server.js --inspect=9229",
  ports: [3000, 9229],
  startedAt: 1_700_000_000_000,
  cwd: "/w",
};

const task: BackgroundTask = {
  toolUseId: "toolu-1",
  taskId: "b3875slp0",
  kind: "local_bash",
  description: "sleep 20",
  status: "running",
};

describe("ChatPanel · background tasks popover wiring", () => {
  beforeEach(() => {
    headerProps.length = 0;
    appMocks.StopBackgroundTask.mockReset();
    appMocks.KillSessionProcess.mockReset();
    appMocks.OpenSessionProcessPort.mockReset();
    runtimeMocks.BrowserOpenURL.mockReset();
    sonnerMocks.toast.error.mockReset();
    sonnerMocks.toast.success.mockReset();
    sessionStore.session = makeSession({ agentStatus: "running" });
    vi.spyOn(console, "error").mockImplementation(() => {});
  });

  it("Given the stop RPC fails, When the popover stops a task, Then the callback rejects so the button can recover", async () => {
    appMocks.StopBackgroundTask.mockRejectedValue(new Error("missing task id"));
    render(<ChatPanel sessionId={42} />);
    const onStopTask =
      lastHeaderProp<(t: BackgroundTask) => Promise<void>>("onStopTask");
    await expect(onStopTask(task)).rejects.toThrow("missing task id");
    expect(appMocks.StopBackgroundTask).toHaveBeenCalledWith({
      sessionId: 42,
      toolUseId: "toolu-1",
    });
  });

  it("Given the kill succeeds, When the popover kills a process, Then it resolves and a toast names the command", async () => {
    appMocks.KillSessionProcess.mockResolvedValue(undefined);
    render(<ChatPanel sessionId={42} />);
    const onKill =
      lastHeaderProp<(p: SessionProcess) => Promise<void>>("onKillProcess");
    await expect(onKill(proc)).resolves.toBeUndefined();
    expect(appMocks.KillSessionProcess).toHaveBeenCalledWith(
      42,
      4242,
      1_700_000_000_000,
    );
    expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
      "Stopped node server.js --inspect=9229",
    );
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
  });

  it("Given the kill fails, When the popover kills a process, Then it rejects after reporting the failure", async () => {
    appMocks.KillSessionProcess.mockRejectedValue(new Error("denied"));
    render(<ChatPanel sessionId={42} />);
    const onKill =
      lastHeaderProp<(p: SessionProcess) => Promise<void>>("onKillProcess");
    await expect(onKill(proc)).rejects.toThrow("denied");
    expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
      "Failed to stop process: denied",
    );
    expect(sonnerMocks.toast.success).not.toHaveBeenCalled();
  });

  it("Given a process with several ports, When the popover opens the second port, Then that port is forwarded and opened", async () => {
    appMocks.OpenSessionProcessPort.mockResolvedValue("http://localhost:9229");
    render(<ChatPanel sessionId={42} />);
    const onOpen =
      lastHeaderProp<(p: SessionProcess, port: number) => void>(
        "onOpenProcess",
      );
    onOpen(proc, 9229);
    await vi.waitFor(() =>
      expect(appMocks.OpenSessionProcessPort).toHaveBeenCalledWith(42, 9229),
    );
    await vi.waitFor(() =>
      expect(runtimeMocks.BrowserOpenURL).toHaveBeenCalledWith(
        "http://localhost:9229",
      ),
    );
  });
});
