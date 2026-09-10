// Package hooktool_svc 脚本 Hook 工具(agent 内置工具 key="hook")的 MCP 接入与审批编排。
// 业务执行全部委托 hook_svc,本包只做 token/开关校验 + 审批挂起。
package hooktool_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/hook_svc"
)

//go:generate mockgen -source deps.go -destination mock_hooktool_svc/mock_deps.go

// HookService 是 hook_svc 的窄投影(读 + CRUD + 试运行/立即运行)。
type HookService interface {
	Load(ctx context.Context, req *hook_svc.LoadHooksRequest) (*hook_svc.LoadHooksResponse, error)
	CreateHook(ctx context.Context, req *hook_svc.CreateHookRequest) (*hook_svc.HookItem, error)
	UpdateHook(ctx context.Context, req *hook_svc.UpdateHookRequest) (*hook_svc.HookItem, error)
	DeleteHook(ctx context.Context, id int64) error
	RunHook(ctx context.Context, req *hook_svc.RunHookRequest) (*hook_svc.RunHookResult, error)
}

// AgentLookup 实时校验调用者 agent 的工具开关(agent_repo 的窄投影)。
type AgentLookup interface {
	Find(ctx context.Context, id int64) (*agent_entity.Agent, error)
}

// ApprovalGateway 审批卡登记/决议(chat_svc 通用工具审批网关的窄投影)。
// Begin 返回等待 channel,waiter 与前端应答路由由 chat_svc 统一持有。
type ApprovalGateway interface {
	BeginToolApproval(ctx context.Context, sessionID int64, blk *blocks.ToolApprovalBlock) (<-chan bool, error)
	FinishToolApproval(ctx context.Context, sessionID int64, requestID, status, result string) error
}
