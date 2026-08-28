package ctl_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
)

//go:generate mockgen -source gateway.go -destination mock_ctl_svc/mock_gateway.go

// AgentGateway 控制 API 对 agent 数据的窄依赖(ISP)。agent_repo.Agent() 直接满足。
// subagent_svc 有一份方法集相同的窄依赖(subagent_svc.AgentGateway);两者各自声明是
// 有意的(ISP 由消费方各自声明其窄依赖),不要为了「去重」把 ctl_svc 改成依赖 subagent_svc
// ——那会让控制 API 平白多背一条与其无关的服务间耦合。
type AgentGateway interface {
	Find(ctx context.Context, id int64) (*agent_entity.Agent, error)
	FindByName(ctx context.Context, name string) (*agent_entity.Agent, error)
	List(ctx context.Context) ([]*agent_entity.Agent, error)
}

// ProjectInfo 是控制 API 回传的扁平项目信息(与 project_svc 的树形态解耦)。
//
// LocalPathMissing 显式标注「本机未配置路径」(R10):Path 为空时,调用方必须靠
// 这个字段判断是"本来就没配"还是别的情况,不能靠 Path == "" 推断(决策 21)。
type ProjectInfo struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Path             string `json:"path"`
	LocalPathMissing bool   `json:"localPathMissing,omitempty"`
}

// ProjectGateway 控制 API 对项目列表的窄依赖：拿到扁平化的可派发项目。
type ProjectGateway interface {
	List(ctx context.Context) ([]ProjectInfo, error)
}

// ChatGateway 控制 API 对 chat_svc 的窄依赖,即 chat_svc 在其服务接口旁声明的无头运行
// 端口(建会话 → 起轮 →(可选)读最终文本 → 停止,外加 SessionProjectID)。subagent_svc
// 的子 agent 工具对 chat_svc 的依赖是同一个端口(多用到 SessionProjectID)。
type ChatGateway = chat_svc.HeadlessRunnerGateway

// ---- 生产实现(供 bootstrap 接线) ----

// ChatSvcGateway 生产用 chat_svc 网关(供 bootstrap 接线)。
func ChatSvcGateway() ChatGateway { return chat_svc.HeadlessRunnerSvcGateway() }

// projectSvcGateway 把 project_svc 的项目树扁平化为 ProjectInfo 列表。
type projectSvcGateway struct{}

func (projectSvcGateway) List(ctx context.Context) ([]ProjectInfo, error) {
	tree, err := project_svc.Default().ListTree(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectInfo, 0, len(tree))
	var walk func(nodes []*project_svc.ProjectNode)
	walk = func(nodes []*project_svc.ProjectNode) {
		for _, n := range nodes {
			if n == nil || n.Project == nil {
				continue
			}
			out = append(out, ProjectInfo{
				ID:               n.Project.ID,
				Name:             n.Project.Name,
				Path:             n.Project.Path,
				LocalPathMissing: n.Project.LocalPathMissing,
			})
			walk(n.Children)
		}
	}
	walk(tree)
	return out, nil
}

// ProjectSvcGateway 生产用项目网关(供 bootstrap 接线)。
func ProjectSvcGateway() ProjectGateway { return projectSvcGateway{} }
