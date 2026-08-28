package chat_svc

import (
	"context"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
)

// effective_llm.go 是 EffectiveLLMConfig v1 解析口（spec「Effective configuration,
// Gateway and Runtime」决策 8）：所有执行入口（Send / Regenerate / Edit / Compact /
// Goal / 复制启动命令 / 网关 token 路由）通过唯一的 EffectiveLLMConfig 决定实际
// Provider 与 Model，Runtime / Gateway / 展示路径不得各自重新拼装优先级。

// effectiveLLMForTurn 在 turn 入口已解析出的有效 Provider（含 #39 回退，见
// sessionProviderOverride）之上，通过 llm_provider_svc.ResolveTarget 解析其模型，
// 组装执行侧 EffectiveLLMConfig v1。
//
//   - prov == nil（CLI 登录态 / 无任何供应商）→ native 配置（所有模型字段为空）；
//   - prov != nil && modelKey == "" → provider-default：经 ResolveTarget 解析 Provider 当前默认模型；
//   - prov != nil && modelKey != "" → fixed-model：经 ResolveTarget 解析指定 Model 记录（Backend 固定模型）。
//
// 解析失败（Provider 存在但没有合法启用默认模型 / 固定模型不存在或停用等配置损坏）返回 error，
// 由调用方阻止本轮，不静默降级（spec 决策 7）。ResolveTarget 是 Go 内部解析口，携带明文 APIKey /
// BaseURL 供执行侧使用，不通过 Wails 绑定暴露给前端。
func (s *chatSvc) effectiveLLMForTurn(ctx context.Context, prov *llm_provider_entity.LLMProvider, modelKey string) (*agentruntime.EffectiveLLMConfig, error) {
	if prov == nil {
		return agentruntime.NewKeysOnlyEffectiveLLMConfig("", ""), nil
	}
	target := llm_provider_svc.ModelTarget{ProviderKey: prov.ProviderKey, ModelKey: strings.TrimSpace(modelKey)}
	resolved, err := llm_provider_svc.LLMProvider().ResolveTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	// 装配走 agentruntime 的唯一构造口(task 6):Mode 由**本轮请求的** TargetModelKey
	// 决定,不能拿解析结果的 ModelKey 算 —— provider-default 解析出的默认模型同样带 key。
	return agentruntime.NewEffectiveLLMConfig(agentruntime.EffectiveLLMConfigInput{
		ProviderKey:      resolved.ProviderKey,
		ProviderType:     resolved.ProviderType,
		ProviderName:     prov.Name,
		TargetModelKey:   target.ModelKey,
		ResolvedModelKey: resolved.ModelKey,
		ResolvedModelID:  resolved.ModelID,
		ContextWindow:    resolved.ContextWindow,
		MaxOutput:        resolved.MaxOutput,
		BaseURL:          resolved.BaseURL,
		APIKey:           resolved.APIKey,
		HasAPIKey:        resolved.HasAPIKey,
	}), nil
}

// effectiveLLMForNonRemoteTurn 是 turn 入口用变体：远端 backend 由 daemon 自家解析
// （desktop 本地 provider 表反映不了 daemon 配置，wire 只透传 key），所以返回一个
// 只含目标 key 的 keys-only 配置（不做本地模型解析，也不让本地模型配置损坏阻塞远端轮）；
// 非远端后端直接委托 effectiveLLMForTurn，并带上本轮该用的 ModelKey（sessionModelKeyFor，
// spec 2026-08-11 决策 1）：会话钉了 provider 时用会话的 ModelKey（空 = provider-default，
// 非空 = fixed-model），未钉（inherit-agent）或会话 provider 已回退时才跟随 backend 的固定
// ModelKey。
//
// keys-only 配置只填 Mode / ProviderKey / ModelKey（task 6 决策 11）：daemon 按 wire
// 的 key 从自家目录解析真实 Provider/Model，desktop 不透传解析结果或任何凭证。
func (s *chatSvc) effectiveLLMForNonRemoteTurn(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend, prov *llm_provider_entity.LLMProvider) (*agentruntime.EffectiveLLMConfig, error) {
	if beTargetsRemote(be) {
		return remoteKeysOnlyEffective(sess, be), nil
	}
	return s.effectiveLLMForTurn(ctx, prov, sessionModelKeyFor(sess, be, prov))
}

// remoteKeysOnlyEffective 组装远端执行的 keys-only 目标（决策 11）：会话钉了
// provider 时用会话的 ProviderKey/ModelKey，未钉时跟随 backend 主绑定。
func remoteKeysOnlyEffective(sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) *agentruntime.EffectiveLLMConfig {
	var providerKey, modelKey string
	if sess != nil && strings.TrimSpace(sess.ProviderKey) != "" {
		providerKey = sess.ProviderKey
		modelKey = sess.ModelKey
	} else if be != nil && strings.TrimSpace(be.LLMProviderKey) != "" {
		providerKey = be.LLMProviderKey
		modelKey = be.LLMModelKey
	}
	return agentruntime.NewKeysOnlyEffectiveLLMConfig(providerKey, modelKey)
}

// sessionModelKeyFor 返回本轮解析用的 ModelKey（spec 2026-08-11 决策 1）：
//   - 会话钉了 Provider（inherit-agent 之外）且当前生效 provider 就是会话钉的那家：用会话
//     的 ModelKey —— 空 = provider-default（每轮解析该 Provider 当前默认，不能被 backend
//     的固定模型带偏），非空 = fixed-model（解析指定子模型）；
//   - 会话未钉（inherit-agent），或会话所钉 provider 缺失/停用已回退到 agent 绑定：跟随
//     backend 绑定 —— backend 钉了固定模型且生效 provider 就是 backend 自家绑定的 provider
//     时沿用该固定模型，否则 provider-default。
//
// 回退分支特别重要：会话 ModelKey 属于那家已失效的供应商，绝不能在回退后拿去解析
// （否则会解析出错误模型或 ModelNotOwned 硬失败）。
func sessionModelKeyFor(sess *chat_entity.Session, be *agent_backend_entity.AgentBackend, prov *llm_provider_entity.LLMProvider) string {
	if sess != nil && strings.TrimSpace(sess.ProviderKey) != "" &&
		prov != nil && prov.ProviderKey == sess.ProviderKey {
		return sess.ModelKey
	}
	return backendModelKeyFor(be, prov)
}

// backendModelKeyFor 返回 backend 固定模型的 ModelKey；仅当 effective provider 就是
// backend 自家绑定的 provider 时才适用。会话把 provider 覆盖到别家时，backend 的固定
// 模型不适用于那家（避免把 fixed model 拿去解析另一家，造成 ModelNotOwned 硬失败）。
func backendModelKeyFor(be *agent_backend_entity.AgentBackend, prov *llm_provider_entity.LLMProvider) string {
	if be == nil || prov == nil {
		return ""
	}
	if be.LLMProviderKey != "" && be.LLMProviderKey == prov.ProviderKey {
		return be.LLMModelKey
	}
	return ""
}
