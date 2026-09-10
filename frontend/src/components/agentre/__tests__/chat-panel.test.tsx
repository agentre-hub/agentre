/**
 * chat-panel.test.tsx — ChatPanel 内部派生行为测试（T17 breadcrumb + T18 worktree merge）。
 *
 * 策略：mock 掉所有 wailsjs RPC、heavy child components（ChatComposer / ChatTranscript /
 * ProjectMergeDialog），以及 use-project-tree / use-chat-session，保持 ChatPanel
 * 自身的派生逻辑可测而不拉全量组件树。
 */

import {
  act,
  fireEvent,
  render as rtlRender,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import * as React from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, onTestFinished, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

vi.mock("sonner", () => sonnerMocks);

// ── wailsjs App RPC mocks ────────────────────────────────────────────────────

const appMocks = vi.hoisted(() => ({
  CancelQueuedChatMessage: vi.fn(),
  CompactChatSession: vi.fn(),
  DeleteChatSession: vi.fn(),
  EditChatMessage: vi.fn(),
  EnqueueChatMessage: vi.fn(),
  EnsureChatSession: vi.fn(),
  GetCCUsage: vi.fn().mockResolvedValue({ reason: "" }),
  GetChatLaunchCommand: vi.fn(),
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
  ResolveLocalCommandScope: vi.fn(),
  SendChatMessage: vi.fn(),
  SetChatGoal: vi.fn(),
  StartChatGoal: vi.fn(),
  StopChatMessage: vi.fn(),
  TerminalClose: vi.fn(),
  TerminalRunCommand: vi.fn(),
  ClearChatGoal: vi.fn(),
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
  // 侧栏的多工作根认领：本组用例都不关心它，恒为空 → 根切换器不渲染。
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
  // 需要 ProjectListTree 供 use-project-tree，但我们 mock 掉整个 hook
  ProjectListTree: vi.fn().mockResolvedValue([]),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);
// 侧栏那几个 hook 用的是别名写法，vitest 按写法登记 mock，两条 specifier 都要挂。
vi.mock("@/../wailsjs/go/app/App", () => appMocks);

const componentMocks = vi.hoisted(() => ({
  chatComposerProps: [] as Array<Record<string, unknown>>,
  chatTranscriptProps: [] as Array<Record<string, unknown>>,
  // ChatTranscript 桩要不要渲染 [data-message-id] 行。默认不渲染 —— 只有需要
  // 「视口下沿落在哪条消息上」的用例才打开,免得别的用例凭空多出锚点行。
  transcriptRowMessageIds: [] as number[],
  permissionModePillProps: [] as Array<Record<string, unknown>>,
  permissionMode: "plan",
  localCommandMenuActive: false,
  cycleMode: vi.fn(),
  setMode: vi.fn(),
  // ChatComposer 的命令式句柄桩:ChatPanel 经 composerRef 调 restoreDraft/clearDraft。
  composerHandle: {
    restoreDraft: vi.fn(),
    clearDraft: vi.fn(),
  },
  // 控制 useSessionCapabilities 桩返回的 caps;测试按 backend 切换 switchableDuringTurn。
  capsSwitchableDuringTurn: true,
  capsAllowedModes: ["default", "plan", "acceptEdits", "bypassPermissions"],
  capsImageInput: true,
  // reasoning_effort 能力位（spec 2026-09-01 决策 6）：默认 false，只有专门测试
  // 显式打开它，避免既有用例平白多出这颗控件。
  capsReasoningEffort: false,
  effectiveExecTarget: null as null | {
    kind: "local" | "desktop" | "daemon";
    deviceId: string;
    deviceName: string;
    backendType?: string;
    llmProviderKey?: string;
    llmModelKey?: string;
  },
  computeComposerContextUsage: vi.fn((..._args: unknown[]) => ({
    max: 0,
    used: 0,
  })),
}));

// ── wailsjs runtime mock（EventsOn / EventsOff）────────────────────────────

const runtimeMocks = vi.hoisted(() => ({
  EventsOff: vi.fn(),
  EventsOn: vi.fn((_event?: string, _handler?: (...args: unknown[]) => void) =>
    vi.fn(),
  ),
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => runtimeMocks);

// ── use-project-tree: 单例缓存 hook，直接 mock 返回测试用树 ──────────────────

vi.mock("@/hooks/use-project-tree", () => ({
  useProjectTree: () => ({
    tree: [
      {
        project: { id: 1, name: "Agentre" },
        children: [
          {
            project: {
              id: 2,
              name: "backend",
              color: "agent-5",
              path: "/local/repo",
            },
            children: [],
          },
        ],
      },
    ],
    invalidate: () => {},
    loaded: true,
  }),
}));

// ── use-chat-session: 直接 mock，避免真实 LoadChatSession RPC 被调用 ────────

// makeMockSession 构造最小化的 ChatSessionDetail，只提供测试需要的字段。
// 通过 `overrides` 注入测试想要的字段（projectId / workMode / title 等）。
const mockSessionStore: {
  messages: Array<Record<string, unknown>>;
  session: Record<string, unknown> | null;
  loading: boolean;
  error: string | null;
} = {
  messages: [],
  session: null,
  loading: false,
  error: null,
};

// setMessagesSpy 允许断言 setMessages 是否被调用（T29 subagent_activity_started）
const setMessagesSpy = vi.hoisted(() => vi.fn());
// reloadSpy 允许断言点「停止」后是否主动 reload 会话（重启遗孤 reconcile 后收回按钮）
const reloadSpy = vi.hoisted(() => vi.fn(() => Promise.resolve()));

vi.mock("@/hooks/use-chat-session", () => ({
  useChatSession: () => ({
    session: mockSessionStore.session,
    messages: mockSessionStore.messages,
    loading: mockSessionStore.loading,
    error: mockSessionStore.error,
    reload: reloadSpy,
    setMessages: setMessagesSpy,
  }),
}));

// useCCUsage: 捕获每次调用 deviceKey, 让测试断言 ChatPanel 把"哪台 device 的配额"
// 派给了 ChatComposer。返回值固定 undefined(未首探), 测试只关心 key 路由。
const ccUsageMock = vi.hoisted(() => ({
  calls: [] as string[],
}));

vi.mock("@/hooks/use-cc-usage", () => ({
  useCCUsage: (deviceKey: string) => {
    ccUsageMock.calls.push(deviceKey);
    return undefined;
  },
}));

// ── child component mocks ──────────────────────────────────────────────────

// ChatComposer / ChatTranscript 各自有大量依赖（TipTap / prism 等），mock 成最简桩。
// ChatComposer 用 forwardRef 把 ChatPanel 传下来的 composerRef 指向测试桩,
// 让 doSend 失败路径的 restoreDraft/clearDraft 调用可被断言。
vi.mock("../chat", async () => {
  const React = await import("react");
  return {
    QuotaMeter: () =>
      React.createElement("div", { "data-testid": "quota-meter" }),
    ChatComposer: React.forwardRef(
      (
        props: {
          localCommandHistoryScope?: unknown;
          onSubmit?: (text: string) => void;
          leadingControls?: React.ReactNode;
          trailingControls?: React.ReactNode;
          topSlot?: React.ReactNode;
        },
        ref: React.Ref<unknown>,
      ) => {
        componentMocks.chatComposerProps.push(props as Record<string, unknown>);
        if (ref) {
          (ref as React.MutableRefObject<unknown>).current =
            componentMocks.composerHandle;
        }
        return React.createElement(
          React.Fragment,
          null,
          props.topSlot,
          props.leadingControls,
          props.trailingControls,
          componentMocks.localCommandMenuActive &&
            props.localCommandHistoryScope
            ? React.createElement("div", {
                "data-testid": "local-command-history-menu",
                role: "listbox",
              })
            : null,
        );
      },
    ),
    ChatTranscript: (props: Record<string, unknown>) => {
      componentMocks.chatTranscriptProps.push(props);
      return React.createElement(
        "div",
        { "data-testid": "chat-transcript" },
        componentMocks.transcriptRowMessageIds.map((id) =>
          React.createElement("div", {
            key: id,
            "data-message-id": String(id),
          }),
        ),
      );
    },
  };
});

// ProjectMergeDialog：只渲染一个可识别的占位 span，供 T18 断言用。
vi.mock("../project-merge-dialog", () => ({
  ProjectMergeDialog: ({ sessionID }: { sessionID: number }) =>
    sessionID > 0
      ? React.createElement("div", { "data-testid": "merge-dialog" }, null)
      : null,
}));

vi.mock("../session-exec-target", async () => {
  const React = await import("react");
  return {
    NewSessionExecTargetLine: ({
      onEffectiveTarget,
    }: {
      onEffectiveTarget?: (
        target: typeof componentMocks.effectiveExecTarget,
      ) => void;
    }) => {
      React.useEffect(() => {
        onEffectiveTarget?.(componentMocks.effectiveExecTarget);
      }, [onEffectiveTarget]);
      return null;
    },
  };
});

// PermissionModePill 住在共享包里（两端同一颗），所以桩要打在包上；这里做**部分**
// mock，包的其余导出原样透传——整块替掉会连 ChatComposer、转录、ContextMeter 一起
// 换成假的，这个文件要测的集成行为就无从谈起了。
vi.mock("@agentre-hub/agentre-ui", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@agentre-hub/agentre-ui")>();
  const React = await import("react");
  return {
    ...actual,
    PermissionModePill: (props: Record<string, unknown>) => {
      componentMocks.permissionModePillProps.push(props);
      return React.createElement("button", {
        "data-testid": "permission-mode-pill",
        disabled: Boolean(props.disabled),
        type: "button",
      });
    },
  };
});

// usePermissionMode 留在宿主（它 import 了 Wails 绑定）：桩仍打在本地那个模块上。
vi.mock("../permission-mode", async () => {
  return {
    usePermissionMode: () => ({
      mode: componentMocks.permissionMode,
      modes: [],
      setMode: componentMocks.setMode,
      cycleMode: componentMocks.cycleMode,
      error: null,
      permissionModeAtLaunch: "",
      hasActiveSession: false,
    }),
  };
});

// useSessionCapabilities 桩 — Plan C 起 chat-panel 通过它读 set_permission_mode +
// PermissionModeMeta.SwitchableDuringTurn。codex 测试通过 capsSwitchableDuringTurn=false
// 模拟"turn 中不允许切 mode"行为(原走 backendType === 'codex' 硬分支)。
// 真实 hook 在 sessionId<=0 时返 null caps;桩同样按真实行为返 null,让"新对话"
// 路径走 useBackendCapabilities 分支。
function makeCapsStub(backendType?: string | null) {
  const supportsCompact = backendType === "codex" || backendType === "piagent";
  return {
    has: (c: string) =>
      c === "set_permission_mode" ||
      (c === "image_input" && componentMocks.capsImageInput) ||
      (c === "compact" && supportsCompact) ||
      (c === "reasoning_effort" && componentMocks.capsReasoningEffort),
    permissionModeMeta: {
      allowedModes: componentMocks.capsAllowedModes,
      defaultMode: "default",
      switchableDuringTurn: componentMocks.capsSwitchableDuringTurn,
      order: componentMocks.capsAllowedModes,
    },
  };
}

vi.mock("../capability/use-session-capabilities", () => ({
  useSessionCapabilities: (sessionId?: number | null) => ({
    caps:
      sessionId && sessionId > 0
        ? makeCapsStub(String(mockSessionStore.session?.backendType ?? ""))
        : null,
  }),
}));

// useBackendCapabilities 桩 — 新对话(sessionId<=0)按 backendType 拉 caps,
// 让 PermissionModePill 在首发前就能渲染。
vi.mock("../capability/use-backend-capabilities", () => ({
  useBackendCapabilities: (backendType?: string | null) => ({
    caps: backendType ? makeCapsStub(backendType) : null,
  }),
}));

vi.mock("../queued-messages-bar", () => ({
  QueuedMessagesBar: () => null,
}));

vi.mock("../task-progress/task-progress-bar", () => ({
  TaskProgressBar: () => null,
}));

vi.mock("../task-progress/derive", () => ({
  deriveTaskProgress: () => ({ total: 0, done: 0 }),
}));

// 本地命令卡片里的只读输出终端已随卡片搬进共享包,没法再按宿主路径桩掉。
// 桩掉 xterm 三件套即可:这些用例盯的是卡片外壳与停止/移除动作,终端渲染由
// 包里的 output-terminal.test.tsx 覆盖。
vi.mock("@xterm/xterm", () => ({
  Terminal: vi.fn().mockImplementation(() => ({
    open: vi.fn(),
    write: vi.fn(),
    loadAddon: vi.fn(),
    dispose: vi.fn(),
    focus: vi.fn(),
    cols: 80,
    rows: 24,
    buffer: { active: { length: 1, baseY: 0, cursorY: 0 } },
    options: {},
  })),
}));
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: vi.fn().mockImplementation(() => ({
    fit: vi.fn(),
    proposeDimensions: () => ({ cols: 80, rows: 24 }),
  })),
}));
vi.mock("@xterm/addon-web-links", () => ({ WebLinksAddon: vi.fn() }));

// chat-panel-context-usage 有复杂计算，桩掉
vi.mock("../chat-panel-context-usage", () => ({
  computeComposerContextUsage: (...args: unknown[]) =>
    componentMocks.computeComposerContextUsage(...args),
}));

// ── import after mocks ─────────────────────────────────────────────────────

import { ChatPanel } from "../chat-panel";
import {
  __resetCatchUpStateForTesting,
  openCatchUpWindow,
  recordCatchUp,
} from "../chat-panel-catchup-state";
import {
  __resetChatPanelScrollStateForTesting,
  COLLAPSED_RESTORE_GUARD_MS,
  loadTranscriptScrollState,
  LocalCommandCard,
  LocalCommandsProvider,
  TerminalTransportProvider,
} from "@agentre-hub/agentre-ui";
import {
  streamForMessage,
  useChatStreamsStore,
} from "@/stores/chat-streams-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionConnStore } from "@/stores/session-conn-store";
import { localCommandRuntimeStore } from "@/stores/local-command-runtime-store";
import { useLocalCommandsStore } from "@/stores/local-commands-store";
import { desktopLocalCommandsAccess } from "../local-commands-access-desktop";
import { desktopTerminalTransport } from "../terminal/terminal-transport-desktop";

// ChatPanel 经终端传输端口订阅本地命令的 PTY;卡片经本地命令接缝读条目。
// 生产里两个 Provider 都挂在 App 根。这里统一套上桌面实现(而不是替身):
// 本地命令与终端视图共用同一套 Wails 事件扇出与同一个 store,正是这些用例要盯的东西。
function TerminalTransportHost({ children }: { children?: React.ReactNode }) {
  return (
    <TerminalTransportProvider transport={desktopTerminalTransport}>
      <LocalCommandsProvider access={desktopLocalCommandsAccess}>
        {children}
      </LocalCommandsProvider>
    </TerminalTransportProvider>
  );
}

function render(
  ui: React.ReactElement,
  options?: Parameters<typeof rtlRender>[1],
) {
  return rtlRender(ui, { ...options, wrapper: TerminalTransportHost });
}

/** 清 store streams 以避免测试间串台 */
function resetStore() {
  __resetChatPanelScrollStateForTesting();
  __resetCatchUpStateForTesting();
  componentMocks.transcriptRowMessageIds = [];
  mockSessionStore.messages = [];
  mockSessionStore.session = null;
  mockSessionStore.loading = false;
  mockSessionStore.error = null;
  useChatStreamsStore.getState().streams.clear();
  useSessionConnStore.getState().__reset();
  runtimeMocks.EventsOff.mockReset();
  runtimeMocks.EventsOn.mockReset();
  runtimeMocks.EventsOn.mockImplementation(
    (_event?: string, _handler?: (...args: unknown[]) => void) => vi.fn(),
  );
  setMessagesSpy.mockClear();
  reloadSpy.mockClear();
  componentMocks.chatComposerProps.length = 0;
  componentMocks.chatTranscriptProps.length = 0;
  componentMocks.permissionModePillProps.length = 0;
  componentMocks.composerHandle.restoreDraft.mockClear();
  componentMocks.composerHandle.clearDraft.mockClear();
  componentMocks.permissionMode = "plan";
  componentMocks.localCommandMenuActive = false;
  // 默认 claudecode-like caps(允许 turn 中切 mode);Codex 测试用例显式置 false。
  componentMocks.capsSwitchableDuringTurn = true;
  componentMocks.capsAllowedModes = [
    "default",
    "plan",
    "acceptEdits",
    "bypassPermissions",
  ];
  componentMocks.capsImageInput = true;
  componentMocks.capsReasoningEffort = false;
  componentMocks.effectiveExecTarget = null;
  componentMocks.computeComposerContextUsage.mockClear();
  componentMocks.cycleMode.mockClear();
  componentMocks.setMode.mockClear();
  ccUsageMock.calls.length = 0;
  appMocks.SendChatMessage.mockReset();
  appMocks.PeerRunFresh.mockReset();
  appMocks.ListLLMModels.mockReset();
  appMocks.ListLLMModels.mockResolvedValue({ items: [] });
  appMocks.RegenerateChatMessage.mockReset();
  appMocks.SetChatGoal.mockReset();
  appMocks.GetChatGoal.mockReset();
  appMocks.ClearChatGoal.mockReset();
  appMocks.StartChatGoal.mockReset();
  appMocks.CompactChatSession.mockReset();
  appMocks.EnqueueChatMessage.mockReset();
  appMocks.EnsureChatSession.mockReset();
  appMocks.GetChatLaunchCommand.mockReset();
  appMocks.ResolveLocalCommandScope.mockReset();
  appMocks.ResolveLocalCommandScope.mockImplementation(
    () => new Promise(() => undefined),
  );
  appMocks.RemoteDeviceFingerprint.mockReset();
  appMocks.RemoteDeviceFingerprint.mockResolvedValue("sha256:local-desktop");
  appMocks.RemoteDeviceList.mockReset();
  appMocks.RemoteDeviceList.mockResolvedValue([]);
  appMocks.RemoteDeviceListProviders.mockReset();
  appMocks.RemoteDeviceListProviders.mockResolvedValue([]);
  appMocks.TerminalClose.mockReset();
  appMocks.TerminalRunCommand.mockReset();
  appMocks.SetChatSessionReasoningEffort.mockReset();
  localCommandRuntimeStore.resetForTesting();
  useLocalCommandsStore.setState({ entries: {} });
  sonnerMocks.toast.error.mockClear();
  sonnerMocks.toast.success.mockClear();
}

function observeLocalCommandFinish() {
  const originalFinish = useLocalCommandsStore.getState().finish;
  const finish = vi.fn(originalFinish);
  useLocalCommandsStore.setState({ finish });
  onTestFinished(() =>
    useLocalCommandsStore.setState({ finish: originalFinish }),
  );
  return finish;
}

type StopLocalCommand = (terminalId: string) => void | Promise<void>;

const TestLocalCommandCard = LocalCommandCard as React.ComponentType<
  React.ComponentProps<typeof LocalCommandCard> & {
    onStop?: StopLocalCommand;
  }
>;

function renderLocalCommandCardFromTranscript(terminalId: string) {
  const onStop = componentMocks.chatTranscriptProps.at(-1)
    ?.onStopLocalCommand as StopLocalCommand | undefined;
  return render(
    <TestLocalCommandCard
      entryId={terminalId}
      onOpenInTerminal={vi.fn()}
      onStop={onStop ?? (() => undefined)}
    />,
  );
}

function terminalListenerCleanups(terminalId: string) {
  return runtimeMocks.EventsOn.mock.calls.flatMap(([event], index) => {
    const cleanup = runtimeMocks.EventsOn.mock.results[index]?.value;
    return event?.startsWith(`terminal:${terminalId}:`) &&
      typeof cleanup === "function"
      ? [cleanup as ReturnType<typeof vi.fn>]
      : [];
  });
}

function deferred<T>() {
  let reject!: (reason?: unknown) => void;
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

describe("ChatPanel error recovery actions", () => {
  it("Given a failed turn, When Continue is clicked, Then it sends the literal continue message", async () => {
    mockSessionStore.session = makeSession({ id: 42, agentId: 9 });
    appMocks.SendChatMessage.mockResolvedValue({
      assistantMessageId: 102,
      sessionId: 42,
      stream: "chat:42:102",
      userMessageId: 101,
    });

    render(<ChatPanel active sessionId={42} />);
    const onContinue = componentMocks.chatTranscriptProps.at(-1)?.onContinue as
      | (() => void)
      | undefined;

    expect(onContinue).toBeTypeOf("function");
    act(() => onContinue?.());

    await waitFor(() =>
      expect(appMocks.SendChatMessage).toHaveBeenCalledOnce(),
    );
    expect(appMocks.SendChatMessage.mock.calls[0]?.[0]).toMatchObject({
      agentId: 9,
      sessionId: 42,
      text: "continue",
    });
  });

  it("Given a failed turn, When Regenerate is clicked, Then it waits for confirmation and warns that later conversation is lost", () => {
    mockSessionStore.session = makeSession({ id: 42, agentId: 9 });
    mockSessionStore.messages = [
      { blocks: [{ text: "try it", type: "text" }], id: 1, role: "user" },
      { blocks: [], errorText: "boom", id: 2, role: "assistant" },
    ];

    render(<ChatPanel active sessionId={42} />);
    const onRerun = componentMocks.chatTranscriptProps.at(-1)?.onRerun as
      | ((messageId: number) => void)
      | undefined;
    act(() => onRerun?.(2));

    expect(appMocks.RegenerateChatMessage).not.toHaveBeenCalled();
    expect(
      screen.getByText(
        /permanently discards this reply and all later conversation history/i,
      ),
    ).toBeInTheDocument();
  });
});

function transcriptScroller(container: HTMLElement): HTMLElement {
  const el = container.querySelector("section");
  if (!el) throw new Error("transcript scroller not found");
  Object.defineProperty(el, "clientHeight", {
    configurable: true,
    get: () => 480,
  });
  Object.defineProperty(el, "scrollHeight", {
    configurable: true,
    get: () => 4_000,
  });
  return el as HTMLElement;
}

function transcriptScrollerWithDynamicHeight(
  container: HTMLElement,
  scrollHeight: () => number,
): HTMLElement {
  const el = transcriptScroller(container);
  Object.defineProperty(el, "scrollHeight", {
    configurable: true,
    get: scrollHeight,
  });
  return el;
}

/** 模拟「用户从底部往上翻」:先落到底部,再往上滚 —— 只有 scrollTop 变小才算上滚。 */
function scrollUpFromBottom(el: HTMLElement, to = 1_240) {
  act(() => {
    el.scrollTop = 3_500;
    fireEvent.scroll(el);
  });
  act(() => {
    el.scrollTop = to;
    fireEvent.scroll(el);
  });
}

/** 构造 ChatSessionDetail 最小形状 */
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

// ─── T17: breadcrumb 派生 ─────────────────────────────────────────────────────

describe("ChatPanel · T17 breadcrumb 派生", () => {
  it("长会话标题在 toolbar 中最多显示两行而不是单行截断", () => {
    resetStore();
    const longTitle =
      "这是一个很长的 AI 对话标题，用来确认工具栏会尽量展示完整内容而不是过早省略";
    mockSessionStore.session = makeSession({ id: 42, title: longTitle });

    render(<ChatPanel sessionId={42} />);

    const title = screen.getByText(longTitle);
    expect(title).toHaveClass("line-clamp-2");
    expect(title).not.toHaveClass("truncate");
    expect(title).toHaveAttribute("title", longTitle);
  });

  it("session.projectId=2 时 header 显示 'Agentre / backend'", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, projectId: 2 });

    render(<ChatPanel sessionId={42} />);

    // 树里 id=2 的路径是 Agentre → backend
    const projectPath = screen.getByText("Agentre / backend");
    expect(projectPath).toHaveClass("text-agent-5");
    expect(projectPath.previousElementSibling).toHaveClass("text-agent-5");
    // session id 也显示
    expect(screen.getByText("sess-42")).toHaveClass("text-muted-foreground");
  });

  it("session.projectId=1 时 header 显示 'Agentre'（顶级）", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 10, projectId: 1 });

    render(<ChatPanel sessionId={10} />);

    expect(screen.getByText("Agentre")).toBeInTheDocument();
    expect(screen.getByText("sess-10")).toBeInTheDocument();
  });

  it("session.projectId=0 时 header 仍显示 session id", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, projectId: 0 });

    render(<ChatPanel sessionId={42} />);

    expect(screen.queryByText(/Agentre/)).not.toBeInTheDocument();
    expect(screen.getByText("sess-42")).toBeInTheDocument();
  });
});

// ─── 转录脚注的模型回退值 ────────────────────────────────────────────────────
//
// 一轮还在跑时,占位 assistant 行的 model 是空的(后端真消息还没回来),脚注
// 因此落到 fallbackModel。这个槽是**给人看的模型名**,不能塞会话的稳定
// model_key —— 那是 uuid.NewString() 生成的引用键,画到脸上就是一串 UUID。
describe("ChatPanel · 转录脚注的模型回退值", () => {
  it("Given a session fixed to one model, When the transcript renders, Then the fallback model is the model ID rather than the stable model key", async () => {
    resetStore();
    onTestFinished(() => {
      appMocks.ListLLMProviders.mockResolvedValue({ items: [] });
    });
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [
        {
          id: 1,
          providerKey: "acme-anthropic",
          name: "Acme Claude",
          type: "anthropic",
          enabled: true,
          defaultModelKey: "",
        },
      ],
    });
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        {
          modelKey: "c05987e3-c685-444c-945a-793eba176709",
          modelId: "glm-5.3",
          name: "glm-5.3",
          enabled: true,
        },
      ],
    });
    mockSessionStore.session = makeSession({
      id: 42,
      providerKey: "acme-anthropic",
      modelKey: "c05987e3-c685-444c-945a-793eba176709",
    });

    render(<ChatPanel sessionId={42} />);

    await waitFor(() =>
      expect(componentMocks.chatTranscriptProps.at(-1)?.fallbackModel).toBe(
        "glm-5.3",
      ),
    );
  });

  it("Given the fixed model is gone from the catalog, When the transcript renders, Then the stable model key never reaches the fallback slot", async () => {
    resetStore();
    onTestFinished(() => {
      appMocks.ListLLMProviders.mockResolvedValue({ items: [] });
    });
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [
        {
          id: 1,
          providerKey: "acme-anthropic",
          name: "Acme Claude",
          type: "anthropic",
          enabled: true,
          defaultModelKey: "",
        },
      ],
    });
    appMocks.ListLLMModels.mockResolvedValue({ items: [] });
    mockSessionStore.session = makeSession({
      id: 42,
      providerKey: "acme-anthropic",
      modelKey: "c05987e3-c685-444c-945a-793eba176709",
    });

    render(<ChatPanel sessionId={42} />);

    await waitFor(() => expect(appMocks.ListLLMModels).toHaveBeenCalled());
    await act(async () => undefined);

    // 目录里解析不出来时脚注宁可空着（画成「—」），也不能把引用键当模型名写出来。
    // 断言扫过每一次渲染,而不只是最后一次:泄漏发生在解析完成前的任何一帧都算数。
    expect(
      componentMocks.chatTranscriptProps.map((props) => props.fallbackModel),
    ).not.toContain("c05987e3-c685-444c-945a-793eba176709");
  });
});

describe("ChatPanel · transcript cwd", () => {
  it("Given session has cwd, When transcript renders, Then cwd is passed through for local link classification", () => {
    resetStore();
    mockSessionStore.session = makeSession({
      cwd: "/Users/codfrm/Code/agentre/agentre",
      id: 42,
    });

    render(<ChatPanel sessionId={42} />);

    expect(componentMocks.chatTranscriptProps.at(-1)?.cwd).toBe(
      "/Users/codfrm/Code/agentre/agentre",
    );
  });
});

// ─── R13:断连活信号的接线 ────────────────────────────────────────────────────
//
// 两端各有用例(ChatStreamsHost 把 chat:conn:<sid> 写进 store、TranscriptRowView
// 按 prop 换形态),中间这一段 ——「按**本**会话读连接态并派给转录流」—— 没有。
// 把它错接成另一条会话、错读成 "lost"、或干脆写死 false,整套用例仍然全绿,而用户
// 在断连期间看到的是打字指示器,也就是 R13 要消除的那个困惑本身。
describe("ChatPanel · 断连活信号接线 (R13)", () => {
  it("Given the session's channel is reconnecting, When the transcript renders, Then it is told to swap in the disconnected form", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    useSessionConnStore.getState().setConnState(42, "reconnecting");

    render(<ChatPanel sessionId={42} />);

    expect(componentMocks.chatTranscriptProps.at(-1)?.reconnecting).toBe(true);
  });

  it("Given the channel is connected again, When the transcript re-renders, Then it goes straight back to the typing form", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    useSessionConnStore.getState().setConnState(42, "reconnecting");

    render(<ChatPanel sessionId={42} />);
    expect(componentMocks.chatTranscriptProps.at(-1)?.reconnecting).toBe(true);

    act(() => {
      useSessionConnStore.getState().setConnState(42, "connected");
    });

    expect(componentMocks.chatTranscriptProps.at(-1)?.reconnecting).toBe(false);
  });

  it("Given another session is reconnecting, When this session's transcript renders, Then it stays on the typing form", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    useSessionConnStore.getState().setConnState(7, "reconnecting");

    render(<ChatPanel sessionId={42} />);

    expect(componentMocks.chatTranscriptProps.at(-1)?.reconnecting).toBe(false);
  });
});

// ─── R14:补齐静默落定 + 底部跳转控件 ─────────────────────────────────────────
//
// 断线期间攒下的通知在重连后一次性重放,转录区会突然长出一大截。用户如果正在翻
// 历史,位置被夺走就是最刺人的那种打扰;但内容真的多了,他也需要一条回去的路。
// 下面每条用例各钉一句 R14:位置不动 / 非贴底且有新增才浮出 / 条数与待处理项数
// 正确且是文字(色点不能是唯一载体)/ 点击跳到最新 / 本就贴底则照旧跟随、不出控件。
describe("ChatPanel · 补齐落定与跳转控件 (R14)", () => {
  function messagesBatch(n: number) {
    return Array.from({ length: n }, (_, i) => ({
      blocks: [],
      createtime: 0,
      id: i + 1,
      role: i % 2 === 0 ? "assistant" : "user",
    }));
  }

  /**
   * 补齐落定:掉线开窗 → 后台一次性重放的内容到位(每条消息一行)→ 连接态那一发。
   * 重放的通知条数刻意与行数不同量级 —— 控件报的必须是后者。
   */
  function catchUpLands(
    view: { rerender: (ui: React.ReactElement) => void },
    opts: { items: number; pending: number },
  ) {
    act(() => {
      openCatchUpWindow(42);
    });
    act(() => {
      mockSessionStore.messages = messagesBatch(opts.items);
      view.rerender(<ChatPanel sessionId={42} scrollStateKey="chat-tab-r14" />);
    });
    act(() => {
      recordCatchUp(42, opts.items * 100, opts.pending);
    });
  }

  it("Given the user is reading history, When a catch-up batch lands, Then the scroll position does not move", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-r14" />,
    );
    const scroller = transcriptScroller(view.container);
    scrollUpFromBottom(scroller);

    catchUpLands(view, { items: 12, pending: 0 });

    expect(scroller.scrollTop).toBe(1_240);
  });

  it("Given the user is not at the bottom, When a catch-up brings new content, Then a jump control surfaces stating how many items arrived", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-r14" />,
    );
    const scroller = transcriptScroller(view.container);
    scrollUpFromBottom(scroller);

    catchUpLands(view, { items: 12, pending: 0 });

    const control = screen.getByTestId("transcript-jump-control");
    expect(control).toHaveTextContent("12");
    expect(screen.queryByTestId("transcript-jump-pending")).toBeNull();
  });

  it("Given the catch-up carries unanswered decisions, Then the same control states the pending count in TEXT, not by a status dot alone", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-r14" />,
    );
    const scroller = transcriptScroller(view.container);
    scrollUpFromBottom(scroller);

    catchUpLands(view, { items: 12, pending: 3 });

    const pending = screen.getByTestId("transcript-jump-pending");
    expect(pending).toHaveTextContent("3");
    // 无障碍:待处理项数必须进可访问名,颜色/圆点只能是修饰 —— 只靠色点的实现
    // 在这里就红:纯装饰节点 aria-hidden 后可访问名里根本没有那个 3。
    expect(screen.getByTestId("transcript-jump-control")).toHaveAccessibleName(
      expect.stringContaining("3"),
    );
  });

  it("Given the jump control is showing, When the user clicks it, Then the transcript jumps to the latest position and the control goes away", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-r14" />,
    );
    const scroller = transcriptScroller(view.container);
    scrollUpFromBottom(scroller);
    catchUpLands(view, { items: 12, pending: 1 });

    act(() => {
      fireEvent.click(screen.getByTestId("transcript-jump-control"));
    });

    expect(scroller.scrollTop).toBe(3_520);
    expect(screen.queryByTestId("transcript-jump-control")).toBeNull();
  });

  it("Given the user was already at the bottom, When a catch-up lands, Then no control appears and the transcript keeps following the bottom", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-r14" />,
    );
    const scroller = transcriptScroller(view.container);
    act(() => {
      scroller.scrollTop = 3_500;
      fireEvent.scroll(scroller);
    });

    catchUpLands(view, { items: 12, pending: 1 });

    expect(screen.queryByTestId("transcript-jump-control")).toBeNull();
    expect(scroller.scrollTop).toBe(3_520);
  });

  it("Given the user scrolls back up after the catch-up was already seen at the bottom, Then no stale control comes back", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-r14" />,
    );
    const scroller = transcriptScroller(view.container);
    act(() => {
      scroller.scrollTop = 3_500;
      fireEvent.scroll(scroller);
    });
    catchUpLands(view, { items: 12, pending: 1 });

    scrollUpFromBottom(scroller);

    // 控件只有药丸一种形状（2026-08-24）：补齐销账后它还在，只是文案退回「回到底部」。
    expect(screen.getByTestId("transcript-jump-control")).toHaveAccessibleName(
      "Back to bottom",
    );
  });

  // 销账条件是「人回到了底部」,不是「点了那枚控件」—— 自己滚回底部同样意味着补齐
  // 内容已经看过。不销账的话下次往上翻会撞见一枚数字早已过期的控件。
  it("Given the jump control is showing, When the user scrolls back to the bottom themselves, Then the summary is discharged and scrolling up again brings back only the plain control", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-r14" />,
    );
    const scroller = transcriptScroller(view.container);
    scrollUpFromBottom(scroller);
    catchUpLands(view, { items: 12, pending: 1 });
    expect(screen.getByTestId("transcript-jump-control")).toHaveTextContent(
      "12",
    );

    act(() => {
      scroller.scrollTop = 3_520;
      fireEvent.scroll(scroller);
    });
    expect(screen.queryByTestId("transcript-jump-control")).toBeNull();

    scrollUpFromBottom(scroller);

    // 控件只有药丸一种形状（2026-08-24）：补齐销账后它还在，只是文案退回「回到底部」。
    expect(screen.getByTestId("transcript-jump-control")).toHaveAccessibleName(
      "Back to bottom",
    );
  });

  // 从不断连的会话走的是同一枚控件的另一档文案:没有未看的补齐 = 药丸写「回到底部」。
  // 2026-08-24 之前这一档是一颗只有 ↓ 的圆钮,话全藏在 tooltip 里;现在两档同形,
  // 标签与跳转这两件事仍然得有人钉住,否则接错 onJump / 丢了文案也照样全绿。
  it("Given a session that never disconnected, When the user scrolls up, Then the plain back-to-bottom control keeps its label and returns to the bottom on click", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-r14" />,
    );
    const scroller = transcriptScroller(view.container);
    scrollUpFromBottom(scroller);

    const control = screen.getByTestId("transcript-jump-control");
    expect(control).toHaveAccessibleName("Back to bottom");

    act(() => {
      fireEvent.click(control);
    });

    expect(scroller.scrollTop).toBe(3_520);
    expect(screen.queryByTestId("transcript-jump-control")).toBeNull();
  });
});

// ─── R14 修复轮:控件的计量单位是转录行,不是通知条数 ─────────────────────────
//
// 「N 条新内容」里的 N 早先取的是 daemon 重放的**通知**条数 —— daemon 对每个
// agentruntime 事件都落一行日志(TextDelta / ThinkingDelta / UsageUpdate 全在内)。
// 断网两分钟、期间 agent 流式吐一条长回复,重连后控件写着「1206 条新内容」,而用户
// 只多了一条助手消息。用户嘴里的「一条」是转录区里的一行,下面把计量单位钉在行上。
describe("ChatPanel · 跳转控件按转录行计数 (R14 修复轮)", () => {
  /** 控件文案里的数字。只比数字,免得把 zh/en 文案模板抄进断言。 */
  function digitsIn(el: HTMLElement): string {
    return (el.textContent ?? "").replace(/\D+/g, "");
  }

  /** 一条在飞的远端会话:用户问句 + 正在流式的那条助手消息。 */
  function streamingSession(assistantBlocks: unknown[]) {
    mockSessionStore.session = makeSession({ id: 42 });
    mockSessionStore.messages = [
      {
        blocks: [{ text: "跑一下测试", type: "text" }],
        createtime: 1,
        id: 1,
        role: "user",
      },
      { blocks: assistantBlocks, createtime: 2, id: 2, role: "assistant" },
    ];
    useChatStreamsStore.getState().openStream({
      assistantMessageId: 2,
      name: "chat:event:42:2",
      sessionId: 42,
      streamStartedAt: Date.now(),
    });
  }

  /** 断连期间攒下、重连后一次性重放的那一大批通知落进流里。 */
  function replayDeltas(count: number) {
    const store = useChatStreamsStore.getState();
    for (let i = 0; i < count; i++) store.appendLiveText(42, 2, "字");
  }

  function replayToolRoundTrips(ids: string[]) {
    const store = useChatStreamsStore.getState();
    for (const toolUseId of ids) {
      store.appendLiveToolUse(42, 2, { toolName: "Bash", toolUseId });
      store.appendLiveToolResult(42, 2, { text: "ok", toolUseId });
    }
  }

  it("Given a two-minute outage while one long reply streamed, When the catch-up replays 1212 notifications that land in two new transcript rows, Then the control says two", () => {
    resetStore();
    streamingSession([]);
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-rows" />,
    );
    scrollUpFromBottom(transcriptScroller(view.container));

    act(() => {
      openCatchUpWindow(42); // 通道掉了
    });
    act(() => {
      replayDeltas(1_200);
      replayToolRoundTrips(["t1", "t2", "t3"]);
      replayDeltas(6);
    });
    act(() => {
      recordCatchUp(42, 1_212, 0); // 补齐落定那一发
    });

    // 转录区多出的是「一段文字 + 一个活动块(三次连续调用聚合成一行) + 一段
    // 文字」,占位行被第一段文字顶掉 —— 净增两行。1212 条通知不是那个数字。
    expect(digitsIn(screen.getByTestId("transcript-jump-control"))).toBe("2");
  });

  // 补齐可以只是把内容追加进**已经存在**的那一行(还在流的助手消息吃掉全部 delta)。
  // R14 说的是「补齐产生了新内容」,不是「产生了新行」——控件照常给一条回去的路,
  // 但一个数不出来的数字不能编。
  it("Given the catch-up only grew a row that was already there, When it lands, Then the control still surfaces and states no count", () => {
    resetStore();
    streamingSession([{ text: "回复已经开了个头", type: "text" }]);
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-rows" />,
    );
    scrollUpFromBottom(transcriptScroller(view.container));

    act(() => {
      openCatchUpWindow(42);
    });
    act(() => {
      replayDeltas(1_200);
    });
    act(() => {
      recordCatchUp(42, 1_200, 0);
    });

    const control = screen.getByTestId("transcript-jump-control");
    expect(control).toHaveAccessibleName("New content");
    expect(digitsIn(control)).toBe("");
  });

  // 待决策的条数与转录行数是两笔账:后者可能一行都没多,前者仍然必须报出来 ——
  // 断连期间埋进来的待决策不写在这里就等于没人知道。
  it("Given a catch-up with no new rows but an unanswered decision, Then the pending count still shows", () => {
    resetStore();
    streamingSession([{ text: "回复已经开了个头", type: "text" }]);
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-rows" />,
    );
    scrollUpFromBottom(transcriptScroller(view.container));

    act(() => {
      openCatchUpWindow(42);
    });
    act(() => {
      replayDeltas(600);
    });
    act(() => {
      recordCatchUp(42, 600, 2);
    });

    expect(screen.getByTestId("transcript-jump-pending")).toHaveTextContent(
      "2",
    );
  });
});

describe("ChatPanel · local command scope and execution", () => {
  it("Given a new remote project target, When the composer mounts, Then history uses only the async pre-session resolver without creating a session", async () => {
    resetStore();
    mockSessionStore.session = null;
    const resolution = deferred<{ deviceId: string; cwd: string }>();
    appMocks.ResolveLocalCommandScope.mockReturnValueOnce(resolution.promise);

    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Remote Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            deviceID: "7",
          } as never
        }
        newSessionContext={{ projectId: 2 }}
      />,
    );

    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledWith(
      expect.objectContaining({ agentId: 7, projectId: 2, sessionId: 0 }),
    );
    expect(
      componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
    ).toBeUndefined();

    await act(async () => {
      resolution.resolve({ deviceId: "7", cwd: "/home/me/proj" });
      await resolution.promise;
    });

    await waitFor(() => {
      expect(
        componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
      ).toEqual({ deviceId: "7", cwd: "/home/me/proj" });
    });
    expect(appMocks.EnsureChatSession).not.toHaveBeenCalled();
  });

  it("Given a local free-chat target, When scope resolves, Then AgentCwd is shown instead of an empty or project-derived cwd", async () => {
    resetStore();
    mockSessionStore.session = null;
    appMocks.ResolveLocalCommandScope.mockResolvedValueOnce({
      deviceId: "",
      cwd: "/Users/me/agent-cwd",
    });

    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 8,
            name: "Local Eng",
            agentBackendId: 2,
            backendType: "codex",
            deviceID: "",
          } as never
        }
      />,
    );

    await waitFor(() => {
      expect(
        componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
      ).toEqual({ deviceId: "", cwd: "/Users/me/agent-cwd" });
    });
    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledWith(
      expect.objectContaining({ agentId: 8, projectId: 0, sessionId: 0 }),
    );
    expect(appMocks.EnsureChatSession).not.toHaveBeenCalled();
  });

  it("Given an existing session snapshot, When scope resolves, Then the composer uses the session-only resolver result rather than snapshot device/cwd", async () => {
    resetStore();
    mockSessionStore.session = makeSession({
      cwd: "/stale/session/cwd",
      deviceID: "stale-device",
      id: 42,
    });
    appMocks.ResolveLocalCommandScope.mockResolvedValueOnce({
      deviceId: "remote-device-7",
      cwd: "/srv/current",
    });

    render(<ChatPanel sessionId={42} />);

    await waitFor(() => {
      expect(
        componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
      ).toEqual({ deviceId: "remote-device-7", cwd: "/srv/current" });
    });
    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledWith(
      expect.objectContaining({ agentId: 0, projectId: 0, sessionId: 42 }),
    );
  });

  it("Given a resolved session scope and open history menu, When status/read/permission/display fields replace the session object, Then the scope and menu stay open without another resolution", async () => {
    resetStore();
    componentMocks.localCommandMenuActive = true;
    mockSessionStore.session = makeSession({
      agentId: 7,
      agentName: "Eng",
      backendType: "claudecode",
      cwd: "/srv/project",
      deviceID: "remote-7",
      deviceName: "Build box",
      id: 42,
      projectId: 2,
    });
    appMocks.ResolveLocalCommandScope.mockResolvedValueOnce({
      deviceId: "remote-7",
      cwd: "/srv/project",
    });
    const view = render(<ChatPanel sessionId={42} />);

    await waitFor(() => {
      expect(screen.getByTestId("local-command-history-menu")).toBeVisible();
    });
    const resolvedScope =
      componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope;
    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(1);

    mockSessionStore.session = makeSession({
      agentId: 7,
      agentName: "Renamed Eng",
      agentStatus: "running",
      backendType: "claudecode",
      contextWindow: 200_000,
      cwd: "/srv/project",
      deviceID: "remote-7",
      deviceName: "Renamed build box",
      id: 42,
      lastReadAt: 99,
      needsAttention: true,
      permissionMode: "acceptEdits",
      projectId: 2,
      title: "Updated display title",
    });
    view.rerender(<ChatPanel sessionId={42} />);

    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(1);
    expect(
      componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
    ).toBe(resolvedScope);
    expect(screen.getByTestId("local-command-history-menu")).toBeVisible();
  });

  it("Given a resolved pre-session scope and open history menu, When unrelated agent metadata replaces the agent object, Then the scope and menu stay open without another resolution", async () => {
    resetStore();
    componentMocks.localCommandMenuActive = true;
    mockSessionStore.session = null;
    const initialAgent = {
      activeCount: 1,
      backendType: "claudecode",
      defaultPermissionMode: "plan",
      deviceID: "remote-7",
      deviceName: "Build box",
      id: 7,
      name: "Remote Eng",
      online: true,
      pinned: false,
    } as never;
    appMocks.ResolveLocalCommandScope.mockResolvedValueOnce({
      deviceId: "remote-7",
      cwd: "/srv/project",
    });
    const view = render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={initialAgent}
        newSessionContext={{ projectId: 2 }}
      />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("local-command-history-menu")).toBeVisible();
    });
    const resolvedScope =
      componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope;
    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(1);

    view.rerender(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            activeCount: 8,
            backendType: "claudecode",
            defaultPermissionMode: "acceptEdits",
            deviceID: "remote-7",
            deviceName: "Renamed build box",
            id: 7,
            name: "Renamed Remote Eng",
            online: false,
            pinned: true,
          } as never
        }
        newSessionContext={{ projectId: 2 }}
      />,
    );

    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(1);
    expect(
      componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
    ).toBe(resolvedScope);
    expect(screen.getByTestId("local-command-history-menu")).toBeVisible();
  });

  it.each([
    ["cwd", { cwd: "/srv/next" }],
    ["device", { deviceID: "remote-8" }],
    ["backend", { backendType: "codex" }],
    ["agent", { agentId: 8 }],
    ["project", { projectId: 3 }],
  ] as const)(
    "Given a resolved existing-session scope, When the %s target scalar changes, Then exactly one fresh resolution runs",
    async (_label, targetChange) => {
      resetStore();
      const initialTarget = {
        agentId: 7,
        backendType: "claudecode",
        cwd: "/srv/project",
        deviceID: "remote-7",
        id: 42,
        projectId: 2,
      };
      mockSessionStore.session = makeSession(initialTarget);
      appMocks.ResolveLocalCommandScope.mockResolvedValue({
        deviceId: "remote-7",
        cwd: "/srv/project",
      });
      const view = render(<ChatPanel sessionId={42} />);
      await waitFor(() => {
        expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(1);
      });

      mockSessionStore.session = makeSession({
        ...initialTarget,
        ...targetChange,
      });
      view.rerender(<ChatPanel sessionId={42} />);

      expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(2);
    },
  );

  it("Given a resolved session target, When the target changes, Then the old scope clears before exactly one new async resolution finishes", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    appMocks.ResolveLocalCommandScope.mockResolvedValueOnce({
      deviceId: "remote-7",
      cwd: "/srv/old-target",
    });
    const nextTarget = deferred<{ deviceId: string; cwd: string }>();
    appMocks.ResolveLocalCommandScope.mockReturnValueOnce(nextTarget.promise);
    const view = render(<ChatPanel sessionId={42} />);

    await waitFor(() => {
      expect(
        componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
      ).toEqual({ deviceId: "remote-7", cwd: "/srv/old-target" });
    });

    mockSessionStore.session = makeSession({ id: 43 });
    view.rerender(<ChatPanel sessionId={43} />);

    expect(
      componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
    ).toBeUndefined();
    expect(appMocks.ResolveLocalCommandScope).toHaveBeenLastCalledWith(
      expect.objectContaining({ agentId: 0, projectId: 0, sessionId: 43 }),
    );
    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(2);
  });

  it("Given a resolved scope, When ! mode and then session/location refresh re-resolve out of order, Then old scope clears immediately and stale responses are ignored", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ cwd: "/snapshot-a", id: 42 });
    const initial = deferred<{ deviceId: string; cwd: string }>();
    const commandModeRefresh = deferred<{ deviceId: string; cwd: string }>();
    const sessionRefresh = deferred<{ deviceId: string; cwd: string }>();
    appMocks.ResolveLocalCommandScope.mockReturnValueOnce(initial.promise)
      .mockReturnValueOnce(commandModeRefresh.promise)
      .mockReturnValueOnce(sessionRefresh.promise);
    const view = render(<ChatPanel sessionId={42} />);

    await act(async () => {
      initial.resolve({ deviceId: "remote-7", cwd: "/srv/old" });
      await initial.promise;
    });
    await waitFor(() => {
      expect(
        componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
      ).toEqual({ deviceId: "remote-7", cwd: "/srv/old" });
    });

    act(() => {
      const onCommandModeChange = componentMocks.chatComposerProps.at(-1)
        ?.onCommandModeChange as ((active: boolean) => void) | undefined;
      onCommandModeChange?.(true);
    });
    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(2);
    expect(
      componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
    ).toBeUndefined();

    mockSessionStore.session = makeSession({ cwd: "/snapshot-b", id: 42 });
    view.rerender(<ChatPanel sessionId={42} />);
    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(3);

    await act(async () => {
      sessionRefresh.resolve({ deviceId: "remote-7", cwd: "/srv/current" });
      await sessionRefresh.promise;
    });
    await waitFor(() => {
      expect(
        componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
      ).toEqual({ deviceId: "remote-7", cwd: "/srv/current" });
    });

    await act(async () => {
      commandModeRefresh.resolve({
        deviceId: "remote-7",
        cwd: "/srv/stale-response",
      });
      await commandModeRefresh.promise;
    });
    expect(
      componentMocks.chatComposerProps.at(-1)?.localCommandHistoryScope,
    ).toEqual({ deviceId: "remote-7", cwd: "/srv/current" });
  });

  it("Given scope pre-resolution fails, When a command is submitted, Then execution still launches once and returns the terminal response scope", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    appMocks.ResolveLocalCommandScope.mockRejectedValueOnce(
      new Error("scope unavailable"),
    );
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-9", cwd: "/srv/run" },
    });

    render(<ChatPanel sessionId={42} />);
    await waitFor(() => {
      expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(1);
    });
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    await expect(runCommand("pwd")).resolves.toEqual({
      deviceId: "remote-9",
      cwd: "/srv/run",
    });
    expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
    expect(appMocks.EnsureChatSession).not.toHaveBeenCalled();
  });

  it("Given scope pre-resolution throws before returning a promise, When a command is submitted, Then the composer stays available and execution still launches once", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    appMocks.ResolveLocalCommandScope.mockImplementationOnce(() => {
      throw new Error("scope bridge unavailable");
    });
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-10", cwd: "/srv/run" },
    });

    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    await expect(runCommand("pwd")).resolves.toEqual({
      deviceId: "remote-10",
      cwd: "/srv/run",
    });
    expect(appMocks.ResolveLocalCommandScope).toHaveBeenCalledTimes(1);
    expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
    expect(appMocks.EnsureChatSession).not.toHaveBeenCalled();
  });

  it("Given a running legacy card has no runtime controller, When Stop is clicked, Then the missing authority is reported without issuing an unowned duplicate close", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const terminalId = "legacy-running-terminal";
    useLocalCommandsStore.getState().start({
      id: terminalId,
      sessionId: 42,
      command: "sleep 30",
      createdAt: 1,
    });
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    onTestFinished(() => error.mockRestore());

    render(<ChatPanel sessionId={42} />);
    const card = renderLocalCommandCardFromTranscript(terminalId);
    await userEvent.click(
      within(card.container).getByRole("button", { name: /停止|Stop/ }),
    );

    expect(error).toHaveBeenCalledWith(
      "[chat] stop local command failed: runtime controller missing",
      { terminalId },
    );
    expect(appMocks.TerminalClose).not.toHaveBeenCalled();
    expect(useLocalCommandsStore.getState().get(terminalId)?.status).toBe(
      "running",
    );
  });

  it("Given panel A launched a running command and unmounted, When panel B reopens the same session and Stop is clicked through its transcript card, Then the original controller closes and settles exactly once", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-12", cwd: "/srv/exact" },
    });

    const panelA = render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;
    await runCommand("sleep 30");
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    const cleanups = terminalListenerCleanups(terminalId);
    panelA.unmount();

    render(<ChatPanel sessionId={42} />);
    const card = renderLocalCommandCardFromTranscript(terminalId);
    await userEvent.click(
      within(card.container).getByRole("button", { name: /停止|Stop/ }),
    );

    await waitFor(() => {
      expect(useLocalCommandsStore.getState().get(terminalId)?.status).toBe(
        "stopped",
      );
    });
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
    expect(appMocks.TerminalClose).toHaveBeenCalledWith(terminalId);
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "stopped");
    for (const cleanup of cleanups) expect(cleanup).toHaveBeenCalledTimes(1);
    await expect(localCommandRuntimeStore.stop(terminalId)).resolves.toBe(
      false,
    );
  });

  it("Given panel A unmounted with a running command and panel B receives a close failure, When Stop is retried through B, Then the original controller remains retryable until the second close succeeds", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-12", cwd: "/srv/exact" },
    });
    appMocks.TerminalClose.mockRejectedValueOnce(
      new Error("remote cleanup failed after terminal not open"),
    );
    appMocks.TerminalClose.mockResolvedValueOnce(undefined);

    const panelA = render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;
    await runCommand("sleep 30");
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    const cleanups = terminalListenerCleanups(terminalId);
    panelA.unmount();

    render(<ChatPanel sessionId={42} />);
    const card = renderLocalCommandCardFromTranscript(terminalId);
    const stopButton = () =>
      within(card.container).getByRole("button", { name: /停止|Stop/ });

    await userEvent.click(stopButton());
    await waitFor(() =>
      expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1),
    );
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      output: "Error: remote cleanup failed after terminal not open",
      status: "running",
    });
    expect(finish).not.toHaveBeenCalled();
    for (const cleanup of cleanups) expect(cleanup).not.toHaveBeenCalled();

    await userEvent.click(stopButton());
    await waitFor(() => {
      expect(useLocalCommandsStore.getState().get(terminalId)?.status).toBe(
        "stopped",
      );
    });
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(2);
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "stopped");
    for (const cleanup of cleanups) expect(cleanup).toHaveBeenCalledTimes(1);
    await expect(localCommandRuntimeStore.stop(terminalId)).resolves.toBe(
      false,
    );
  });

  it("Given a started command with no exit event, When Stop is clicked through the transcript card, Then close, stopped settlement, and both listener cleanups happen exactly once immediately", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-12", cwd: "/srv/exact" },
    });

    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    await expect(runCommand("sleep 30")).resolves.toEqual({
      deviceId: "remote-12",
      cwd: "/srv/exact",
    });
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    const cleanups = terminalListenerCleanups(terminalId);
    expect(cleanups).toHaveLength(2);
    for (const cleanup of cleanups) expect(cleanup).not.toHaveBeenCalled();

    const card = renderLocalCommandCardFromTranscript(terminalId);
    await userEvent.click(
      within(card.container).getByRole("button", { name: /停止|Stop/ }),
    );

    await waitFor(() => {
      expect(useLocalCommandsStore.getState().get(terminalId)?.status).toBe(
        "stopped",
      );
    });
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
    expect(appMocks.TerminalClose).toHaveBeenCalledWith(terminalId);
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "stopped");
    for (const cleanup of cleanups) expect(cleanup).toHaveBeenCalledTimes(1);
  });

  it("Given TerminalClose reports exact terminal not open, When Stop is clicked, Then the result is authoritative success and listeners settle immediately", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-12", cwd: "/srv/exact" },
    });
    appMocks.TerminalClose.mockRejectedValueOnce(
      new Error("terminal not open"),
    );

    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;
    await runCommand("sleep 30");
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    const cleanups = terminalListenerCleanups(terminalId);

    const card = renderLocalCommandCardFromTranscript(terminalId);
    await userEvent.click(
      within(card.container).getByRole("button", { name: /停止|Stop/ }),
    );

    await waitFor(() => {
      expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
        output: "",
        status: "stopped",
      });
    });
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "stopped");
    for (const cleanup of cleanups) expect(cleanup).toHaveBeenCalledTimes(1);
  });

  it("Given TerminalClose fails, When Stop is retried after the first failure, Then the command and listeners stay active until the retry succeeds", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-12", cwd: "/srv/exact" },
    });
    appMocks.TerminalClose.mockRejectedValueOnce(
      new Error("remote cleanup failed after terminal not open"),
    );
    appMocks.TerminalClose.mockResolvedValueOnce(undefined);

    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;
    await runCommand("sleep 30");
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    const cleanups = terminalListenerCleanups(terminalId);
    const card = renderLocalCommandCardFromTranscript(terminalId);
    const stopButton = () =>
      within(card.container).getByRole("button", { name: /停止|Stop/ });

    await userEvent.click(stopButton());
    await waitFor(() =>
      expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1),
    );
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      output: "Error: remote cleanup failed after terminal not open",
      status: "running",
    });
    expect(finish).not.toHaveBeenCalled();
    for (const cleanup of cleanups) expect(cleanup).not.toHaveBeenCalled();

    await userEvent.click(stopButton());
    await waitFor(() => {
      expect(useLocalCommandsStore.getState().get(terminalId)?.status).toBe(
        "stopped",
      );
    });
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(2);
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "stopped");
    for (const cleanup of cleanups) expect(cleanup).toHaveBeenCalledTimes(1);
  });

  it("Given TerminalClose is pending, When Stop is clicked concurrently, Then both clicks share one close and one settlement", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    const closing = deferred<void>();
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-12", cwd: "/srv/exact" },
    });
    appMocks.TerminalClose.mockReturnValue(closing.promise);

    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;
    await runCommand("sleep 30");
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    const cleanups = terminalListenerCleanups(terminalId);
    const card = renderLocalCommandCardFromTranscript(terminalId);
    const stopButton = within(card.container).getByRole("button", {
      name: /停止|Stop/,
    });

    fireEvent.click(stopButton);
    fireEvent.click(stopButton);
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
    expect(useLocalCommandsStore.getState().get(terminalId)?.status).toBe(
      "running",
    );

    await act(async () => {
      closing.resolve();
      await closing.promise;
    });
    await waitFor(() => {
      expect(useLocalCommandsStore.getState().get(terminalId)?.status).toBe(
        "stopped",
      );
    });
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "stopped");
    for (const cleanup of cleanups) expect(cleanup).toHaveBeenCalledTimes(1);
  });

  it.each(["data", "exit"] as const)(
    "Given the terminal %s listener registration fails once, When retry installs the complete pair and the command exits naturally, Then it launches once, settles, and cleans every listener without duplicate data or a guardian timer",
    async (throwingListener) => {
      vi.useFakeTimers();
      onTestFinished(() => {
        vi.clearAllTimers();
        vi.useRealTimers();
      });
      resetStore();
      const finish = observeLocalCommandFinish();
      mockSessionStore.session = makeSession({ id: 42 });
      const listenerError = new Error(`${throwingListener} listener failed`);
      const activeListeners = new Map<
        string,
        Set<(...args: unknown[]) => void>
      >();
      let failureCount = 0;
      runtimeMocks.EventsOn.mockImplementation((event, handler) => {
        if (!event?.startsWith("terminal:") || !handler) return vi.fn();
        if (event.endsWith(`:${throwingListener}`) && failureCount === 0) {
          failureCount += 1;
          throw listenerError;
        }
        const listeners = activeListeners.get(event) ?? new Set();
        listeners.add(handler);
        activeListeners.set(event, listeners);
        return vi.fn(() => listeners.delete(handler));
      });
      runtimeMocks.EventsOff.mockImplementation((event?: string) => {
        if (event) activeListeners.delete(event);
      });
      appMocks.TerminalRunCommand.mockResolvedValueOnce({
        scope: { deviceId: "remote-12", cwd: "/srv/exact" },
      });

      render(<ChatPanel sessionId={42} />);
      const runCommand = componentMocks.chatComposerProps.at(-1)
        ?.onCommandSubmit as (command: string) => Promise<unknown>;

      await expect(runCommand("printf done")).resolves.toEqual({
        deviceId: "remote-12",
        cwd: "/srv/exact",
      });
      expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
      const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
      const dataEvent = `terminal:${terminalId}:data`;
      const exitEvent = `terminal:${terminalId}:exit`;
      expect(activeListeners.get(dataEvent)?.size).toBe(1);
      expect(activeListeners.get(exitEvent)?.size).toBe(1);
      const terminalRegistrations = runtimeMocks.EventsOn.mock.calls.filter(
        ([event]) => event?.startsWith(`terminal:${terminalId}:`),
      );
      expect(terminalRegistrations).toHaveLength(
        throwingListener === "data" ? 3 : 4,
      );
      expect(terminalRegistrations.slice(-2).map(([event]) => event)).toEqual([
        dataEvent,
        exitEvent,
      ]);

      act(() => {
        for (const handler of activeListeners.get(dataEvent) ?? []) {
          handler({ data: "ZG9uZQo=" });
        }
      });
      expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
        command: "printf done",
        output: "done\n",
        status: "running",
      });

      act(() => {
        for (const handler of activeListeners.get(exitEvent) ?? []) {
          handler({ code: 0, reason: "exited" });
        }
      });
      expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
        exitCode: 0,
        output: "done\n",
        status: "done",
      });
      expect(activeListeners.get(dataEvent)?.size ?? 0).toBe(0);
      expect(activeListeners.get(exitEvent)?.size ?? 0).toBe(0);
      expect(finish).toHaveBeenCalledTimes(1);
      expect(finish).toHaveBeenCalledWith(terminalId, "done", 0);
      expect(vi.getTimerCount()).toBe(0);
    },
  );

  it.each(["data", "exit"] as const)(
    "Given the terminal %s listener registration remains unavailable after one retry, When the command starts, Then it launches once, returns the real scope, and fails only after one automatic close",
    async (throwingListener) => {
      resetStore();
      const finish = observeLocalCommandFinish();
      mockSessionStore.session = makeSession({ id: 42 });
      const listenerError = new Error(`${throwingListener} listener failed`);
      const activeListeners = new Map<
        string,
        Set<(...args: unknown[]) => void>
      >();
      runtimeMocks.EventsOn.mockImplementation((event, handler) => {
        if (!event?.startsWith("terminal:") || !handler) return vi.fn();
        if (event.endsWith(`:${throwingListener}`)) throw listenerError;
        const listeners = activeListeners.get(event) ?? new Set();
        listeners.add(handler);
        activeListeners.set(event, listeners);
        return vi.fn(() => listeners.delete(handler));
      });
      runtimeMocks.EventsOff.mockImplementation((event?: string) => {
        if (event) activeListeners.delete(event);
      });
      appMocks.TerminalRunCommand.mockResolvedValueOnce({
        scope: { deviceId: "remote-12", cwd: "/srv/exact" },
      });
      appMocks.TerminalClose.mockResolvedValueOnce(undefined);

      render(<ChatPanel sessionId={42} />);
      const runCommand = componentMocks.chatComposerProps.at(-1)
        ?.onCommandSubmit as (command: string) => Promise<unknown>;

      await expect(runCommand("pwd")).resolves.toEqual({
        deviceId: "remote-12",
        cwd: "/srv/exact",
      });
      expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
      const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
      await waitFor(() => {
        expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
          command: "pwd",
          exitCode: -1,
          output: String(listenerError),
          status: "failed",
        });
      });
      const terminalRegistrations = runtimeMocks.EventsOn.mock.calls.filter(
        ([event]) => event?.startsWith(`terminal:${terminalId}:`),
      );
      expect(terminalRegistrations).toHaveLength(
        throwingListener === "data" ? 2 : 4,
      );
      expect(
        activeListeners.get(`terminal:${terminalId}:data`)?.size ?? 0,
      ).toBe(0);
      expect(
        activeListeners.get(`terminal:${terminalId}:exit`)?.size ?? 0,
      ).toBe(0);
      expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
      expect(appMocks.TerminalClose).toHaveBeenCalledWith(terminalId);
      expect(finish).toHaveBeenCalledTimes(1);
      expect(finish).toHaveBeenCalledWith(terminalId, "failed", -1);
      await expect(localCommandRuntimeStore.stop(terminalId)).resolves.toBe(
        false,
      );
    },
  );

  it("Given observers stay unavailable and TerminalRunCommand returns startError with scope, When the response arrives, Then the card fails once without an unnecessary close and the real scope remains available for history", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    const listenerError = new Error("data listener failed");
    runtimeMocks.EventsOn.mockImplementation((event) => {
      if (event?.startsWith("terminal:")) throw listenerError;
      return vi.fn();
    });
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-12", cwd: "/srv/exact" },
      startError: "executable not found",
    });

    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    await expect(runCommand("missing-tool")).resolves.toEqual({
      deviceId: "remote-12",
      cwd: "/srv/exact",
    });
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      command: "missing-tool",
      exitCode: -1,
      output: "executable not found",
      status: "failed",
    });
    expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
    expect(appMocks.TerminalClose).not.toHaveBeenCalled();
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "failed", -1);
  });

  it("Given automatic close keeps failing after a successful unobservable start, When its app-lifetime guardian survives panel reopen, Then paced retries settle failed only on exact close authority", async () => {
    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    const listenerError = new Error("data listener failed");
    runtimeMocks.EventsOn.mockImplementation((event) => {
      if (event?.startsWith("terminal:")) throw listenerError;
      return vi.fn();
    });
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-12", cwd: "/srv/exact" },
    });
    appMocks.TerminalClose.mockRejectedValueOnce(
      new Error("close unavailable"),
    );
    appMocks.TerminalClose.mockRejectedValueOnce(
      new Error("close still unavailable"),
    );
    appMocks.TerminalClose.mockRejectedValueOnce(
      new Error("terminal not open"),
    );

    const panel = render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    await expect(runCommand("pwd")).resolves.toEqual({
      deviceId: "remote-12",
      cwd: "/srv/exact",
    });
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    await act(async () => {
      await Promise.resolve();
    });
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      output: String(listenerError),
      status: "running",
    });
    expect(finish).not.toHaveBeenCalled();

    panel.unmount();
    render(<ChatPanel sessionId={42} />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(99);
    });
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(2);
    expect(useLocalCommandsStore.getState().get(terminalId)?.status).toBe(
      "running",
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(199);
    });
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });

    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(3);
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      exitCode: -1,
      output: String(listenerError),
      status: "failed",
    });
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "failed", -1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("Given observers are unavailable and TerminalRunCommand rejects before scope, When defensive close is pending, Then submission returns undefined promptly and failure waits for close authority", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    const listenerError = new Error("data listener failed");
    const runError = new Error("target resolution failed");
    const closing = deferred<void>();
    runtimeMocks.EventsOn.mockImplementation((event) => {
      if (event?.startsWith("terminal:")) throw listenerError;
      return vi.fn();
    });
    appMocks.TerminalRunCommand.mockRejectedValueOnce(runError);
    appMocks.TerminalClose.mockReturnValueOnce(closing.promise);

    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    await expect(runCommand("pwd")).resolves.toBeUndefined();
    expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
    expect(appMocks.TerminalClose).toHaveBeenCalledWith(terminalId);
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      output: String(runError),
      status: "running",
    });
    expect(finish).not.toHaveBeenCalled();

    await act(async () => {
      closing.resolve();
      await closing.promise;
    });
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      exitCode: -1,
      output: String(runError),
      status: "failed",
    });
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "failed", -1);
  });

  it("Given Stop owns close while an unobservable TerminalRunCommand response is pending, When the successful response starts automatic cleanup, Then both paths share one close and settle stopped once", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    const listenerError = new Error("exit listener failed");
    const terminalRun = deferred<{
      scope: { deviceId: string; cwd: string };
    }>();
    const closing = deferred<void>();
    runtimeMocks.EventsOn.mockImplementation((event, handler) => {
      if (!event?.startsWith("terminal:") || !handler) return vi.fn();
      if (event.endsWith(":exit")) throw listenerError;
      return vi.fn();
    });
    appMocks.TerminalRunCommand.mockReturnValueOnce(terminalRun.promise);
    appMocks.TerminalClose.mockReturnValueOnce(closing.promise);

    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    const result = runCommand("sleep 30");
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    const card = renderLocalCommandCardFromTranscript(terminalId);
    fireEvent.click(
      within(card.container).getByRole("button", { name: /停止|Stop/ }),
    );
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);

    await act(async () => {
      terminalRun.resolve({
        scope: { deviceId: "remote-12", cwd: "/srv/exact" },
      });
      await result;
    });
    await expect(result).resolves.toEqual({
      deviceId: "remote-12",
      cwd: "/srv/exact",
    });
    expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      output: String(listenerError),
      status: "running",
    });

    await act(async () => {
      closing.resolve();
      await closing.promise;
    });
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      output: String(listenerError),
      status: "stopped",
    });
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "stopped");
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
  });

  it("Given the runtime cannot cancel a partially installed observer, When automatic close settles the card, Then it settles once, retains no cleanup timer, and the orphaned listener can never reach the card again", async () => {
    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
    });
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    const listenerError = new Error("exit listener failed");
    const cleanupError = new Error("listener cleanup failed");
    const activeListeners = new Map<
      string,
      Set<(...args: unknown[]) => void>
    >();
    runtimeMocks.EventsOn.mockImplementation((event, handler) => {
      if (!event?.startsWith("terminal:") || !handler) return vi.fn();
      if (event.endsWith(":exit")) throw listenerError;
      const listeners = activeListeners.get(event) ?? new Set();
      listeners.add(handler);
      activeListeners.set(event, listeners);
      return vi.fn(() => {
        throw cleanupError;
      });
    });
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "remote-12", cwd: "/srv/exact" },
    });
    appMocks.TerminalClose.mockResolvedValueOnce(undefined);

    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    await expect(runCommand("pwd")).resolves.toEqual({
      deviceId: "remote-12",
      cwd: "/srv/exact",
    });
    expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    const dataEvent = `terminal:${terminalId}:data`;
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      command: "pwd",
      exitCode: -1,
      output: String(listenerError),
      status: "failed",
    });
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "failed", -1);
    // 撤不掉的那个监听留在运行时里,但它扇给的订阅表是空的 —— 它再吐字节也
    // 进不了卡片、更不会二次结算。所以既不需要一个不断重试的清理看门狗,也
    // 不该退回 `EventsOff(event)`:那一步会把同一条 PTY 上别人的订阅一起摘掉。
    expect(vi.getTimerCount()).toBe(0);
    expect(runtimeMocks.EventsOff).not.toHaveBeenCalled();

    act(() => {
      for (const handler of [...(activeListeners.get(dataEvent) ?? [])]) {
        handler({ data: "ZG9uZQo=" });
      }
    });

    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      exitCode: -1,
      output: String(listenerError),
      status: "failed",
    });
    expect(finish).toHaveBeenCalledTimes(1);
  });

  it("Given two deferred commands in a new chat, When the panel unmounts and one terminal RPC rejects, Then session creation stays shared while both commands continue and settle independently exactly once", async () => {
    resetStore();
    mockSessionStore.session = null;
    const ensured = deferred<number>();
    const firstRun = deferred<{
      scope: { deviceId: string; cwd: string };
    }>();
    const secondRun = deferred<{
      scope: { deviceId: string; cwd: string };
    }>();
    appMocks.EnsureChatSession.mockReturnValueOnce(ensured.promise);
    appMocks.TerminalRunCommand.mockImplementation(
      (_terminalId, _sessionId, command) =>
        command === "first" ? firstRun.promise : secondRun.promise,
    );
    const onSessionCreated = vi.fn();
    const onSidebarShouldReload = vi.fn();
    const view = render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Remote Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            deviceID: "7",
          } as never
        }
        newSessionContext={{ projectId: 2 }}
        onSessionCreated={onSessionCreated}
        onSidebarShouldReload={onSidebarShouldReload}
      />,
    );
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    let first!: Promise<unknown>;
    let second!: Promise<unknown>;
    act(() => {
      first = runCommand("first");
      second = runCommand("second");
    });
    expect(appMocks.EnsureChatSession).toHaveBeenCalledTimes(1);
    expect(appMocks.TerminalRunCommand).not.toHaveBeenCalled();

    view.unmount();
    await act(async () => {
      ensured.resolve(99);
      await ensured.promise;
    });
    await waitFor(() => {
      expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(2);
    });

    const calls = appMocks.TerminalRunCommand.mock.calls;
    const firstCall = calls.find((call) => call[2] === "first");
    const secondCall = calls.find((call) => call[2] === "second");
    const firstTerminalId = String(firstCall?.[0]);
    const secondTerminalId = String(secondCall?.[0]);
    expect(firstCall).toBeDefined();
    expect(secondCall).toBeDefined();
    expect(firstTerminalId).not.toBe(secondTerminalId);
    expect(calls.filter((call) => call[2] === "first")).toHaveLength(1);
    expect(calls.filter((call) => call[2] === "second")).toHaveLength(1);

    let secondSettled = false;
    void second.then(() => {
      secondSettled = true;
    });
    const firstError = new Error("first target failed");
    await act(async () => {
      firstRun.reject(firstError);
      await first;
    });
    await expect(first).resolves.toBeUndefined();
    expect(secondSettled).toBe(false);
    expect(useLocalCommandsStore.getState().get(firstTerminalId)).toMatchObject(
      {
        exitCode: -1,
        output: String(firstError),
        status: "failed",
      },
    );
    expect(
      useLocalCommandsStore.getState().get(secondTerminalId),
    ).toMatchObject({
      command: "second",
      sessionId: 99,
      status: "running",
    });

    await act(async () => {
      secondRun.resolve({
        scope: { deviceId: "7", cwd: "/srv/second" },
      });
      await second;
    });
    await expect(second).resolves.toEqual({
      deviceId: "7",
      cwd: "/srv/second",
    });
    expect(secondSettled).toBe(true);

    const secondExitListener = runtimeMocks.EventsOn.mock.calls.find(
      (call) => call[0] === `terminal:${secondTerminalId}:exit`,
    )?.[1] as ((payload: { code: number; reason: string }) => void) | undefined;
    expect(secondExitListener).toBeDefined();
    act(() => {
      secondExitListener?.({ code: 0, reason: "exited" });
    });
    expect(
      useLocalCommandsStore.getState().get(secondTerminalId),
    ).toMatchObject({
      exitCode: 0,
      status: "done",
    });
    expect(onSessionCreated).toHaveBeenCalledTimes(1);
    expect(onSessionCreated).toHaveBeenCalledWith(99, 7);
    expect(onSidebarShouldReload).toHaveBeenCalledTimes(1);
  });

  it("Given Stop settles a card while TerminalRunCommand is deferred, When a scoped startError arrives, Then both listeners are cleaned and the stopped card is not overwritten or settled twice", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    const terminalRun = deferred<{
      scope: { deviceId: string; cwd: string };
      startError?: string;
    }>();
    appMocks.TerminalRunCommand.mockReturnValueOnce(terminalRun.promise);
    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    const result = runCommand("sleep 30");
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    const card = renderLocalCommandCardFromTranscript(terminalId);
    await userEvent.click(
      within(card.container).getByRole("button", { name: /停止|Stop/ }),
    );
    await waitFor(() => {
      expect(useLocalCommandsStore.getState().get(terminalId)?.status).toBe(
        "stopped",
      );
    });
    const stoppedAt = useLocalCommandsStore
      .getState()
      .get(terminalId)?.finishedAt;

    await act(async () => {
      terminalRun.resolve({
        scope: { deviceId: "remote-13", cwd: "/srv/exact" },
        startError: "terminal command start preempted",
      });
      await result;
    });

    await expect(result).resolves.toEqual({
      deviceId: "remote-13",
      cwd: "/srv/exact",
    });
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      command: "sleep 30",
      output: "",
      status: "stopped",
      finishedAt: stoppedAt,
    });
    expect(
      useLocalCommandsStore.getState().get(terminalId)?.exitCode,
    ).toBeUndefined();
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "stopped");
    const listenerCleanups = runtimeMocks.EventsOn.mock.calls.flatMap(
      ([event], index) => {
        const cleanup = runtimeMocks.EventsOn.mock.results[index]?.value;
        return event?.startsWith(`terminal:${terminalId}:`) &&
          typeof cleanup === "function"
          ? [cleanup]
          : [];
      },
    );
    expect(listenerCleanups).toHaveLength(2);
    for (const cleanup of listenerCleanups) {
      expect(cleanup).toHaveBeenCalledTimes(1);
    }
    expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
    expect(appMocks.TerminalClose).toHaveBeenCalledTimes(1);
    expect(appMocks.TerminalClose).toHaveBeenCalledWith(terminalId);
  });

  it("Given TerminalRunCommand returns startError with scope after observers are installed, When the terminal result arrives, Then the card fails once and the exact returned scope remains available for history", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    const terminalRun = deferred<{
      scope: { deviceId: string; cwd: string };
      startError?: string;
    }>();
    appMocks.TerminalRunCommand.mockReturnValueOnce(terminalRun.promise);
    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    const result = runCommand("missing-tool");
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      command: "missing-tool",
      output: "",
      status: "running",
    });

    await act(async () => {
      terminalRun.resolve({
        scope: { deviceId: "remote-11", cwd: "/srv/exact" },
        startError: "executable not found",
      });
      await terminalRun.promise;
    });
    await expect(result).resolves.toEqual({
      deviceId: "remote-11",
      cwd: "/srv/exact",
    });
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      command: "missing-tool",
      exitCode: -1,
      output: "executable not found",
      status: "failed",
    });
    expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "failed", -1);
  });

  it("Given TerminalRunCommand rejects after observers are installed, When the terminal result arrives, Then the card fails once, undefined prevents history, and no rejection escapes", async () => {
    resetStore();
    const finish = observeLocalCommandFinish();
    mockSessionStore.session = makeSession({ id: 42 });
    const terminalRun = deferred<{
      scope: { deviceId: string; cwd: string };
    }>();
    appMocks.TerminalRunCommand.mockReturnValueOnce(terminalRun.promise);
    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;

    const result = runCommand("pwd");
    const terminalId = String(appMocks.TerminalRunCommand.mock.calls[0]?.[0]);
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      command: "pwd",
      output: "",
      status: "running",
    });

    await act(async () => {
      terminalRun.reject(new Error("target resolution failed"));
      await result;
    });
    await expect(result).resolves.toBeUndefined();
    expect(useLocalCommandsStore.getState().get(terminalId)).toMatchObject({
      exitCode: -1,
      output: "Error: target resolution failed",
      status: "failed",
    });
    expect(appMocks.TerminalRunCommand).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledTimes(1);
    expect(finish).toHaveBeenCalledWith(terminalId, "failed", -1);
  });

  it("Given a terminal view watching the same PTY through the transport, When the chat panel installs its own PTY observers, Then the terminal view keeps receiving the stdout bytes", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    // 卡片的 terminalId 是 launchLocalCommand 内部生成的;钉死它,好让终端视图
    // 在命令起飞之前就订上同一条 PTY(“在终端中打开”正是这个形状)。
    const terminalId = "shared-pty";
    const uuid = vi
      .spyOn(crypto, "randomUUID")
      .mockReturnValue(terminalId as ReturnType<typeof crypto.randomUUID>);
    onTestFinished(() => uuid.mockRestore());

    // 复刻 Wails v2 事件运行时的两条关键语义:`EventsOn` 的返回值只摘自己,
    // 而 `EventsOff(event)` 摘掉该事件名下的**全部**监听者。
    const listeners = new Map<string, Set<(...args: unknown[]) => void>>();
    let dataRegistrations = 0;
    runtimeMocks.EventsOn.mockImplementation((event, handler) => {
      if (!event?.startsWith("terminal:") || !handler) return vi.fn();
      // 第 1 次注册 data 的是终端视图(经 transport);第 2 次才是 chat-panel。
      // 让 chat-panel 那次失败一回 —— 这是它的重试世代唯一会走到 EventsOff 兜底的入口。
      if (event.endsWith(":data") && ++dataRegistrations === 2) {
        throw new Error("data listener failed");
      }
      const bucket = listeners.get(event) ?? new Set();
      bucket.add(handler);
      listeners.set(event, bucket);
      return vi.fn(() => bucket.delete(handler));
    });
    runtimeMocks.EventsOff.mockImplementation((event?: string) => {
      if (event) listeners.delete(event);
    });
    appMocks.TerminalRunCommand.mockResolvedValueOnce({
      scope: { deviceId: "", cwd: "/repo" },
    });

    const terminalView = {
      onData: vi.fn<(bytes: Uint8Array) => void>(),
      onExit: vi.fn(),
    };
    const detachTerminalView = desktopTerminalTransport.subscribe(
      terminalId,
      terminalView,
    );
    onTestFinished(() => detachTerminalView());

    render(<ChatPanel sessionId={42} />);
    const runCommand = componentMocks.chatComposerProps.at(-1)
      ?.onCommandSubmit as (command: string) => Promise<unknown>;
    await runCommand("printf done");

    act(() => {
      for (const handler of [
        ...(listeners.get(`terminal:${terminalId}:data`) ?? []),
      ]) {
        handler({ data: "ZG9uZQo=" });
      }
    });

    // 卡片照常收到输出;终端视图也必须收到同一份字节 —— 它的订阅不该被
    // 另一个视图的清理连坐摘掉。
    expect(useLocalCommandsStore.getState().get(terminalId)?.output).toBe(
      "done\n",
    );
    expect(terminalView.onData).toHaveBeenCalledTimes(1);
    expect(Array.from(terminalView.onData.mock.calls[0][0])).toEqual([
      0x64, 0x6f, 0x6e, 0x65, 0x0a,
    ]);
  });
});

// 「回到底部」药丸此前只在断连补齐之后报得出数字;平时往回翻,它只写「回到底部」,
// 说不出用户落后了多少。这一组钉的是常显的那个数:轮数从视口下沿那条消息之后开始数,
// 数不出边界时不猜。
describe("ChatPanel · 未贴底时报出落后的轮数", () => {
  function turnMessages() {
    // 三轮:1/2、3/4、5/6。轮由 user 消息开启,紧跟的 assistant 属于同一轮。
    return [1, 2, 3, 4, 5, 6].map((id) => ({
      blocks: [],
      createtime: 0,
      id,
      role: id % 2 === 1 ? "user" : "assistant",
    }));
  }

  /** jsdom 没有布局:给滚动容器与各消息行钉上几何,否则 rect 全是 0。 */
  function layout(
    scroller: HTMLElement,
    bottom: number,
    tops: Record<number, number>,
  ) {
    scroller.getBoundingClientRect = () => ({ bottom, top: 0 }) as DOMRect;
    for (const row of scroller.querySelectorAll<HTMLElement>(
      "[data-message-id]",
    )) {
      const id = Number(row.getAttribute("data-message-id"));
      const top = tops[id];
      row.getBoundingClientRect = () =>
        ({ top, bottom: top + 50 }) as unknown as DOMRect;
    }
  }

  it("Given 用户上滚到第一轮、下面还压着两轮, When 药丸浮出, Then 它写出轮数", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    mockSessionStore.messages = turnMessages();
    componentMocks.transcriptRowMessageIds = [1, 2, 3, 4, 5, 6];
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-turns" />,
    );
    const scroller = transcriptScroller(view.container);
    // 视口下沿 400:消息 3 的顶边已在其下 → 下沿那条是消息 2,之后还有第 3、第 5 两轮。
    layout(scroller, 400, { 1: 0, 2: 200, 3: 420, 4: 500, 5: 600, 6: 700 });

    scrollUpFromBottom(scroller);

    expect(screen.getByTestId("transcript-jump-control")).toHaveTextContent(
      "2 turns below",
    );
  });

  it("Given 用户只上滚了一点、还在最后一轮里, When 药丸浮出, Then 退回「回到底部」", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    mockSessionStore.messages = turnMessages();
    componentMocks.transcriptRowMessageIds = [1, 2, 3, 4, 5, 6];
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-turns-0" />,
    );
    const scroller = transcriptScroller(view.container);
    // 下沿那条是消息 5(本轮的用户消息),其后只有本轮的 assistant 回复。
    layout(scroller, 400, { 1: 0, 2: 60, 3: 120, 4: 180, 5: 240, 6: 420 });

    scrollUpFromBottom(scroller);

    expect(screen.getByTestId("transcript-jump-control")).toHaveTextContent(
      "Back to bottom",
    );
  });

  // 补齐账回答的是另一个问题(「你离开时流进来多少」),断连刚回来时它才是用户要的。
  it("Given 补齐账与轮数同时在场, When 药丸浮出, Then 补齐文案压过轮数", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    mockSessionStore.messages = turnMessages();
    componentMocks.transcriptRowMessageIds = [1, 2, 3, 4, 5, 6];
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-turns-catchup" />,
    );
    const scroller = transcriptScroller(view.container);
    layout(scroller, 400, { 1: 0, 2: 200, 3: 420, 4: 500, 5: 600, 6: 700 });
    scrollUpFromBottom(scroller);

    act(() => {
      openCatchUpWindow(42);
    });
    act(() => {
      recordCatchUp(42, 900, 0);
    });

    const control = screen.getByTestId("transcript-jump-control");
    expect(control).not.toHaveTextContent("turns below");
    // 补齐把内容全追加进了已经存在的那一行,行数没变 —— 报不出条数就只说有新内容,
    // 但走的仍是补齐那一档。
    expect(control).toHaveTextContent("New content");
  });

  // 这一端的转录列靠左（ml-10 + max-w-measure，没有 mx-auto），所以居中必须按这条列
  // 算，不能按整面板宽的滚动容器算 —— 后者会把药丸推到正文右边那片空白里。
  // 列几何是宿主布局：包里不写死，由这里交给它。
  it("Given 桌面端靠左的转录列, When 药丸浮出, Then 浮层外壳按这条列定位", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    mockSessionStore.messages = turnMessages();
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-turns-col" />,
    );
    scrollUpFromBottom(transcriptScroller(view.container));

    const column = screen.getByTestId("transcript-jump-control").parentElement;
    expect(column?.className).toMatch(/(^|\s)ml-10(\s|$)/);
    expect(column?.className).toMatch(/(^|\s)max-w-measure(\s|$)/);
    expect(column?.className).toMatch(/(^|\s)justify-center(\s|$)/);
  });

  it("Given 转录里还没有消息行, When 药丸浮出, Then 不猜数字，写「回到底部」", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    mockSessionStore.messages = turnMessages();
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-turns-none" />,
    );
    const scroller = transcriptScroller(view.container);
    layout(scroller, 400, {});

    scrollUpFromBottom(scroller);

    expect(screen.getByTestId("transcript-jump-control")).toHaveTextContent(
      "Back to bottom",
    );
  });
});

describe("ChatPanel · transcript scroll restoration", () => {
  it("Given a tab-scoped scroll key, When ChatPanel unmounts across routes and remounts, Then it restores the previous scrollTop", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const first = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-a" />,
    );
    const firstScroller = transcriptScroller(first.container);

    act(() => {
      firstScroller.scrollTop = 1_240;
      fireEvent.scroll(firstScroller);
    });

    first.unmount();
    const second = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-a" />,
    );
    const secondScroller = transcriptScroller(second.container);

    expect(secondScroller.scrollTop).toBe(1_240);
  });

  it("Given saved scroll before messages load, When messages arrive after route remount, Then it restores the saved scrollTop instead of following bottom", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const first = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-a" />,
    );
    const firstScroller = transcriptScroller(first.container);

    act(() => {
      firstScroller.scrollTop = 1_240;
      fireEvent.scroll(firstScroller);
    });

    first.unmount();
    let height = 480;
    const second = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-a" />,
    );
    const secondScroller = transcriptScrollerWithDynamicHeight(
      second.container,
      () => height,
    );
    act(() => {
      secondScroller.scrollTop = 0;
    });

    act(() => {
      mockSessionStore.messages = [
        { blocks: [], createtime: 0, id: 1, role: "assistant" },
      ];
      height = 4_000;
      second.rerender(<ChatPanel sessionId={42} scrollStateKey="chat-tab-a" />);
    });

    expect(secondScroller.scrollTop).toBe(1_240);
  });

  it("Given a tab resumes at a tall bottom position, When virtualized height briefly collapses, Then the collapsed scroll event does not overwrite the saved position", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    let height = 8_392;
    const view = render(
      <ChatPanel active sessionId={42} scrollStateKey="chat-tab-collapse" />,
    );
    const scroller = transcriptScrollerWithDynamicHeight(
      view.container,
      () => height,
    );

    act(() => {
      scroller.scrollTop = 7_912;
      fireEvent.scroll(scroller);
    });
    expect(loadTranscriptScrollState("chat-tab-collapse")).toEqual({
      atBottom: true,
      scrollTop: 7_912,
    });

    view.rerender(
      <ChatPanel
        active={false}
        sessionId={42}
        scrollStateKey="chat-tab-collapse"
      />,
    );
    view.rerender(
      <ChatPanel active sessionId={42} scrollStateKey="chat-tab-collapse" />,
    );

    act(() => {
      height = 1_096;
      scroller.scrollTop = 896;
      fireEvent.scroll(scroller);
    });

    expect(loadTranscriptScrollState("chat-tab-collapse")).toEqual({
      atBottom: true,
      scrollTop: 7_912,
    });
  });

  it("Given a tab resumes while virtualized height is collapsed, When active-follow runs, Then it does not overwrite the saved bottom position", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    let height = 8_392;
    const view = render(
      <ChatPanel
        active
        sessionId={42}
        scrollStateKey="chat-tab-active-follow"
      />,
    );
    const scroller = transcriptScrollerWithDynamicHeight(
      view.container,
      () => height,
    );

    act(() => {
      scroller.scrollTop = 7_912;
      fireEvent.scroll(scroller);
    });
    expect(loadTranscriptScrollState("chat-tab-active-follow")).toEqual({
      atBottom: true,
      scrollTop: 7_912,
    });

    view.rerender(
      <ChatPanel
        active={false}
        sessionId={42}
        scrollStateKey="chat-tab-active-follow"
      />,
    );
    act(() => {
      height = 200;
    });
    view.rerender(
      <ChatPanel
        active
        sessionId={42}
        scrollStateKey="chat-tab-active-follow"
      />,
    );

    expect(loadTranscriptScrollState("chat-tab-active-follow")).toEqual({
      atBottom: true,
      scrollTop: 7_912,
    });
  });

  it("Given a tab ignored collapsed scroll events, When the virtualized height recovers, Then it restores the saved position before saving again", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    let height = 8_392;
    const view = render(
      <ChatPanel active sessionId={42} scrollStateKey="chat-tab-recover" />,
    );
    const scroller = transcriptScrollerWithDynamicHeight(
      view.container,
      () => height,
    );

    act(() => {
      scroller.scrollTop = 7_912;
      fireEvent.scroll(scroller);
    });

    view.rerender(
      <ChatPanel
        active={false}
        sessionId={42}
        scrollStateKey="chat-tab-recover"
      />,
    );
    view.rerender(
      <ChatPanel active sessionId={42} scrollStateKey="chat-tab-recover" />,
    );

    act(() => {
      height = 1_096;
      scroller.scrollTop = 896;
      fireEvent.scroll(scroller);
    });
    expect(loadTranscriptScrollState("chat-tab-recover")).toEqual({
      atBottom: true,
      scrollTop: 7_912,
    });

    act(() => {
      height = 8_392;
      scroller.scrollTop = 896;
      fireEvent.scroll(scroller);
    });

    expect(scroller.scrollTop).toBe(7_912);
    expect(loadTranscriptScrollState("chat-tab-recover")).toEqual({
      atBottom: true,
      scrollTop: 7_912,
    });
  });

  it("Given a tab is visible at the top while virtualized height recovers, When no scroll event fires, Then it proactively restores the saved position", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    let height = 8_392;
    const view = render(
      <ChatPanel active sessionId={42} scrollStateKey="chat-tab-raf-restore" />,
    );
    const scroller = transcriptScrollerWithDynamicHeight(
      view.container,
      () => height,
    );

    act(() => {
      scroller.scrollTop = 7_912;
      fireEvent.scroll(scroller);
    });

    view.rerender(
      <ChatPanel
        active={false}
        sessionId={42}
        scrollStateKey="chat-tab-raf-restore"
      />,
    );
    act(() => {
      height = 200;
      scroller.scrollTop = 0;
    });
    view.rerender(
      <ChatPanel active sessionId={42} scrollStateKey="chat-tab-raf-restore" />,
    );

    expect(scroller.style.visibility).toBe("hidden");

    act(() => {
      height = 8_392;
    });

    await waitFor(() => {
      expect(scroller.scrollTop).toBe(7_912);
    });
    expect(scroller.style.visibility).toBe("");
  });

  it("Given the transcript is suppressed for restore while requestAnimationFrame never fires, When the guard deadline elapses, Then it stops suppressing without waiting for a user scroll", async () => {
    vi.useFakeTimers();
    onTestFinished(() => {
      vi.clearAllTimers();
      vi.useRealTimers();
      vi.unstubAllGlobals();
    });
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    let height = 8_392;
    const view = render(
      <ChatPanel active sessionId={42} scrollStateKey="chat-tab-raf-starved" />,
    );
    const scroller = transcriptScrollerWithDynamicHeight(
      view.container,
      () => height,
    );

    act(() => {
      scroller.scrollTop = 7_912;
      fireEvent.scroll(scroller);
    });

    // 窗口被遮挡 / 不在前台时 WebView 会整段停掉 rAF(本地实测 Chromium 停过 6.4s),
    // 而 setTimeout 只被钳到 ~1s。恢复循环只挂 rAF 时,期限就永远等不到被检查 ——
    // 转录区无限期停在 visibility:hidden,只有用户滚一下才解除。
    vi.stubGlobal("requestAnimationFrame", () => 1);
    vi.stubGlobal("cancelAnimationFrame", () => {});

    view.rerender(
      <ChatPanel
        active={false}
        sessionId={42}
        scrollStateKey="chat-tab-raf-starved"
      />,
    );
    act(() => {
      height = 200;
      scroller.scrollTop = 0;
    });
    view.rerender(
      <ChatPanel active sessionId={42} scrollStateKey="chat-tab-raf-starved" />,
    );

    expect(scroller.style.visibility).toBe("hidden");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(COLLAPSED_RESTORE_GUARD_MS + 100);
    });

    expect(scroller.style.visibility).toBe("");
  });

  it("Given a new tab starts at the bottom on collapsed virtualized height, When height grows, Then it keeps following the bottom", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    let height = 1_096;
    const view = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-new-bottom" />,
    );
    const scroller = transcriptScrollerWithDynamicHeight(
      view.container,
      () => height,
    );
    act(() => {
      mockSessionStore.messages = [
        { blocks: [], createtime: 0, id: 1, role: "assistant" },
      ];
      view.rerender(
        <ChatPanel sessionId={42} scrollStateKey="chat-tab-new-bottom" />,
      );
    });

    expect(loadTranscriptScrollState("chat-tab-new-bottom")).toEqual({
      atBottom: true,
      scrollTop: 616,
    });

    act(() => {
      height = 8_392;
      fireEvent.scroll(scroller);
    });

    expect(scroller.scrollTop).toBe(7_912);
    expect(loadTranscriptScrollState("chat-tab-new-bottom")).toEqual({
      atBottom: true,
      scrollTop: 7_912,
    });
  });

  it("Given a different tab-scoped scroll key, When the same session opens in a new tab, Then it does not restore the old tab scrollTop", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const first = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-a" />,
    );
    const firstScroller = transcriptScroller(first.container);

    act(() => {
      firstScroller.scrollTop = 1_240;
      fireEvent.scroll(firstScroller);
    });

    first.unmount();
    const second = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-b" />,
    );
    const secondScroller = transcriptScroller(second.container);

    expect(secondScroller.scrollTop).toBe(0);
  });

  it("Given the user scrolls away from the bottom, When the transcript is rendered, Then a back-to-bottom control appears and returns to the bottom", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const { container } = render(
      <ChatPanel sessionId={42} scrollStateKey="chat-tab-a" />,
    );
    const scroller = transcriptScroller(container);

    act(() => {
      scroller.scrollTop = 300;
      fireEvent.scroll(scroller);
    });

    const button = await screen.findByRole("button", {
      name: "Back to bottom",
    });
    fireEvent.click(button);

    expect(scroller.scrollTop).toBe(3_520);
    expect(
      screen.queryByRole("button", { name: "Back to bottom" }),
    ).not.toBeInTheDocument();
  });
});

// QuotaMeter 路由回归: 新建会话(sessionId=0)还没首发前, quotaDeviceKey 不能
// 一律落到 "local" —— 远端 agent 起的新对话必须取 newSessionAgent.deviceID 作为
// "remote:<id>", 否则前端会把本机 5h/7d 配额错画在远端 chat 上(bug repro: 用户
// 用远端 agent 新建会话, agentred 那台没登录, 但 HUD 显示桌面本机的配额数字)。
describe("ChatPanel · 新对话 QuotaMeter 路由", () => {
  it("Given 远端 claudecode agent 起的新会话, When 还没首发, Then useCCUsage 用 remote:<id> 而不是 local", () => {
    resetStore();
    mockSessionStore.session = null;
    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            deviceID: "5",
            deviceName: "remote-box",
          } as never
        }
      />,
    );
    expect(ccUsageMock.calls).toContain("remote:5");
    expect(ccUsageMock.calls).not.toContain("local");
  });

  it("Given 本地 claudecode agent 起的新会话, When 还没首发, Then useCCUsage 用 local", () => {
    resetStore();
    mockSessionStore.session = null;
    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            // 本地 backend: deviceID 为空串
            deviceID: "",
          } as never
        }
      />,
    );
    expect(ccUsageMock.calls).toContain("local");
  });
});

describe("ChatPanel · 新对话 PermissionModePill", () => {
  it("sessionId=0 + newSessionAgent 是 claudecode 时,按 backend caps 渲染 pill (回归: 此前因 caps 永为 null 而隐藏)", () => {
    resetStore();
    mockSessionStore.session = null;
    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            defaultPermissionMode: "plan",
          } as never
        }
      />,
    );
    expect(screen.getByTestId("permission-mode-pill")).toBeInTheDocument();
  });

  it("sessionId=0 且无 newSessionAgent 时不渲染 pill (空态)", () => {
    resetStore();
    mockSessionStore.session = null;
    render(<ChatPanel sessionId={0} />);
    expect(
      screen.queryByTestId("permission-mode-pill"),
    ).not.toBeInTheDocument();
  });

  it("sessionId=0 + newSessionAgent 时渲染供应商选择器，选中后首发 Send 把 providerKey 透传", async () => {
    resetStore();
    mockSessionStore.session = null;
    appMocks.SendChatMessage.mockResolvedValue({
      assistantMessageId: 1001,
      sessionId: 42,
      stream: "chat:event:42:1001",
      userMessageId: 1000,
    });
    // 未绑 provider（CLI 登录态）的新建会话：供应商选择器照常显示（决策 5）。
    // claudecode 只兼容 anthropic 类型，列表里只有 Acme Claude。
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [
        {
          id: 11,
          providerKey: "acme-anthropic",
          name: "Acme Claude",
          type: "anthropic",
          enabled: true,
          defaultModelKey: "",
          model: "claude-sonnet-4-5",
        },
      ],
    });
    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            llmProviderKey: "",
          } as never
        }
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);
    await user.click(
      within(screen.getByRole("listbox")).getByRole("option", {
        name: /Follow this provider's default/,
      }),
    );

    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;
    expect(submit).toBeDefined();
    act(() => submit?.("hello"));

    await waitFor(() => {
      expect(appMocks.SendChatMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          sessionId: 0,
          agentId: 7,
          providerKey: "acme-anthropic",
        }),
      );
    });
  });

  it("Given a desktop peer target and a transient provider-default selection, When the first message is dispatched, Then PeerRunFresh carries the selected model target", async () => {
    resetStore();
    mockSessionStore.session = null;
    componentMocks.effectiveExecTarget = {
      kind: "desktop",
      deviceId: "sha256:peer-desktop",
      deviceName: "Peer Desktop",
    };
    appMocks.PeerRunFresh.mockResolvedValue({ sessionId: 42 });
    appMocks.RemoteDeviceList.mockResolvedValue([
      {
        id: 9,
        name: "Peer Desktop",
        daemonFingerprint: "sha256:peer-desktop",
        online: true,
        supportsLLMModelTarget: true,
      },
    ]);
    appMocks.ListLLMModels.mockResolvedValue({
      items: [
        {
          id: 21,
          providerId: 11,
          providerKey: "acme-anthropic",
          modelKey: "mk-sonnet",
          modelId: "claude-sonnet-4-5",
          name: "Sonnet",
          enabled: true,
        },
      ],
    });
    appMocks.RemoteDeviceListProviders.mockResolvedValue([
      {
        key: "acme-anthropic",
        name: "Acme Claude",
        type: "anthropic",
        enabled: true,
        defaultModelKey: "mk-sonnet",
        models: [
          {
            key: "mk-sonnet",
            modelId: "claude-sonnet-4-5",
            name: "Sonnet",
            enabled: true,
          },
        ],
      },
    ]);
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [
        {
          id: 11,
          providerKey: "acme-anthropic",
          name: "Acme Claude",
          type: "anthropic",
          enabled: true,
          defaultModelKey: "mk-sonnet",
          model: "claude-sonnet-4-5",
        },
      ],
    });
    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            llmProviderKey: "",
          } as never
        }
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).not.toBeDisabled());
    const user = userEvent.setup();
    await user.click(pill);
    await user.click(
      within(screen.getByRole("listbox")).getByRole("option", {
        name: /Follow this provider's default/,
      }),
    );

    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;
    act(() => submit?.("hello peer"));

    await waitFor(() => {
      expect(appMocks.PeerRunFresh).toHaveBeenCalledWith(
        expect.objectContaining({
          fingerprint: "sha256:peer-desktop",
          providerKey: "acme-anthropic",
          modelKey: "",
        }),
      );
    });
    expect(appMocks.SendChatMessage).not.toHaveBeenCalled();
  });

  it("sessionId=0 + 已绑 agent 的新建会话：未选时 pill 显示 agent 绑定供应商名", async () => {
    resetStore();
    mockSessionStore.session = null;
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [
        {
          id: 11,
          providerKey: "acme-anthropic",
          name: "Acme Claude",
          type: "anthropic",
          enabled: true,
          defaultModelKey: "",
          model: "claude-sonnet-4-5",
        },
      ],
    });
    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            llmProviderKey: "acme-anthropic",
          } as never
        }
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).not.toBeDisabled());
    // 未选时 pill 显示 agent 绑定供应商名（决策 5：已绑 → 绑定供应商名）。
    expect(pill).toHaveTextContent("Acme Claude");
  });

  it("openclaw 新建会话渲染 disabled pill（决策 10：禁用而非隐藏，不消费 agentre provider）", async () => {
    resetStore();
    mockSessionStore.session = null;
    // 本文件无 beforeEach 清 mock，先前测试的调用会累计 —— 这里先清掉只看本测试。
    appMocks.ListLLMProviders.mockClear();
    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 8,
            name: "Claw",
            agentBackendId: 2,
            backendType: "openclaw",
            llmProviderKey: "",
          } as never
        }
      />,
    );

    const pill = await screen.findByTestId("provider-pill");
    expect(pill).toBeDisabled();
    expect(appMocks.ListLLMProviders).not.toHaveBeenCalled();
  });

  it("已有会话跟随 Agent 固定绑定时，组合根把 agentModelKey 交给 pill 并显示解析模型", async () => {
    resetStore();
    appMocks.ListLLMProviders.mockClear();
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [
        {
          id: 11,
          providerKey: "acme-anthropic",
          name: "Acme",
          type: "anthropic",
          enabled: true,
          defaultModelKey: "mk-default",
          model: "claude-sonnet-4-5",
        },
      ],
    });
    appMocks.ListLLMModels.mockImplementation(() =>
      Promise.resolve({
        items: [
          {
            modelKey: "mk-default",
            modelId: "claude-haiku-4-5",
            name: "claude-haiku-4-5",
            enabled: true,
          },
          {
            modelKey: "mk-fixed",
            modelId: "claude-sonnet-4-5",
            name: "claude-sonnet-4-5",
            enabled: true,
          },
        ],
      }),
    );
    mockSessionStore.session = makeSession({
      backendType: "claudecode",
      providerKey: "",
      modelKey: "",
      agentProviderKey: "acme-anthropic",
      agentModelKey: "mk-fixed",
    });
    render(<ChatPanel sessionId={42} newSessionAgent={null} />);

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => {
      expect(pill).not.toBeDisabled();
      expect(appMocks.ListLLMModels).toHaveBeenCalled();
      expect(pill).toHaveTextContent(
        "Follow agent binding · claude-sonnet-4-5",
      );
    });
    expect(pill).toHaveTextContent("Follow agent binding");
    // 单行 pill 写的是解析出的模型 ID，供应商人读名由品牌标识承担 —— 这里要证的是
    // 组合根把 agentModelKey 传下去了（解析到固定绑定，而不是供应商默认）。
    expect(pill).not.toHaveTextContent("claude-haiku-4-5");
    expect(appMocks.ListLLMProviders).toHaveBeenCalled();
  });

  it("已有会话 openclaw 渲染 disabled pill + tooltip（决策 10）", async () => {
    resetStore();
    mockSessionStore.session = makeSession({
      backendType: "openclaw",
      providerKey: "",
      agentProviderKey: "",
    });
    render(<ChatPanel sessionId={43} newSessionAgent={null} />);

    const pill = await screen.findByTestId("provider-pill");
    expect(pill).toBeDisabled();
    expect(pill).toHaveAttribute(
      "title",
      "This backend does not use agentre providers",
    );
  });

  it("已有会话选中供应商：调用 SetChatSessionModelTarget(sessionId, providerKey, modelKey) 并 reload 会话以取新的切换 notice", async () => {
    resetStore();
    appMocks.ListLLMProviders.mockClear();
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [
        {
          id: 11,
          providerKey: "acme-anthropic",
          name: "Acme",
          type: "anthropic",
          enabled: true,
          defaultModelKey: "",
          model: "claude-sonnet-4-5",
        },
      ],
    });
    appMocks.SetChatSessionModelTarget.mockResolvedValue({
      providerKey: "acme-anthropic",
      modelKey: "",
      agentProviderKey: "",
      agentModelKey: "",
    });
    reloadSpy.mockClear();
    mockSessionStore.session = makeSession({
      backendType: "claudecode",
      providerKey: "",
      agentProviderKey: "",
    });
    render(<ChatPanel sessionId={42} newSessionAgent={null} />);

    const pill = await screen.findByTestId("provider-pill");
    await waitFor(() => expect(pill).not.toBeDisabled());

    const user = userEvent.setup();
    await user.click(pill);
    await user.click(
      within(screen.getByRole("listbox")).getByRole("option", {
        name: /Follow this provider's default/,
      }),
    );

    await waitFor(() => {
      expect(appMocks.SetChatSessionModelTarget).toHaveBeenCalledWith({
        sessionId: 42,
        providerKey: "acme-anthropic",
        modelKey: "",
      });
    });
    await waitFor(() => expect(reloadSpy).toHaveBeenCalled());
  });
});

describe("ChatPanel · 新对话空白态文案", () => {
  const newSessionAgent = {
    id: 7,
    name: "Eng",
    agentBackendId: 1,
    backendType: "claudecode",
  } as never;

  it("Given a chat is created from a project, When it has no first message yet, Then the empty copy names the project context", () => {
    resetStore();
    mockSessionStore.session = null;

    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={newSessionAgent}
        newSessionContext={{ projectId: 2 }}
      />,
    );

    expect(
      screen.getByText("Start a project chat with Eng in Agentre / backend"),
    ).toBeInTheDocument();
  });

  it("Given a free chat is created, When it has no first message yet, Then the empty copy stays generic", () => {
    resetStore();
    mockSessionStore.session = null;

    render(<ChatPanel sessionId={0} newSessionAgent={newSessionAgent} />);

    expect(screen.getByText("Start a chat with Eng")).toBeInTheDocument();
    expect(screen.queryByText(/project workspace/)).not.toBeInTheDocument();
  });
});

describe("ChatPanel · Codex collaboration mode", () => {
  it("uses live Codex contextWindow while session detail still has 0", () => {
    resetStore();
    mockSessionStore.session = makeSession({
      backendType: "codex",
      contextWindow: 0,
      id: 42,
      permissionMode: "default",
    });

    act(() => {
      useChatStreamsStore.getState().openStream({
        assistantMessageId: 1001,
        name: "chat:event:42:1001",
        sessionId: 42,
        streamStartedAt: Date.now(),
      });
      useChatStreamsStore.getState().patchLiveContextWindow(42, 1001, 258400);
    });

    render(<ChatPanel sessionId={42} />);

    expect(componentMocks.computeComposerContextUsage).toHaveBeenLastCalledWith(
      [],
      258400,
      null,
    );
  });

  it("disables mode switching while the current Codex turn is streaming", () => {
    resetStore();
    // Codex caps: switchableDuringTurn=false → turn 中 pill 应被禁用。
    componentMocks.capsSwitchableDuringTurn = false;
    componentMocks.capsAllowedModes = ["default", "plan"];
    mockSessionStore.session = makeSession({
      backendType: "codex",
      id: 42,
      permissionMode: "default",
    });
    act(() => {
      useChatStreamsStore.getState().openStream({
        assistantMessageId: 1001,
        name: "chat:event:42:1001",
        sessionId: 42,
        streamStartedAt: Date.now(),
      });
    });

    render(<ChatPanel sessionId={42} />);

    expect(componentMocks.permissionModePillProps.at(-1)?.disabled).toBe(true);
    expect(componentMocks.chatComposerProps.at(-1)?.onShiftTab).toBeUndefined();
    expect(screen.getByTestId("permission-mode-pill")).toBeDisabled();
  });

  it("disables mode switching when Codex session status is already running", () => {
    resetStore();
    componentMocks.capsSwitchableDuringTurn = false;
    componentMocks.capsAllowedModes = ["default", "plan"];
    mockSessionStore.session = makeSession({
      agentStatus: "running",
      backendType: "codex",
      id: 42,
      permissionMode: "default",
    });

    render(<ChatPanel sessionId={42} />);

    expect(componentMocks.permissionModePillProps.at(-1)?.disabled).toBe(true);
    expect(componentMocks.chatComposerProps.at(-1)?.onShiftTab).toBeUndefined();
    expect(screen.getByTestId("permission-mode-pill")).toBeDisabled();
  });

  it("sends the selected plan mode after the Codex turn is idle", async () => {
    resetStore();
    // Codex caps: switchableDuringTurn=false → turn 中 pill 应被禁用。
    componentMocks.capsSwitchableDuringTurn = false;
    componentMocks.capsAllowedModes = ["default", "plan"];
    mockSessionStore.session = makeSession({
      backendType: "codex",
      id: 42,
      permissionMode: "plan",
    });
    appMocks.SendChatMessage.mockResolvedValue({
      assistantMessageId: 1001,
      sessionId: 42,
      stream: "chat:event:42:1001",
      userMessageId: 1000,
    });

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;
    expect(submit).toBeDefined();

    act(() => {
      submit?.("next turn");
    });

    await waitFor(() => {
      expect(appMocks.SendChatMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          permissionMode: "plan",
          sessionId: 42,
          text: "next turn",
        }),
      );
    });
  });

  it("sends image attachments in the SendChatMessage payload", async () => {
    resetStore();
    mockSessionStore.session = makeSession({
      backendType: "builtin",
      id: 42,
    });
    appMocks.SendChatMessage.mockResolvedValue({
      assistantMessageId: 1001,
      sessionId: 42,
      stream: "chat:event:42:1001",
      userMessageId: 1000,
    });

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((message: {
          text: string;
          images?: Array<{ dataUrl: string; mediaType: string; name: string }>;
        }) => void)
      | undefined;
    expect(submit).toBeDefined();

    act(() => {
      submit?.({
        text: "",
        images: [
          {
            dataUrl: "data:image/png;base64,AQID",
            mediaType: "image/png",
            name: "shot.png",
          },
        ],
      });
    });

    await waitFor(() => {
      expect(appMocks.SendChatMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          sessionId: 42,
          text: "",
          images: [
            {
              dataUrl: "data:image/png;base64,AQID",
              name: "shot.png",
            },
          ],
        }),
      );
    });
  });

  it("blocks image payloads when the backend capability is absent", async () => {
    resetStore();
    componentMocks.capsImageInput = false;
    mockSessionStore.session = makeSession({
      backendType: "claudecode",
      id: 42,
    });

    render(<ChatPanel sessionId={42} />);
    expect(componentMocks.chatComposerProps.at(-1)?.supportsImageInput).toBe(
      false,
    );
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((message: {
          text: string;
          images?: Array<{ dataUrl: string; mediaType: string; name: string }>;
        }) => void)
      | undefined;

    act(() => {
      submit?.({
        text: "describe",
        images: [
          {
            dataUrl: "data:image/png;base64,AQID",
            mediaType: "image/png",
            name: "shot.png",
          },
        ],
      });
    });

    expect(appMocks.SendChatMessage).not.toHaveBeenCalled();
    expect(
      await screen.findByText(
        "The current agent backend does not support image input",
      ),
    ).toBeInTheDocument();
  });

  it.each(["codex", "piagent"])(
    "exact /compact starts %s compact RPC instead of sending a user message",
    async (backendType) => {
      resetStore();
      componentMocks.capsSwitchableDuringTurn = false;
      componentMocks.capsAllowedModes = ["default", "plan"];
      mockSessionStore.session = makeSession({
        backendType,
        id: 42,
        permissionMode: "default",
      });
      appMocks.CompactChatSession.mockResolvedValue({
        assistantMessageId: 1001,
        sessionId: 42,
        stream: "chat:event:42:1001",
      });

      render(<ChatPanel sessionId={42} />);
      const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
        | ((text: string) => void)
        | undefined;
      expect(submit).toBeDefined();

      act(() => {
        submit?.("/compact");
      });

      await waitFor(() => {
        expect(appMocks.CompactChatSession).toHaveBeenCalledWith({
          sessionId: 42,
        });
      });
      expect(appMocks.SendChatMessage).not.toHaveBeenCalled();
      expect(
        streamForMessage(useChatStreamsStore.getState(), 42, 1001)?.name,
      ).toBe("chat:event:42:1001");
    },
  );

  it("rejects exact /compact when image attachments are present", async () => {
    resetStore();
    componentMocks.capsSwitchableDuringTurn = false;
    componentMocks.capsAllowedModes = ["default", "plan"];
    mockSessionStore.session = makeSession({
      backendType: "codex",
      id: 42,
      permissionMode: "default",
    });

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((message: {
          text: string;
          images?: Array<{ dataUrl: string; mediaType: string; name: string }>;
        }) => void)
      | undefined;

    act(() => {
      submit?.({
        text: "/compact",
        images: [
          {
            dataUrl: "data:image/png;base64,AQID",
            mediaType: "image/png",
            name: "shot.png",
          },
        ],
      });
    });

    expect(appMocks.CompactChatSession).not.toHaveBeenCalled();
    expect(appMocks.SendChatMessage).not.toHaveBeenCalled();
    expect(
      await screen.findByText("/compact cannot be sent with images"),
    ).toBeInTheDocument();
  });

  it("exact /compact is rejected while the Codex turn is streaming", async () => {
    resetStore();
    componentMocks.capsSwitchableDuringTurn = false;
    componentMocks.capsAllowedModes = ["default", "plan"];
    mockSessionStore.session = makeSession({
      backendType: "codex",
      id: 42,
      permissionMode: "default",
    });
    act(() => {
      useChatStreamsStore.getState().openStream({
        assistantMessageId: 1001,
        name: "chat:event:42:1001",
        sessionId: 42,
        streamStartedAt: Date.now(),
      });
    });

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;

    act(() => {
      submit?.("/compact");
    });

    await new Promise((r) => setTimeout(r, 0));
    expect(appMocks.CompactChatSession).not.toHaveBeenCalled();
    expect(appMocks.SendChatMessage).not.toHaveBeenCalled();
    expect(appMocks.EnqueueChatMessage).not.toHaveBeenCalled();
  });

  it("exact /compact in a new Codex chat asks for an existing thread", async () => {
    resetStore();
    componentMocks.capsSwitchableDuringTurn = false;
    componentMocks.capsAllowedModes = ["default", "plan"];
    mockSessionStore.session = null;

    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Codex",
            agentBackendId: 1,
            backendType: "codex",
            defaultPermissionMode: "default",
          } as never
        }
      />,
    );
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;

    act(() => {
      submit?.("/compact");
    });

    await new Promise((r) => setTimeout(r, 0));
    expect(appMocks.CompactChatSession).not.toHaveBeenCalled();
    expect(appMocks.SendChatMessage).not.toHaveBeenCalled();
  });

  it("/goal objective sets Codex thread goal and starts a user turn", async () => {
    resetStore();
    componentMocks.capsSwitchableDuringTurn = false;
    componentMocks.capsAllowedModes = ["default", "plan"];
    mockSessionStore.session = makeSession({
      backendType: "codex",
      id: 42,
      permissionMode: "default",
    });
    appMocks.SetChatGoal.mockResolvedValue({
      goal: { objective: "ship rpc", status: "active", tokensUsed: 0 },
    });
    appMocks.SendChatMessage.mockResolvedValue({
      assistantMessageId: 1001,
      sessionId: 42,
      stream: "chat:event:42:1001",
      userMessageId: 1000,
    });

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;

    act(() => {
      submit?.("/goal ship rpc");
    });

    await waitFor(() => {
      expect(appMocks.SetChatGoal).toHaveBeenCalledWith({
        sessionId: 42,
        objective: "ship rpc",
        status: "active",
      });
    });
    await waitFor(() => {
      expect(appMocks.SendChatMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          permissionMode: "plan",
          sessionId: 42,
          text: "ship rpc",
        }),
      );
    });
  });

  it("/goal clear calls Codex clear goal RPC", async () => {
    resetStore();
    componentMocks.capsSwitchableDuringTurn = false;
    componentMocks.capsAllowedModes = ["default", "plan"];
    mockSessionStore.session = makeSession({
      backendType: "codex",
      id: 42,
      permissionMode: "default",
    });
    appMocks.ClearChatGoal.mockResolvedValue({ cleared: true });

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;

    act(() => {
      submit?.("/goal clear");
    });

    await waitFor(() => {
      expect(appMocks.ClearChatGoal).toHaveBeenCalledWith({ sessionId: 42 });
    });
    expect(appMocks.SendChatMessage).not.toHaveBeenCalled();
  });

  it("/goal is rejected while the Codex turn is still streaming", async () => {
    resetStore();
    componentMocks.capsSwitchableDuringTurn = false;
    componentMocks.capsAllowedModes = ["default", "plan"];
    mockSessionStore.session = makeSession({
      backendType: "codex",
      id: 42,
      permissionMode: "default",
    });
    useChatStreamsStore.getState().openStream({
      name: "chat:stream:goal-wait",
      sessionId: 42,
      assistantMessageId: 99,
      streamStartedAt: Date.now(),
    });

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;

    act(() => {
      submit?.("/goal complete");
    });

    expect(
      await screen.findByText(
        "Wait for this turn to finish before changing the goal",
      ),
    ).toBeInTheDocument();
    expect(appMocks.SetChatGoal).not.toHaveBeenCalled();
    expect(appMocks.ClearChatGoal).not.toHaveBeenCalled();
    expect(appMocks.SendChatMessage).not.toHaveBeenCalled();
    expect(appMocks.EnqueueChatMessage).not.toHaveBeenCalled();
  });

  it("/goal objective in a new Codex chat creates the goal session and starts a user turn", async () => {
    resetStore();
    componentMocks.capsSwitchableDuringTurn = false;
    componentMocks.capsAllowedModes = ["default", "plan"];
    mockSessionStore.session = null;
    const onSessionCreated = vi.fn();
    appMocks.StartChatGoal.mockResolvedValue({
      sessionId: 123,
      goal: { objective: "ship rpc", status: "active", tokensUsed: 0 },
    });
    appMocks.SendChatMessage.mockResolvedValue({
      assistantMessageId: 1001,
      sessionId: 123,
      stream: "chat:event:123:1001",
      userMessageId: 1000,
    });

    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Codex",
            agentBackendId: 1,
            backendType: "codex",
            defaultPermissionMode: "default",
          } as never
        }
        newSessionContext={{ projectId: 55 }}
        onSessionCreated={onSessionCreated}
      />,
    );
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;

    act(() => {
      submit?.("/goal ship rpc");
    });

    await waitFor(() => {
      expect(appMocks.StartChatGoal).toHaveBeenCalledWith({
        agentId: 7,
        projectId: 55,
        objective: "ship rpc",
        status: "active",
        permissionMode: "plan",
      });
    });
    expect(onSessionCreated).toHaveBeenCalledWith(123, 7);
    await waitFor(() => {
      expect(appMocks.SendChatMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          permissionMode: "plan",
          sessionId: 123,
          text: "ship rpc",
        }),
      );
    });
    expect(appMocks.SetChatGoal).not.toHaveBeenCalled();
  });

  // codex plan approve/continue 不再由 chat-panel 中转 SendChatMessage —— PlanCard
  // 直接调 wailsResolvePlanAction(canonical-tool/plan/card.test.tsx 覆盖)。
  // backend 端 plan_action_test.go 验证 actionId → Send 的入参映射。
});

// ─── 回归: SendChatMessage 失败需在 UI 上 inline 显示, 不能被 void 吞掉 ─────
// doSend 的所有调用点 (新建会话首发 / 已有会话续发) 都是 void doSend(...) fire-and-forget,
// 历史上整个函数没有 try/catch, 失败时 Promise rejection 进 console 都未必到,
// UI 完全无声, 用户体感"发出去有错误但没任何报错信息出来"。
describe("ChatPanel · doSend error surfacing", () => {
  it("shows an inline error notice when SendChatMessage rejects on a new chat", async () => {
    resetStore();
    mockSessionStore.session = null;
    appMocks.SendChatMessage.mockRejectedValueOnce(
      new Error("provider not configured"),
    );

    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            defaultPermissionMode: "default",
          } as never
        }
      />,
    );
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;
    expect(submit).toBeDefined();

    act(() => {
      submit?.("hello");
    });

    await waitFor(() => {
      expect(
        screen.getByText(/Send failed.*provider not configured/),
      ).toBeInTheDocument();
    });
  });

  it("shows an inline error notice when SendChatMessage rejects on an existing session", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    appMocks.SendChatMessage.mockRejectedValueOnce(new Error("backend down"));

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;

    act(() => {
      submit?.("next turn");
    });

    await waitFor(() => {
      expect(screen.getByText(/Send failed.*backend down/)).toBeInTheDocument();
    });
  });
});

describe("ChatPanel · launch command copy feedback", () => {
  it("Given the backend is Pi Agent, When the menu opens, Then copy launch command is available", async () => {
    const user = userEvent.setup();
    resetStore();
    mockSessionStore.session = makeSession({
      backendType: "piagent",
      id: 42,
      title: "Pi turn",
    });

    render(<ChatPanel sessionId={42} />);

    await user.click(screen.getByRole("button", { name: "More actions" }));

    expect(await screen.findByText("Copy Launch Command")).toBeInTheDocument();
  });

  it("Given the backend is built-in, When the menu opens, Then copy launch command is unavailable", async () => {
    const user = userEvent.setup();
    resetStore();
    mockSessionStore.session = makeSession({
      backendType: "builtin",
      id: 42,
      title: "Built-in turn",
    });

    render(<ChatPanel sessionId={42} />);

    await user.click(screen.getByRole("button", { name: "More actions" }));

    expect(screen.queryByText("Copy Launch Command")).not.toBeInTheDocument();
  });

  it("Given the launch command is copied, When the user selects the copy action, Then feedback appears as a timed bottom-right Sonner toast", async () => {
    const user = userEvent.setup();
    resetStore();
    mockSessionStore.session = makeSession({
      backendType: "codex",
      id: 42,
      title: "Remote turn",
    });
    appMocks.GetChatLaunchCommand.mockResolvedValueOnce({
      command: "AGENTRE_TOKEN=t agentre claudecode resume 42",
    });
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });

    render(<ChatPanel sessionId={42} />);

    await user.click(screen.getByRole("button", { name: "More actions" }));
    await user.click(await screen.findByText("Copy Launch Command"));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(
        "AGENTRE_TOKEN=t agentre claudecode resume 42",
      );
    });
    expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
      "Launch command copied",
      expect.objectContaining({
        description: expect.stringContaining("Includes a token"),
        duration: 5000,
        position: "bottom-right",
      }),
    );
    expect(
      screen.queryByText(/Launch command copied.*Includes a token/),
    ).not.toBeInTheDocument();
  });
});

// ─── Mark-read gating by `active` prop ────────────────────────────────────────
// chat-panel-host 把所有 tab 都 mount 起来,只用 display:none 控制可见;
// 隐藏 tab 的 ChatPanel 不应在 Done 时把 session 标记成已读 ——
// 那会让用户在另一个 tab 时,后台 turn 完成后未读 indicator 永远不出现。
// 同时 active=true 时,session.lastMessageAt 非零 / 推进时应 MarkRead。

import { useSessionStatusStore } from "@/stores/session-status-store";

// ─── turn 收尾:最后一轮不许先空掉再等 reload 回填 ─────────────────────────
// finishStream 会同步删掉 LiveStream(liveDelta/liveBlocks 当场清零),而 messages
// 里那条 assistant 还是发送时插的空占位 —— 若只靠异步 reloadSession 回填,这中间
// 至少绘一帧「最后一轮正文整段消失」,长会话上 IPC 往返越久闪得越明显。
// 后端在 done/aborted 事件里已经把最终 assistant 消息一起发过来了(chat.go 的
// `ChatStreamEvent{Kind: StreamDone, Message: final}`),这里必须同步落表。

describe("ChatPanel · turn 收尾不闪空", () => {
  const placeholder = {
    blocks: [],
    createtime: 1,
    id: 900,
    role: "assistant",
    seq: 0,
    sessionId: 42,
  };

  function finalMessage() {
    return {
      ...placeholder,
      blocks: [{ text: "final answer", type: "text" }],
    };
  }

  it("Given a turn whose live stream just dropped, When done carries the final assistant message, Then it lands synchronously instead of waiting for the reload round trip", async () => {
    resetStore();
    useSessionStatusStore.getState().__reset();
    mockSessionStore.session = makeSession({ id: 42 });
    mockSessionStore.messages = [placeholder];

    render(<ChatPanel sessionId={42} active={false} />);
    setMessagesSpy.mockClear();

    const final = finalMessage();
    act(() => {
      useSessionStatusStore
        .getState()
        .bumpDone(42, { kind: "done", message: final as never });
    });

    await waitFor(() => expect(setMessagesSpy).toHaveBeenCalled());
    const updater = setMessagesSpy.mock.calls.at(-1)?.[0] as (
      prev: Array<Record<string, unknown>>,
    ) => Array<Record<string, unknown>>;
    expect(updater([placeholder])).toEqual([final]);
  });

  it("Given the user stopped the turn, When aborted carries the partial assistant message, Then the partial content lands synchronously too", async () => {
    resetStore();
    useSessionStatusStore.getState().__reset();
    mockSessionStore.session = makeSession({ id: 42 });
    mockSessionStore.messages = [placeholder];

    render(<ChatPanel sessionId={42} active={false} />);
    setMessagesSpy.mockClear();

    const partial = finalMessage();
    act(() => {
      useSessionStatusStore
        .getState()
        .bumpDone(42, { kind: "aborted", message: partial as never });
    });

    await waitFor(() => expect(setMessagesSpy).toHaveBeenCalled());
    const updater = setMessagesSpy.mock.calls.at(-1)?.[0] as (
      prev: Array<Record<string, unknown>>,
    ) => Array<Record<string, unknown>>;
    expect(updater([placeholder])).toEqual([partial]);
  });
});

describe("ChatPanel · mark-read gated by active prop", () => {
  it("does not call MarkChatSessionRead when active=false and Done fires", async () => {
    resetStore();
    appMocks.MarkChatSessionRead.mockClear();
    useSessionStatusStore.getState().__reset();
    mockSessionStore.session = makeSession({
      id: 42,
      lastMessageAt: 2000,
    });

    render(<ChatPanel sessionId={42} active={false} />);

    // 模拟 turn 完成 — chat-streams-host 会调 bumpDone。
    act(() => {
      useSessionStatusStore.getState().bumpDone(42, {
        kind: "done",
        message: { sessionId: 42 } as never,
      });
    });

    // 给 effect 一个 tick;若隐藏 tab 错误地 MarkRead,这里就会断言失败。
    await waitFor(() => {
      expect(useSessionStatusStore.getState().statuses.get(42)?.doneTick).toBe(
        1,
      );
    });
    expect(appMocks.MarkChatSessionRead).not.toHaveBeenCalled();
  });

  it("calls MarkChatSessionRead when active=true with non-zero lastMessageAt", async () => {
    resetStore();
    appMocks.MarkChatSessionRead.mockClear();
    useSessionStatusStore.getState().__reset();
    mockSessionStore.session = makeSession({
      id: 7,
      lastMessageAt: 1500,
    });

    render(<ChatPanel sessionId={7} active={true} />);

    await waitFor(() => {
      expect(appMocks.MarkChatSessionRead).toHaveBeenCalledWith(
        expect.objectContaining({ sessionId: 7, timestamp: 1500 }),
      );
    });
  });
});

// ─── T26: 会话内终端 toggle 已移除 ───────────────────────────────────────────

describe("chat-panel · 终端 toggle 已移除", () => {
  it("渲染后不存在 title 含「终端」的 toggle 按钮", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 7 });

    render(<ChatPanel sessionId={7} />);

    expect(screen.queryByTitle(/终端/)).not.toBeInTheDocument();
  });

  it("⌘` 快捷键不再触发任何 terminal 动作", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 7 });

    render(<ChatPanel sessionId={7} />);
    // 触发原来的快捷键，不应抛错也不应改变任何可观测状态
    fireEvent.keyDown(window, { key: "`", metaKey: true });

    // 只要不报错且 TerminalPanel 不出现即为通过
    expect(screen.queryByTestId("terminal-panel")).not.toBeInTheDocument();
  });

  it("不渲染 TerminalPanel（终端已移至独立 tab）", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 5 });

    render(<ChatPanel sessionId={5} />);

    expect(screen.queryByTestId("terminal-panel")).not.toBeInTheDocument();
  });
});

// ─── T29: subagent_activity_started 旁路事件 ─────────────────────────────────
// 后台 subagent 开始产生内部活动时，后端经 "chat:autonomous:<sessionId>" 推
// subagent_activity_started 事件。前端必须仅调 openStream（指向发起消息），
// 不插入新消息行，不将 session 标记为 running。
describe("ChatPanel · T29 subagent_activity_started 旁路订阅", () => {
  /**
   * 找 EventsOn 中注册在 "chat:autonomous:<sessionId>" 信道上的 handler。
   * useChatStream 调 EventsOn(stream, handler) —— 我们从 mock.calls 里找对应条目。
   */
  function getAutonomousHandler(
    sessionId: number,
  ): ((ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void) | null {
    const calls = runtimeMocks.EventsOn.mock.calls as unknown as Array<
      [string, (ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void]
    >;
    const found = calls.find(
      ([name]) => name === `chat:autonomous:${sessionId}`,
    );
    return found ? found[1] : null;
  }

  it("Given a subagent_activity_started event, When it arrives on the autonomous channel, Then openStream is called with the launch message id", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 1 });

    render(<ChatPanel sessionId={1} />);

    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:1",
        expect.any(Function),
      ),
    );

    const handler = getAutonomousHandler(1);
    expect(handler).not.toBeNull();

    act(() => {
      handler!({
        kind: "subagent_activity_started",
        stream: "chat:event:1:42",
        sessionId: 1,
        launchMessageId: 42,
        toolUseId: "toolu_agent",
      } as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    // (a) openStream was called with the launch message id and stream name
    const liveStream = streamForMessage(useChatStreamsStore.getState(), 1, 42);
    expect(liveStream).toBeDefined();
    expect(liveStream?.assistantMessageId).toBe(42);
    expect(liveStream?.name).toBe("chat:event:1:42");
  });

  it("Given a subagent_activity_started event, When it fires, Then setMessages is NOT called to add a new message row", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 1 });

    render(<ChatPanel sessionId={1} />);

    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:1",
        expect.any(Function),
      ),
    );

    const handler = getAutonomousHandler(1);
    act(() => {
      handler!({
        kind: "subagent_activity_started",
        stream: "chat:event:1:42",
        sessionId: 1,
        launchMessageId: 42,
        toolUseId: "toolu_agent",
      } as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    // (b) setMessages must NOT be called — the launch message already exists
    expect(setMessagesSpy).not.toHaveBeenCalled();
  });

  it("Given a subagent_activity_started event, When it fires, Then the session is NOT marked running", async () => {
    resetStore();
    useSessionStatusStore.getState().__reset();
    mockSessionStore.session = makeSession({ id: 1, agentStatus: "idle" });

    render(<ChatPanel sessionId={1} />);

    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:1",
        expect.any(Function),
      ),
    );

    const handler = getAutonomousHandler(1);
    act(() => {
      handler!({
        kind: "subagent_activity_started",
        stream: "chat:event:1:42",
        sessionId: 1,
        launchMessageId: 42,
        toolUseId: "toolu_agent",
      } as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    // (c) session must NOT be marked running — background activity keeps session idle
    const status = useSessionStatusStore.getState().statuses.get(1);
    expect(status?.agentStatus).not.toBe("running");
  });

  // 后台 subagent 的活动流与用户轮共用同一条 assistant 消息时,后端两边发的流名
  // 都是 StreamName(sid, launchMsgID) —— 完全一致。此时再 openStream 只会把在跑
  // 那一轮已经流到屏幕、还没落库的 liveBlocks(以及计时 / 用量)整段清零,转录靠
  // 「持久化正文 ++ liveBlocks」拼出来的这一轮当场少掉一大段(sess-3396)。
  it("Given a live stream already open on the launch message, When subagent_activity_started arrives, Then the running turn's liveBlocks survive", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 1 });

    render(<ChatPanel sessionId={1} />);

    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:1",
        expect.any(Function),
      ),
    );

    act(() => {
      useChatStreamsStore.getState().openStream({
        name: "chat:event:1:42",
        sessionId: 1,
        assistantMessageId: 42,
        streamStartedAt: 1,
      });
      useChatStreamsStore.getState().appendLiveToolUse(1, 42, {
        type: "tool_use",
        toolUseId: "toolu_running",
        toolName: "Bash",
      } as never);
    });

    const handler = getAutonomousHandler(1);
    act(() => {
      handler!({
        kind: "subagent_activity_started",
        stream: "chat:event:1:42",
        sessionId: 1,
        launchMessageId: 42,
        toolUseId: "toolu_agent",
      } as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    const liveStream = streamForMessage(useChatStreamsStore.getState(), 1, 42);
    expect(liveStream?.liveBlocks.map((b) => b.toolUseId)).toEqual([
      "toolu_running",
    ]);
  });
});

// ─── T30: 后台任务完成翻转 ────────────────────────────────────────────────────
// 后台任务(run_in_background bash / subagent)的完成是跨轮的:主轮结束后,发起它的
// tool_use block 已从 liveBlocks 落进 messages。autonomous_started 携带 completedTask
// 到达时,只翻 liveBlocks(mergeSubagentMeta)翻不到那条块 —— 必须同时翻 messages,
// 否则后台任务面板胶囊 + 行内 pill 永远 spin(bug #2)。
describe("ChatPanel · T30 后台任务完成翻转 messages", () => {
  function getAutonomousHandler(
    sessionId: number,
  ): ((ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void) | null {
    const calls = runtimeMocks.EventsOn.mock.calls as unknown as Array<
      [string, (ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void]
    >;
    const found = calls.find(
      ([name]) => name === `chat:autonomous:${sessionId}`,
    );
    return found ? found[1] : null;
  }

  it("Given an autonomous_started with completedTask, When it arrives, Then setMessages flips the persisted background block to completed", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 1 });
    mockSessionStore.messages = [
      {
        id: 42,
        role: "assistant",
        blocks: [
          {
            type: "tool_use",
            toolUseId: "toolu_bg",
            toolInput: { run_in_background: true },
            subagent: { kind: "local_bash", status: "running" },
          },
        ],
      },
    ];

    render(<ChatPanel sessionId={1} />);
    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:1",
        expect.any(Function),
      ),
    );

    const handler = getAutonomousHandler(1);
    act(() => {
      handler!({
        kind: "autonomous_started",
        sessionId: 1,
        completedTask: {
          toolUseId: "toolu_bg",
          status: "completed",
          summary: "Background command finished (exit 0)",
        },
      } as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    expect(setMessagesSpy).toHaveBeenCalled();
    const updater = setMessagesSpy.mock.calls.at(-1)![0] as (
      prev: Array<Record<string, unknown>>,
    ) => Array<Record<string, unknown>>;
    const result = updater(
      mockSessionStore.messages as Array<Record<string, unknown>>,
    );
    const block = (result[0].blocks as Array<Record<string, unknown>>)[0];
    const subagent = block.subagent as Record<string, unknown>;
    expect(subagent.status).toBe("completed");
    expect(subagent.summary).toBe("Background command finished (exit 0)");
  });
});

// ─── T31: R18 —— 浏览器「开新一轮」的 user 行随 autonomous_started 插入 ────────
// 浏览器在空闲会话上发消息,daemon 把这一轮作为带 user_message 标记的补齐轮扇出。
// 前端收到 autonomous_started 时,必须先插 user 行再插 assistant 行,否则桌面端看到的
// 又是「没有提问的回复」。来源标识在 user 消息 DTO 上(transcript 渲染层复用
// chat.message.fromDevice 的 inline pill;本机消息无 sourceDevice,零变化)。

describe("ChatPanel · T31 R18 浏览器开新轮的 user 行插入", () => {
  function getAutonomousHandler(
    sessionId: number,
  ): ((ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void) | null {
    const calls = runtimeMocks.EventsOn.mock.calls as unknown as Array<
      [string, (ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void]
    >;
    const found = calls.find(
      ([name]) => name === `chat:autonomous:${sessionId}`,
    );
    return found ? found[1] : null;
  }

  it("Given an autonomous_started with userMessages, When it arrives, Then user row lands before the assistant row", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 1 });

    render(<ChatPanel sessionId={1} />);
    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:1",
        expect.any(Function),
      ),
    );

    const handler = getAutonomousHandler(1);
    act(() => {
      handler!({
        kind: "autonomous_started",
        sessionId: 1,
        stream: "chat:event:1:52",
        trigger: "catch_up",
        userMessages: [
          {
            id: 50,
            role: "user",
            blocks: [{ type: "text", text: "浏览器发来的消息" }],
            sourceDevice: "sha256:web-device",
            sourceDeviceName: "Chrome · macOS",
          },
        ],
        assistantMessage: {
          id: 52,
          role: "assistant",
          blocks: [],
        },
      } as unknown as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    expect(setMessagesSpy).toHaveBeenCalled();
    const updater = setMessagesSpy.mock.calls.at(-1)![0] as (
      prev: Array<Record<string, unknown>>,
    ) => Array<Record<string, unknown>>;
    const result = updater(mockSessionStore.messages);
    expect(result.map((m) => m.id)).toEqual([50, 52]);
    expect(result[0]).toMatchObject({
      role: "user",
      sourceDevice: "sha256:web-device",
      sourceDeviceName: "Chrome · macOS",
    });
    expect(result[1]).toMatchObject({ role: "assistant" });
  });

  it("Given an autonomous_started without userMessages (true autonomous turn), When it arrives, Then only the assistant row is appended (zero change)", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 2 });

    render(<ChatPanel sessionId={2} />);
    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:2",
        expect.any(Function),
      ),
    );

    const handler = getAutonomousHandler(2);
    act(() => {
      handler!({
        kind: "autonomous_started",
        sessionId: 2,
        stream: "chat:event:2:62",
        trigger: "background_task",
        assistantMessage: { id: 62, role: "assistant", blocks: [] },
      } as unknown as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    const updater = setMessagesSpy.mock.calls.at(-1)![0] as (
      prev: Array<Record<string, unknown>>,
    ) => Array<Record<string, unknown>>;
    const result = updater(mockSessionStore.messages);
    expect(result.map((m) => m.id)).toEqual([62]);
  });
});

// ─── T30b: 空闲态后台 subagent 的进度回写 messages(sess-2275)──────────────────
// 后台 subagent 在会话空闲态一直跑,CLI 每次工具调用都吐 task_progress。派遣卡的
// tool_use block 早已从 liveBlocks 落进 messages —— store 的 mergeSubagentMeta 只翻
// liveBlocks,合并落空,卡片上的工具数 / token 会一直停在派遣那一刻不动。会话级流上
// 镜像的那份 subagent_progress 必须同时合并进 messages。
describe("ChatPanel · T30b 空闲后台 subagent 进度回写", () => {
  function getAutonomousHandler(
    sessionId: number,
  ): ((ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void) | null {
    const calls = runtimeMocks.EventsOn.mock.calls as unknown as Array<
      [string, (ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void]
    >;
    const found = calls.find(
      ([name]) => name === `chat:autonomous:${sessionId}`,
    );
    return found ? found[1] : null;
  }

  it("Given a session-level subagent_progress, When it arrives, Then setMessages merges the new tool count / tokens into the persisted spawn card", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 1 });
    mockSessionStore.messages = [
      {
        id: 42,
        role: "assistant",
        blocks: [
          {
            type: "tool_use",
            toolUseId: "toolu_agent",
            toolInput: { run_in_background: true },
            subagent: {
              kind: "local_agent",
              status: "running",
              toolUses: 9,
              totalTokens: 84739,
            },
          },
        ],
      },
    ];

    render(<ChatPanel sessionId={1} />);
    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:1",
        expect.any(Function),
      ),
    );

    const handler = getAutonomousHandler(1);
    act(() => {
      handler!({
        kind: "subagent_progress",
        sessionId: 1,
        toolUseId: "toolu_agent",
        subagent: {
          toolUses: 21,
          totalTokens: 132480,
          lastToolName: "Edit",
        },
      } as unknown as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    expect(setMessagesSpy).toHaveBeenCalled();
    const updater = setMessagesSpy.mock.calls.at(-1)![0] as (
      prev: Array<Record<string, unknown>>,
    ) => Array<Record<string, unknown>>;
    const result = updater(
      mockSessionStore.messages as Array<Record<string, unknown>>,
    );
    const block = (result[0].blocks as Array<Record<string, unknown>>)[0];
    const subagent = block.subagent as Record<string, unknown>;
    expect(subagent.toolUses).toBe(21);
    expect(subagent.totalTokens).toBe(132480);
    expect(subagent.lastToolName).toBe("Edit");
    // 这一帧没带的字段保持不变
    expect(subagent.status).toBe("running");
  });
});

// ─── T30c: 空闲态后台 subagent 的模型回写 messages ────────────────────────────
// subagent_model 后端只带 toolUseId + model(不复用整份 Subagent 快照),避免浅合并
// 把已累计的 toolUses/totalTokens/status 覆盖成空值(R4)。派遣卡的 tool_use block
// 早已从 liveBlocks 落进 messages 时,会话级流上镜像的这份事件同样要合并进 messages。
describe("ChatPanel · T30c 空闲后台 subagent 模型回写", () => {
  function getAutonomousHandler(
    sessionId: number,
  ): ((ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void) | null {
    const calls = runtimeMocks.EventsOn.mock.calls as unknown as Array<
      [string, (ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void]
    >;
    const found = calls.find(
      ([name]) => name === `chat:autonomous:${sessionId}`,
    );
    return found ? found[1] : null;
  }

  it("Given a session-level subagent_model, When it arrives, Then setMessages merges model into the persisted spawn card without clearing progress/status", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 1 });
    mockSessionStore.messages = [
      {
        id: 42,
        role: "assistant",
        blocks: [
          {
            type: "tool_use",
            toolUseId: "toolu_agent",
            toolInput: { run_in_background: true },
            subagent: {
              kind: "local_agent",
              status: "running",
              toolUses: 9,
              totalTokens: 84739,
            },
          },
        ],
      },
    ];

    render(<ChatPanel sessionId={1} />);
    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:1",
        expect.any(Function),
      ),
    );

    const handler = getAutonomousHandler(1);
    act(() => {
      handler!({
        kind: "subagent_model",
        sessionId: 1,
        toolUseId: "toolu_agent",
        model: "claude-haiku-4-5-20251001",
      } as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    expect(setMessagesSpy).toHaveBeenCalled();
    const updater = setMessagesSpy.mock.calls.at(-1)![0] as (
      prev: Array<Record<string, unknown>>,
    ) => Array<Record<string, unknown>>;
    const result = updater(
      mockSessionStore.messages as Array<Record<string, unknown>>,
    );
    const block = (result[0].blocks as Array<Record<string, unknown>>)[0];
    const subagent = block.subagent as Record<string, unknown>;
    expect(subagent.model).toBe("claude-haiku-4-5-20251001");
    // 已累计的进度/状态字段不得被这次纯模型更新清空
    expect(subagent.status).toBe("running");
    expect(subagent.toolUses).toBe(9);
    expect(subagent.totalTokens).toBe(84739);
  });
});

// ─── T31: 自主续轮进行中用户又发消息(sess-1950)────────────────────────────────
// 后台任务完成 → 自主续轮正在流式输出。此时用户在输入框再发一条消息:
//   1. streaming=true(自主轮已 openStream)→ 走 doEnqueue;
//   2. 后端 Steer 只认 user turn 的 inTurn 标记,自主轮不置该标记 → 返
//      ChatSteerNoActive → 前端 fallback 到 doSend 起新一轮;
//   3. doSend 拿到新 assistant 行后调 openStream —— 而 chat-streams-store 的
//      LiveStream 是 **按 sessionId 单槽位** 的,新 openStream 直接覆盖整条 entry:
//      自主续轮已经流到屏幕上、但还没落库的 liveDelta / liveBlocks 全部丢失,
//      transcript 回退到该消息的持久化态(sess-1950 里就是稀疏 checkpoint),
//      同时 ChatStreamsHost 的订阅也从自主轮流名切走,自主轮后续事件无人接收。
// 用户可见症状:「已经输出的内容清空回退」。
describe("ChatPanel · T31 自主续轮流式中再发消息", () => {
  function getAutonomousHandler(
    sessionId: number,
  ): ((ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void) | null {
    const calls = runtimeMocks.EventsOn.mock.calls as unknown as Array<
      [string, (ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void]
    >;
    const found = calls.find(
      ([name]) => name === `chat:autonomous:${sessionId}`,
    );
    return found ? found[1] : null;
  }

  it("Given an autonomous turn is streaming, When the user sends a new message, Then the autonomous turn's already-streamed output is not discarded", async () => {
    resetStore();
    mockSessionStore.session = makeSession({
      id: 1950,
      backendType: "claudecode",
    });
    // 自主续轮的 assistant 行:落库时 blocks 还是空的(内容只在 liveDelta 里)。
    mockSessionStore.messages = [{ id: 13912, role: "assistant", blocks: [] }];
    // Enqueue 打到后端 → 自主轮没置 inTurn → ChatSteerNoActive(前端按文案前缀识别)。
    appMocks.EnqueueChatMessage.mockRejectedValue(
      new Error("没有进行中的对话可以插入消息"), // code.ChatSteerNoActive zh_cn 原文
    );
    appMocks.SendChatMessage.mockResolvedValue({
      sessionId: 1950,
      stream: "chat:event:1950:13914",
      assistantMessageId: 13914,
      userMessage: { id: 13913, role: "user", blocks: [] },
      assistantMessage: { id: 13914, role: "assistant", blocks: [] },
    });

    render(<ChatPanel sessionId={1950} />);
    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:1950",
        expect.any(Function),
      ),
    );

    // 自主续轮开始并流出一段文字。
    const handler = getAutonomousHandler(1950);
    act(() => {
      handler!({
        kind: "autonomous_started",
        sessionId: 1950,
        stream: "chat:event:1950:13912",
        trigger: "background_task",
        assistantMessage: { id: 13912, role: "assistant", blocks: [] },
      } as unknown as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });
    act(() => {
      useChatStreamsStore
        .getState()
        .appendLiveText(1950, 13912, "AUTONOMOUS-PARTIAL-OUTPUT");
    });
    expect(
      streamForMessage(useChatStreamsStore.getState(), 1950, 13912)?.liveDelta,
    ).toContain("AUTONOMOUS-PARTIAL-OUTPUT");

    // 用户在自主轮流式中又发一条。
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;
    expect(submit).toBeDefined();
    await act(async () => {
      submit!("给 e2e 补一个 AI 场景 spec");
      await new Promise((r) => setTimeout(r, 0));
    });

    // 已经流到屏幕上的自主轮输出不能凭空消失。
    const live = streamForMessage(useChatStreamsStore.getState(), 1950, 13912);
    const stillRenderable =
      live?.liveDelta?.includes("AUTONOMOUS-PARTIAL-OUTPUT") ||
      JSON.stringify(live?.liveBlocks ?? []).includes(
        "AUTONOMOUS-PARTIAL-OUTPUT",
      ) ||
      JSON.stringify(mockSessionStore.messages).includes(
        "AUTONOMOUS-PARTIAL-OUTPUT",
      );
    expect(stillRenderable).toBe(true);
  });
});

// ─── T32: 自主轮/后台活动轮的 per-turn 终态被漏(sess-2146)──────────────────────
// per-turn 流的 openStream(ChatPanel 收到 autonomous_started 才调)与 EventsOn 订阅
// (ChatStreamsHost 下一 render 才挂)跨 render 解耦。短轮的 per-turn done/closed 可能
// 赶在订阅注册前发完、被 fire-and-forget 事件丢掉 → LiveStream 永远留在 store →
// streaming 卡死:输入框被逼走 doEnqueue 发不出、自主轮那条空 assistant 行也不 reload
// 回填内容(用户可见症状:「发不出消息 + 有结果也不显示」)。
// 修复:收尾在**会话级**流(ChatPanel 挂载即订阅、无此 race)补发 autonomous_finished,
// 前端据 launchMessageId 兜底 finishStream(幂等)→ streaming 解卡 + doneTick 触发 reload。
describe("ChatPanel · T32 会话级 autonomous_finished 兜底漏掉的 per-turn 终态", () => {
  function getAutonomousHandler(
    sessionId: number,
  ): ((ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void) | null {
    const calls = runtimeMocks.EventsOn.mock.calls as unknown as Array<
      [string, (ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void]
    >;
    const found = calls.find(
      ([name]) => name === `chat:autonomous:${sessionId}`,
    );
    return found ? found[1] : null;
  }

  it("Given an autonomous turn's per-turn done was missed (orphan stream), When autonomous_finished arrives on the session channel, Then the stream is finished and the session reloads", async () => {
    resetStore();
    mockSessionStore.session = makeSession({
      id: 2146,
      backendType: "claudecode",
    });
    mockSessionStore.messages = [{ id: 5001, role: "assistant", blocks: [] }];

    render(<ChatPanel sessionId={2146} />);
    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:2146",
        expect.any(Function),
      ),
    );

    const handler = getAutonomousHandler(2146);
    expect(handler).toBeTruthy();

    // 自主轮开始:插入 assistant 行 + openStream。随后模拟「per-turn done 被漏掉」——
    // 本测试没挂 ChatStreamsHost,per-turn 流根本没有订阅者,done 发到虚空,orphan 成立。
    act(() => {
      handler!({
        kind: "autonomous_started",
        sessionId: 2146,
        stream: "chat:event:2146:5001",
        trigger: "background_task",
        assistantMessage: { id: 5001, role: "assistant", blocks: [] },
      } as unknown as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });
    // orphan 流在 store 里 → streaming=true(输入框被卡)。
    expect(
      streamForMessage(useChatStreamsStore.getState(), 2146, 5001),
    ).toBeTruthy();

    // 初次挂载的 reload 不算数,清掉,只断言兜底触发的那次。
    reloadSpy.mockClear();

    // 会话级流补发终态兜底。
    act(() => {
      handler!({
        kind: "autonomous_finished",
        sessionId: 2146,
        launchMessageId: 5001,
      } as unknown as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    // orphan 流被 finish → streaming 解卡。
    expect(
      streamForMessage(useChatStreamsStore.getState(), 2146, 5001),
    ).toBeNull();
    // doneTick 自增 → ChatPanel 兜底 reload,把空 assistant 行回填成落库的最终内容。
    await waitFor(() => expect(reloadSpy).toHaveBeenCalled());
  });

  it("Given the per-turn done was already received (stream gone), When autonomous_finished arrives, Then it is a no-op (idempotent, no extra reload)", async () => {
    resetStore();
    mockSessionStore.session = makeSession({
      id: 2146,
      backendType: "claudecode",
    });
    mockSessionStore.messages = [{ id: 5001, role: "assistant", blocks: [] }];

    render(<ChatPanel sessionId={2146} />);
    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        "chat:autonomous:2146",
        expect.any(Function),
      ),
    );
    const handler = getAutonomousHandler(2146);

    // 没有任何 orphan 流(per-turn done 正常收到、流已被移除的场景)。
    reloadSpy.mockClear();
    act(() => {
      handler!({
        kind: "autonomous_finished",
        sessionId: 2146,
        launchMessageId: 5001,
      } as unknown as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    expect(
      streamForMessage(useChatStreamsStore.getState(), 2146, 5001),
    ).toBeNull();
    // 流本就不在 → 不 finishStream → 不额外 reload。
    expect(reloadSpy).not.toHaveBeenCalled();
  });
});

// ─── T33: 自主续轮落库失败的会话级错误事件 ─────────────────────────────────────
// 自主续轮的 assistant 消息落库最终失败时,后端把会话翻 error、经**会话级**流推一条
// error 事件、中断 CLI 这一轮,再抽干事件(见
// docs/specs/2026-08-07-autonomous-turn-resilience.md「自主续轮落库失败时的可观察结果」)。
// 这一轮压根没有 assistant 行 —— 失败的正是建那一行的写事务,per-turn 流也从未开过,
// ChatStreamsHost 按 assistantMessageId 收口的 error 路径接不住。会话级 handler 必须
// 自己把它渲染出来:否则用户在会话里什么都看不到,状态胶囊还停在上一态(规范
// 「用户故事 1:立刻在会话里看到出错而不是永久转圈」)。
describe("ChatPanel · T33 自主续轮落库失败的会话级错误事件", () => {
  function getAutonomousHandler(
    sessionId: number,
  ): ((ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void) | null {
    const calls = runtimeMocks.EventsOn.mock.calls as unknown as Array<
      [string, (ev: import("@/hooks/use-chat-stream").ChatStreamEvent) => void]
    >;
    const found = calls.find(
      ([name]) => name === `chat:autonomous:${sessionId}`,
    );
    return found ? found[1] : null;
  }

  async function mountPanel(sessionId: number) {
    resetStore();
    useSessionStatusStore.getState().__reset();
    mockSessionStore.session = makeSession({
      id: sessionId,
      backendType: "claudecode",
    });
    mockSessionStore.messages = [];
    render(<ChatPanel sessionId={sessionId} />);
    await waitFor(() =>
      expect(runtimeMocks.EventsOn).toHaveBeenCalledWith(
        `chat:autonomous:${sessionId}`,
        expect.any(Function),
      ),
    );
    const handler = getAutonomousHandler(sessionId);
    expect(handler).toBeTruthy();
    return handler!;
  }

  it("Given the backend dropped an autonomous turn after its persist failed, When the session-level error event arrives, Then the panel surfaces the error copy and flips the session status to error", async () => {
    const handler = await mountPanel(2627);

    act(() => {
      handler({
        kind: "error",
        sessionId: 2627,
        error: "操作失败\ndatabase is locked (5) (SQLITE_BUSY)",
      } as unknown as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    // 文案原样来自后端 mapTurnError(动态输出,不进 i18n),headline / cause 按既有
    // splitErrorDetail 拆成两段渲染。
    expect(screen.getByText("操作失败")).toBeInTheDocument();
    expect(screen.getByTestId("notice-detail")).toHaveTextContent(
      "database is locked (5) (SQLITE_BUSY)",
    );
    // 后端只把 error 落了库、没有 emit session_status,前端不补这一刀,tab / sidebar
    // 会停在上一状态(同 markSessionRunning 的既有理由)。
    expect(
      useSessionStatusStore.getState().statuses.get(2627)?.agentStatus,
    ).toBe("error");
  });

  it("Given a session-level error event carrying no error text, When it arrives, Then no notice is shown (boundary: nothing to display)", async () => {
    const handler = await mountPanel(2628);

    act(() => {
      handler({
        kind: "error",
        sessionId: 2628,
      } as unknown as import("@/hooks/use-chat-stream").ChatStreamEvent);
    });

    expect(screen.queryByTestId("notice-detail")).toBeNull();
    expect(
      useSessionStatusStore.getState().statuses.get(2628)?.agentStatus,
    ).toBeUndefined();
  });
});

describe("ChatPanel · /new 斜杠命令", () => {
  it("exact /new 在新 tab 中开同 agent+项目的空白会话并跳转,不动当前会话", () => {
    resetStore();
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    mockSessionStore.session = makeSession({
      backendType: "claudecode",
      id: 42,
      agentId: 7,
      projectId: 3,
      permissionMode: "default",
    });

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;
    expect(submit).toBeDefined();

    act(() => {
      submit?.("/new");
    });

    const state = useChatTabsStore.getState();
    expect(state.tabs).toHaveLength(1);
    expect(state.tabs[0].meta).toMatchObject({
      kind: "new",
      agentId: 7,
      projectId: 3,
    });
    expect(state.activeTabId).toBe(state.tabs[0].id);
    // 当前会话完全不受影响:既不发消息,也不压缩。
    expect(appMocks.SendChatMessage).not.toHaveBeenCalled();
    expect(appMocks.CompactChatSession).not.toHaveBeenCalled();
  });
});

// ─── 停止「重启遗孤」会话:Stop 成功后主动 reload 把死按钮收回去 ──────────────────

describe("ChatPanel · 停止重启遗孤会话", () => {
  it("Given DB 卡在 running 但本地无活跃 stream, When 点停止且后端 reconcile 成功, Then 主动 reload 收回按钮", async () => {
    resetStore();
    // 重启遗孤:turn goroutine 早死了,DB agent_status 还停在 running,本地 store 无 stream。
    // 后端 Stop 会把它 reconcile 回 idle 并返 stopped:true;但这类会话没有活跃 stream
    // 不会推 aborted 事件,前端必须主动 reload 才能让「停止」按钮回灰。
    appMocks.StopChatMessage.mockResolvedValue({ stopped: true });
    mockSessionStore.session = makeSession({ id: 42, agentStatus: "running" });

    render(<ChatPanel sessionId={42} />);

    const stopBtn = screen.getByRole("button", { name: "Stop" });
    expect(stopBtn).toBeEnabled();

    // 只断言点击后那一次 reload,排除 mount 期可能的 reload 干扰。
    reloadSpy.mockClear();
    fireEvent.click(stopBtn);

    await waitFor(() => {
      expect(appMocks.StopChatMessage).toHaveBeenCalledWith({ sessionId: 42 });
    });
    await waitFor(() => {
      expect(reloadSpy).toHaveBeenCalled();
    });
  });
});

// ─── notice 错误详情:后端 cause 拆分渲染 ─────────────────────────────────────

describe("ChatPanel · notice 错误详情", () => {
  it("Given 后端错误带 cause, When 发送失败, Then 详情块渲染 cause 且可选中复制", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ backendType: "builtin", id: 42 });
    appMocks.SendChatMessage.mockRejectedValue(
      new Error(
        "操作失败\nSQL logic error: table chat_sessions has no column named run_id (1)",
      ),
    );

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;
    expect(submit).toBeDefined();

    act(() => {
      submit?.("hi");
    });

    const detail = await screen.findByTestId("notice-detail");
    expect(detail).toHaveTextContent(
      "SQL logic error: table chat_sessions has no column named run_id (1)",
    );
    // 可选中复制(globals.css 全局 user-select:none 的 opt-in),而不是加复制按钮
    expect(detail).toHaveAttribute("data-selectable-text", "true");
  });

  // Wails 真实形状:dispatcher 写 callbackMessage.Err = err.Error(),runtime 的 Callback 再
  // reject(message.error) —— reject 的是**裸字符串**,不是 Error 对象。上一个用例沿用了本文件
  // 既有的 new Error(...) 惯例,但那个形状生产环境不会出现;这里锁死真实形状也能拆出详情。
  it("Given Wails 以裸字符串 reject(生产真实形状), When 发送失败, Then 详情块照样渲染", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ backendType: "builtin", id: 42 });
    appMocks.SendChatMessage.mockRejectedValue(
      "操作失败\nSQL logic error: table chat_sessions has no column named run_id (1)",
    );

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;

    act(() => {
      submit?.("hi");
    });

    const detail = await screen.findByTestId("notice-detail");
    expect(detail).toHaveTextContent(
      "SQL logic error: table chat_sessions has no column named run_id (1)",
    );
  });

  it("Given 后端错误无 cause, When 发送失败, Then 不渲染详情块", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ backendType: "builtin", id: 42 });
    appMocks.SendChatMessage.mockRejectedValue(new Error("操作失败"));

    render(<ChatPanel sessionId={42} />);
    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;

    act(() => {
      submit?.("hi");
    });

    // 先等 notice 出现,再断言详情块不存在 —— 否则可能在 notice 渲染前就通过了。
    await waitFor(() => {
      expect(appMocks.SendChatMessage).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(screen.queryByTestId("notice-detail")).toBeNull();
    });
  });
});

// ─── 任务 3：新会话 tab 输入守卫 ─────────────────────────────────────────────
//
// 不可对话 Agent 的 kind:"new" tab：输入框上方内联引导块 + ChatComposer 禁用 +
// 占位「请先配置 Agent 后端」；可对话 Agent 的新 tab 现状不变。ChatComposer 在
// 本文件被 mock 成桩，props 记录在 componentMocks.chatComposerProps，据此断言
// disabled / placeholder 是否传导。引导块内含 useNavigate，测试用 MemoryRouter 包一层。
describe("ChatPanel · 新会话 tab 输入守卫（非可对话 Agent）", () => {
  function newSessionAgentFor(overrides: Record<string, unknown>) {
    return {
      id: 7,
      name: "CEO 助手",
      backendType: "builtin",
      chattable: true,
      chattableHint: "",
      blockReason: "",
      ...overrides,
    } as never;
  }

  it("Given 非可对话 Agent 的新 tab, When 渲染, Then 显示内联引导块（徽标+标题+原因+主/次按钮）且 ChatComposer 被禁用并提示先配置", () => {
    resetStore();
    mockSessionStore.session = null;
    render(
      <MemoryRouter>
        <ChatPanel
          sessionId={0}
          newSessionAgent={newSessionAgentFor({
            chattable: false,
            blockReason: "no-backend",
          })}
        />
      </MemoryRouter>,
    );

    const guard = screen.getByTestId("new-session-guard");
    expect(guard).toBeInTheDocument();
    expect(guard).toHaveAttribute("role", "alert");
    expect(guard).toHaveAttribute("aria-live", "polite");
    // 徽标（复用 agent-list 的未配置后端徽标文案）
    expect(
      within(guard).getByText(/Backend not configured/i),
    ).toBeInTheDocument();
    // 标题：配置完成后即可与 {{name}} 对话
    expect(
      within(guard).getByText(/start chatting with CEO 助手 once configured/i),
    ).toBeInTheDocument();
    // 原因描述（复用任务 2 的 no-backend 原因）
    expect(
      within(guard).getByText(/no Agent backend configured/i),
    ).toBeInTheDocument();
    // 主按钮 + 次按钮（复用任务 2 的 CTA 文案）
    expect(
      within(guard).getByRole("button", {
        name: /Configure Agent backend/i,
      }),
    ).toBeInTheDocument();
    expect(
      within(guard).getByRole("button", {
        name: /Go to Agent backend settings/i,
      }),
    ).toBeInTheDocument();

    const composer = componentMocks.chatComposerProps.at(-1);
    expect(composer?.disabled).toBe(true);
    expect(composer?.placeholder).toBe("Configure the Agent backend first");
  });

  it("Given 可对话 Agent 的新 tab, When 渲染, Then 不显示引导块且 ChatComposer 保持可用", () => {
    resetStore();
    mockSessionStore.session = null;
    render(
      <MemoryRouter>
        <ChatPanel
          sessionId={0}
          newSessionAgent={newSessionAgentFor({
            chattable: true,
            blockReason: "",
          })}
        />
      </MemoryRouter>,
    );

    expect(screen.queryByTestId("new-session-guard")).toBeNull();
    const composer = componentMocks.chatComposerProps.at(-1);
    expect(composer?.disabled).toBeFalsy();
  });
});

describe("ChatPanel · 远端 ModelTarget Picker 门控（gap 1：ProviderPill 接收 daemon 目录/能力）", () => {
  it("Given the effective new-session backend has a different binding from the Agent primary backend, When the Composer loads, Then follow-binding resolves from the effective backend", async () => {
    resetStore();
    mockSessionStore.session = null;
    componentMocks.effectiveExecTarget = {
      kind: "local",
      deviceId: "",
      deviceName: "Local",
      backendType: "claudecode",
      llmProviderKey: "target-provider",
      llmModelKey: "",
    };
    appMocks.ListLLMProviders.mockResolvedValue({
      items: [
        {
          id: 11,
          providerKey: "primary-provider",
          name: "Primary Provider",
          type: "anthropic",
          enabled: true,
          defaultModelKey: "",
        },
        {
          id: 12,
          providerKey: "target-provider",
          name: "Target Provider",
          type: "anthropic",
          enabled: true,
          defaultModelKey: "",
        },
      ],
    });

    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            llmProviderKey: "primary-provider",
          } as never
        }
      />,
    );

    expect(await screen.findByTestId("provider-pill")).toHaveTextContent(
      "Target Provider",
    );
  });

  it("Given a new chat overrides the Agent default to another remote target, When the Composer loads, Then model gating uses the effective target", async () => {
    resetStore();
    mockSessionStore.session = null;
    componentMocks.effectiveExecTarget = {
      kind: "daemon",
      deviceId: "sha256:target-b",
      deviceName: "Target B",
    };
    appMocks.RemoteDeviceList.mockResolvedValue([
      {
        id: 9,
        name: "Target B",
        daemonFingerprint: "sha256:target-b",
        online: true,
        supportsLLMModelTarget: false,
      },
    ]);
    appMocks.RemoteDeviceListProviders.mockResolvedValue([]);

    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "claudecode",
            deviceID: "sha256:target-a",
          } as never
        }
      />,
    );

    await waitFor(() => {
      expect(appMocks.RemoteDeviceListProviders).toHaveBeenCalledWith(9);
    });
  });

  it("Given 会话绑定了远端设备, When 渲染 composer, Then ProviderPill 按该设备拉取 daemon 目录/能力做远端门控", async () => {
    resetStore();
    mockSessionStore.session = makeSession({
      id: 42,
      deviceID: "7",
      backendType: "claudecode",
    });
    appMocks.RemoteDeviceList.mockResolvedValue([
      { id: 7, name: "Build box", online: true, supportsLLMModelTarget: false },
    ]);
    appMocks.RemoteDeviceListProviders.mockResolvedValue([]);

    render(<ChatPanel sessionId={42} />);

    await waitFor(() => {
      expect(appMocks.RemoteDeviceList).toHaveBeenCalled();
      expect(appMocks.RemoteDeviceListProviders).toHaveBeenCalledWith(7);
    });
  });

  it("Given 本机会话（无设备）, When 渲染 composer, Then ProviderPill 不拉远端目录（无远端门控）", async () => {
    resetStore();
    mockSessionStore.session = makeSession({
      id: 42,
      deviceID: "",
      backendType: "claudecode",
    });

    render(<ChatPanel sessionId={42} />);

    await waitFor(() => {
      expect(componentMocks.chatComposerProps.length).toBeGreaterThan(0);
    });
    expect(appMocks.RemoteDeviceList).not.toHaveBeenCalled();
    expect(appMocks.RemoteDeviceListProviders).not.toHaveBeenCalled();
  });
});

// ─── 会话加载失败 UX:不再静默关闭 tab,而是渲染错误卡(Retry / Close) ───────────

describe("ChatPanel · session load failure UX", () => {
  it("Given a session load error, When the panel renders, Then an error card with Retry/Close is shown and the tab stays open", () => {
    resetStore();
    mockSessionStore.session = null;
    mockSessionStore.messages = [];
    mockSessionStore.error = "Chat session not found";
    mockSessionStore.loading = false;
    const onDeleted = vi.fn();
    render(<ChatPanel active sessionId={42} onSessionDeleted={onDeleted} />);

    // 错误卡渲染,不是静默关 tab
    expect(screen.getByText("Couldn't load session")).toBeInTheDocument();
    expect(screen.getByText("Chat session not found")).toBeInTheDocument();
    // not-found 专属提示
    expect(
      screen.getByText("This session no longer exists or has been deleted."),
    ).toBeInTheDocument();
    expect(onDeleted).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Close session" }),
    ).toBeInTheDocument();
  });

  it("Given a non-not-found load error, When the panel renders, Then no not-found hint is shown", () => {
    resetStore();
    mockSessionStore.session = null;
    mockSessionStore.messages = [];
    mockSessionStore.error = "backend unavailable";
    mockSessionStore.loading = false;
    render(<ChatPanel active sessionId={42} />);

    expect(screen.getByText("Couldn't load session")).toBeInTheDocument();
    expect(screen.getByText("backend unavailable")).toBeInTheDocument();
    expect(
      screen.queryByText("This session no longer exists or has been deleted."),
    ).toBeNull();
  });

  it("Given a load error, When Close is clicked, Then the tab closes via onSessionDeleted", () => {
    resetStore();
    mockSessionStore.session = null;
    mockSessionStore.messages = [];
    mockSessionStore.error = "boom";
    mockSessionStore.loading = false;
    const onDeleted = vi.fn();
    render(<ChatPanel active sessionId={42} onSessionDeleted={onDeleted} />);

    fireEvent.click(screen.getByRole("button", { name: "Close session" }));
    expect(onDeleted).toHaveBeenCalledOnce();
  });

  it("Given a load error, When Retry is clicked, Then reloadSession is invoked", () => {
    resetStore();
    mockSessionStore.session = null;
    mockSessionStore.messages = [];
    mockSessionStore.error = "boom";
    mockSessionStore.loading = false;
    render(<ChatPanel active sessionId={42} />);
    reloadSpy.mockClear();

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(reloadSpy).toHaveBeenCalledOnce();
  });

  it("Given the session is loading with no content yet, When the panel renders, Then a skeleton holds the rows and the scroller announces itself busy", () => {
    resetStore();
    mockSessionStore.session = null;
    mockSessionStore.messages = [];
    mockSessionStore.loading = true;
    mockSessionStore.error = null;
    const view = render(<ChatPanel active sessionId={42} />);

    // 规格 2026-08-23 决策 9：骨架本身对读屏隐身，「下面还会变」改由滚动带的
    // aria-busy 说 —— 此前这里是骨架自己顶着 role=status + aria-label。
    expect(screen.getByTestId("transcript-skeleton")).toBeInTheDocument();
    expect(view.container.querySelector("section")).toHaveAttribute(
      "aria-busy",
      "true",
    );
    // 骨架占位期间不渲染 transcript
    expect(screen.queryByTestId("chat-transcript")).toBeNull();
  });

  it("Given a loaded session with existing messages, When loading is true (turn-end reload), Then no skeleton is shown", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    mockSessionStore.messages = [
      { blocks: [{ text: "hi", type: "text" }], id: 1, role: "user" },
    ];
    mockSessionStore.loading = true;
    mockSessionStore.error = null;
    render(<ChatPanel active sessionId={42} />);

    expect(
      screen.queryByRole("status", { name: "Loading session…" }),
    ).toBeNull();
    expect(screen.getByTestId("chat-transcript")).toBeInTheDocument();
  });
});

// ─── 发送失败草稿恢复:restoreDraft 被调用,notice 挂 Retry / Discard ───────────

describe("ChatPanel · send failure draft restore", () => {
  const image = {
    dataUrl: "data:image/png;base64,AQID",
    mediaType: "image/png",
    name: "shot.png",
  };

  it("Given SendChatMessage rejects, When a message with images is submitted, Then restoreDraft is invoked with the exact text + images", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, agentId: 9 });
    appMocks.SendChatMessage.mockRejectedValue(
      new Error("operation failed\ndatabase is locked"),
    );
    render(<ChatPanel active sessionId={42} />);
    const onSubmit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((msg: unknown) => void)
      | undefined;
    expect(onSubmit).toBeDefined();

    act(() => {
      onSubmit?.({ text: "hello world", images: [image] });
    });

    await waitFor(() =>
      expect(componentMocks.composerHandle.restoreDraft).toHaveBeenCalledWith(
        "hello world",
        [image],
      ),
    );
    // notice 保留草稿 + Retry / Discard
    expect(screen.getByText(/Send failed — draft kept/)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Retry send" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Discard draft" }),
    ).toBeInTheDocument();
  });

  it("Given the notice offers Discard, When it is clicked, Then clearDraft is invoked and the notice closes", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, agentId: 9 });
    appMocks.SendChatMessage.mockRejectedValue(new Error("boom"));
    render(<ChatPanel active sessionId={42} />);
    const onSubmit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((msg: unknown) => void)
      | undefined;

    act(() => {
      onSubmit?.({ text: "draft me" });
    });
    await waitFor(() =>
      expect(componentMocks.composerHandle.restoreDraft).toHaveBeenCalled(),
    );

    fireEvent.click(screen.getByRole("button", { name: "Discard draft" }));
    expect(componentMocks.composerHandle.clearDraft).toHaveBeenCalledOnce();
    await waitFor(() =>
      expect(screen.queryByText(/Send failed — draft kept/)).toBeNull(),
    );
  });

  it("Given the notice offers Retry, When it is clicked, Then the same message is re-sent", async () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, agentId: 9 });
    appMocks.SendChatMessage.mockRejectedValueOnce(new Error("first attempt"));
    render(<ChatPanel active sessionId={42} />);
    const onSubmit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((msg: unknown) => void)
      | undefined;

    act(() => {
      onSubmit?.({ text: "retry me" });
    });
    await waitFor(() =>
      expect(componentMocks.composerHandle.restoreDraft).toHaveBeenCalledWith(
        "retry me",
        [],
      ),
    );
    expect(appMocks.SendChatMessage).toHaveBeenCalledTimes(1);

    appMocks.SendChatMessage.mockResolvedValueOnce({
      assistantMessageId: 102,
      sessionId: 42,
      stream: "chat:42:102",
      userMessageId: 101,
    });
    fireEvent.click(screen.getByRole("button", { name: "Retry send" }));

    await waitFor(() =>
      expect(appMocks.SendChatMessage).toHaveBeenCalledTimes(2),
    );
    expect(appMocks.SendChatMessage.mock.calls[1]?.[0]).toMatchObject({
      agentId: 9,
      sessionId: 42,
      text: "retry me",
    });
    await waitFor(() =>
      expect(screen.queryByText(/Send failed — draft kept/)).toBeNull(),
    );
  });
});

// ─── R15b: 会话所在机器离线 ───────────────────────────────────────────────────
//
// 这一档从桌面端本地的 `SessionOfflineBanner` 换成了共享包的 `MachineOfflineBanner`
// （两端唯一都成立、也都各画过一份的一档）。此前两端说的不是同一件事：桌面端讲
// 「为什么不会自动换机器」，agentre-server 讲「历史还读得到、消息不会排队」——
// 同一个用户在两端遇到同一件事得到两种解释。正文因此取并集，住进包里。
//
// 桌面端在这里保留的只有「按下去往哪走」：就地开一条同项目同 Agent 的新会话。
describe("ChatPanel · R15b 会话所在机器离线", () => {
  function offlineSession() {
    return makeSession({
      id: 42,
      agentId: 7,
      projectId: 2,
      deviceID: "remote-7",
      deviceName: "Build box",
      online: false,
    });
  }

  it("Given 会话钉在一台离线的远端机器, When 渲染, Then 说的是包里那套并集文案", () => {
    resetStore();
    mockSessionStore.session = offlineSession();

    render(<ChatPanel sessionId={42} />);

    expect(screen.getByText("Build box is offline")).toBeInTheDocument();
    // 并集：server 那半（历史照常读 / 消息不排队）与桌面端那半（上下文在那台机器
    // 上、不会改派）。少哪一半都是回归。
    const body = screen.getByTestId("status-banner-body").textContent ?? "";
    expect(body).toContain("History still reads");
    expect(body).toContain("will not be reassigned");
  });

  it("Given 那张横幅, When 按下出口, Then 就地开一条同项目同 Agent 的新会话", async () => {
    resetStore();
    mockSessionStore.session = offlineSession();
    const openNewSession = vi.fn();
    useChatTabsStore.setState({ openNewSession });

    render(<ChatPanel sessionId={42} />);
    await userEvent.click(
      screen.getByRole("button", { name: "Start a new conversation" }),
    );

    expect(openNewSession).toHaveBeenCalledWith(2, 7, "");
  });

  it("Given 机器在线, When 渲染, Then 一个字都不出", () => {
    resetStore();
    mockSessionStore.session = makeSession({
      id: 42,
      deviceID: "remote-7",
      deviceName: "Build box",
      online: true,
    });

    render(<ChatPanel sessionId={42} />);

    expect(screen.queryByText("Build box is offline")).toBeNull();
  });
});

// ─── 2026-08-23 对话页外壳收口 · 头部 ─────────────────────────────────────────

describe("ChatPanel · 头部在四态都在位且恒高", () => {
  const newSessionAgent = {
    id: 7,
    name: "Eng",
    agentBackendId: 1,
    backendType: "claudecode",
  } as never;

  /** 渲染一次、取头部的 className、再卸载 —— 用来横比四种会话情形。 */
  function headerClassOf(ui: React.ReactElement): string {
    const { unmount } = render(ui);
    const cls = screen.getByTestId("chat-header").className;
    unmount();
    return cls;
  }

  it("Given 已有会话, When 渲染, Then 头部高度是写死的两行高、不再是 min-height", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });

    render(<ChatPanel sessionId={42} />);

    const header = screen.getByTestId("chat-header");
    expect(header.className).toMatch(/(^|\s)h-\[68px\](\s|$)/);
    expect(header.className).not.toMatch(/min-h-/);
  });

  it("Given 新建会话尚未首发 / 加载中 / 加载失败, When 渲染, Then 头部都在位且外壳类与已有会话一模一样", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });
    const loaded = headerClassOf(<ChatPanel sessionId={42} />);

    resetStore();
    const fresh = headerClassOf(
      <ChatPanel sessionId={0} newSessionAgent={newSessionAgent} />,
    );

    resetStore();
    mockSessionStore.loading = true;
    const loading = headerClassOf(<ChatPanel sessionId={42} />);

    resetStore();
    mockSessionStore.error = "boom";
    const failed = headerClassOf(<ChatPanel sessionId={42} />);

    expect(fresh).toBe(loaded);
    expect(loading).toBe(loaded);
    expect(failed).toBe(loaded);
  });

  it("Given 新建会话尚未首发, When 渲染, Then 标题位说这是一条还没开始的对话并带上 Agent 名", () => {
    resetStore();

    render(<ChatPanel sessionId={0} newSessionAgent={newSessionAgent} />);

    expect(
      screen.getByRole("heading", { level: 2, name: "New chat · Eng" }),
    ).toBeInTheDocument();
  });

  it("Given 长短两种标题, When 渲染, Then 头部外壳类不变（标题仍是两行，高度不跟着涨）", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, title: "短" });
    const short = headerClassOf(<ChatPanel sessionId={42} />);

    resetStore();
    const longTitle =
      "这是一个很长的 AI 对话标题，用来确认工具栏会尽量展示完整内容而不是过早省略";
    mockSessionStore.session = makeSession({ id: 42, title: longTitle });
    const long = headerClassOf(<ChatPanel sessionId={42} />);

    expect(long).toBe(short);
  });

  it("Given 会话标题, When 渲染, Then 它是页面上的一个二级标题元素", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, title: "Test session" });

    render(<ChatPanel sessionId={42} />);

    const heading = screen.getByRole("heading", {
      level: 2,
      name: "Test session",
    });
    expect(heading).toHaveClass("line-clamp-2");
  });

  it("Given 头部, When 渲染, Then role=toolbar 只装右侧控件、标题与 meta 不在其中", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, title: "Test session" });

    render(<ChatPanel sessionId={42} />);

    const toolbar = screen.getByRole("toolbar");
    expect(within(toolbar).queryByText("Test session")).toBeNull();
    expect(
      within(toolbar).getByRole("button", { name: "Context sidebar" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2 })).not.toBe(toolbar);
    expect(toolbar.contains(screen.getByRole("heading", { level: 2 }))).toBe(
      false,
    );
  });

  it("Given 这一轮停不下来, When 渲染, Then 不摆一个禁用的停止按钮", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, agentStatus: "idle" });

    render(<ChatPanel sessionId={42} />);

    expect(screen.queryByRole("button", { name: /Stop/ })).toBeNull();
  });

  it("Given 这一轮在跑, When 渲染, Then 停止按钮出现", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, agentStatus: "running" });

    render(<ChatPanel sessionId={42} />);

    expect(screen.getByRole("button", { name: /Stop/ })).toBeInTheDocument();
  });

  it("Given meta 行, When 渲染, Then 它恒为单行，不靠 flex-wrap 折行", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42, projectId: 2 });

    render(<ChatPanel sessionId={42} />);

    const meta = screen.getByTestId("chat-header-meta");
    expect(meta.className).not.toMatch(/flex-wrap/);
    // 窄档按固定顺序收：先机器名、再项目/分支 —— 两段各自带自己的分档类。
    expect(screen.getByTestId("chat-header-topline").className).toMatch(
      /@max-\[\d+px\]\/header:hidden/,
    );
  });
});

// ─── 2026-08-23 对话页外壳收口 · 转录列与输入带 ───────────────────────────────

describe("ChatPanel · 输入框与对话流同列、输入带边界跟随贴底", () => {
  it("Given 转录与输入框, When 渲染, Then 两者的左右内边距同一个值、内容让出同一条头像列并封顶同一测量宽度", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });

    const view = render(<ChatPanel sessionId={42} />);

    const transcript = view.container.querySelector("section");
    const band = screen.getByTestId("chat-composer-band");
    expect(transcript?.className).toMatch(/(^|\s)px-7(\s|$)/);
    expect(band.className).toMatch(/(^|\s)px-7(\s|$)/);

    const column = screen.getByTestId("chat-composer-column");
    // 让出 28px 头像 + gap-3 = 40px，再封顶 --container-measure —— 输入框的第一个
    // 字符与消息正文的第一个字符落在同一条竖线上。
    expect(column.className).toMatch(/(^|\s)ml-10(\s|$)/);
    expect(column.className).toMatch(/(^|\s)max-w-measure(\s|$)/);
  });

  it("Given 转录贴底, When 渲染, Then 末条消息与输入框之间只留一段间距 —— 转录不再另加底部内边距", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });

    const view = render(<ChatPanel sessionId={42} />);

    // 末行本身已带 pb-7 的消息间距；转录再叠 pb-6、输入带再叠 pt-3，
    // 贴底时会攒出 ~64px 的空档。底部内边距交还给末行，输入带只留落脚的一点。
    const transcript = view.container.querySelector("section");
    expect(transcript?.className).toMatch(/(^|\s)pt-6(\s|$)/);
    expect(transcript?.className).not.toMatch(/(^|\s)p[by]-\d/);

    const band = screen.getByTestId("chat-composer-band");
    expect(band.className).toMatch(/(^|\s)pt-2(\s|$)/);
  });

  it("Given 转录贴底, When 渲染, Then 输入带既没有分隔线也没有渐隐 —— 读作一整片", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });

    render(<ChatPanel sessionId={42} />);

    const band = screen.getByTestId("chat-composer-band");
    expect(band).toHaveAttribute("data-scrolled", "false");
    expect(band.className).not.toMatch(/border-t/);
    expect(screen.queryByTestId("chat-composer-band-fade")).toBeNull();
  });

  it("Given 用户上滚离开底部, When 渲染, Then 输入带顶部出现分隔线与一段不接收指针事件的向上渐隐", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });

    const view = render(<ChatPanel sessionId={42} />);
    scrollUpFromBottom(transcriptScroller(view.container));

    const band = screen.getByTestId("chat-composer-band");
    expect(band).toHaveAttribute("data-scrolled", "true");
    expect(band.className).toMatch(/(^|\s)border-t(\s|$)/);
    const fade = screen.getByTestId("chat-composer-band-fade");
    expect(fade.className).toMatch(/(^|\s)pointer-events-none(\s|$)/);
    expect(fade).toHaveAttribute("aria-hidden", "true");
  });

  it("Given 会话所在机器离线, When 渲染, Then 那条横幅也落在输入带的同一条列里", () => {
    resetStore();
    mockSessionStore.session = makeSession({
      id: 42,
      deviceID: "remote-7",
      deviceName: "Build box",
      online: false,
    });

    render(<ChatPanel sessionId={42} />);

    const column = screen.getByTestId("chat-composer-column");
    expect(column.contains(screen.getByTestId("status-banner-body"))).toBe(
      true,
    );
  });

  it("Given 不可对话 Agent 的新 tab, When 渲染, Then 那条引导块也落在输入带的同一条列里", () => {
    resetStore();

    render(
      <MemoryRouter>
        <ChatPanel
          sessionId={0}
          newSessionAgent={
            {
              id: 7,
              name: "Eng",
              agentBackendId: 1,
              backendType: "claudecode",
              chattable: false,
              blockReason: "no-backend",
            } as never
          }
        />
      </MemoryRouter>,
    );

    const column = screen.getByTestId("chat-composer-column");
    expect(column.contains(screen.getByTestId("new-session-guard"))).toBe(true);
  });

  it("Given 不可对话 Agent 的新 tab, When 渲染, Then 那条引导块与输入框左右对齐 —— 列已经给了内边距,它不再自己缩一圈", () => {
    resetStore();

    render(
      <MemoryRouter>
        <ChatPanel
          sessionId={0}
          newSessionAgent={
            {
              id: 7,
              name: "Eng",
              agentBackendId: 1,
              backendType: "claudecode",
              chattable: false,
              blockReason: "no-backend",
            } as never
          }
        />
      </MemoryRouter>,
    );

    // 这一条列的左右内边距由 chat-composer-band / chat-composer-column 一次给全;
    // 引导块再叠自己的 mx-*,就会比紧挨着它的输入框每边多缩一截(决策 5)。
    expect(screen.getByTestId("new-session-guard").className).not.toMatch(
      /(^|\s)mx-\d/,
    );
  });
});

// ─── 2026-08-23 对话页外壳收口 · 加载与失败 ───────────────────────────────────

describe("ChatPanel · 加载骨架与失败态改用共享呈现件", () => {
  it("Given 会话还在加载, When 渲染, Then 转录位是共享骨架，滚动带说自己 busy", () => {
    resetStore();
    mockSessionStore.loading = true;

    const view = render(<ChatPanel sessionId={42} />);

    expect(screen.getByTestId("transcript-skeleton")).toBeInTheDocument();
    expect(view.container.querySelector("section")).toHaveAttribute(
      "aria-busy",
      "true",
    );
  });

  it("Given 会话加载完了, When 渲染, Then 滚动带不再说 busy", () => {
    resetStore();
    mockSessionStore.session = makeSession({ id: 42 });

    const view = render(<ChatPanel sessionId={42} />);

    expect(view.container.querySelector("section")).toHaveAttribute(
      "aria-busy",
      "false",
    );
  });

  it("Given 会话加载失败, When 渲染, Then 那条告警来自共享包的 Alert，动作与详情都还在", () => {
    resetStore();
    mockSessionStore.error = "backend unavailable";

    render(<ChatPanel sessionId={42} />);

    const alert = screen.getByRole("alert");
    // 共享 Alert 的形状：destructive 变体 + grid 布局，不再是手搓的 border/bg 拼装。
    expect(alert.className).toMatch(/(^|\s)grid(\s|$)/);
    expect(alert.className).not.toMatch(/bg-destructive-soft/);
    expect(screen.getByText("backend unavailable")).toHaveAttribute(
      "data-selectable-text",
      "true",
    );
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Close session" }),
    ).toBeInTheDocument();
  });
});

// ─── 会话级思考力度控件（spec 2026-09-01，决策 6/9）────────────────────────────

describe("ChatPanel · 会话级思考力度控件（trailing 侧，决策 6/9）", () => {
  it("reasoning_effort 能力位为真：控件出现在计量器之后，比权限/供应商 pill 更靠右（trailing 侧）", async () => {
    resetStore();
    componentMocks.capsReasoningEffort = true;
    mockSessionStore.session = makeSession({
      id: 42,
      backendType: "claudecode",
    });

    const { container } = render(<ChatPanel sessionId={42} />);

    await screen.findByTestId("provider-pill");
    const pill = await screen.findByTestId("reasoning-effort-pill");
    expect(pill).toBeInTheDocument();

    // leadingControls(权限模式 + 供应商 pill)先于 trailingControls(配额计量器 +
    // 思考力度控件)插入 DOM——真实 ChatComposer 把 trailingControls 整体排在
    // 提交键之前(packages/agentre-ui/src/composer/chat-composer.tsx)，这里只能
    // 验证到"它属于 trailing 分组、且是分组里最靠右那个"。
    const order = Array.from(
      container.querySelectorAll<HTMLElement>("[data-testid]"),
    )
      .map((el) => el.getAttribute("data-testid"))
      .filter((id): id is string =>
        [
          "permission-mode-pill",
          "provider-pill",
          "quota-meter",
          "reasoning-effort-pill",
        ].includes(id ?? ""),
      );
    expect(order).toEqual([
      "permission-mode-pill",
      "provider-pill",
      "quota-meter",
      "reasoning-effort-pill",
    ]);
  });

  it("后端未声明 reasoning_effort 能力（openclaw 等）：整颗控件不渲染", async () => {
    resetStore();
    componentMocks.capsReasoningEffort = false;
    mockSessionStore.session = makeSession({ id: 42, backendType: "openclaw" });

    render(<ChatPanel sessionId={42} />);

    await screen.findByTestId("quota-meter");
    expect(
      screen.queryByTestId("reasoning-effort-pill"),
    ).not.toBeInTheDocument();
  });

  it("选定一档立即调用 SetChatSessionReasoningEffort", async () => {
    resetStore();
    componentMocks.capsReasoningEffort = true;
    mockSessionStore.session = makeSession({
      id: 42,
      backendType: "claudecode",
    });
    appMocks.SetChatSessionReasoningEffort.mockResolvedValue({
      reasoningEffort: "high",
      backendReasoningEffort: "",
    });

    render(<ChatPanel sessionId={42} />);
    const pill = await screen.findByTestId("reasoning-effort-pill");
    const user = userEvent.setup();
    await user.click(pill);
    await user.click(await screen.findByRole("option", { name: "high" }));

    await waitFor(() => {
      expect(appMocks.SetChatSessionReasoningEffort).toHaveBeenCalledWith({
        sessionId: 42,
        reasoningEffort: "high",
      });
    });
  });

  it("重开一条钉了档位的会话：控件水合到那一档，而不是「Default」", async () => {
    resetStore();
    componentMocks.capsReasoningEffort = true;
    mockSessionStore.session = makeSession({
      id: 42,
      backendType: "codex",
      reasoningEffort: "high",
      agentReasoningEffort: "medium",
    });

    render(<ChatPanel sessionId={42} />);

    const pill = await screen.findByTestId("reasoning-effort-pill");
    expect(pill).toHaveTextContent("high");
    expect(pill).not.toHaveTextContent("Default");
  });

  it("会话行为空：脸上显示后端配置的那一档（跟随后端配置）", async () => {
    resetStore();
    componentMocks.capsReasoningEffort = true;
    mockSessionStore.session = makeSession({
      id: 42,
      backendType: "codex",
      reasoningEffort: "",
      agentReasoningEffort: "medium",
    });

    render(<ChatPanel sessionId={42} />);

    const pill = await screen.findByTestId("reasoning-effort-pill");
    expect(pill).toHaveTextContent("medium");
  });

  it("草稿态选中的档位随首条消息透传给 Send，且不发切换 IPC", async () => {
    resetStore();
    componentMocks.capsReasoningEffort = true;
    mockSessionStore.session = null;
    appMocks.SendChatMessage.mockResolvedValue({
      assistantMessageId: 1001,
      sessionId: 42,
      stream: "chat:event:42:1001",
      userMessageId: 1000,
    });

    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "codex",
            llmProviderKey: "",
          } as never
        }
      />,
    );

    const pill = await screen.findByTestId("reasoning-effort-pill");
    const user = userEvent.setup();
    await user.click(pill);
    await user.click(await screen.findByRole("option", { name: "high" }));

    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;
    expect(submit).toBeDefined();
    act(() => submit?.("hello"));

    await waitFor(() => {
      expect(appMocks.SendChatMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          sessionId: 0,
          agentId: 7,
          reasoningEffort: "high",
        }),
      );
    });
    // 草稿态还没有会话行可写：档位是瞬态的，随首条消息一并落库。
    expect(appMocks.SetChatSessionReasoningEffort).not.toHaveBeenCalled();
  });

  it("草稿态派到另一台桌面端：PeerRunFresh 也带上选中的档位", async () => {
    resetStore();
    componentMocks.capsReasoningEffort = true;
    mockSessionStore.session = null;
    componentMocks.effectiveExecTarget = {
      kind: "desktop",
      deviceId: "sha256:peer-desktop",
      deviceName: "Peer Desktop",
    };
    appMocks.PeerRunFresh.mockResolvedValue({ sessionId: 42 });

    render(
      <ChatPanel
        sessionId={0}
        newSessionAgent={
          {
            id: 7,
            name: "Eng",
            agentBackendId: 1,
            backendType: "codex",
            llmProviderKey: "",
          } as never
        }
      />,
    );

    const pill = await screen.findByTestId("reasoning-effort-pill");
    const user = userEvent.setup();
    await user.click(pill);
    await user.click(await screen.findByRole("option", { name: "xhigh" }));

    const submit = componentMocks.chatComposerProps.at(-1)?.onSubmit as
      | ((text: string) => void)
      | undefined;
    act(() => submit?.("hello peer"));

    await waitFor(() => {
      expect(appMocks.PeerRunFresh).toHaveBeenCalledWith(
        expect.objectContaining({
          fingerprint: "sha256:peer-desktop",
          reasoningEffort: "xhigh",
        }),
      );
    });
    expect(appMocks.SendChatMessage).not.toHaveBeenCalled();
  });

  it("写库失败：控件回滚到上一档，重新打开弹层能看到原因", async () => {
    resetStore();
    componentMocks.capsReasoningEffort = true;
    mockSessionStore.session = makeSession({
      id: 42,
      backendType: "claudecode",
    });
    appMocks.SetChatSessionReasoningEffort.mockRejectedValue(
      new Error("db down"),
    );

    render(<ChatPanel sessionId={42} />);
    const pill = await screen.findByTestId("reasoning-effort-pill");
    const user = userEvent.setup();
    await user.click(pill);
    await user.click(await screen.findByRole("option", { name: "high" }));

    // 乐观值先短暂显示 high,写库失败后回滚回「Default」——会话行没被改写。
    await waitFor(() => {
      expect(pill).toHaveTextContent("Default");
    });

    // select() 一点即关弹层,失败原因要重新打开才看得到。
    await user.click(pill);
    expect(await screen.findByRole("alert")).toHaveTextContent("db down");
  });
});
