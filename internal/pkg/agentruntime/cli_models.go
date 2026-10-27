package agentruntime

//go:generate mockgen -source cli_models.go -destination mock_agentruntime/mock_cli_models.go

import (
	"context"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// CLIModelQueryTimeout 是一次 CLI 模型查询（起短命进程到读完目录）的上限。调用方
// 常常没有 deadline（Wails 调用、开轮前解析 CLI 默认），卡住的 CLI 不能无限期占着
// 会话的切换锁或开轮。
const CLIModelQueryTimeout = 15 * time.Second

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

// 会话 CLI 三态的取值（chat_entity.ValidCLIModelChoice 校验）；空串 = 跟随 Agent 绑定。
const (
	CLIModelModeCLIDefault = "cli-default"
	CLIModelModeFixed      = "fixed"
)

// CLIModelChoiceValue 把会话三态折成交给 CLI 的值：固定 → 所选值；跟随 CLI 默认 → 空串；
// 跟随 Agent 绑定 → 后端默认模型（空 = CLI 默认）。各 runtime 的账本、启动参数与
// chat_svc 判「挂起」都用这一份，避免各自修剪/回退口径漂移。
func CLIModelChoiceValue(mode, value, backendDefault string) string {
	switch mode {
	case CLIModelModeFixed:
		return strings.TrimSpace(value)
	case CLIModelModeCLIDefault:
		return ""
	default:
		return strings.TrimSpace(backendDefault)
	}
}

// CLIModelChoiceValue 是本轮请求的会话口径选择值（见同名包级函数）。
func (r RunRequest) CLIModelChoiceValue() string {
	backendDefault := ""
	if r.Backend != nil {
		backendDefault = r.Backend.DefaultModel
	}
	return CLIModelChoiceValue(r.CLIModelMode, r.CLIModel, backendDefault)
}
