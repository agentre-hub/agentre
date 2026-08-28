package chat_svc

import (
	"context"
	"strings"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

// 会话级 LLM 供应商：切换入口 + 有效供应商解析的唯一口径。
// 规格：docs/specs/2026-08-10-session-provider-switch.md

// effectiveProviderKey 是「这条会话下一轮用哪个供应商」的**唯一**解析口（spec
// 「有效供应商解析（唯一口径）」）：会话级 provider_key 非空取它，否则取 agent 绑定；
// 两者皆空表示无供应商，即 CLI 自身登录态。
//
// 任何消费点（turn 解析、网关 token、CLI env/config 门控、goal 入口、LoadSession 展示、
// 复制启动命令、远端 wire）都必须走这里，不得再各自直接读 be.LLMProviderKey —— 各读各的
// 正是「会话选了供应商却打到 agent 绑定那家」的成因。goal 尤其不能漏：它与 turn 共用同
// 一个 CLI 会话池，两边解析不一致会让启动期比对键（决策 4）反复翻转、把在用的子进程
// evict 掉重 spawn。
//
// 已知残留（本地已收口，远端未）：远端 goal 的 wire.GoalParams 不带 provider key，daemon
// 的 hydrateGoalProvider 仍按 agent 绑定自解 —— 补齐需要给该 RPC 加 wire 字段（与
// RunParams.LLMProviderKey 同形），属协议改动。
func effectiveProviderKey(sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) string {
	var sessKey, agentKey string
	if sess != nil {
		sessKey = sess.ProviderKey
	}
	if be != nil {
		agentKey = be.LLMProviderKey
	}
	return view.FirstNonEmpty(sessKey, agentKey)
}

// providerKeyOf 是「本轮解析出来的这家供应商的 key」，nil（CLI 自身登录态，没有任何
// 供应商）返回空串。turn 侧把它交给网关 token 用：解析后的 prov 才是真正会被请求打到的
// 那家（会话所选缺失/停用时已回退过），拿 effectiveProviderKey 的原始值反而会让回退的
// 那一轮在网关上 502。
func providerKeyOf(prov *llm_provider_entity.LLMProvider) string {
	if prov == nil {
		return ""
	}
	return prov.ProviderKey
}

// resolveEffectiveProvider 把 effectiveProviderKey 解析成 provider 实体，供**展示侧**
// 消费点（LoadSession 的供应商类型/上下文窗口、复制启动命令）使用。
//
// 会话所选供应商缺失/停用/与后端 kind 不兼容时回落 agent 绑定 —— 与 turn 侧
// sessionProviderOverride 同一段代码、同一套回退语义，展示因此不会与真正会执行的那家
// 供应商说两套话（回退 notice 只在真正跑轮时产出，展示侧丢弃）。
//
// 返回的 error 只来自 agent 绑定那一次查询：调用方（复制启动命令）沿用既有的「查询
// 失败即报错」；会话所选 key 的查询失败按回退处理，不把展示整块打挂。
func (s *chatSvc) resolveEffectiveProvider(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
) (*llm_provider_entity.LLMProvider, error) {
	if be == nil {
		return nil, nil
	}
	var base *llm_provider_entity.LLMProvider
	if be.LLMProviderKey != "" {
		var err error
		base, err = llm_provider_repo.LLMProvider().FindByKey(ctx, be.LLMProviderKey)
		if err != nil {
			return nil, err
		}
	}
	if sess == nil || strings.TrimSpace(sess.ProviderKey) == "" {
		return base, nil
	}
	prov, _, err := s.sessionProviderOverride(ctx, be, sess.ProviderKey, sess.ModelKey, base)
	if err != nil {
		// 展示路径容忍 fixed-model 目标失效：strict-block 只在真正跑轮时强制（决策 7），
		// LoadSession / 复制启动命令不能因为目标失效就整块打挂 —— 前端 Picker 的
		// invalid 标记（目录里解析不出 target）负责呈现「目标已失效」。这里回落到 agent
		// 绑定供头部展示，next 轮仍会被 turn 入口严格阻止。
		return base, nil //nolint:nilerr // 展示路径故意容忍目标失效，不把 LoadSession 整块打挂
	}
	return prov, nil
}

// SetChatSessionModelTarget 切换已有会话的 LLM ModelTarget（spec 2026-08-11 决策 1）。
//
// 只写 chat_sessions.provider_key + model_key 两列（同一原子语句）：agent / backend /
// cli_path / reasoning_effort / permission mode / cwd / 钉住的执行目标一律不动（硬不变量
// 2），也不写回 agent 绑定。允许在轮中调用：本轮已 spawn 的子进程不受影响，新 target 自
// 下一轮生效（决策 8），所以这里不加 turn 锁、也不 evict 任何东西。
//
// 校验与新建会话同一套口径（validateSessionModelTarget）：provider-default 校验 provider
// 存在 / active / enabled / 与后端 kind 兼容；fixed-model 再校验 model 存在 / enabled /
// 归属该 provider；inherit-agent（双空）不校验。不通过一律拒绝写库并原样报错，会话保持原
// target —— 不产生「写进去了但下一轮必然失败」的会话。
//
// no-op：选中当前已生效的同一**完整组合**（ProviderKey + ModelKey 都相同）不写库、也不
// 追加 notice —— 否则每点一次就往 transcript 里塞一条「已改用 X」。（spec 决策 1：组合
// 比较，不能只比 providerKey。）
//
// 错误码：
//   - SessionID <= 0 → InvalidParameter
//   - 会话不存在 → ChatSessionNotFound
//   - agent/backend 解析不出来 → AgentNotFound / ChatAgentNoBackend
//   - 所选 provider/model 不可用 → ChatAgentNotChattable / LLMProviderModel* 系列
func (s *chatSvc) SetChatSessionModelTarget(ctx context.Context, req *SetChatSessionModelTargetRequest) (*SetChatSessionModelTargetResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	providerKey := strings.TrimSpace(req.ProviderKey)
	modelKey := strings.TrimSpace(req.ModelKey)

	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", req.SessionID))
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	be, err := sessionProviderBackend(ctx, sess)
	if err != nil {
		return nil, err
	}
	// 选中的就是当前生效的同一完整组合（弹层里点已选中的行）：什么都不变，不写库也不
	// 追加 notice。组合比较：同一 provider 的 provider-default 与 fixed-model 是两个不同
	// 目标，不能因为 providerKey 相同就误判为 no-op。
	if providerKey == sess.ProviderKey && modelKey == sess.ModelKey {
		return &SetChatSessionModelTargetResponse{
			ProviderKey: providerKey, ModelKey: modelKey,
			AgentProviderKey: be.LLMProviderKey, AgentModelKey: be.LLMModelKey,
		}, nil
	}
	// 校验顺带解析出的实体留着给 notice 当展示名（2026-08-10 显示缺陷修复决策 1）：
	// 校验已经查过一次，不再为取名字重复查询。
	prov, model, err := s.validateSessionModelTarget(ctx, be, providerKey, modelKey)
	if err != nil {
		return nil, err
	}
	if err := chat_repo.Session().UpdateModelTarget(ctx, sess.ID, providerKey, modelKey); err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", sess.ID))
	}
	sess.ProviderKey = providerKey
	sess.ModelKey = modelKey
	logger.Ctx(ctx).Info("chat_svc.SetChatSessionModelTarget: session model target switched",
		zap.Int64("sessionId", sess.ID),
		zap.String("providerKey", providerKey),
		zap.String("modelKey", modelKey),
		zap.String("agentProviderKey", be.LLMProviderKey),
		zap.String("backendType", be.Type))
	s.appendProviderSwitchNotice(ctx, sess, be, providerKey, modelKey, view.ProviderDisplayName(prov), view.ModelDisplayName(model))
	return &SetChatSessionModelTargetResponse{
		ProviderKey: providerKey, ModelKey: modelKey,
		AgentProviderKey: be.LLMProviderKey, AgentModelKey: be.LLMModelKey,
	}, nil
}

// validateSessionModelTarget 校验一个将要持久化的会话 ModelTarget（新建随首条消息落库、
// 已有会话经 SetChatSessionModelTarget），spec 2026-08-11 决策 2/3：
//   - 双空（inherit-agent）→ 不校验，返回 (nil, nil, nil)；
//   - providerKey 非空 → provider 必须存在、IsActive、IsEnabled 且与后端 kind 兼容；
//   - 双非空（fixed-model）→ 再校验 model 存在、IsEnabled 且归属该 provider。
//
// 返回解析出的 provider 与 model（仅 fixed-model 时有值），供 notice 展示名使用 —— 校验
// 已查过一次，不再为取名字重复查询。
func (s *chatSvc) validateSessionModelTarget(ctx context.Context, be *agent_backend_entity.AgentBackend, providerKey, modelKey string) (*llm_provider_entity.LLMProvider, *llm_provider_model_entity.LLMProviderModel, error) {
	key := strings.TrimSpace(providerKey)
	mk := strings.TrimSpace(modelKey)
	if key == "" {
		// fixed-model 必须有 provider：modelKey 单独出现是畸形目标，拒绝写库，
		// 否则会产生「没有 provider 的固定模型」这种下一轮必失败的会话。
		if mk != "" {
			return nil, nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		return nil, nil, nil
	}
	prov, err := s.validateNewSessionProvider(ctx, be, key)
	if err != nil {
		return nil, nil, err
	}
	// provider-default / fixed-model 都要在下一轮经 ResolveTarget 解析：那里看的是
	// IsEnabled（独立于 status 软删除），切换时就把停用的供应商拦下，避免写进去但下一轮
	// 必然失败。
	if !prov.IsEnabled() {
		return nil, nil, i18n.NewError(ctx, code.ChatAgentNotChattable)
	}
	if mk == "" {
		return prov, nil, nil
	}
	m, err := llm_provider_repo.LLMProvider().FindModelByKey(ctx, mk)
	if err != nil {
		return nil, nil, operationFailedWithCause(ctx, err)
	}
	if m == nil {
		return nil, nil, i18n.NewError(ctx, code.LLMProviderModelNotFound)
	}
	if !m.IsEnabled() {
		return nil, nil, i18n.NewError(ctx, code.LLMProviderModelDisabled)
	}
	if m.ProviderID != prov.ID {
		return nil, nil, i18n.NewError(ctx, code.LLMProviderModelNotOwned)
	}
	return prov, m, nil
}

// sessionProviderBackend 解析这条会话「下一轮落在哪一档」的 backend：钉住的那一档优先
// （R15b / 决策36），否则 Agent 的默认档 —— 与 GetLaunchCommand / LoadSession 展示取的
// 是同一档，切换校验因此对的是用户真正会跑的那个后端 kind。只读仓储，无副作用。
func sessionProviderBackend(ctx context.Context, sess *chat_entity.Session) (*agent_backend_entity.AgentBackend, error) {
	a, err := agent_repo.Agent().Find(ctx, sess.AgentID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("agentId", sess.AgentID))
	}
	if a == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	backendID := sessionBackendID(sess, a)
	if backendID <= 0 {
		return nil, i18n.NewError(ctx, code.ChatAgentNoBackend)
	}
	be, err := agent_backend_repo.AgentBackend().Find(ctx, backendID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("agentBackendId", backendID))
	}
	if be == nil {
		return nil, i18n.NewError(ctx, code.ChatAgentNoBackend)
	}
	return be, nil
}

// appendProviderSwitchNotice 在 transcript 追加一条持久 notice，标出「从这里起换了
// ModelTarget」（决策 9）：两家供应商配同一个 model 时，逐条消息的 model 字段看不出分界。
// 负载与既有回退 notice 同构（结构化 JSON + 前端 t() 渲染，不把原始 JSON 泄漏给前端），
// 仍用 kind=switch，仅扩展 providerKey/modelKey + 展示名。
//
// 落库失败只记日志：切换本身已经成功落库，为了一条提示把整个切换报成失败，会让用户以为
// 没切成（实际下一轮已经换了）——这里的降级方向必须是「少一条提示」。
func (s *chatSvc) appendProviderSwitchNotice(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	providerKey string,
	modelKey string,
	providerName string,
	modelName string,
) {
	msg := &chat_entity.Message{
		SessionID:         sess.ID,
		DeviceFingerprint: be.DeviceFingerprint,
		Role:              "assistant",
		BlocksJSON:        "[]",
	}
	if err := msg.SetBlocks([]blocks.ContentBlock{blocks.NoticeBlock{
		Level: "info",
		Text:  view.EncodeProviderSwitch(providerKey, modelKey, providerName, modelName),
	}}); err != nil {
		logger.Ctx(ctx).Warn("chat_svc.appendProviderSwitchNotice: encode notice failed",
			zap.Int64("sessionId", sess.ID), zap.Error(err))
		return
	}
	seq, err := chat_repo.Message().NextSeq(ctx, sess.ID)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.appendProviderSwitchNotice: next seq failed",
			zap.Int64("sessionId", sess.ID), zap.Error(err))
		return
	}
	msg.Seq = seq
	if err := chat_repo.Message().Create(ctx, msg); err != nil {
		logger.Ctx(ctx).Warn("chat_svc.appendProviderSwitchNotice: persist notice failed",
			zap.Int64("sessionId", sess.ID), zap.Error(err))
	}
}
