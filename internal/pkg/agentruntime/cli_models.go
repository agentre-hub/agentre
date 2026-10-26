package agentruntime

//go:generate mockgen -source cli_models.go -destination mock_agentruntime/mock_cli_models.go

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// CLIModel 的 Value 是交给 CLI 的值，ModelID 是 CLI 解析出的实际模型。
type CLIModel struct {
	Value       string `json:"value"`
	DisplayName string `json:"displayName"`
	ModelID     string `json:"modelId"`
	Group       string `json:"group,omitempty"`
	Legacy      bool   `json:"legacy,omitempty"`
}

// CLIModelCatalog 只描述不下发模型参数时的默认值，与任何会话当前模型无关。
type CLIModelCatalog struct {
	Models           []CLIModel `json:"models"`
	EffectiveValue   string     `json:"effectiveValue"`
	EffectiveModelID string     `json:"effectiveModelId"`
}

// CLIModelQueryRequest 的 Backend 已由宿主解析到执行设备的 CLI 路径。
// 查询必须忽略供应商绑定和 DefaultModel，使用 CLI 登录态的环境。
type CLIModelQueryRequest struct {
	Backend *agent_backend_entity.AgentBackend
}

// CLIModelQuerier 总是启动独立的短命查询进程，绝不借用会话的活进程。
type CLIModelQuerier interface {
	QueryCLIModels(context.Context, CLIModelQueryRequest) (CLIModelCatalog, error)
}
