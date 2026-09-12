package issue_entity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueCheck(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, (&Issue{Title: "ok", State: StateOpen}).Check(ctx))

	assert.Error(t, (&Issue{Title: "  ", State: StateOpen}).Check(ctx))
	assert.Error(t, (&Issue{Title: "ok", State: "weird"}).Check(ctx))
}

func TestIssueCloseReopen(t *testing.T) {
	i := &Issue{Title: "x", State: StateOpen}
	i.Close(1234)
	assert.Equal(t, StateClosed, i.State)
	assert.Equal(t, int64(1234), i.ClosedAt)
	assert.True(t, i.IsClosed())

	i.Reopen()
	assert.Equal(t, StateOpen, i.State)
	assert.Equal(t, int64(0), i.ClosedAt)
	assert.True(t, i.IsOpen())
}

func TestIssueSetStage_DoneClosesAndReopens(t *testing.T) {
	i := &Issue{State: StateOpen, Stage: StageTodo}

	i.SetStage(StageDone, 1234)
	assert.Equal(t, StageDone, i.Stage)
	assert.Equal(t, StateClosed, i.State)
	assert.Equal(t, int64(1234), i.ClosedAt)

	i.SetStage(StageDoing, 5678)
	assert.Equal(t, StageDoing, i.Stage)
	assert.Equal(t, StateOpen, i.State)
	assert.Equal(t, int64(0), i.ClosedAt)
}

func TestIssueCheck_RejectsUnknownStage(t *testing.T) {
	i := &Issue{Title: "x", State: StateOpen, Stage: "bogus"}
	assert.Error(t, i.Check(context.Background()))
}

func TestIssueCheck_EmptyStageDefaultsValid(t *testing.T) {
	i := &Issue{Title: "x", State: StateOpen, Stage: ""}
	assert.NoError(t, i.Check(context.Background()))
}

// TestIssueCarriesExecutionAssignment 执行归属三个字段(机器 / 供应商 / 模型)与既有的
// assignee_agent_id 一起随任务保存。本轮没有任何路径**读**它们(不会因此启动执行),
// 但它们必须在实体上存在并往返,否则表单存了等于没存。
func TestIssueCarriesExecutionAssignment(t *testing.T) {
	i := &Issue{
		Title: "x", State: StateOpen,
		AssigneeAgentID: 3, AgentBackendID: 7,
		LLMProviderKey: "openai", LLMModelKey: "gpt-5",
	}
	assert.NoError(t, i.Check(context.Background()))
	assert.Equal(t, int64(7), i.AgentBackendID)
	assert.Equal(t, "openai", i.LLMProviderKey)
	assert.Equal(t, "gpt-5", i.LLMModelKey)
}

// TestIssueCarriesSyncMeta 任务并入账号级同步组:整行带同步元数据。
func TestIssueCarriesSyncMeta(t *testing.T) {
	i := &Issue{Title: "x", State: StateOpen}
	i.EnsureSyncID()
	assert.NotEmpty(t, i.SyncID)
	assert.False(t, i.IsClaimed())
}
