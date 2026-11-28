package chat_svc

import (
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 本文件锁住 agrctl send 在桌面端的派发（spec 2026-11-25 「agrctl」）：--device / --engine 经
// 同一份共享派发函数校验，选中的设备与引擎配置在首轮之前钉到新会话上，模型类覆盖按最终的
// 引擎配置校验。

func ctlPlan() *dispatch_svc.Plan {
	return &dispatch_svc.Plan{
		Found: true, Chosen: "sha256:box", ChosenBy: "account_order",
		Engine: dispatch_svc.EngineView{ID: 31, Name: "Claude", Runtime: "claudecode"},
		Devices: []*dispatch_svc.DeviceResult{
			{Fingerprint: "sha256:self", IsSelf: true, Reason: &dispatch_svc.ReasonView{Code: "runtime_missing", Runtime: "claudecode"}},
			{Fingerprint: "sha256:box", Name: "build-box", Eligible: true, Chosen: true},
		},
	}
}

// Given 成员生效的引擎配置与两台参与派发的设备 When 取 agrctl 的派发方案 Then 带回引擎配置、
// 选中的设备，以及每台设备的名字、--device 能选中它的写法、是否通过与原因；本机多出
// 「本机」「local」两种写法。
func TestCtlDispatchPlan_ReturnsEngineChosenDeviceAndEveryDevicesVerdict(t *testing.T) {
	_, m, svc := setupEnginePinTest(t)
	m.dispatch.plan = ctlPlan()

	got, err := svc.CtlDispatchPlan(t.Context(), 401, 3, 0)
	require.NoError(t, err)
	assert.Equal(t, dispatch_svc.PlanRequest{AgentID: 401, ProjectID: 3}, m.dispatch.reqs[0])
	assert.True(t, got.Found)
	assert.Equal(t, int64(31), got.EngineProfileID)
	require.Len(t, got.Devices, 2)

	self, box := got.Devices[0], got.Devices[1]
	assert.Equal(t, "this desktop", self.Name)
	assert.Contains(t, self.Refs, "本机")
	assert.Contains(t, self.Refs, "local")
	assert.Contains(t, self.Refs, "sha256:self")
	assert.False(t, self.Eligible)
	require.NotNil(t, self.Reason)
	assert.Equal(t, "runtime_missing", self.Reason.Code)

	assert.Equal(t, "build-box", box.Name)
	assert.ElementsMatch(t, []string{"build-box", "sha256:box"}, box.Refs)
	assert.True(t, box.Eligible)
	assert.True(t, box.Chosen)
}

// Given 「这次改用」的引擎配置 When 取方案 Then 方案按那份配置派发。
func TestCtlDispatchPlan_PassesTheEngineOverrideToTheDispatchRule(t *testing.T) {
	_, m, svc := setupEnginePinTest(t)
	m.dispatch.plan = ctlPlan()
	_, err := svc.CtlDispatchPlan(t.Context(), 401, 0, 32)
	require.NoError(t, err)
	assert.Equal(t, int64(32), m.dispatch.reqs[0].EngineProfileID)
}

// Given 引擎配置的名字或 id When 解析 --engine Then 找到活的那份；找不到或已删除时给出可选的
// 引擎配置名（命令行据此给用法错误）。
func TestResolveCtlEngine_ByNameOrIDOrListsTheOptions(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	deleted := claudeProfile(33)
	deleted.Name, deleted.Status = "gone", consts.DELETE
	codex := &agent_backend_entity.EngineProfile{ID: 32, Name: "Codex", Type: "codex", Status: consts.ACTIVE}
	m.profiles.EXPECT().List(ctx).Return([]*agent_backend_entity.EngineProfile{claudeProfile(31), codex}, nil).AnyTimes()

	for _, ref := range []string{"Codex", "32"} {
		got, err := svc.ResolveCtlEngine(ctx, ref)
		require.NoError(t, err, ref)
		assert.Equal(t, int64(32), got.ID)
		assert.Equal(t, "Codex", got.Name)
	}
	for _, ref := range []string{"nope", "99", "gone"} {
		_, err := svc.ResolveCtlEngine(ctx, ref)
		var nf *CtlEngineNotFoundError
		require.ErrorAs(t, err, &nf, ref)
		assert.Equal(t, ref, nf.Ref)
		assert.Equal(t, []string{"Claude", "Codex"}, nf.Options)
	}
}

// Given 方案选中远端设备与引擎配置 When 首轮之前钉住 Then 先写设备（连配对行号）再写引擎配置；
// 选中本机时只写引擎配置。
func TestPinCtlDispatch_PinsDeviceThenEngine(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	installPairedDevice(t, gomock.NewController(t), 7)
	remoteFP := testDeviceFingerprint(7)
	m.session.EXPECT().Find(ctx, int64(900)).Return(&chat_entity.Session{ID: 900, AgentID: 401}, nil)
	gomock.InOrder(
		m.session.EXPECT().UpdateExecDaemon(ctx, int64(900), int64(7), remoteFP, int64(0)).Return(nil),
		m.session.EXPECT().UpdateEngineProfile(ctx, int64(900), int64(31)).Return(nil),
	)
	require.NoError(t, svc.PinCtlDispatch(ctx, 900, 31, remoteFP))

	m.session.EXPECT().Find(ctx, int64(901)).Return(&chat_entity.Session{ID: 901, AgentID: 401}, nil)
	m.session.EXPECT().UpdateEngineProfile(ctx, int64(901), int64(31)).Return(nil)
	require.NoError(t, svc.PinCtlDispatch(ctx, 901, 31, "sha256:self"), "本机指纹 = 不写设备列")
}

// Given 钉不上（写库失败，pinTurn 只记日志）When 钉住 Then 报错而不是让会话悄悄落到别处。
func TestPinCtlDispatch_GivenTheWriteFailsThenReturnsAnError(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	installPairedDevice(t, gomock.NewController(t), 7)
	m.session.EXPECT().Find(ctx, int64(902)).Return(&chat_entity.Session{ID: 902, AgentID: 401}, nil)
	m.session.EXPECT().UpdateEngineProfile(ctx, int64(902), int64(31)).Return(assert.AnError)
	require.Error(t, svc.PinCtlDispatch(ctx, 902, 31, "sha256:self"))
}

// Given 新会话 --engine 选了 Codex 配置 When 校验 --permission-mode Then 按 Codex 的权限模式
// 校验，而不是成员生效的 Claude 那份。
func TestCheckCtlOverrides_AgainstTheEngineOverrideForANewSession(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	memberEffective(t, claudeProfile(31))
	codex := &agent_backend_entity.EngineProfile{ID: 32, Name: "Codex", Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE}
	m.profiles.EXPECT().Find(ctx, int64(32)).Return(codex, nil).AnyTimes()
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil).AnyTimes()

	err := svc.CheckCtlOverrides(ctx, 401, 32, 0, CtlOverrides{PermissionMode: "acceptEdits"})
	var oe *CtlOverrideError
	require.ErrorAs(t, err, &oe, "claude's acceptEdits is not a codex mode")
	assert.Equal(t, "permission-mode", oe.Flag)

	require.NoError(t, svc.CheckCtlOverrides(ctx, 401, 0, 0, CtlOverrides{PermissionMode: "acceptEdits"}), "the member's own claude engine accepts it")
}

// Given 钉着引擎配置、没有旧执行档的会话 When 校验覆盖 Then 按钉住的那份配置校验。
func TestCheckCtlOverrides_AgainstThePinnedEngineOfAnExistingSession(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	m.profiles.EXPECT().FindAnyStatus(ctx, int64(31)).Return(claudeProfile(31), nil).AnyTimes()
	m.session.EXPECT().Find(ctx, int64(100)).Return(&chat_entity.Session{ID: 100, AgentID: 7, EngineProfileID: 31}, nil)
	m.agent.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{ID: 7}, nil).AnyTimes()
	require.NoError(t, svc.CheckCtlOverrides(ctx, 0, 0, 100, CtlOverrides{PermissionMode: "plan", Effort: "high"}))
}

var _ devicefp.Carrier
