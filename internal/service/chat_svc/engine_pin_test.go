package chat_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/sessionengine"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/sessionengine/sessionenginetest"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 本文件锁住 spec 2026-11-25 决策 10「会话钉住」：第一轮按派发方案钉住设备与引擎配置（含
// 手动指定的设备与「这次改用」的配置），远端钉住一定写设备指纹；已钉住的会话（含迁移前钉在
// 某台设备某一档上的老会话）续轮不再派发；无处可跑时带上每台设备的原因、不发出。

// fakeDispatch 只答 Plan，记下收到的请求；碰到别的方法当场 panic。
type fakeDispatch struct {
	dispatch_svc.DispatchSvc
	plan *dispatch_svc.Plan
	err  error
	reqs []dispatch_svc.PlanRequest
	// validateErr / validated 是 ValidateManual 的应答与收到的请求。
	validateErr error
	validated   []dispatch_svc.PlanRequest
}

// ValidateManual 记下收到的请求，答 validateErr。
func (f *fakeDispatch) ValidateManual(_ context.Context, req *dispatch_svc.PlanRequest) error {
	f.validated = append(f.validated, *req)
	return f.validateErr
}

func (f *fakeDispatch) Plan(_ context.Context, req *dispatch_svc.PlanRequest) (*dispatch_svc.Plan, error) {
	f.reqs = append(f.reqs, *req)
	return f.plan, f.err
}

type enginePinMocks struct {
	agent    *mock_agent_repo.MockAgentRepo
	backend  *mock_agent_backend_repo.MockAgentBackendRepo
	profiles *mock_agent_backend_repo.MockEngineProfileRepo
	session  *mock_chat_repo.MockSessionRepo
	dispatch *fakeDispatch
}

func setupEnginePinTest(t *testing.T) (context.Context, *enginePinMocks, *chatSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := &enginePinMocks{
		agent:    mock_agent_repo.NewMockAgentRepo(ctrl),
		backend:  mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		profiles: mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl),
		session:  mock_chat_repo.NewMockSessionRepo(ctrl),
		dispatch: &fakeDispatch{},
	}
	prevAgent, prevBackend, prevProfiles := agent_repo.Agent(), agent_backend_repo.AgentBackend(), agent_backend_repo.EngineProfile()
	prevSession, prevDispatch := chat_repo.Session(), dispatch_svc.Dispatch()
	agent_repo.RegisterAgent(m.agent)
	agent_backend_repo.RegisterAgentBackend(m.backend)
	agent_backend_repo.RegisterEngineProfile(m.profiles)
	chat_repo.RegisterSession(m.session)
	dispatch_svc.RegisterDispatch(m.dispatch)
	sessionengine.RegisterEffective(nil)
	t.Cleanup(func() {
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
		agent_backend_repo.RegisterEngineProfile(prevProfiles)
		chat_repo.RegisterSession(prevSession)
		dispatch_svc.RegisterDispatch(prevDispatch)
		sessionengine.RegisterEffective(sessionenginetest.LegacyEffective) // 把 TestMain 装的旧夹具桥装回去
	})
	return context.Background(), m, NewChat(NoopEmitter{}).(*chatSvc)
}

// memberEffective 让成员生效的引擎配置是 p。
func memberEffective(t *testing.T, p *agent_backend_entity.EngineProfile) {
	t.Helper()
	sessionengine.RegisterEffective(func(context.Context, *agent_entity.Agent) (*agent_backend_entity.EngineProfile, error) {
		return p, nil
	})
}

// claudeProfile 是一份 CLI 登录态（不绑供应商）的 Claude Code 引擎配置：校验不查供应商、
// 不要网关，用例只关心派发与钉住。
func claudeProfile(id int64) *agent_backend_entity.EngineProfile {
	return &agent_backend_entity.EngineProfile{
		ID: id, Type: string(agent_backend_entity.TypeClaudeCode), Name: "Claude",
		ReasoningEffort: "high", DefaultPermissionMode: "acceptEdits", DefaultModel: "opus", Status: consts.ACTIVE,
	}
}

func chosenOn(fp devicefp.Carrier, self bool) *dispatch_svc.Plan {
	return &dispatch_svc.Plan{Found: true, Chosen: fp, Devices: []*dispatch_svc.DeviceResult{
		{Fingerprint: fp, IsSelf: self, Eligible: true, Chosen: true},
	}}
}

func requireEngineCode(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

// ── 第一轮钉住 ──────────────────────────────────────────────────────────────

// Given 还没钉住的已落库会话，When 第一轮派发选中一台已配对的远端设备，Then 这一轮在那台
// 设备上跑成员生效的那份配置；钉住时先写设备指纹（连同配对行号）再写引擎配置 —— 只写配置
// 不写设备的远端钉住会被派发作用域当成本机会话。
func TestResolveTurn_GivenUnpinnedSession_WhenPlanChoosesRemoteDevice_ThenPinsDeviceThenProfile(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	installPairedDevice(t, gomock.NewController(t), 7)
	remoteFP := testDeviceFingerprint(7)
	memberEffective(t, claudeProfile(31))
	m.dispatch.plan = chosenOn(remoteFP, false)
	sess := &chat_entity.Session{ID: 900, AgentID: 401, ProjectID: 3}
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil)

	_, be, _, pin, err := svc.resolveTurn(ctx, sess, sess.AgentID, sess.ProjectID, turnPick{})
	require.NoError(t, err)
	assert.Equal(t, remoteFP, be.DeviceFingerprint)
	assert.Equal(t, "opus", be.DefaultModel)
	assert.Zero(t, be.ID, "引擎配置视图不是 agent_backends 的一行")
	require.Len(t, m.dispatch.reqs, 1)
	assert.Equal(t, dispatch_svc.PlanRequest{AgentID: 401, ProjectID: 3, EngineProfileID: 31}, m.dispatch.reqs[0])
	require.Equal(t, &turnPin{engineProfileID: 31, device: remoteFP}, pin)

	gomock.InOrder(
		m.session.EXPECT().UpdateExecDaemon(ctx, int64(900), int64(7), remoteFP, int64(0)).Return(nil),
		m.session.EXPECT().UpdateEngineProfile(ctx, int64(900), int64(31)).Return(nil),
	)
	svc.pinTurn(ctx, sess, pin)
	assert.Equal(t, int64(31), sess.EngineProfileID, "写库成功后内存实体同步")
	assert.Equal(t, remoteFP, sess.ExecDeviceFingerprint)
	assert.Equal(t, int64(7), sess.ExecDeviceID)

	// 已经钉住：自动续轮带着同一份钉住再来一次也不再写（这里没有更多期望）。
	svc.pinTurn(ctx, sess, pin)
}

// Given 还没钉住的会话，When 派发选中本机，Then 只钉引擎配置、不写设备列（本机即空）。
func TestPinTurn_GivenLocalDevice_ThenPinsProfileOnly(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	sess := &chat_entity.Session{ID: 901, AgentID: 401}
	m.session.EXPECT().UpdateEngineProfile(ctx, int64(901), int64(31)).Return(nil)

	svc.pinTurn(ctx, sess, &turnPin{engineProfileID: 31})
	assert.Equal(t, int64(31), sess.EngineProfileID)
	assert.Empty(t, sess.ExecDeviceFingerprint)
}

// Given 写设备指纹失败，Then 不再写引擎配置（否则会留下「有配置、没设备」被当成本机的行），
// 这一轮不被阻断，下一轮仍落在「没钉」分支重试。
func TestPinTurn_GivenDeviceWriteFails_ThenDoesNotPinTheProfile(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	sess := &chat_entity.Session{ID: 902, AgentID: 401}
	m.session.EXPECT().UpdateExecDaemon(ctx, int64(902), int64(0), devicefp.Carrier("sha256:box"), int64(0)).
		Return(assert.AnError)

	svc.pinTurn(ctx, sess, &turnPin{engineProfileID: 31, device: "sha256:box"})
	assert.Zero(t, sess.EngineProfileID)
	assert.Empty(t, sess.ExecDeviceFingerprint)
}

// Given 还没钉住的会话，When 只读入口（停止、查进程、对端控制）解析它的后端，Then 照样拿到
// 派发出来的那份，但不写库 —— 钉住只在第一轮发出时发生。
func TestResolveAgentBackend_GivenUnpinnedSession_ThenResolvesWithoutPinning(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	memberEffective(t, claudeProfile(31))
	m.dispatch.plan = chosenOn("", true)
	sess := &chat_entity.Session{ID: 903, AgentID: 401}
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil)

	_, be, _, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, 0)
	require.NoError(t, err)
	assert.Equal(t, "opus", be.DefaultModel)
	assert.Zero(t, sess.EngineProfileID)
}

// Given 新会话第一轮手动选了一台设备、又「这次改用」另一份配置，When 解析这一轮，Then 派发
// 请求带上两者，跑的是改用的那份（不是成员生效的那份），钉住留给建档之后。
func TestResolveTurn_GivenManualDeviceAndEngineOverride_ThenPlanCarriesBothAndUsesOverride(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	installPairedDevice(t, gomock.NewController(t), 7)
	remoteFP := testDeviceFingerprint(7)
	memberEffective(t, claudeProfile(31))
	override := claudeProfile(32)
	override.DefaultModel = "sonnet"
	m.profiles.EXPECT().Find(ctx, int64(32)).Return(override, nil)
	m.dispatch.plan = chosenOn(remoteFP, false)
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil)

	_, be, _, pin, err := svc.resolveTurn(ctx, nil, 401, 3, turnPick{device: remoteFP, engineProfileID: 32})
	require.NoError(t, err)
	assert.Equal(t, "sonnet", be.DefaultModel)
	assert.Equal(t, remoteFP, be.DeviceFingerprint)
	require.Len(t, m.dispatch.reqs, 1)
	assert.Equal(t, dispatch_svc.PlanRequest{AgentID: 401, ProjectID: 3, EngineProfileID: 32, ManualDevice: remoteFP}, m.dispatch.reqs[0])
	assert.Equal(t, &turnPin{engineProfileID: 32, device: remoteFP}, pin)
}

// Given 「这次改用」的配置已被删除，Then 报 EngineProfileNotFound，不派发。
func TestResolveTurn_GivenDeletedEngineOverride_ThenEngineProfileNotFound(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	deleted := claudeProfile(32)
	deleted.Status = consts.DELETE
	m.profiles.EXPECT().Find(ctx, int64(32)).Return(deleted, nil)
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil)

	_, _, _, _, err := svc.resolveTurn(ctx, nil, 401, 0, turnPick{engineProfileID: 32})
	requireEngineCode(t, err, code.EngineProfileNotFound)
	assert.Empty(t, m.dispatch.reqs)
}

// Given 成员没有任何生效的引擎配置，Then 报 EngineProfileRequired。
func TestResolveTurn_GivenNoEffectiveEngine_ThenEngineProfileRequired(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil)

	_, _, _, _, err := svc.resolveTurn(ctx, nil, 401, 0, turnPick{})
	requireEngineCode(t, err, code.EngineProfileRequired)
}

// ── 无处可跑 ────────────────────────────────────────────────────────────────

// Given 所有设备都不通过，When 解析第一轮，Then 报 *NoEligibleDeviceError：码是
// DispatchNoEligibleDevice，带着每台设备与它的原因码，文本里逐台写出；什么都不钉。
func TestResolveAgentBackend_GivenNoEligibleDevice_ThenErrorCarriesEveryDeviceReason(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	memberEffective(t, claudeProfile(31))
	m.dispatch.plan = &dispatch_svc.Plan{Found: false, Devices: []*dispatch_svc.DeviceResult{
		{Fingerprint: "sha256:self", IsSelf: true, Reason: &dispatch_svc.ReasonView{Code: "runtime_missing"}},
		{Fingerprint: "sha256:box", Name: "box", Reason: &dispatch_svc.ReasonView{Code: "offline"}},
	}}
	sess := &chat_entity.Session{ID: 902, AgentID: 401}
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil)

	_, _, _, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, 0)
	requireEngineCode(t, err, code.DispatchNoEligibleDevice)
	var noDevice *NoEligibleDeviceError
	require.ErrorAs(t, err, &noDevice)
	require.Len(t, noDevice.Devices, 2)
	assert.Equal(t, "runtime_missing", noDevice.Devices[0].Reason.Code)
	assert.Equal(t, "offline", noDevice.Devices[1].Reason.Code)
	assert.Contains(t, err.Error(), "runtime_missing")
	assert.Contains(t, err.Error(), "box")
	assert.Contains(t, err.Error(), "offline")
	assert.Zero(t, sess.EngineProfileID, "无处可跑时什么都不钉")
}

// Given 新会话（SessionID=0）无处可跑，When Send，Then 返回带逐设备原因的错误，且不建会话行。
func TestSend_GivenNewSessionWithNoEligibleDevice_ThenFailsBeforeCreatingTheSession(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	memberEffective(t, claudeProfile(31))
	m.dispatch.plan = &dispatch_svc.Plan{Found: false, Devices: []*dispatch_svc.DeviceResult{
		{Fingerprint: "sha256:self", IsSelf: true, Reason: &dispatch_svc.ReasonView{Code: "not_logged_in"}},
	}}
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil)

	_, err := svc.Send(ctx, &SendRequest{AgentID: 401, Text: "hi"})
	var noDevice *NoEligibleDeviceError
	require.ErrorAs(t, err, &noDevice)
	assert.Equal(t, "not_logged_in", noDevice.Devices[0].Reason.Code)
}

// ── 已钉住的会话续轮 ────────────────────────────────────────────────────────

// Given 迁移前钉在设备 D 上 backend X 的会话（迁移把 X 换成它所在组的引擎配置，设备列不动），
// When 续轮，Then 不派发、不写库，仍在 D 上，用 X 那份模型 / 力度 / 权限设置；远端一轮的
// RunRequest 也带上这份引擎配置。
func TestResolveAgentBackend_GivenMigratedSessionPinnedOnDeviceD_ThenResumesOnDWithSameSettings(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	deviceD := testDeviceFingerprint(7)
	migrated := &agent_backend_entity.EngineProfile{
		ID: 31, Type: string(agent_backend_entity.TypeClaudeCode), Name: "Claude on D",
		LLMModelKey: "model-x", ReasoningEffort: "max", DefaultPermissionMode: "acceptEdits", DefaultModel: "opus",
		Status: consts.ACTIVE,
	}
	sess := &chat_entity.Session{
		ID: 903, AgentID: 401, EngineProfileID: 31, ExecAgentBackendID: 52, ExecDeviceID: 7,
		ExecDeviceFingerprint: deviceD, ProviderSessionID: "claude-session-1", PermissionMode: "acceptEdits",
	}
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401, AgentBackendID: 99}, nil)
	m.profiles.EXPECT().FindAnyStatus(ctx, int64(31)).Return(migrated, nil)

	_, be, _, err := svc.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, m.dispatch.reqs, "已钉住的会话续轮不派发")
	assert.Equal(t, deviceD, be.DeviceFingerprint)
	assert.Equal(t, "model-x", be.LLMModelKey)
	assert.Equal(t, "max", be.ReasoningEffort)
	assert.Equal(t, "acceptEdits", be.DefaultPermissionMode)
	assert.Equal(t, "opus", be.DefaultModel)

	env := newMemoryTurnEnv(t)
	remote := env.remote(capability.Capabilities{})
	require.Equal(t, deviceD, remote.DeviceFingerprint, "同一台设备")
	installCLIPathRepo(t, env.ctrl).EXPECT().
		Find(gomock.Any(), string(agent_backend_entity.TypeClaudeCode), deviceD).Return(nil, nil)
	req, err := env.turn(sess, &agent_entity.Agent{ID: 401}, be, "")
	require.NoError(t, err)
	assert.Equal(t, int64(31), req.EngineProfileID, "远端 runtime.run 带引擎配置")
	assert.Equal(t, "claude-session-1", req.ProviderSessionID, "--resume 续接同一个 CLI 会话")
	assert.Equal(t, "max", req.Backend.ReasoningEffort)
	assert.Equal(t, deviceD, req.Backend.DeviceFingerprint)
}

// Given 只钉了旧执行目标档（agrctl --backend / 旧派发写下的）的会话，When 续轮，Then 原样用那一
// 行后端，不派发；那一行已被删时按第一轮重新派发，交回要钉的配置。
func TestResolveAgentBackend_GivenLegacyBackendPin(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil).Times(2)
	m.backend.EXPECT().Find(ctx, int64(52)).Return(&agent_backend_entity.AgentBackend{
		ID: 52, Type: string(agent_backend_entity.TypeClaudeCode),
	}, nil)
	_, be, _, err := svc.resolveAgentBackend(ctx, &chat_entity.Session{ID: 904, AgentID: 401, ExecAgentBackendID: 52}, 401, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(52), be.ID)
	assert.Empty(t, m.dispatch.reqs)

	memberEffective(t, claudeProfile(31))
	m.dispatch.plan = chosenOn("", true)
	m.backend.EXPECT().Find(ctx, int64(53)).Return(nil, nil)
	_, be, _, pin, err := svc.resolveTurn(ctx, &chat_entity.Session{ID: 905, AgentID: 401, ExecAgentBackendID: 53}, 401, 0, turnPick{})
	require.NoError(t, err)
	assert.Equal(t, "opus", be.DefaultModel, "钉住的旧后端已被删：按成员生效的配置重新派发")
	assert.Equal(t, &turnPin{engineProfileID: 31}, pin, "并在这一轮发出时钉住")
}

// Given 设备列已经有值、只差引擎配置没钉的会话（一次写库半途失败），When 解析，Then 设备不变
// （会话不能换设备），只补钉引擎配置。
func TestResolveAgentBackend_GivenDeviceAlreadyPinned_ThenKeepsTheDevice(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	deviceD := testDeviceFingerprint(7)
	memberEffective(t, claudeProfile(31))
	m.dispatch.plan = chosenOn("sha256:other", false)
	sess := &chat_entity.Session{ID: 906, AgentID: 401, ExecDeviceID: 7, ExecDeviceFingerprint: deviceD}
	m.agent.EXPECT().Find(ctx, int64(401)).Return(&agent_entity.Agent{ID: 401}, nil)

	_, be, _, pin, err := svc.resolveTurn(ctx, sess, sess.AgentID, 0, turnPick{})
	require.NoError(t, err)
	assert.Equal(t, deviceD, be.DeviceFingerprint)
	require.Len(t, m.dispatch.reqs, 1)
	assert.Equal(t, deviceD, m.dispatch.reqs[0].ManualDevice)

	m.session.EXPECT().UpdateEngineProfile(ctx, int64(906), int64(31)).Return(nil)
	svc.pinTurn(ctx, sess, pin)
	assert.Equal(t, int64(31), sess.EngineProfileID)
}

// execTargetPinMocks 是钉在旧执行目标档（exec_agent_backend_id）上的会话的用例环境：协议
// 切换前这种会话仍原样用那一行后端，解析不派发（sessionengine.Pinned）。
type execTargetPinMocks struct {
	agent      *mock_agent_repo.MockAgentRepo
	execTarget *mock_agent_repo.MockAgentExecTargetRepo
	backend    *mock_agent_backend_repo.MockAgentBackendRepo
	session    *mock_chat_repo.MockSessionRepo
}

func setupExecTargetPinTest(t *testing.T) (context.Context, *execTargetPinMocks, *chatSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := &execTargetPinMocks{
		agent:      mock_agent_repo.NewMockAgentRepo(ctrl),
		execTarget: mock_agent_repo.NewMockAgentExecTargetRepo(ctrl),
		backend:    mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
		session:    mock_chat_repo.NewMockSessionRepo(ctrl),
	}
	prevAgent, prevExecTarget := agent_repo.Agent(), agent_repo.AgentExecTarget()
	prevBackend, prevSession := agent_backend_repo.AgentBackend(), chat_repo.Session()
	agent_repo.RegisterAgent(m.agent)
	agent_repo.RegisterAgentExecTarget(m.execTarget)
	agent_backend_repo.RegisterAgentBackend(m.backend)
	chat_repo.RegisterSession(m.session)
	t.Cleanup(func() {
		agent_repo.RegisterAgent(prevAgent)
		agent_repo.RegisterAgentExecTarget(prevExecTarget)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
		chat_repo.RegisterSession(prevSession)
	})
	return context.Background(), m, NewChat(NoopEmitter{}).(*chatSvc)
}

// Given 新会话请求，When 翻成第一轮的手动选择，Then 新字段（ExecDevice / EngineProfileID）原样
// 带上；旧界面的 ExecTargetOverride（选中的是某一档后端）翻成「在那一档所在的设备上跑」——
// 远端档取它的设备，本机档取本机指纹；那一档不存在时报错；新字段在场时不再看旧字段。
func TestSendTurnPick(t *testing.T) {
	ctx, m, svc := setupEnginePinTest(t)
	installPairedDevice(t, gomock.NewController(t), 7)

	pick, err := svc.sendTurnPick(ctx, &SendRequest{ExecDevice: "sha256:box", EngineProfileID: 32, ExecTargetOverride: 52})
	require.NoError(t, err)
	assert.Equal(t, turnPick{device: "sha256:box", engineProfileID: 32}, pick)

	m.backend.EXPECT().Find(ctx, int64(52)).Return(&agent_backend_entity.AgentBackend{ID: 52, DeviceFingerprint: testDeviceFingerprint(7)}, nil)
	pick, err = svc.sendTurnPick(ctx, &SendRequest{ExecTargetOverride: 52})
	require.NoError(t, err)
	assert.Equal(t, turnPick{device: testDeviceFingerprint(7)}, pick)

	m.backend.EXPECT().Find(ctx, int64(53)).Return(&agent_backend_entity.AgentBackend{ID: 53}, nil)
	pick, err = svc.sendTurnPick(ctx, &SendRequest{ExecTargetOverride: 53})
	require.NoError(t, err)
	assert.Equal(t, turnPick{device: "sha256:self"}, pick, "本机档 = 手动指定本机")

	m.backend.EXPECT().Find(ctx, int64(54)).Return(nil, nil)
	_, err = svc.sendTurnPick(ctx, &SendRequest{ExecTargetOverride: 54})
	requireEngineCode(t, err, code.ChatAgentNotChattable)

	_, err = svc.sendTurnPick(ctx, &SendRequest{EngineProfileID: -1})
	requireEngineCode(t, err, code.InvalidParameter)
}
