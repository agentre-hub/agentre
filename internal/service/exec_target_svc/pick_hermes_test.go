package exec_target_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
)

// 本文件锁住 hermes 在「可对话判定」里的插槽语义（Stage 3）：
//   - 本机 hermes 自带 provider/model/凭证，且不经本地网关 → 不查 provider 是否
//     激活、不查 gateway 是否在跑，直接判可对话；URL 连不上由轮次启动时报错。
//   - 远端（agentred）hermes 没有执行通道 → 明确拒绝，并给 hermes 自己的结构化
//     原因与中英文案，不能复用 openclaw 的文案，也不能悄悄放行。

// inactiveProvider 是一个存在但未激活的 provider：hermes 不该看它。
func inactiveProvider() *llm_provider_entity.LLMProvider {
	return &llm_provider_entity.LLMProvider{
		ID: 1, ProviderKey: "any", Type: string(llm_provider_entity.TypeAnthropic), Status: 0,
	}
}

func TestBlockReasonForBackend_GivenLocalHermes_ThenChattableWithoutProviderOrGateway(t *testing.T) {
	ctx, _, _ := setupPickExecTargetTest(t)
	be := &agent_backend_entity.AgentBackend{ID: 901, Type: string(agent_backend_entity.TypeHermes)}

	// 故意传一个未激活的 provider、并声明 gateway 没在跑：hermes 两样都不该看。
	chattable, reason, hint := exec_target_svc.BlockReasonForBackend(ctx, be, inactiveProvider(), false)
	assert.True(t, chattable, "本机 hermes 不依赖 Agentre provider 或本地网关，应判可对话")
	assert.Empty(t, reason)
	assert.Empty(t, hint)
}

func TestBlockReasonForBackend_GivenRemoteHermes_ThenBlockedWithHermesSpecificReason(t *testing.T) {
	ctx, _, _ := setupPickExecTargetTest(t)
	be := &agent_backend_entity.AgentBackend{
		ID:                902,
		Type:              string(agent_backend_entity.TypeHermes),
		DeviceFingerprint: pickTestFingerprint(88),
	}

	chattable, reason, hint := exec_target_svc.BlockReasonForBackend(ctx, be, nil, false)
	require.False(t, chattable, "hermes 不能派发到 agentred，必须拒绝")
	assert.Equal(t, exec_target_svc.BlockReasonRemoteHermesUnavailable, reason)
	assert.Equal(t, i18n.T(ctx, code.ChatBackendHintRemoteHermes), hint)
	assert.NotEqual(t, i18n.T(ctx, code.ChatBackendHintRemoteOpenClaw), hint,
		"必须给 hermes 自己的文案，不能复用 openclaw 的")
}

// 文案是用户可见正文，必须真的走语言包：换成 en 之后文案跟着变。
func TestBlockReasonForBackend_GivenRemoteHermes_ThenHintIsLocalized(t *testing.T) {
	run := func(lang string) (string, context.Context) {
		base, _, _ := setupPickExecTargetTest(t)
		ctx := i18n.WithLanguage(base, lang)
		be := &agent_backend_entity.AgentBackend{
			ID:                903,
			Type:              string(agent_backend_entity.TypeHermes),
			DeviceFingerprint: pickTestFingerprint(89),
		}
		_, _, hint := exec_target_svc.BlockReasonForBackend(ctx, be, nil, false)
		return hint, ctx
	}
	zhHint, zhCtx := run("zh-cn")
	enHint, enCtx := run("en")

	assert.Equal(t, i18n.T(zhCtx, code.ChatBackendHintRemoteHermes), zhHint)
	assert.Equal(t, i18n.T(enCtx, code.ChatBackendHintRemoteHermes), enHint)
	assert.NotEqual(t, zhHint, enHint)
}

func TestPickExecTarget_GivenLocalHermes_ThenPicksWithoutProviderProbeOrGateway(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(401)).Return([]*agent_entity.AgentExecTarget{
		{ID: 61, AgentID: 401, AgentBackendID: 961, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(961)).Return(&agent_backend_entity.AgentBackend{
		ID: 961, Type: string(agent_backend_entity.TypeHermes),
	}, nil)
	// 不给 provider / gateway 设任何期望：碰它们就是做了不该做的预探测。

	choice, err := svc.PickExecTarget(ctx, 401, 0)
	require.NoError(t, err)
	require.NotNil(t, choice)
	assert.Equal(t, int64(961), choice.Backend.ID)
}

func TestPickExecTarget_GivenRemoteHermes_ThenReportsRemoteHermesReason(t *testing.T) {
	ctx, m, svc := setupPickExecTargetTest(t)
	m.execTarget.EXPECT().ListByAgent(ctx, int64(402)).Return([]*agent_entity.AgentExecTarget{
		{ID: 62, AgentID: 402, AgentBackendID: 962, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(962)).Return(&agent_backend_entity.AgentBackend{
		ID:                962,
		Type:              string(agent_backend_entity.TypeHermes),
		DeviceFingerprint: pickTestFingerprint(90),
	}, nil)
	m.pairedDevices(pairedDevice(90, true))

	_, err := svc.PickExecTarget(ctx, 402, 0)
	require.Error(t, err)
	var noneErr *exec_target_svc.ExecTargetNoneAvailableError
	require.ErrorAs(t, err, &noneErr)
	require.Len(t, noneErr.Reasons, 1)
	assert.Equal(t, exec_target_svc.BlockReasonRemoteHermesUnavailable, noneErr.Reasons[0].Reason)
}
