package piagent

import (
	"context"
	"errors"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	pkgpiagent "github.com/agentre-hub/agentre/pkg/piagent"
)

var _ agentruntime.CLIModelQuerier = (*Runtime)(nil)

// QueryCLIModels 与真实 CLI 登录态共用 CLI 路径与环境装配，但不带供应商、不带后端
// 默认模型、不挂会话：查询进程只发 get_available_models / get_state，不消耗 token。
// 绝不问会话在跑的进程 —— 它可能是以某个模型启动或被切换过的。
func (r *Runtime) QueryCLIModels(ctx context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
	if req.Backend == nil {
		return agentruntime.CLIModelCatalog{}, errors.New("piagent: backend is required")
	}
	backend := *req.Backend
	backend.LLMProviderKey = ""
	backend.LLMModelKey = ""
	backend.DefaultModel = ""
	env, err := agentruntime.BuildPiAgentEnv(&backend, agentruntime.CLIDeps{})
	if err != nil {
		return agentruntime.CLIModelCatalog{}, err
	}
	binary := strings.TrimSpace(backend.CLIPath)
	if binary == "" {
		binary = defaultBinary
	}
	client := pkgpiagent.New(
		pkgpiagent.WithBinary(binary),
		pkgpiagent.WithEnv(env),
		pkgpiagent.WithRawSink(piRawFrameSink(0, "")),
	)
	query := r.queryModels
	if query == nil {
		query = func(ctx context.Context, c *pkgpiagent.Client) (pkgpiagent.ModelCatalog, error) {
			return c.QueryModels(ctx)
		}
	}
	logger.Ctx(ctx).Debug("piagent.QueryCLIModels: starting query process", zap.Int64("backendId", backend.ID))
	result, err := query(ctx, client)
	if err != nil {
		return agentruntime.CLIModelCatalog{}, err
	}
	logger.Ctx(ctx).Debug("piagent.QueryCLIModels: query process completed", zap.Int64("backendId", backend.ID))
	catalog := agentruntime.CLIModelCatalog{Models: make([]agentruntime.CLIModel, 0, len(result.Models))}
	for _, m := range result.Models {
		value := piModelValue(m)
		if value == "" {
			continue
		}
		// Value 是交给 pi 的 provider/id；同一个 pi 供应商的模型在 Picker 里分到同一小节。
		catalog.Models = append(catalog.Models, agentruntime.CLIModel{
			Value:       value,
			DisplayName: piModelDisplayName(m),
			ModelID:     piModelID(m),
			Group:       strings.TrimSpace(m.Provider),
		})
	}
	// 生效模型只来自 get_state：解析不出就留空，不猜列表里的第一项。
	if value := piModelValue(result.Effective); value != "" {
		catalog.EffectiveValue = value
		catalog.EffectiveModelID = piModelID(result.Effective)
	}
	return catalog, nil
}

// piModelValue 拼出交给 pi 的模型值：set_model 拆成 {provider, modelId}，CLI 的
// --model 认的是 "provider/id"。provider 缺失时退回裸 id（用户自定义单供应商配置）。
func piModelValue(m pkgpiagent.Model) string {
	id := strings.TrimSpace(m.ID)
	if id == "" {
		return ""
	}
	provider := strings.TrimSpace(m.Provider)
	if provider == "" {
		return id
	}
	return provider + "/" + id
}

func piModelID(m pkgpiagent.Model) string { return strings.TrimSpace(m.ID) }

// piModelDisplayName 没给展示名时退回模型 id（不拼 provider，副行另有模型 ID）。
func piModelDisplayName(m pkgpiagent.Model) string {
	if name := strings.TrimSpace(m.Name); name != "" {
		return name
	}
	return piModelID(m)
}
