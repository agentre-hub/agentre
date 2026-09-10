package protowire

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// TestGoalRequestFromWire_CarriesAgentSyncID 钉死账号级同步标识落到执行侧。
//
// agentred 上的 codex 在 cwd 为空时按 ResolveAgentCwd 取兜底工作目录：有同步标识就
// 用 agents/sync-<syncID>/，没有才回落本地主键。这一格在 wire → GoalRequest 的转换
// 里漏掉，标识就到不了 runtime——跨机的 goal 退回按发起端主键取目录（两台桌面端同号
// 即共用目录），web 发起的会话连主键都没有，直接报 "AgentCwd needs agentID > 0"。
func TestGoalRequestFromWire_CarriesAgentSyncID(t *testing.T) {
	got, err := GoalRequestToDomain(&agentrewire.RuntimeGoalRequest{
		ConversationId: "11111111-1111-7111-8111-111111111111",
		AgentSyncId:    "01KZNE7YKJQ6A79YVDCMW1A63R",
	})
	require.NoError(t, err)
	require.Equal(t, "01KZNE7YKJQ6A79YVDCMW1A63R", got.AgentSyncID)
}
