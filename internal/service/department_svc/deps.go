package department_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
)

//go:generate mockgen -source deps.go -destination mock_deps/mock_deps.go -package mock_deps

// AgentPort is department_svc's narrow view of agent_repo.AgentRepo (ISP): only the
// agent lookups/mutations this package needs to load the org summary and to
// move/reparent/delete departments, not the full ~19-method surface.
// agent_repo.Agent() satisfies it structurally.
type AgentPort interface {
	List(ctx context.Context) ([]*agent_entity.Agent, error)
	Find(ctx context.Context, id int64) (*agent_entity.Agent, error)
	ListByDepartment(ctx context.Context, departmentID int64) ([]*agent_entity.Agent, error)
	FindSystem(ctx context.Context) (*agent_entity.Agent, error)
	UpdatePlacement(ctx context.Context, id, departmentID, parentAgentID int64, sortOrder int) error
	Delete(ctx context.Context, id int64) error
}

// AgentExecTargetPort is department_svc's narrow view of agent_repo.AgentExecTargetRepo
// (ISP): only the batch lookup the org summary needs. agent_repo.AgentExecTarget()
// satisfies it structurally.
type AgentExecTargetPort interface {
	ListByAgents(ctx context.Context, agentIDs []int64) (map[int64][]*agent_entity.AgentExecTarget, error)
}

// AgentBackendPort is department_svc's narrow view of agent_backend_repo.AgentBackendRepo
// (ISP): only the listing the org summary needs. agent_backend_repo.AgentBackend()
// satisfies it structurally.
type AgentBackendPort interface {
	List(ctx context.Context) ([]*agent_backend_entity.AgentBackend, error)
}

// LLMProviderPort is department_svc's narrow view of llm_provider_repo.LLMProviderRepo
// (ISP): only the listing + model lookup the org summary needs. llm_provider_repo.LLMProvider()
// satisfies it structurally.
type LLMProviderPort interface {
	List(ctx context.Context) ([]*llm_provider_entity.LLMProvider, error)
	FindModelByKey(ctx context.Context, modelKey string) (*llm_provider_model_entity.LLMProviderModel, error)
}

// agentRepoDelegate / agentExecTargetRepoDelegate / agentBackendRepoDelegate /
// llmProviderRepoDelegate 生产实现:懒解析对应的仓储单例,兼容 bootstrap 接线早于
// Register 的时序(同 ctl_svc.chatSvcGateway)。

type agentRepoDelegate struct{}

func (agentRepoDelegate) List(ctx context.Context) ([]*agent_entity.Agent, error) {
	return agent_repo.Agent().List(ctx)
}

func (agentRepoDelegate) Find(ctx context.Context, id int64) (*agent_entity.Agent, error) {
	return agent_repo.Agent().Find(ctx, id)
}

func (agentRepoDelegate) ListByDepartment(ctx context.Context, departmentID int64) ([]*agent_entity.Agent, error) {
	return agent_repo.Agent().ListByDepartment(ctx, departmentID)
}

func (agentRepoDelegate) FindSystem(ctx context.Context) (*agent_entity.Agent, error) {
	return agent_repo.Agent().FindSystem(ctx)
}

func (agentRepoDelegate) UpdatePlacement(ctx context.Context, id, departmentID, parentAgentID int64, sortOrder int) error {
	return agent_repo.Agent().UpdatePlacement(ctx, id, departmentID, parentAgentID, sortOrder)
}

func (agentRepoDelegate) Delete(ctx context.Context, id int64) error {
	return agent_repo.Agent().Delete(ctx, id)
}

type agentExecTargetRepoDelegate struct{}

func (agentExecTargetRepoDelegate) ListByAgents(ctx context.Context, agentIDs []int64) (map[int64][]*agent_entity.AgentExecTarget, error) {
	return agent_repo.AgentExecTarget().ListByAgents(ctx, agentIDs)
}

type agentBackendRepoDelegate struct{}

func (agentBackendRepoDelegate) List(ctx context.Context) ([]*agent_backend_entity.AgentBackend, error) {
	return agent_backend_repo.AgentBackend().List(ctx)
}

type llmProviderRepoDelegate struct{}

func (llmProviderRepoDelegate) List(ctx context.Context) ([]*llm_provider_entity.LLMProvider, error) {
	return llm_provider_repo.LLMProvider().List(ctx)
}

func (llmProviderRepoDelegate) FindModelByKey(ctx context.Context, modelKey string) (*llm_provider_model_entity.LLMProviderModel, error) {
	return llm_provider_repo.LLMProvider().FindModelByKey(ctx, modelKey)
}
