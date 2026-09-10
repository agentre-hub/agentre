package chat_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
)

// 本文件锁住 R15a「手动指定执行目标」的校验半边（validateExecTargetOverride）：
// 指定的档必须在 Agent 的执行目标列表里且此刻可用，拒绝指定一个不可用的档。
// 写回半边不新增写口——send() 把校验通过的 agentBackendID 塞进一个只填了
// ExecAgentBackendID 的探针 session 喂给 resolveAgentBackend（见 chat_test.go 里
// end-to-end 的 TestSend_NewSessionWithExecTargetOverride_* 系列），resolveAgentBackend
// 命中"已钉住"分支的行为已经被 exec_target_pin_test.go 的
// TestResolveAgentBackend_GivenSessionWithStickyPin_* 锁住，这里不重复。

func TestValidateExecTargetOverride_GivenTargetInListAndAvailable_ThenNoError(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)

	m.execTarget.EXPECT().ListByAgent(ctx, int64(601)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 601, AgentBackendID: 51, SortOrder: 0},
		{ID: 2, AgentID: 601, AgentBackendID: 52, SortOrder: 1},
	}, nil)
	// 手动指定排第二的 52（非默认第一档），必须能通过校验——不是只能选排最前的那个。
	m.backend.EXPECT().Find(ctx, int64(52)).Return(localClaudeCode(52), nil)

	err := svc.validateExecTargetOverride(ctx, 601, 0, 52)
	require.NoError(t, err)
}

func TestValidateExecTargetOverride_GivenTargetNotInAgentsList_ThenRejects(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)

	m.execTarget.EXPECT().ListByAgent(ctx, int64(602)).Return([]*agent_entity.AgentExecTarget{
		{ID: 3, AgentID: 602, AgentBackendID: 51, SortOrder: 0},
	}, nil)

	// 999 压根不在这个 Agent 的执行目标列表里：必须拒绝，不查 backend。
	err := svc.validateExecTargetOverride(ctx, 602, 0, 999)
	require.Error(t, err)
}

func TestValidateExecTargetOverride_GivenTargetCurrentlyUnavailable_ThenRejects(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)

	m.execTarget.EXPECT().ListByAgent(ctx, int64(603)).Return([]*agent_entity.AgentExecTarget{
		{ID: 4, AgentID: 603, AgentBackendID: 61, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(61)).Return(&agent_backend_entity.AgentBackend{
		ID: 61, Type: "totally-unknown-type",
	}, nil)

	err := svc.validateExecTargetOverride(ctx, 603, 0, 61)
	require.Error(t, err, "当前不可用的档必须被拒绝")
}

// TestLoadSession_PopulatesExecTargetCount_MultiTarget 用不带 chat_test.go 里
// setupChatTest 默认宽松桩的干净 mock 环境搭建——那条 AnyTimes() 空列表桩按
// gomock 注册顺序会拦掉这里想验证的精确期望（同一坑见上方注释），因此复用
// setupExecTargetPinTest 这套没有默认桩的 mock，自己补一个 message 仓储桩。
func TestLoadSession_PopulatesExecTargetCount_MultiTarget(t *testing.T) {
	ctx, m, svc := setupExecTargetPinTest(t)
	msgCtrl := gomock.NewController(t)
	t.Cleanup(msgCtrl.Finish)
	msgMock := mock_chat_repo.NewMockMessageRepo(msgCtrl)
	prevMessage := chat_repo.Message()
	chat_repo.RegisterMessage(msgMock)
	t.Cleanup(func() { chat_repo.RegisterMessage(prevMessage) })

	m.session.EXPECT().Find(ctx, int64(110)).Return(&chat_entity.Session{
		ID: 110, AgentID: 54, Status: 1,
	}, nil)
	m.agent.EXPECT().Find(ctx, int64(54)).Return(&agent_entity.Agent{
		ID: 54, Name: "多档", AgentBackendID: 81, Status: 1,
	}, nil)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(54)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 54, AgentBackendID: 81, SortOrder: 0},
		{ID: 2, AgentID: 54, AgentBackendID: 82, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(81)).Return(&agent_backend_entity.AgentBackend{
		ID: 81, Type: string(agent_backend_entity.TypeClaudeCode), Status: 1,
	}, nil)
	msgMock.EXPECT().ListMeta(ctx, int64(110)).Return(nil, nil)
	msgMock.EXPECT().FillBlocks(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	resp, err := svc.LoadSession(ctx, &LoadSessionRequest{SessionID: 110})
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Session.ExecTargetCount)
}
