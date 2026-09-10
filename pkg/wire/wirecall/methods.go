package wirecall

import "github.com/agentre-hub/agentre/pkg/wire/agentrewire"

// 本文件是配对表本身:一个方法一行。顺序按域分组,与 wire.proto 里 RpcMethod 的
// 分组一致。加一个 RPC 方法就在这里加一行 —— 漏了的话 wirecall 的完备性守卫判红。

// ── 握手与设备 ──────────────────────────────────────────────────

var AgentredSelfUpdate = Define[*agentrewire.AgentredSelfUpdateRequest](agentrewire.RpcMethod_RPC_METHOD_AGENTRED_SELF_UPDATE,
	func() *agentrewire.AgentredSelfUpdateResponse { return &agentrewire.AgentredSelfUpdateResponse{} })

var AuthAccount = Define[*agentrewire.AuthAccountRequest](agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT,
	func() *agentrewire.AuthAccountResponse { return &agentrewire.AuthAccountResponse{} })

var AuthConnect = Define[*agentrewire.AuthConnectRequest](agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT,
	func() *agentrewire.AuthConnectResponse { return &agentrewire.AuthConnectResponse{} })

var AuthPair = Define[*agentrewire.AuthPairRequest](agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR,
	func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })

var AuthRevoke = Define[*agentrewire.AuthRevokeRequest](agentrewire.RpcMethod_RPC_METHOD_AUTH_REVOKE,
	func() *agentrewire.AuthRevokeResponse { return &agentrewire.AuthRevokeResponse{} })

var HealthPing = Define[*agentrewire.HealthPingRequest](agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING,
	func() *agentrewire.HealthPingResponse { return &agentrewire.HealthPingResponse{} })

// ── 引擎与凭据 ──────────────────────────────────────────────────

var ClaudeCodeUsage = Define[*agentrewire.ClaudeCodeUsageRequest](agentrewire.RpcMethod_RPC_METHOD_CLAUDE_CODE_USAGE,
	func() *agentrewire.ClaudeCodeUsageResponse { return &agentrewire.ClaudeCodeUsageResponse{} })

var CLIProbe = Define[*agentrewire.CLIProbeRequest](agentrewire.RpcMethod_RPC_METHOD_CLI_PROBE,
	func() *agentrewire.CLIProbeResponse { return &agentrewire.CLIProbeResponse{} })

var CLIResolvePath = Define[*agentrewire.CLIResolvePathRequest](agentrewire.RpcMethod_RPC_METHOD_CLI_RESOLVE_PATH,
	func() *agentrewire.CLIResolvePathResponse { return &agentrewire.CLIResolvePathResponse{} })

var LLMDelete = Define[*agentrewire.LLMDeleteRequest](agentrewire.RpcMethod_RPC_METHOD_LLM_DELETE,
	func() *agentrewire.LLMDeleteResponse { return &agentrewire.LLMDeleteResponse{} })

var LLMList = Define[*agentrewire.LLMListRequest](agentrewire.RpcMethod_RPC_METHOD_LLM_LIST,
	func() *agentrewire.LLMListResponse { return &agentrewire.LLMListResponse{} })

var LLMUpsert = Define[*agentrewire.LLMUpsertRequest](agentrewire.RpcMethod_RPC_METHOD_LLM_UPSERT,
	func() *agentrewire.LLMUpsertResponse { return &agentrewire.LLMUpsertResponse{} })

var SkillCatalog = Define[*agentrewire.SkillCatalogRequest](agentrewire.RpcMethod_RPC_METHOD_SKILLS_CATALOG,
	func() *agentrewire.SkillCatalogResponse { return &agentrewire.SkillCatalogResponse{} })

var SkillsList = Define[*agentrewire.SkillsListRequest](agentrewire.RpcMethod_RPC_METHOD_SKILLS_LIST,
	func() *agentrewire.SkillsListResponse { return &agentrewire.SkillsListResponse{} })

// ── 项目与文件系统 ──────────────────────────────────────────────

var ProjectClearLocalPath = Define[*agentrewire.ProjectClearLocalPathRequest](agentrewire.RpcMethod_RPC_METHOD_PROJECT_CLEAR_LOCAL_PATH,
	func() *agentrewire.ProjectLocalPathResponse { return &agentrewire.ProjectLocalPathResponse{} })

var ProjectSetLocalPath = Define[*agentrewire.ProjectSetLocalPathRequest](agentrewire.RpcMethod_RPC_METHOD_PROJECT_SET_LOCAL_PATH,
	func() *agentrewire.ProjectLocalPathResponse { return &agentrewire.ProjectLocalPathResponse{} })

var RemoteFsListDir = Define[*agentrewire.RemoteFsListDirRequest](agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_LIST_DIR,
	func() *agentrewire.RemoteFsListDirResponse { return &agentrewire.RemoteFsListDirResponse{} })

var RemoteFsMkdir = Define[*agentrewire.RemoteFsMkdirRequest](agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_MKDIR,
	func() *agentrewire.RemoteFsMkdirResponse { return &agentrewire.RemoteFsMkdirResponse{} })

var WorkspaceFsGitBranches = Define[*agentrewire.WorkspaceFsGitBranchesRequest](agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_BRANCHES,
	func() *agentrewire.WorkspaceFsGitBranchesResponse {
		return &agentrewire.WorkspaceFsGitBranchesResponse{}
	})

var WorkspaceFsGitChanges = Define[*agentrewire.WorkspaceFsGitChangesRequest](agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_CHANGES,
	func() *agentrewire.WorkspaceFsGitChangesResponse { return &agentrewire.WorkspaceFsGitChangesResponse{} })

var WorkspaceFsGitFileContent = Define[*agentrewire.WorkspaceFsGitFileContentRequest](agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_FILE_CONTENT,
	func() *agentrewire.WorkspaceFsGitFileContentResponse {
		return &agentrewire.WorkspaceFsGitFileContentResponse{}
	})

var WorkspaceFsGitState = Define[*agentrewire.WorkspaceFsGitStateRequest](agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_STATE,
	func() *agentrewire.WorkspaceFsGitStateResponse { return &agentrewire.WorkspaceFsGitStateResponse{} })

var WorkspaceFsListDir = Define[*agentrewire.WorkspaceFsListDirRequest](agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_LIST_DIR,
	func() *agentrewire.WorkspaceFsListDirResponse { return &agentrewire.WorkspaceFsListDirResponse{} })

var WorkspaceFsReadFile = Define[*agentrewire.WorkspaceFsReadFileRequest](agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE,
	func() *agentrewire.WorkspaceFsReadFileResponse { return &agentrewire.WorkspaceFsReadFileResponse{} })

var WorkspaceFsSearchFiles = Define[*agentrewire.WorkspaceFsSearchFilesRequest](agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_SEARCH_FILES,
	func() *agentrewire.WorkspaceFsSearchFilesResponse {
		return &agentrewire.WorkspaceFsSearchFilesResponse{}
	})

// ── 终端 ────────────────────────────────────────────────────────

var TerminalClose = Define[*agentrewire.TerminalCloseRequest](agentrewire.RpcMethod_RPC_METHOD_TERMINAL_CLOSE,
	func() *agentrewire.Empty { return &agentrewire.Empty{} })

var TerminalOpen = Define[*agentrewire.TerminalOpenRequest](agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN,
	func() *agentrewire.TerminalOpenResponse { return &agentrewire.TerminalOpenResponse{} })

var TerminalResize = Define[*agentrewire.TerminalResizeRequest](agentrewire.RpcMethod_RPC_METHOD_TERMINAL_RESIZE,
	func() *agentrewire.Empty { return &agentrewire.Empty{} })

var TerminalWrite = Define[*agentrewire.TerminalWriteRequest](agentrewire.RpcMethod_RPC_METHOD_TERMINAL_WRITE,
	func() *agentrewire.Empty { return &agentrewire.Empty{} })

// ── 会话 ────────────────────────────────────────────────────────

var SessionAttach = Define[*agentrewire.SessionAttachRequest](agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH,
	func() *agentrewire.SessionAttachResponse { return &agentrewire.SessionAttachResponse{} })

var SessionCounts = Define[*agentrewire.SessionCountsRequest](agentrewire.RpcMethod_RPC_METHOD_SESSION_COUNTS,
	func() *agentrewire.SessionCountsResponse { return &agentrewire.SessionCountsResponse{} })

var SessionDelete = Define[*agentrewire.SessionDeleteRequest](agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE,
	func() *agentrewire.SessionDeleteResponse { return &agentrewire.SessionDeleteResponse{} })

var SessionList = Define[*agentrewire.SessionListRequest](agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST,
	func() *agentrewire.SessionListResponse { return &agentrewire.SessionListResponse{} })

var SessionPendingWaiters = Define[*agentrewire.SessionPendingWaitersRequest](agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS,
	func() *agentrewire.SessionPendingWaitersResponse { return &agentrewire.SessionPendingWaitersResponse{} })

var SessionPull = Define[*agentrewire.SessionPullRequest](agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL,
	func() *agentrewire.SessionPullResponse { return &agentrewire.SessionPullResponse{} })

// ── 运行时 ──────────────────────────────────────────────────────

var RuntimeAbort = Define[*agentrewire.RuntimeAbortRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT,
	func() *agentrewire.RuntimeAbortResponse { return &agentrewire.RuntimeAbortResponse{} })

var RuntimeCancelSteer = Define[*agentrewire.RuntimeCancelSteerRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CANCEL_STEER,
	func() *agentrewire.RuntimeCancelSteerResponse { return &agentrewire.RuntimeCancelSteerResponse{} })

var RuntimeCapabilities = Define[*agentrewire.RuntimeCapabilitiesRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES,
	func() *agentrewire.RuntimeCapabilitiesResponse { return &agentrewire.RuntimeCapabilitiesResponse{} })

var RuntimeDrainPending = Define[*agentrewire.RuntimeDrainPendingRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_DRAIN_PENDING,
	func() *agentrewire.RuntimeDrainPendingResponse { return &agentrewire.RuntimeDrainPendingResponse{} })

var RuntimeGoalClear = Define[*agentrewire.RuntimeGoalRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_CLEAR,
	func() *agentrewire.RuntimeGoalClearResponse { return &agentrewire.RuntimeGoalClearResponse{} })

var RuntimeGoalGet = Define[*agentrewire.RuntimeGoalRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_GET,
	func() *agentrewire.RuntimeGoalResponse { return &agentrewire.RuntimeGoalResponse{} })

var RuntimeGoalSet = Define[*agentrewire.RuntimeGoalRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_SET,
	func() *agentrewire.RuntimeGoalResponse { return &agentrewire.RuntimeGoalResponse{} })

var RuntimeRun = Define[*agentrewire.RuntimeRunRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN,
	func() *agentrewire.RuntimeRunResponse { return &agentrewire.RuntimeRunResponse{} })

var RuntimeSetPermissionMode = Define[*agentrewire.RuntimeSetPermissionModeRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE,
	func() *agentrewire.Empty { return &agentrewire.Empty{} })

var RuntimeSteer = Define[*agentrewire.RuntimeSteerRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER,
	func() *agentrewire.Empty { return &agentrewire.Empty{} })

var RuntimeStopBackgroundTask = Define[*agentrewire.RuntimeStopBackgroundTaskRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STOP_BACKGROUND_TASK,
	func() *agentrewire.Empty { return &agentrewire.Empty{} })

var RuntimeSubmitAnswer = Define[*agentrewire.RuntimeSubmitAnswerRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER,
	func() *agentrewire.PeerSessionControlResponse { return &agentrewire.PeerSessionControlResponse{} })

var RuntimeSubmitToolPermission = Define[*agentrewire.RuntimeSubmitToolPermissionRequest](agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION,
	func() *agentrewire.PeerSessionControlResponse { return &agentrewire.PeerSessionControlResponse{} })

// ── 转录导入与统计 ──────────────────────────────────────────────

var ActivityRollup = Define[*agentrewire.ActivityRollupRequest](agentrewire.RpcMethod_RPC_METHOD_ACTIVITY_ROLLUP,
	func() *agentrewire.ActivityRollupResponse { return &agentrewire.ActivityRollupResponse{} })

var TranscriptImportExecute = Define[*agentrewire.TranscriptImportExecuteRequest](agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_EXECUTE,
	func() *agentrewire.TranscriptImportExecuteResponse {
		return &agentrewire.TranscriptImportExecuteResponse{}
	})

var TranscriptImportOpen = Define[*agentrewire.TranscriptImportOpenRequest](agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_OPEN,
	func() *agentrewire.TranscriptImportOpenResponse { return &agentrewire.TranscriptImportOpenResponse{} })

var TranscriptImportScan = Define[*agentrewire.TranscriptImportScanRequest](agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_SCAN,
	func() *agentrewire.TranscriptImportScanResponse { return &agentrewire.TranscriptImportScanResponse{} })

var TranscriptImportTurns = Define[*agentrewire.TranscriptImportTurnsRequest](agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_TURNS,
	func() *agentrewire.TranscriptImportTurnsResponse { return &agentrewire.TranscriptImportTurnsResponse{} })

// ── 其它 ────────────────────────────────────────────────────────

var EngineDiscover = Define[*agentrewire.EngineDiscoverRequest](agentrewire.RpcMethod_RPC_METHOD_ENGINE_DISCOVER,
	func() *agentrewire.EngineDiscoverResponse { return &agentrewire.EngineDiscoverResponse{} })

var EngineScan = Define[*agentrewire.EngineScanRequest](agentrewire.RpcMethod_RPC_METHOD_ENGINE_SCAN,
	func() *agentrewire.EngineScanResponse { return &agentrewire.EngineScanResponse{} })

var EngineTest = Define[*agentrewire.EngineTestRequest](agentrewire.RpcMethod_RPC_METHOD_ENGINE_TEST,
	func() *agentrewire.EngineTestResponse { return &agentrewire.EngineTestResponse{} })

var MCPProxy = Define[*agentrewire.MCPProxyRequest](agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY,
	func() *agentrewire.MCPProxyResponse { return &agentrewire.MCPProxyResponse{} })

var SetModelTarget = Define[*agentrewire.SetModelTargetRequest](agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET,
	func() *agentrewire.SetModelTargetResponse { return &agentrewire.SetModelTargetResponse{} })

var SetSessionReasoningEffort = Define[*agentrewire.SetSessionReasoningEffortRequest](agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_REASONING_EFFORT,
	func() *agentrewire.SetSessionReasoningEffortResponse {
		return &agentrewire.SetSessionReasoningEffortResponse{}
	})
