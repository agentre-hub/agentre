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
	SessionID         int64
	AgentID           int64
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
