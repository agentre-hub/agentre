import { vi, type Mock } from "vitest";

type AnyFn = (...args: unknown[]) => unknown;
type LocalCommandScope = { deviceId: string; cwd: string };
type ResolveLocalCommandScopeRequest = {
  agentId: number;
  projectId: number;
  sessionId: number;
};
type TerminalRunCommandResponse = {
  scope: LocalCommandScope;
  startError?: string;
};

function windowBackedMock(
  name: string,
  fallback: AnyFn,
): ReturnType<typeof vi.fn> {
  const mock: ReturnType<typeof vi.fn> = vi.fn((...args: unknown[]) => {
    const fn = window.go?.app?.App?.[name];
    if (typeof fn === "function" && fn !== mock) return fn(...args);
    return fallback(...args);
  });
  return mock;
}

function typedWindowBackedMock<TArgs extends unknown[], TResult>(
  name: string,
  fallback: (...args: TArgs) => TResult,
): Mock<(...args: TArgs) => TResult> {
  const mock = vi.fn((...args: TArgs) => {
    const fn = window.go?.app?.App?.[name];
    if (typeof fn === "function" && fn !== (mock as unknown as typeof fn)) {
      return fn(...args) as TResult;
    }
    return fallback(...args);
  }) as Mock<(...args: TArgs) => TResult>;
  return mock;
}

function missingWailsBinding(name: string): Promise<never> {
  return Promise.reject(new Error(`Wails binding ${name} not available`));
}

export const Greet = vi.fn((name: string) =>
  Promise.resolve(`Hello ${name}, It's show time!`),
);

export const AnswerUserQuestion = vi.fn(() => Promise.resolve({}));
export const AnswerToolPermission = vi.fn(() => Promise.resolve({}));
export const AnswerToolApproval = vi.fn(() => Promise.resolve({}));
export const ResolvePlanAction = vi.fn(() => Promise.resolve({}));

export const Info = vi.fn(() =>
  Promise.resolve({
    version: "dev",
    commit: "dev",
    builtAt: "",
    runtimeMode: "interactive",
  }),
);

// LLM provider bindings — used by llm-providers.tsx and agent-backends.tsx.
// Tests override these via window.go.app.App.* or vi.mock at test level.
export const ListLLMProviders = windowBackedMock("ListLLMProviders", () =>
  Promise.resolve({ items: [] }),
);
export const CreateLLMProvider = windowBackedMock("CreateLLMProvider", () =>
  Promise.resolve({ item: { id: 1 } }),
);
export const UpdateLLMProvider = windowBackedMock("UpdateLLMProvider", () =>
  Promise.resolve({ item: { id: 1 } }),
);
export const DeleteLLMProvider = windowBackedMock("DeleteLLMProvider", () =>
  Promise.resolve({}),
);
export const ListLLMModels = windowBackedMock("ListLLMModels", () =>
  Promise.resolve({ items: [] }),
);
export const PreviewLLMModels = windowBackedMock("PreviewLLMModels", () =>
  Promise.resolve({ items: [] }),
);
export const TestLLMProvider = windowBackedMock("TestLLMProvider", () =>
  Promise.resolve({ ok: true, message: "", modelCount: 0 }),
);
export const LookupLLMModel = windowBackedMock("LookupLLMModel", () =>
  Promise.resolve({ known: false, vendor: "", contextWindow: 0, maxOutput: 0 }),
);

// Agent backend bindings
export const ListAgentBackends = windowBackedMock("ListAgentBackends", () =>
  Promise.resolve({ items: [] }),
);
export const ListAgentBackendCLIOverlays = windowBackedMock(
  "ListAgentBackendCLIOverlays",
  () => Promise.resolve({ items: [] }),
);
export const GetAgentBackendCLIOverlay = windowBackedMock(
  "GetAgentBackendCLIOverlay",
  () => Promise.resolve({ cliPath: "", status: "unchecked" }),
);
export const SetAgentBackendCLIOverlay = windowBackedMock(
  "SetAgentBackendCLIOverlay",
  () => Promise.resolve({ cliPath: "", status: "unchecked" }),
);
export const ListAgentExecTargetAvailability = windowBackedMock(
  "ListAgentExecTargetAvailability",
  () => Promise.resolve([]),
);
export const CreateAgentBackend = windowBackedMock("CreateAgentBackend", () =>
  Promise.resolve({ item: { id: 1 } }),
);
export const UpdateAgentBackend = windowBackedMock("UpdateAgentBackend", () =>
  Promise.resolve({ item: { id: 1 } }),
);
export const DeleteAgentBackend = windowBackedMock("DeleteAgentBackend", () =>
  Promise.resolve({}),
);
export const TestAgentBackend = windowBackedMock("TestAgentBackend", () =>
  Promise.resolve({ ok: true, latencyMs: 0, message: "" }),
);
export const CancelTestAgentBackend = windowBackedMock(
  "CancelTestAgentBackend",
  () => Promise.resolve({ canceled: true }),
);
export const ResolveAgentBackendCLIPath = windowBackedMock(
  "ResolveAgentBackendCLIPath",
  () => Promise.resolve({ path: "", found: false }),
);
export const ScanAndCreateAgentBackends = windowBackedMock(
  "ScanAndCreateAgentBackends",
  () => Promise.resolve({ results: [] }),
);
export const GetGatewayStatus = windowBackedMock("GetGatewayStatus", () =>
  Promise.resolve({ status: "stopped", listenURL: "", reason: "", routes: [] }),
);

// Organization bindings
export const LoadOrg = windowBackedMock("LoadOrg", () =>
  Promise.resolve({ departments: [], agents: [] }),
);
export const CreateDepartment = windowBackedMock("CreateDepartment", () =>
  Promise.resolve({ item: {} }),
);
export const UpdateDepartment = windowBackedMock("UpdateDepartment", () =>
  Promise.resolve({ item: {} }),
);
export const MoveDepartment = windowBackedMock("MoveDepartment", () =>
  Promise.resolve({ item: {} }),
);
export const DeleteDepartment = windowBackedMock("DeleteDepartment", () =>
  Promise.resolve({}),
);
export const CreateAgent = windowBackedMock("CreateAgent", () =>
  Promise.resolve({ item: {} }),
);
export const UpdateAgent = windowBackedMock("UpdateAgent", () =>
  Promise.resolve({ item: {} }),
);
export const MoveAgent = windowBackedMock("MoveAgent", () =>
  Promise.resolve({ item: {} }),
);
export const DeleteAgent = windowBackedMock("DeleteAgent", () =>
  Promise.resolve({}),
);
export const UploadAgentAvatar = windowBackedMock("UploadAgentAvatar", () =>
  Promise.resolve({ item: {} }),
);
export const DeleteAgentAvatar = windowBackedMock("DeleteAgentAvatar", () =>
  Promise.resolve({}),
);
export const ListAgentSkillPacks = windowBackedMock("ListAgentSkillPacks", () =>
  Promise.resolve({ packs: [] }),
);

// Chat and project bindings
export const ListChatAgents = windowBackedMock("ListChatAgents", () =>
  Promise.resolve({ agents: [] }),
);
export const ListChatAgentSessions = windowBackedMock(
  "ListChatAgentSessions",
  () => Promise.resolve({ sessions: [] }),
);
export const LoadChatSession = windowBackedMock("LoadChatSession", () =>
  Promise.resolve({ session: null, messages: [] }),
);
export const MarkChatSessionRead = windowBackedMock("MarkChatSessionRead", () =>
  Promise.resolve({}),
);
export const ResolveLocalCommandScope = typedWindowBackedMock<
  [request: ResolveLocalCommandScopeRequest],
  Promise<LocalCommandScope>
>("ResolveLocalCommandScope", () =>
  missingWailsBinding("ResolveLocalCommandScope"),
);
export const EnsureChatSession = typedWindowBackedMock<
  [agentId: number, projectId: number],
  Promise<number>
>("EnsureChatSession", () => Promise.resolve(0));
export const TerminalRunCommand = typedWindowBackedMock<
  [
    terminalId: string,
    sessionId: number,
    command: string,
    cols: number,
    rows: number,
  ],
  Promise<TerminalRunCommandResponse>
>("TerminalRunCommand", () => missingWailsBinding("TerminalRunCommand"));
export const ProjectListTree = windowBackedMock("ProjectListTree", () =>
  Promise.resolve([]),
);
export const ProjectGet = windowBackedMock("ProjectGet", () =>
  Promise.resolve({ item: null }),
);
export const ProjectListSessions = windowBackedMock("ProjectListSessions", () =>
  Promise.resolve([]),
);
export const ProjectLocationList = windowBackedMock("ProjectLocationList", () =>
  Promise.resolve([]),
);
export const ProjectCreate = windowBackedMock("ProjectCreate", () =>
  Promise.resolve({ item: { id: 1 } }),
);
export const ProjectUpdate = windowBackedMock("ProjectUpdate", () =>
  Promise.resolve({ item: { id: 1 } }),
);
export const ProjectDelete = windowBackedMock("ProjectDelete", () =>
  Promise.resolve({}),
);
export const ProjectReorder = windowBackedMock("ProjectReorder", () =>
  Promise.resolve({}),
);
export const ProjectAddMember = windowBackedMock("ProjectAddMember", () =>
  Promise.resolve({}),
);
export const ProjectRemoveMember = windowBackedMock("ProjectRemoveMember", () =>
  Promise.resolve({}),
);
export const ProjectMove = windowBackedMock("ProjectMove", () =>
  Promise.resolve({ item: { id: 1 } }),
);
export const ProjectSetLocalPath = windowBackedMock("ProjectSetLocalPath", () =>
  Promise.resolve({ id: 1 }),
);
export const ProjectMerge = windowBackedMock("ProjectMerge", () =>
  Promise.resolve({ id: 1 }),
);

// Issue bindings
const emptyIssue = {
  id: 0,
  title: "",
  stage: "todo",
  labels: [],
  assigneeAgentID: 0,
  agentBackendID: 0,
  llmProviderKey: "",
  llmModelKey: "",
};
export const IssueList = windowBackedMock("IssueList", () =>
  Promise.resolve({
    issues: [],
    stageCounts: {},
    stageTotals: {},
    projectCounts: [],
  }),
);
export const IssueListLabels = windowBackedMock("IssueListLabels", () =>
  Promise.resolve([]),
);
export const IssueGet = windowBackedMock("IssueGet", () =>
  Promise.resolve({ ...emptyIssue }),
);
export const IssueCreate = windowBackedMock("IssueCreate", () =>
  Promise.resolve({ ...emptyIssue }),
);
export const IssueUpdate = windowBackedMock("IssueUpdate", () =>
  Promise.resolve({ ...emptyIssue }),
);
export const IssueMove = windowBackedMock("IssueMove", () =>
  Promise.resolve({ ...emptyIssue }),
);
export const IssueDelete = windowBackedMock("IssueDelete", () =>
  Promise.resolve(undefined),
);
export const IssueCreateLabel = windowBackedMock("IssueCreateLabel", () =>
  Promise.resolve({ id: 0, name: "", tone: "gray", usageCount: 0 }),
);
export const IssueUpdateLabel = windowBackedMock("IssueUpdateLabel", () =>
  Promise.resolve({ id: 0, name: "", tone: "gray", usageCount: 0 }),
);
export const IssueDeleteLabel = windowBackedMock("IssueDeleteLabel", () =>
  Promise.resolve(undefined),
);

// 看板并入同步组的那条一次性说明：默认「不欠着」，要它出现的用例自己改返回。
export const SyncStatus = windowBackedMock("SyncStatus", () =>
  Promise.resolve({ enabled: false, boardJoinNoticePending: false }),
);
export const SyncAcknowledgeBoardJoinNotice = windowBackedMock(
  "SyncAcknowledgeBoardJoinNotice",
  () => Promise.resolve(undefined),
);

export const GetSessionCapabilities = windowBackedMock(
  "GetSessionCapabilities",
  () => Promise.resolve({ capabilities: [], permissionModeMeta: null }),
);
export const GetBackendCapabilities = windowBackedMock(
  "GetBackendCapabilities",
  () => Promise.resolve({ capabilities: [], permissionModeMeta: null }),
);
export const SetChatPermissionMode = windowBackedMock(
  "SetChatPermissionMode",
  () => Promise.resolve({}),
);

export const RemoteDeviceList = windowBackedMock("RemoteDeviceList", () =>
  Promise.resolve([]),
);
// R17 本机指纹判定:测试默认给一个非空指纹,用例可改返回以验证本机/他端两种分支。
export const RemoteDeviceFingerprint = windowBackedMock(
  "RemoteDeviceFingerprint",
  () => Promise.resolve("sha256:test-local-device"),
);
export const RemoteFsListDir = windowBackedMock("RemoteFsListDir", () =>
  Promise.resolve({ entries: [] }),
);
export const RemoteFsMkdir = windowBackedMock("RemoteFsMkdir", () =>
  Promise.resolve({}),
);

// Account login (internal/app/server.go) — used by RemoteDevicesPanel's
// login entry point / identity card via use-server-login.ts + login-dialog.tsx.
// Default is the fresh-install logged-out row; tests that exercise the login
// flow override these directly.
export const ServerGetState = windowBackedMock("ServerGetState", () =>
  Promise.resolve({
    ID: 1,
    ServerURL: "",
    DeviceID: 0,
    DeviceFingerprint: "",
    ServerUserID: 0,
    KeychainAccount: "",
    Updatetime: 0,
  }),
);
export const ServerCheckURL = windowBackedMock("ServerCheckURL", () =>
  Promise.resolve("dev"),
);
export const ServerStartLogin = windowBackedMock("ServerStartLogin", () =>
  missingWailsBinding("ServerStartLogin"),
);
export const ServerPollLoginToken = windowBackedMock(
  "ServerPollLoginToken",
  () => Promise.resolve(false),
);
export const ServerCancelLogin = windowBackedMock("ServerCancelLogin", () =>
  Promise.resolve(),
);
export const ServerLogout = windowBackedMock("ServerLogout", () =>
  Promise.resolve(),
);
// use-remote-devices.ts reads the account device list to decide which rows are
// 未认领. The default rejects like ServerStartLogin because ServerGetState's
// default is the logged-out row: a logged-out desktop cannot know the account
// list, and the hook treats that as known=false rather than marking every
// machine unclaimed.
export const ServerListDevices = windowBackedMock("ServerListDevices", () =>
  missingWailsBinding("ServerListDevices"),
);

// Data backup bindings
export const ExportData = windowBackedMock("ExportData", () =>
  Promise.resolve({ path: "", canceled: true, summary: {} }),
);
export const PreviewImportData = windowBackedMock("PreviewImportData", () =>
  Promise.resolve({
    format: "",
    version: 0,
    secretsIncluded: false,
    items: [],
  }),
);
export const ApplyImportData = windowBackedMock("ApplyImportData", () =>
  Promise.resolve({ counts: {} }),
);

// Quit confirmation — called when the user confirms quitting with active sessions.
export const ConfirmQuit = windowBackedMock("ConfirmQuit", () =>
  Promise.resolve(),
);

// Hook bindings（脚本驱动 Hooks 页）。页面改成直接 import 这些绑定之后，缺一条就是
// 「undefined 不是函数」——挂在 effect 里会把整页渲染打断，而不是像旧的
// window.go 桥那样被 getBridgeMethod 的 catch 吞掉。
export const LoadHooks = windowBackedMock("LoadHooks", () =>
  Promise.resolve({ hooks: [], events: [] }),
);
export const CreateHook = windowBackedMock("CreateHook", () =>
  Promise.resolve({ id: 0 }),
);
export const UpdateHook = windowBackedMock("UpdateHook", () =>
  Promise.resolve({ id: 0 }),
);
export const DeleteHook = windowBackedMock("DeleteHook", () =>
  Promise.resolve(),
);
export const ToggleHook = windowBackedMock("ToggleHook", () =>
  Promise.resolve({ id: 0 }),
);
export const RunHook = windowBackedMock("RunHook", () =>
  Promise.resolve({ events: [], newCount: 0, dupCount: 0, persisted: false }),
);
export const ProbeInterpreters = windowBackedMock("ProbeInterpreters", () =>
  Promise.resolve([]),
);

// File drop bindings
export const ChatReadDroppedImages = windowBackedMock(
  "ChatReadDroppedImages",
  () => Promise.resolve({ items: [] }),
);
