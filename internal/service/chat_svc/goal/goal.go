// Package goal 持有 codex「目标」(agentruntime.GoalController 端口)的会话侧编排:
// 解析会话上下文 → 取控制器 → 读/写/清目标,以及「以一个目标开一条新会话」。
//
// 它从 chat_svc 拆出来的判据是这条链路早就站在 agentruntime.GoalController 这个端口
// 之后:目标不写转录、不进 turn 队列、不碰 activeCancels —— 唯一伸进 chat_svc 的是
// 会话/Agent/后端/供应商的解析,以 Host 窄端口注入。
package goal

import (
	"context"
	"strings"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/ipc"
)

// Host 是本包对 chat_svc 的窄端口(ISP):只声明目标链路真正调用的那几件事。
// 会话表、runtime 注册表、i18n 错误码本包自己会用,不必经宿主。
type Host interface {
	// ResolveAgentBackend 解析这条会话(sess 可为 nil = 尚未落库的新会话)该用哪个
	// Agent / 执行目标档 / 供应商。
	ResolveAgentBackend(ctx context.Context, sess *chat_entity.Session, agentID, projectID int64) (
		*agent_entity.Agent, *agent_backend_entity.AgentBackend, *llm_provider_entity.LLMProvider, error)
	// ResolveSessionProvider 把 agent 绑定的供应商按会话覆盖/回退解析成本轮真正生效的那家。
	ResolveSessionProvider(
		ctx context.Context, sess *chat_entity.Session,
		be *agent_backend_entity.AgentBackend, prov *llm_provider_entity.LLMProvider,
	) (*llm_provider_entity.LLMProvider, *blocks.NoticeBlock, error)
	// SelectRunner 取本地 / 远端 runner。
	SelectRunner(ctx context.Context, be *agent_backend_entity.AgentBackend, sessionID int64) (agentruntime.Runtime, error)
	// EffectiveLLM 交出与 turn 同源的执行侧配置(远端为 keys-only)。
	EffectiveLLM(
		ctx context.Context, sess *chat_entity.Session,
		be *agent_backend_entity.AgentBackend, prov *llm_provider_entity.LLMProvider,
	) (*agentruntime.EffectiveLLMConfig, error)
	// ResolveSessionCwd 解析这条会话的工作目录。
	ResolveSessionCwd(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) (string, error)
	// RemoteLeaseFor 报告这一档是否跑在远端,以及它对应的本机配对行 ID。
	// (deviceID, true) 时调用方在用完控制器后必须 ReleaseRemoteRuntime。
	RemoteLeaseFor(ctx context.Context, be *agent_backend_entity.AgentBackend) (int64, bool)
	// ReleaseRemoteRuntime 归还一次远端租约引用。
	ReleaseRemoteRuntime(deviceID, sessionID int64)
	// ResolveProjectContext 校验并归一 StartGoal 传来的 projectID。
	ResolveProjectContext(ctx context.Context, projectID, agentID int64) (int64, error)
	// PinExecTargetIfUnset 把首轮实际落在的那一档钉进会话行(R15b)。
	PinExecTargetIfUnset(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend)
	// SessionTitle 由首条文本派生会话标题。
	SessionTitle(text string) string
	// Fail 是宿主的通用失败包装(cause 一并进日志与前端)。
	Fail(ctx context.Context, cause error) error
}

// Controller 是目标链路的编排者。
type Controller struct{ host Host }

// New 构造编排者。
func New(host Host) *Controller { return &Controller{host: host} }

// Patch 是 SetGoal 的三个可选字段;三个都为 nil 由调用方拒绝(InvalidParameter)。
type Patch struct {
	Objective   *string
	Status      *string
	TokenBudget *int
}

// StartInput 是 StartGoal 的入参。
type StartInput struct {
	AgentID        int64
	ProjectID      int64
	PermissionMode string
	Objective      string
	Patch          Patch
}

// Get 读取该会话当前的目标。
func (c *Controller) Get(ctx context.Context, sessionID int64) (*agentruntime.Goal, error) {
	controller, goalReq, release, err := c.controller(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	defer release()
	goal, err := controller.GetGoal(ctx, goalReq)
	if err != nil {
		logger.Ctx(ctx).Warn("goal.Get: runner.GetGoal failed",
			zap.Int64("sessionId", sessionID),
			zap.Error(err))
		return nil, i18n.NewError(ctx, code.ChatGoalInternal)
	}
	return goal, nil
}

// Set 写入/更新该会话的目标。
func (c *Controller) Set(ctx context.Context, sessionID int64, patch Patch) (*agentruntime.Goal, error) {
	sess, a, be, prov, err := c.sessionContext(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	goal, release, err := c.setOnSession(ctx, sess, a, be, prov, patch)
	defer release()
	return goal, err
}

// Clear 清掉该会话的目标。
func (c *Controller) Clear(ctx context.Context, sessionID int64) (bool, error) {
	controller, goalReq, release, err := c.controller(ctx, sessionID)
	if err != nil {
		return false, err
	}
	defer release()
	cleared, err := controller.ClearGoal(ctx, goalReq)
	if err != nil {
		logger.Ctx(ctx).Warn("goal.Clear: runner.ClearGoal failed",
			zap.Int64("sessionId", sessionID),
			zap.Error(err))
		return false, i18n.NewError(ctx, code.ChatGoalInternal)
	}
	return cleared, nil
}

// Start 以一个目标开一条新会话:建会话行 → 下发目标 → 把 codex 交回的 threadID
// 记成 provider session。threadID 为空视为失败(会话没有可续的 provider 侧身份)。
func (c *Controller) Start(ctx context.Context, in StartInput) (int64, *agentruntime.Goal, error) {
	// sess=nil：这是全新会话，还没有可粘的档；projectID 用请求原始值——
	// ResolveProjectContext 的默认/成员校验发生在下面，这里只用来喂 R15 的
	// "该机器上有没有配这个项目的路径" 判据，两边算的是同一个 project id。
	a, be, prov, err := c.host.ResolveAgentBackend(ctx, nil, in.AgentID, in.ProjectID)
	if err != nil {
		return 0, nil, err
	}
	if !be.IsCodex() {
		return 0, nil, i18n.NewError(ctx, code.ChatGoalUnsupported)
	}
	projectID, err := c.host.ResolveProjectContext(ctx, in.ProjectID, in.AgentID)
	if err != nil {
		return 0, nil, err
	}
	permissionMode, err := ipc.CreatePermissionMode(ctx, be, in.PermissionMode, true)
	if err != nil {
		return 0, nil, err
	}
	objective := strings.TrimSpace(in.Objective)
	sess := &chat_entity.Session{
		AgentID:                in.AgentID,
		ProjectID:              projectID,
		PermissionMode:         permissionMode,
		PermissionModeAtLaunch: permissionMode,
		Title:                  c.host.SessionTitle(objective),
		AgentStatus:            "idle",
		Status:                 consts.ACTIVE,
	}
	if err := chat_repo.Session().Create(ctx, sess); err != nil {
		return 0, nil, c.host.Fail(ctx, err)
	}
	// 首轮实际落在这一档（R15b / 决策36）：会话行已存在，钉住它。
	c.host.PinExecTargetIfUnset(ctx, sess, be)
	patch := in.Patch
	patch.Objective = &objective
	goal, release, err := c.setOnSession(ctx, sess, a, be, prov, patch)
	defer release()
	if err != nil {
		return 0, nil, err
	}
	if goal != nil {
		providerSessionID := strings.TrimSpace(goal.ThreadID)
		if providerSessionID == "" {
			return 0, nil, i18n.NewError(ctx, code.ChatGoalInternal)
		}
		sess.SetProviderSession(providerSessionID)
		if err := chat_repo.Session().Update(ctx, sess); err != nil {
			return 0, nil, c.host.Fail(ctx, err)
		}
	}
	return sess.ID, goal, nil
}

// sessionContext 解析目标链路要的四样东西,并做两道前置门禁:会话必须已有 provider
// session(否则没有可下发目标的 codex 线程),后端必须是 codex。
func (c *Controller) sessionContext(ctx context.Context, sessionID int64) (
	*chat_entity.Session, *agent_entity.Agent, *agent_backend_entity.AgentBackend, *llm_provider_entity.LLMProvider, error,
) {
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return nil, nil, nil, nil, c.host.Fail(ctx, err)
	}
	if sess == nil {
		return nil, nil, nil, nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	if strings.TrimSpace(sess.ProviderSessionID) == "" {
		return nil, nil, nil, nil, i18n.NewError(ctx, code.ChatGoalNoSession)
	}
	a, be, prov, err := c.host.ResolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if !be.IsCodex() {
		return nil, nil, nil, nil, i18n.NewError(ctx, code.ChatGoalUnsupported)
	}
	// goal 与 turn 共用同一个 codex app-server 会话池,所以供应商必须同一口径解析
	// (会话 provider_key > agent 绑定,spec 2026-08-10)。各读各的会让 acquireSession
	// 的启动期比对键(effectiveModel + effectiveProviderKey,决策 4)在 goal 与 turn 之间
	// 反复翻转 —— 一次 /goal 就把这条会话正在用的 app-server evict 掉重 spawn,而且这次
	// goal 本身打在用户没选的那家上游。回退 notice 丢弃:goal 不写 transcript,回退提示
	// 由真正跑轮的那条路径产出。fixed-model 目标失效 → 严格阻止（决策 7）。
	prov, _, err = c.host.ResolveSessionProvider(ctx, sess, be, prov)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return sess, a, be, prov, nil
}

func (c *Controller) controller(ctx context.Context, sessionID int64) (
	agentruntime.GoalController, agentruntime.GoalRequest, func(), error,
) {
	sess, a, be, prov, err := c.sessionContext(ctx, sessionID)
	if err != nil {
		return nil, agentruntime.GoalRequest{}, func() {}, err
	}
	return c.controllerForSession(ctx, sess, a, be, prov)
}

func (c *Controller) controllerForSession(
	ctx context.Context,
	sess *chat_entity.Session,
	a *agent_entity.Agent,
	be *agent_backend_entity.AgentBackend,
	prov *llm_provider_entity.LLMProvider,
) (agentruntime.GoalController, agentruntime.GoalRequest, func(), error) {
	release := func() {}
	runner, err := c.host.SelectRunner(ctx, be, sess.ID)
	if err != nil {
		logger.Ctx(ctx).Warn("goal.controllerForSession: selectRunner failed",
			zap.Int64("sessionId", sess.ID),
			zap.String("backendType", be.Type),
			zap.Error(err))
		return nil, agentruntime.GoalRequest{}, release, i18n.NewError(ctx, code.ChatGoalUnsupported)
	}
	if deviceID, ok := c.host.RemoteLeaseFor(ctx, be); ok {
		released := false
		release = func() {
			if released {
				return
			}
			released = true
			c.host.ReleaseRemoteRuntime(deviceID, sess.ID)
		}
	}
	if !runner.Capabilities().Has(capability.CapGoal) {
		release()
		return nil, agentruntime.GoalRequest{}, func() {}, i18n.NewError(ctx, code.ChatGoalUnsupported)
	}
	controller, ok := runner.(agentruntime.GoalController)
	if !ok {
		release()
		return nil, agentruntime.GoalRequest{}, func() {}, i18n.NewError(ctx, code.ChatGoalUnsupported)
	}
	cwd, err := c.host.ResolveSessionCwd(ctx, sess, be)
	if err != nil {
		return nil, agentruntime.GoalRequest{}, release, err
	}
	// goal 与 turn 共用同一执行侧配置（EffectiveLLMConfig v1 seam）：codex goal 会话池
	// 与 turn 同源解析，避免启动期比对键在 goal 与 turn 之间反复翻转。远端由 daemon
	// 自家解析，desktop 不解析、不发本地结果。
	cfg, err := c.host.EffectiveLLM(ctx, sess, be, prov)
	if err != nil {
		return nil, agentruntime.GoalRequest{}, release, err
	}
	return controller, agentruntime.GoalRequest{
		SessionID:         sess.ID,
		ProviderSessionID: sess.ProviderSessionID,
		Backend:           be,
		Provider:          prov,
		Effective:         cfg,
		Cwd:               cwd,
		AgentID:           a.ID,
	}, release, nil
}

func (c *Controller) setOnSession(
	ctx context.Context,
	sess *chat_entity.Session,
	a *agent_entity.Agent,
	be *agent_backend_entity.AgentBackend,
	prov *llm_provider_entity.LLMProvider,
	patch Patch,
) (*agentruntime.Goal, func(), error) {
	controller, goalReq, release, err := c.controllerForSession(ctx, sess, a, be, prov)
	if err != nil {
		return nil, release, err
	}
	goalReq.Objective = patch.Objective
	goalReq.Status = patch.Status
	goalReq.TokenBudget = patch.TokenBudget
	goal, err := controller.SetGoal(ctx, goalReq)
	if err != nil {
		release()
		logger.Ctx(ctx).Warn("goal.setOnSession: runner.SetGoal failed",
			zap.Int64("sessionId", sess.ID),
			zap.Error(err))
		return nil, func() {}, i18n.NewError(ctx, code.ChatGoalInternal)
	}
	return goal, release, nil
}
