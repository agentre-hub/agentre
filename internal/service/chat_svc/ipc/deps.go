package ipc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// deps.go 声明 ipc 对仓储的**窄端口**(ISP,与 project_svc/deps.go 同形):这个包不是
// chat_repo / agent_repo / agent_backend_repo 的属主,只用得上下面这几个方法,不该
// 整体依赖它们各自四十来个方法的胖接口。既有仓储单例结构化满足这些端口,仓储实现与
// 包级访问器都不变,运行期行为不改。

// SessionPort is ipc's narrow view of chat_repo.SessionRepo: capability 查询要按
// sessionID 找会话,权限模式状态机要读回最新值并写 permission_mode 一列。
type SessionPort interface {
	Find(ctx context.Context, id int64) (*chat_entity.Session, error)
	UpdatePermissionMode(ctx context.Context, id int64, mode string) error
}

// MessagePort is ipc's narrow view of chat_repo.MessageRepo:只用来回看末条 assistant
// 有没有可操作的 plan 块。
type MessagePort interface {
	List(ctx context.Context, sessionID int64) ([]*chat_entity.Message, error)
}

// AgentPort is ipc's narrow view of agent_repo.AgentRepo。
type AgentPort interface {
	Find(ctx context.Context, id int64) (*agent_entity.Agent, error)
}

// AgentBackendPort is ipc's narrow view of agent_backend_repo.AgentBackendRepo。
type AgentBackendPort interface {
	Find(ctx context.Context, id int64) (*agent_backend_entity.AgentBackend, error)
}

// 生产实现:懒解析包级仓储单例,兼容 bootstrap 接线晚于本包初始化的时序
// (同 project_svc.sessionRepoDelegate)。
type sessionRepoDelegate struct{}

func (sessionRepoDelegate) Find(ctx context.Context, id int64) (*chat_entity.Session, error) {
	return chat_repo.Session().Find(ctx, id)
}

func (sessionRepoDelegate) UpdatePermissionMode(ctx context.Context, id int64, mode string) error {
	return chat_repo.Session().UpdatePermissionMode(ctx, id, mode)
}

type messageRepoDelegate struct{}

func (messageRepoDelegate) List(ctx context.Context, sessionID int64) ([]*chat_entity.Message, error) {
	return chat_repo.Message().List(ctx, sessionID)
}

type agentRepoDelegate struct{}

func (agentRepoDelegate) Find(ctx context.Context, id int64) (*agent_entity.Agent, error) {
	return agent_repo.Agent().Find(ctx, id)
}

type agentBackendRepoDelegate struct{}

func (agentBackendRepoDelegate) Find(ctx context.Context, id int64) (*agent_backend_entity.AgentBackend, error) {
	return agent_backend_repo.AgentBackend().Find(ctx, id)
}

// capability 查询是包级自由函数(Wails 直接绑定),端口因此以包级变量持有;
// 状态机那侧走构造函数注入,见 NewPermissionModeController。
var (
	capabilitySessions SessionPort      = sessionRepoDelegate{}
	capabilityAgents   AgentPort        = agentRepoDelegate{}
	capabilityBackends AgentBackendPort = agentBackendRepoDelegate{}
)
