// Package orgtool_svc 组织架构工具(agent 内置工具 key="org")的 MCP 接入与审批编排。
// 业务执行全部委托 department_svc / agent_svc,本包只做 token/开关校验 + 审批挂起。
package orgtool_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
)

//go:generate mockgen -source deps.go -destination mock_orgtool_svc/mock_deps.go

// OrgQuery 读组织架构(department_svc.Load 的窄投影)。
type OrgQuery interface {
	Load(ctx context.Context, req *department_svc.LoadOrgRequest) (*department_svc.LoadOrgResponse, error)
}

// DeptCommand 部门写操作(department_svc 的窄投影)。
type DeptCommand interface {
	Create(ctx context.Context, req *department_svc.CreateDepartmentRequest) (*department_svc.CreateDepartmentResponse, error)
	Update(ctx context.Context, req *department_svc.UpdateDepartmentRequest) (*department_svc.UpdateDepartmentResponse, error)
	Move(ctx context.Context, req *department_svc.MoveDepartmentRequest) (*department_svc.MoveDepartmentResponse, error)
	Delete(ctx context.Context, req *department_svc.DeleteDepartmentRequest) (*department_svc.DeleteDepartmentResponse, error)
}

// AgentCommand agent 写操作(agent_svc 的窄投影)。
type AgentCommand interface {
	Create(ctx context.Context, req *agent_svc.CreateAgentRequest) (*agent_svc.CreateAgentResponse, error)
	Update(ctx context.Context, req *agent_svc.UpdateAgentRequest) (*agent_svc.UpdateAgentResponse, error)
	Move(ctx context.Context, req *agent_svc.MoveAgentRequest) (*agent_svc.MoveAgentResponse, error)
	Delete(ctx context.Context, req *agent_svc.DeleteAgentRequest) (*agent_svc.DeleteAgentResponse, error)
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
