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
	want := wire.RunParams{
		Backend: backendJSON, AgentID: 3, ConversationID: convID(42), PeerFingerprint: "fp", Cwd: "/work", Title: "title",
		AgentSyncID: "01HXsync000000000000000000", ProjectSyncID: "01HXproj00000000000000000",
		UserText: "hello", UserBlocks: []blocks.StoredBlock{{Type: "image", Data: json.RawMessage{0, 1, 255}}},
		History:        []wire.HistoryMessageWire{{Role: "user", Blocks: []blocks.StoredBlock{{Type: "text", Data: json.RawMessage(`{"text":"hi"}`)}}}},
		MCPServers:     []agentruntime.MCPServerSpec{{Name: "org", URL: "http://local", Headers: map[string]string{"Authorization": "Bearer x"}, Tools: []string{"ask"}}},
		EnabledPlugins: map[string]bool{"skill": true}, SourceDevice: "browser", SourceDeviceName: "Chrome",
		// 本轮有效思考力度是**独立 run 参数**(spec 2026-09-01 决策 4),不塞在 backend
		// 负载里 —— 浏览器端发的负载只有一个 {type} 空壳。
		ReasoningEffort: "max",
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
