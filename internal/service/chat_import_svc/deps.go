package chat_import_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

//go:generate mockgen -source deps.go -destination mock_deps/mock_deps.go -package mock_deps

// SessionPort is chat_import_svc's narrow view of chat_repo.SessionRepo (ISP): only the
// session bookkeeping a disk-transcript import needs — create the row, patch it once the
// replay finishes, and dedupe against provider session ids — not the full ~40-method
// surface. chat_repo.Session() satisfies it structurally.
type SessionPort interface {
	Create(ctx context.Context, s *chat_entity.Session) error
	Update(ctx context.Context, s *chat_entity.Session) error
	ListIDsByProviderSessions(ctx context.Context, providerSessionIDs []string) (map[string]int64, error)
}

// MessagePort is chat_import_svc's narrow view of chat_repo.MessageRepo (ISP): only
// message creation, since a replayed turn is written once and never revisited.
// chat_repo.Message() satisfies it structurally.
type MessagePort interface {
	Create(ctx context.Context, m *chat_entity.Message) error
}

// AgentPort is chat_import_svc's narrow view of agent_repo.AgentRepo (ISP): only the
// lookup needed to check the target agent has a backend. agent_repo.Agent() satisfies
// it structurally.
type AgentPort interface {
	Find(ctx context.Context, id int64) (*agent_entity.Agent, error)
}

// AgentBackendPort is chat_import_svc's narrow view of agent_backend_repo.AgentBackendRepo
// (ISP): only the lookup needed to compare the agent's backend type against the
// transcript's. agent_backend_repo.AgentBackend() satisfies it structurally.
type AgentBackendPort interface {
	Find(ctx context.Context, id int64) (*agent_backend_entity.AgentBackend, error)
}

// sessionRepoDelegate / messageRepoDelegate / agentRepoDelegate / agentBackendRepoDelegate
// 生产实现:懒解析对应的仓储单例,兼容 bootstrap 接线早于 Register 的时序
// (同 ctl_svc.chatSvcGateway)。

type sessionRepoDelegate struct{}

func (sessionRepoDelegate) Create(ctx context.Context, s *chat_entity.Session) error {
	return chat_repo.Session().Create(ctx, s)
}

func (sessionRepoDelegate) Update(ctx context.Context, s *chat_entity.Session) error {
	return chat_repo.Session().Update(ctx, s)
}

func (sessionRepoDelegate) ListIDsByProviderSessions(ctx context.Context, providerSessionIDs []string) (map[string]int64, error) {
	return chat_repo.Session().ListIDsByProviderSessions(ctx, providerSessionIDs)
}

type messageRepoDelegate struct{}

func (messageRepoDelegate) Create(ctx context.Context, m *chat_entity.Message) error {
	return chat_repo.Message().Create(ctx, m)
}

type agentRepoDelegate struct{}

func (agentRepoDelegate) Find(ctx context.Context, id int64) (*agent_entity.Agent, error) {
	return agent_repo.Agent().Find(ctx, id)
}

type agentBackendRepoDelegate struct{}

func (agentBackendRepoDelegate) Find(ctx context.Context, id int64) (*agent_backend_entity.AgentBackend, error) {
	return agent_backend_repo.AgentBackend().Find(ctx, id)
}
