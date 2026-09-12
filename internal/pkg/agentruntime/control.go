package agentruntime

// 控制平面小接口:Runtime 按需实现;chat_svc 通过 type assertion 取得。
// Capabilities() 中的 bool 与"该 runtime 是否实现对应接口"必须一致(capability
// matrix 测试强制)。
//
// Steerer / Aborter / SteerCanceler / SteerDrainer / PermissionModeSetter /
// AskAnswerSink / ToolPermissionSink 仍住 runner.go。

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

type Goal struct {
	ThreadID        string `json:"threadId"`
	Objective       string `json:"objective"`
	Status          string `json:"status"`
	TokenBudget     *int   `json:"tokenBudget,omitempty"`
	TokensUsed      int    `json:"tokensUsed"`
	TimeUsedSeconds int    `json:"timeUsedSeconds"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

type GoalRequest struct {
	SessionID int64
	AgentID   int64
	// AgentSyncID 是 Agent 的账号级同步标识，与 RunRequest.AgentSyncID 同源同义：
	// Cwd 为空时的兜底目录由它定（AgentID=0 的 web 发起会话没有本地主键可用），
	// 跨机执行也只发它——agent_id 是发起端库里的自增主键，两台桌面端同号会在同一个
	// agentred 上共用目录。
	AgentSyncID       string
	ProviderSessionID string
	Backend           *agent_backend_entity.AgentBackend
	Provider          *llm_provider_entity.LLMProvider
	// Effective 是本轮执行侧唯一解析结果（EffectiveLLMConfig v1 seam），
	// 与 RunRequest.Effective 同源；goal 与 turn 共用同一个 CLI 会话池，
	// 两边解析不一致会让启动期比对键反复翻转、把在用的子进程 evict 掉。
	Effective    *EffectiveLLMConfig
	Cwd          string
	GatewayURL   string
	GatewayToken string
	Objective    *string
	Status       *string
	TokenBudget  *int
}

type GoalController interface {
	GetGoal(ctx context.Context, req GoalRequest) (*Goal, error)
	SetGoal(ctx context.Context, req GoalRequest) (*Goal, error)
	ClearGoal(ctx context.Context, req GoalRequest) (bool, error)
}
