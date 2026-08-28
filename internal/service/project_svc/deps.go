package project_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

//go:generate mockgen -source deps.go -destination mock_project_svc/mock_deps.go

// SessionPort is project_svc's narrow view of chat_repo.SessionRepo (ISP): only the
// session bookkeeping this package needs when a project is deleted or merged away,
// not the full ~40-method surface. chat_repo.Session() satisfies it structurally.
type SessionPort interface {
	// CountActiveByProject 见 chat_repo.SessionRepo 同名方法：project_svc.Delete
	// 用它挡住「项目下还有 running/waiting 会话」。
	CountActiveByProject(ctx context.Context, projectID int64, agentStatuses []string) (int64, error)
	// ReassignProject 见 chat_repo.SessionRepo 同名方法：Delete/Merge 把幸存会话
	// 摘成自由会话或改挂到保留下来的项目。
	ReassignProject(ctx context.Context, fromProjectID, toProjectID int64) error
}

// AgentPort is project_svc's narrow view of agent_repo.AgentRepo (ISP): only the agent
// lookups this package needs to add members / aggregate project membership, not the
// full method surface. agent_repo.Agent() satisfies it structurally.
type AgentPort interface {
	Find(ctx context.Context, id int64) (*agent_entity.Agent, error)
	List(ctx context.Context) ([]*agent_entity.Agent, error)
}

// sessionRepoDelegate 生产实现:懒解析 chat_repo.Session(),兼容 bootstrap 接线早于
// RegisterSession 的时序(同 ctl_svc.chatSvcGateway)。
type sessionRepoDelegate struct{}

func (sessionRepoDelegate) CountActiveByProject(ctx context.Context, projectID int64, agentStatuses []string) (int64, error) {
	return chat_repo.Session().CountActiveByProject(ctx, projectID, agentStatuses)
}

func (sessionRepoDelegate) ReassignProject(ctx context.Context, fromProjectID, toProjectID int64) error {
	return chat_repo.Session().ReassignProject(ctx, fromProjectID, toProjectID)
}

// agentRepoDelegate 生产实现:懒解析 agent_repo.Agent()。
type agentRepoDelegate struct{}

func (agentRepoDelegate) Find(ctx context.Context, id int64) (*agent_entity.Agent, error) {
	return agent_repo.Agent().Find(ctx, id)
}

func (agentRepoDelegate) List(ctx context.Context) ([]*agent_entity.Agent, error) {
	return agent_repo.Agent().List(ctx)
}
