package codex

import (
	"context"
	"errors"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/codex"
)

var _ agentruntime.CLIModelQuerier = (*Runtime)(nil)

// QueryCLIModels 与真实 CLI 登录态共用路径和环境装配，但不带后端默认模型或供应商。
// 查询进程只发 initialize/initialized/model/list/config/read，不起 thread、不消耗 token。
func (r *Runtime) QueryCLIModels(ctx context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
	if req.Backend == nil {
		return agentruntime.CLIModelCatalog{}, errors.New("codex: backend is required")
	}
	backend := *req.Backend
	backend.LLMProviderKey = ""
	backend.LLMModelKey = ""
	backend.DefaultModel = ""
	env, err := agentruntime.BuildCodexEnv(&backend, agentruntime.CLIDeps{})
	if err != nil {
		return agentruntime.CLIModelCatalog{}, err
	}
	binary := strings.TrimSpace(backend.CLIPath)
	if binary == "" {
		binary = defaultBinary
	}
	opts := []codex.Option{
		codex.WithBinary(binary),
		codex.WithEnv(env),
		codex.WithRawSink(cxRawFrameSink(0, "")),
	}
	query := r.queryModels
	if query == nil {
		query = func(ctx context.Context, c *codex.Client) (codex.ModelCatalog, error) {
			return c.QueryModels(ctx)
		}
	}
	logger.Ctx(ctx).Debug("codex.QueryCLIModels: starting query process", zap.Int64("backendId", backend.ID))
	result, err := query(ctx, codex.New(opts...))
	if err != nil {
		return agentruntime.CLIModelCatalog{}, err
	}
	logger.Ctx(ctx).Debug("codex.QueryCLIModels: query process completed", zap.Int64("backendId", backend.ID))
	catalog := agentruntime.CLIModelCatalog{
		Models:           make([]agentruntime.CLIModel, 0, len(result.Models)),
		EffectiveValue:   result.EffectiveModel,
		EffectiveModelID: result.EffectiveModel,
	}
	for _, m := range result.Models {
		value := strings.TrimSpace(m.ID)
		if value == "" {
			value = strings.TrimSpace(m.Model)
		}
		if value == "" {
			continue
		}
		display := strings.TrimSpace(m.DisplayName)
		if display == "" {
			display = value
		}
		modelID := strings.TrimSpace(m.Model)
		if modelID == "" {
			modelID = value
		}
		catalog.Models = append(catalog.Models, agentruntime.CLIModel{Value: value, DisplayName: display, ModelID: modelID})
	}
	return catalog, nil
}
