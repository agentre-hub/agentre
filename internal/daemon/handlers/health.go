package handlers

import (
	"context"
	"sort"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// HealthPingResult 是 health.ping 的返回。客户端用来探活，不修改 daemon 状态。
//
// DBSizeBytes 是这台 daemon 通知日志所在库的体量。远端盒子上的 transcript 是档案，
// 体量必须可见，用户才能判断何时清理。
// 没有库统计口时省略,而不是报 0 ——「不知道」与「库是空的」在界面上必须是两回事。
//
// **库文件的路径不在这里**。它是绝对路径,通常带着宿主机的 OS 用户名,而这条应答会发给
// 每一个已配对的对端。路径与体量只在 daemon 的本机状态查询中可见，该查询是本机
// IPC(/local/status 与 `agentred status`)—— 路径在那里,给的是这台机器前面的人。
type HealthPingResult struct {
	InstanceUUID string                 `json:"instanceUUID"`
	ServerTimeMs int64                  `json:"serverTimeMs"`
	Providers    []wire.ProviderSummary `json:"providers,omitempty"`
	// Capabilities 是这台 daemon 公布的能力位（决策 11）。
	// 至少含 wire.CapLLMModelTargetV1：桌面端据它决定是否允许选择远端 fixed-model。
	Capabilities []string `json:"capabilities,omitempty"`
	DBSizeBytes  int64    `json:"dbSizeBytes,omitempty"`
}

// HealthHandlers groups health.* RPC methods.
type HealthHandlers struct {
	instanceUUID string
	state        *state.State
	dbStat       DBStatPort
}

// NewHealthHandlers 注入当前 daemon 的 instance uuid、state 与库统计口。
// dbStat 传 nil 表示这台 daemon 不报库信息(应答里两个字段一并省略)。
func NewHealthHandlers(instanceUUID string, st *state.State, dbStat DBStatPort) *HealthHandlers {
	return &HealthHandlers{instanceUUID: instanceUUID, state: st, dbStat: dbStat}
}

// Ping 无副作用心跳；watcher 用它判活 + 推 last_seen_at。
// 顺带返回 daemon 已配置的 LLM provider 目录（含每 Provider 的非敏感模型摘要与
// llm-model-target-v1 能力位），让 desktop watcher 渲染远端可选项与同步状态，无需单独 RPC。
func (h *HealthHandlers) Ping(_ context.Context) (HealthPingResult, error) {
	snap := h.state.Snapshot()
	providers := make([]wire.ProviderSummary, 0, len(snap.LLMProviders))
	for k, v := range snap.LLMProviders {
		var models []wire.ModelSummary //nolint:prealloc // 保持无模型时 nil，避免空目录序列化成 []
		for _, m := range v.Models {
			models = append(models, wire.ModelSummary{
				Key: m.ModelKey, ModelID: m.ModelID, Name: m.Name, Enabled: m.Enabled,
			})
		}
		providers = append(providers, wire.ProviderSummary{
			Key:             k,
			Name:            v.Name,
			Type:            v.Type,
			DefaultModelKey: v.DefaultModelKey,
			Models:          models,
		})
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].Key < providers[j].Key
	})
	res := HealthPingResult{
		InstanceUUID: h.instanceUUID,
		ServerTimeMs: time.Now().UnixMilli(),
		Providers:    providers,
		Capabilities: []string{wire.CapLLMModelTargetV1},
	}
	if h.dbStat != nil {
		res.DBSizeBytes = h.dbStat.DBStat().SizeBytes
	}
	return res, nil
}
