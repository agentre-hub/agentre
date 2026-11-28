package protowire

import (
	"encoding/json"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

func TestRunParamsProtobufDomainRoundTrip(t *testing.T) {
	backend := agent_backend_entity.AgentBackend{ID: 7, Type: "claudecode", Name: "remote", CLIPath: "/bin/claude", EnvJSON: `{"A":"B"}`, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-backend", SyncVersion: 9}}
	backendJSON, err := json.Marshal(backend)
	require.NoError(t, err)
	want := wire.RunParams{ //nolint:gosec // G101: DesktopCtlToken is a credential-shaped round-trip fixture, not a real token.
		Backend: backendJSON, AgentID: 3, ConversationID: convID(42), PeerFingerprint: "fp", Cwd: "/work", Title: "title",
		AgentSyncID: "01HXsync000000000000000000", ProjectSyncID: "01HXproj00000000000000000",
		UserText: "hello", UserBlocks: []blocks.StoredBlock{{Type: "image", Data: json.RawMessage{0, 1, 255}}},
		History:        []wire.HistoryMessageWire{{Role: "user", Blocks: []blocks.StoredBlock{{Type: "text", Data: json.RawMessage(`{"text":"hi"}`)}}}},
		MCPServers:     []agentruntime.MCPServerSpec{{Name: "org", URL: "http://local", Headers: map[string]string{"Authorization": "Bearer x"}, Tools: []string{"ask"}}},
		EnabledPlugins: map[string]bool{"skill": true}, SourceDevice: "browser", SourceDeviceName: "Chrome",
		// 本轮有效思考力度是**独立 run 参数**(spec 2026-09-01 决策 4),不塞在 backend
		// 负载里 —— 浏览器端发的负载只有一个 {type} 空壳。
		ReasoningEffort: "max",
		// 桌面端签给本会话的 agrctl 会话级 token 与它绑定的桌面会话 id(spec 2026-09-22)。
		DesktopCtlToken: "ctl-session-token", DesktopSessionID: 55,
	}
	pb, err := RunRequestToProto(want)
	require.NoError(t, err)
	require.Equal(t, "max", pb.GetReasoningEffort())
	got, err := RunRequestFromProto(pb)
	require.NoError(t, err)
	require.JSONEq(t, string(want.Backend), string(got.Backend))
	want.Backend, got.Backend = nil, nil
	require.Equal(t, want, got)
}

// Given 这一轮跑在引擎配置上（RunParams 点名了配置 ID，Backend 是配置摊成的视图，带着这台
// 目标设备上这种运行时的 CLI 路径），When 翻上线，Then runtime.run 带上引擎配置（设置与
// 配置自己的同步身份，不带设备）与单列的 cli_path；旧的 backend 那一格照旧填着（协议切换前
// 只认它的执行端照常能跑）；翻回来 EngineProfileID 不丢。
func TestRunParamsProtobufCarriesEngineProfile(t *testing.T) {
	view := agent_backend_entity.AgentBackend{
		Type: "codex", Name: "Codex", LLMProviderKey: "prov-x", LLMModelKey: "model-x",
		ReasoningEffort: "high", Sandbox: "workspace-write", Approval: "on-request", EnvJSON: `{"A":"B"}`,
		DefaultModel: "gpt-x", DeviceFingerprint: "sha256:device-d", CLIPath: "/opt/codex",
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "profile-31"},
	}
	backendJSON, err := json.Marshal(view)
	require.NoError(t, err)

	pb, err := RunRequestToProto(wire.RunParams{Backend: backendJSON, ConversationID: convID(42), EngineProfileID: 31})
	require.NoError(t, err)
	engine := pb.GetEngineProfile()
	require.NotNil(t, engine, "跑在引擎配置上的一轮必须带引擎配置")
	require.Equal(t, int64(31), engine.GetId())
	require.Equal(t, "profile-31", engine.GetSyncId())
	require.Equal(t, "codex", engine.GetType())
	require.Equal(t, "prov-x", engine.GetLlmProviderKey())
	require.Equal(t, "model-x", engine.GetLlmModelKey())
	require.Equal(t, "high", engine.GetReasoningEffort())
	require.Equal(t, "workspace-write", engine.GetSandbox())
	require.Equal(t, "on-request", engine.GetApproval())
	require.Equal(t, `{"A":"B"}`, engine.GetEnvJson())
	require.Equal(t, "gpt-x", engine.GetDefaultModel())
	require.Equal(t, "/opt/codex", pb.GetCliPath())
	require.NotNil(t, pb.GetBackend(), "旧 backend 那一格在协议切换前照旧填着")
	require.Equal(t, "profile-31", pb.GetBackend().GetSyncId())

	got, err := RunRequestFromProto(pb)
	require.NoError(t, err)
	require.Equal(t, int64(31), got.EngineProfileID)

	// 执行端按引擎配置建这一轮的后端：设置与同步身份取自配置，CLI 路径取单列的那一格，
	// 不带设备（它就在这台设备上），也不是 agent_backends 的一行。
	be := RunBackendFromProto(pb)
	require.NotNil(t, be)
	require.Equal(t, "profile-31", be.SyncID)
	require.Equal(t, "model-x", be.LLMModelKey)
	require.Equal(t, "high", be.ReasoningEffort)
	require.Equal(t, "/opt/codex", be.CLIPath)
	require.Empty(t, be.DeviceFingerprint)
	require.Zero(t, be.ID)
}

// Given 只带 backend、不带引擎配置的 runtime.run（浏览器派发 / 钉在旧后端行上的会话），Then
// 执行端照旧按 backend 建这一轮。
func TestRunBackendFromProtoFallsBackToBackend(t *testing.T) {
	backend := agent_backend_entity.AgentBackend{ID: 7, Type: "claudecode", CLIPath: "/bin/claude", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-backend"}}
	backendJSON, err := json.Marshal(backend)
	require.NoError(t, err)
	pb, err := RunRequestToProto(wire.RunParams{Backend: backendJSON, ConversationID: convID(42)})
	require.NoError(t, err)
	require.Nil(t, pb.GetEngineProfile())

	be := RunBackendFromProto(pb)
	require.NotNil(t, be)
	require.Equal(t, int64(7), be.ID)
	require.Equal(t, "sync-backend", be.SyncID)
	require.Equal(t, "/bin/claude", be.CLIPath)
	require.Nil(t, RunBackendFromProto(nil))
}

func TestGoalParamsProtobufPreservesOptionalZeroValues(t *testing.T) {
	empty, zero := "", 0
	want := wire.GoalParams{ConversationID: convID(42), Objective: &empty, Status: &empty, TokenBudget: &zero}
	pb, err := GoalRequestToProto(want)
	require.NoError(t, err)
	got, err := GoalRequestFromProto(pb)
	require.NoError(t, err)
	require.NotNil(t, got.Objective)
	require.NotNil(t, got.Status)
	require.NotNil(t, got.TokenBudget)
	require.Equal(t, want, got)
}

// TestGoalParamsProtobufCarriesAgentSyncID 钉死账号级同步标识过 protobuf。
// 它是对端在 cwd 为空时命名 Agent 兜底工作目录的依据；漏传一格，跨机的 goal 就
// 退回按发起端本地主键取目录（两台桌面端同号即共用目录），web 发起的会话更是连
// 本地主键都没有，直接失败。
func TestGoalParamsProtobufCarriesAgentSyncID(t *testing.T) {
	want := wire.GoalParams{ConversationID: convID(42), AgentSyncID: "01KZNE7YKJQ6A79YVDCMW1A63R"}
	pb, err := GoalRequestToProto(want)
	require.NoError(t, err)
	got, err := GoalRequestFromProto(pb)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestBackendProtoRoundTripPreservesACPLaunchFields acp 后端的启动命令与附加 argv
// 必须原样过 protobuf：daemon 只认 BackendFromProto 解出的那一行实体（handlers/
// runtime.go），这里漏一格，远端派发当场报 "backend has no acpCommand configured"。
// 非 acp 后端往返后仍是零值，不凭空造出空 argv。
func TestBackendProtoRoundTripPreservesACPLaunchFields(t *testing.T) {
	acp := agent_backend_entity.AgentBackend{
		ID: 11, Type: "acp", Name: "remote gemini",
		ACPCommand: "npx", ACPArgs: []string{"-y", "@agentclientprotocol/codex-acp"},
	}
	landed := BackendFromProto(BackendToProto(&acp))
	require.NotNil(t, landed)
	require.Equal(t, "npx", landed.ACPCommand)
	require.Equal(t, []string{"-y", "@agentclientprotocol/codex-acp"}, landed.ACPArgs)

	other := agent_backend_entity.AgentBackend{ID: 12, Type: "claudecode", Name: "no acp"}
	zeroed := BackendFromProto(BackendToProto(&other))
	require.NotNil(t, zeroed)
	require.Empty(t, zeroed.ACPCommand)
	require.Empty(t, zeroed.ACPArgs)
}

// Given 发起端派到另一台桌面端的新会话点名了「这次改用」的引擎配置（只知道它的账号级同步
// 身份：本地 ID 在两台机器上不同），When 翻上线再翻回来，Then 同步身份不丢；没点名时为空。
func TestRunParamsProtobufCarriesEngineProfileSyncID(t *testing.T) {
	pb, err := RunRequestToProto(wire.RunParams{ConversationID: convID(42), EngineProfileSyncID: "profile-sync-31"})
	require.NoError(t, err)
	require.Equal(t, "profile-sync-31", pb.GetEngineProfileSyncId())
	require.Nil(t, pb.GetEngineProfile(), "只点名、不带配置内容：不能冒充执行端按配置建后端的那份引擎配置")

	got, err := RunRequestFromProto(pb)
	require.NoError(t, err)
	require.Equal(t, "profile-sync-31", got.EngineProfileSyncID)

	pb, err = RunRequestToProto(wire.RunParams{ConversationID: convID(42)})
	require.NoError(t, err)
	require.Empty(t, pb.GetEngineProfileSyncId())
}
