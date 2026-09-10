package chat_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// 本文件锁住 R15 的一条：全部不可用时逐档给出的说明是**用户可见文案**，必须走
// internal/pkg/code 的两份语言包，而不是在 chat_svc 里手写中文字面量。判据是
// 「换成 en 之后文案跟着变」——只要还是硬编码中文，zh-cn 那一遍会碰巧过、en 那
// 一遍必挂。

// pickAllUnavailable 跑一遍「两档全不可用」的挑选：第一档是没配对的 agentred，
// 第二档是本机档但项目在本机没配路径（R15 第二、第三项判据各一个）。
func pickAllUnavailable(t *testing.T, lang string) (*chat_svc.ExecTargetNoneAvailableError, string, context.Context) {
	t.Helper()
	base, m, svc := setupPickExecTargetTest(t)
	ctx := i18n.WithLanguage(base, lang)

	m.execTarget.EXPECT().ListByAgent(ctx, int64(70)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 70, AgentBackendID: 701, SortOrder: 0},
		{ID: 2, AgentID: 70, AgentBackendID: 702, SortOrder: 1},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(701)).Return(&agent_backend_entity.AgentBackend{
		ID: 701, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: pickTestFingerprint(77),
	}, nil)
	m.pairedDevices()
	m.backend.EXPECT().Find(ctx, int64(702)).Return(&agent_backend_entity.AgentBackend{
		ID: 702, Type: string(agent_backend_entity.TypeClaudeCode),
	}, nil)
	m.project.EXPECT().Find(ctx, int64(700)).Return(&project_entity.Project{
		ID: 700, LocalPathMissing: true,
	}, nil)

	_, err := svc.PickExecTarget(ctx, 70, 700)
	require.Error(t, err)
	var noneErr *chat_svc.ExecTargetNoneAvailableError
	require.True(t, errors.As(err, &noneErr))
	return noneErr, err.Error(), ctx
}

func TestPickExecTarget_GivenAllUnavailable_ThenHintsComeFromTheCodeTable(t *testing.T) {
	zhErr, zhText, zhCtx := pickAllUnavailable(t, "zh-cn")
	enErr, enText, enCtx := pickAllUnavailable(t, "en")

	require.Len(t, zhErr.Reasons, 2)
	require.Len(t, enErr.Reasons, 2)

	// 逐档 Hint 必须逐字等于语言包里的那一条。
	assert.Equal(t, i18n.T(zhCtx, code.ChatExecTargetHintUnpaired), zhErr.Reasons[0].Hint)
	assert.Equal(t, i18n.T(enCtx, code.ChatExecTargetHintUnpaired), enErr.Reasons[0].Hint)
	assert.Equal(t, i18n.T(zhCtx, code.ChatExecTargetHintLocalPathMissing), zhErr.Reasons[1].Hint)
	assert.Equal(t, i18n.T(enCtx, code.ChatExecTargetHintLocalPathMissing), enErr.Reasons[1].Hint)

	// 两份语言真的不同 —— 否则「查表」和「写死中文」不可分辨。
	assert.NotEqual(t, zhErr.Reasons[0].Hint, enErr.Reasons[0].Hint)
	assert.NotEqual(t, zhErr.Reasons[1].Hint, enErr.Reasons[1].Hint)

	// Wails 边界只过 Error() 字符串：表头、机器名与行格式同样得跟着语言走。
	assert.Contains(t, enText, i18n.T(enCtx, code.ChatAgentNoAvailableExecTarget))
	assert.Contains(t, enText, i18n.T(enCtx, code.ChatExecTargetDeviceRemote, pickTestFingerprint(77)))
	assert.Contains(t, enText, i18n.T(enCtx, code.ChatExecTargetDeviceLocal))
	assert.Contains(t, enText, i18n.T(enCtx, code.ChatExecTargetLineFormat,
		1, int64(701), i18n.T(enCtx, code.ChatExecTargetDeviceRemote, pickTestFingerprint(77)), enErr.Reasons[0].Hint))
	assert.Contains(t, zhText, i18n.T(zhCtx, code.ChatExecTargetDeviceLocal))
	assert.NotContains(t, enText, i18n.T(zhCtx, code.ChatExecTargetDeviceLocal))
}

// blockReasonForBackend 的文案也是同一批用户可见正文（它同时喂 ListAgents 的
// ChattableHint 与 PickExecTarget 的逐档说明），一并锁住。
func TestPickExecTarget_GivenProviderMissing_ThenHintComesFromTheCodeTable(t *testing.T) {
	run := func(lang string) (string, context.Context) {
		base, m, svc := setupPickExecTargetTest(t)
		ctx := i18n.WithLanguage(base, lang)
		m.execTarget.EXPECT().ListByAgent(ctx, int64(71)).Return([]*agent_entity.AgentExecTarget{
			{ID: 3, AgentID: 71, AgentBackendID: 711, SortOrder: 0},
		}, nil)
		m.backend.EXPECT().Find(ctx, int64(711)).Return(&agent_backend_entity.AgentBackend{
			ID: 711, Type: string(agent_backend_entity.TypeBuiltin), LLMProviderKey: "gone",
		}, nil)
		m.provider.EXPECT().FindByKey(ctx, "gone").Return(nil, nil)

		_, err := svc.PickExecTarget(ctx, 71, 0)
		require.Error(t, err)
		var noneErr *chat_svc.ExecTargetNoneAvailableError
		require.True(t, errors.As(err, &noneErr))
		require.Len(t, noneErr.Reasons, 1)
		return noneErr.Reasons[0].Hint, ctx
	}
	zhHint, zhCtx := run("zh-cn")
	enHint, enCtx := run("en")
	assert.Equal(t, i18n.T(zhCtx, code.ChatBackendHintActivateProvider), zhHint)
	assert.Equal(t, i18n.T(enCtx, code.ChatBackendHintActivateProvider), enHint)
	assert.NotEqual(t, zhHint, enHint)
}
