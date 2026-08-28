package subagent_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

//go:generate mockgen -source gateway.go -destination mock_subagent_svc/mock_gateway.go
//go:generate mockgen -destination mock_subagent_svc/mock_chatgateway.go -package mock_subagent_svc github.com/agentre-hub/agentre/internal/service/subagent_svc ChatGateway

// AgentGateway 子 agent 工具对 agent 数据的窄依赖(ISP)。agent_repo.Agent() 直接满足。
// ctl_svc 有一份方法集相同的窄依赖(ctl_svc.AgentGateway);两者各自声明是有意的(ISP 由
// 消费方各自声明其窄依赖),不要为了「去重」把两个服务互相 import。
type AgentGateway interface {
	Find(ctx context.Context, id int64) (*agent_entity.Agent, error)
	FindByName(ctx context.Context, name string) (*agent_entity.Agent, error)
	List(ctx context.Context) ([]*agent_entity.Agent, error)
}

// ChatGateway 子 agent 工具对 chat_svc 的窄依赖,即 chat_svc 在其服务接口旁声明的无头运行
// 端口(建会话 → 起轮 →(可选)读最终文本 → 停止,外加 SessionProjectID)。
type ChatGateway = chat_svc.HeadlessRunnerGateway

// ChatSvcGateway 生产用 chat_svc 网关(供 bootstrap 接线)。
func ChatSvcGateway() ChatGateway { return chat_svc.HeadlessRunnerSvcGateway() }
