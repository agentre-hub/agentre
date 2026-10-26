package claudecode

import (
	"context"
	"errors"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

var _ agentruntime.CLIModelQuerier = (*Runtime)(nil)

// QueryCLIModels 与真实 CLI 登录态共用路径和环境装配，但不带后端固定模型或供应商。
func (r *Runtime) QueryCLIModels(ctx context.Context, req agentruntime.CLIModelQueryRequest) (agentruntime.CLIModelCatalog, error) {
	if req.Backend == nil {
		return agentruntime.CLIModelCatalog{}, errors.New("claudecode: backend is required")
	}
	backend := *req.Backend
	backend.LLMProviderKey = ""
	backend.LLMModelKey = ""
	backend.DefaultModel = ""
	env, err := agentruntime.BuildClaudeCodeEnv(&backend, agentruntime.CLIDeps{})
	if err != nil {
		return agentruntime.CLIModelCatalog{}, err
	}
	binary := strings.TrimSpace(backend.CLIPath)
	if binary == "" {
		binary = defaultBinary
	}
	opts := ccBuildClientOpts(ccLaunchSpec{Req: agentruntime.RunRequest{Backend: &backend}, Env: env, DefaultPermissionMode: backend.DefaultPermissionMode}, binary)
	query := r.queryModels
	if query == nil {
		query = func(ctx context.Context, c *claudecode.Client) (claudecode.ModelCatalog, error) {
			return c.QueryModels(ctx)
		}
	}
	logger.Ctx(ctx).Debug("claudecode.QueryCLIModels: starting query process", zap.Int64("backendId", backend.ID))
	result, err := query(ctx, claudecode.New(opts...))
	if err != nil {
		return agentruntime.CLIModelCatalog{}, err
	}
	logger.Ctx(ctx).Debug("claudecode.QueryCLIModels: query process completed", zap.Int64("backendId", backend.ID))
	catalog := agentruntime.CLIModelCatalog{Models: make([]agentruntime.CLIModel, 0, len(result.Models)), EffectiveModelID: result.EffectiveModel, EffectiveValue: result.EffectiveModel}
	// 版本项若不再被任何当前别名指向，归入「其他版本」；不维护硬编码版本清单。
	current := map[string]bool{}
	for _, m := range result.Models {
		if m.Value != m.ResolvedModel {
			current[m.ResolvedModel] = true
		}
	}
	for _, m := range result.Models {
		if m.Value == "default" {
			continue
		}
		catalog.Models = append(catalog.Models, agentruntime.CLIModel{Value: m.Value, DisplayName: m.DisplayName, ModelID: m.ResolvedModel, Legacy: m.Value == m.ResolvedModel && !current[m.ResolvedModel]})
		if result.EffectiveModel != "" && m.ResolvedModel == result.EffectiveModel && m.Value != m.ResolvedModel {
			catalog.EffectiveValue = m.Value
		}
	}
	return catalog, nil
}
