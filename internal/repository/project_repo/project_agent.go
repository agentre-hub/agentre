package project_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
)

//go:generate mockgen -source project_agent.go -destination mock_project_repo/mock_project_agent.go

// ProjectAgentRepo Project ↔ Agent 成员关系仓储。
type ProjectAgentRepo interface {
	Add(ctx context.Context, projectID, agentID int64) error
	// CreateFromSync 落一行下行来的成员关系，沿用它自己的同步标识（R1：跨机身份
	// 终身不变，不能像 Add 那样就地铸一个新的）。
	CreateFromSync(ctx context.Context, row *project_entity.ProjectAgent) error
	Remove(ctx context.Context, projectID, agentID int64) error
	ListByProject(ctx context.Context, projectID int64) ([]*project_entity.ProjectAgent, error)
	ListByProjects(ctx context.Context, projectIDs []int64) (map[int64][]*project_entity.ProjectAgent, error)
	ListByAgent(ctx context.Context, agentID int64) ([]*project_entity.ProjectAgent, error)
}

var defaultProjectAgent ProjectAgentRepo

func ProjectAgent() ProjectAgentRepo             { return defaultProjectAgent }
func RegisterProjectAgent(impl ProjectAgentRepo) { defaultProjectAgent = impl }
func NewProjectAgent() ProjectAgentRepo          { return &projectAgentRepo{} }

type projectAgentRepo struct{}

func (r *projectAgentRepo) Add(ctx context.Context, projectID, agentID int64) error {
	row := &project_entity.ProjectAgent{
		ProjectID: projectID,
		AgentID:   agentID,
		JoinedAt:  time.Now().UnixMilli(),
	}
	// 同步标识在行创建时就地生成，未登录期间也照常写入（R1/R12a）。
	row.EnsureSyncID()
	// 联合主键存在时报错 —— service 层用 ListByProject 预检或忽略；此 repo 不做 upsert。
	return db.Ctx(ctx).Create(row).Error
}

func (r *projectAgentRepo) CreateFromSync(ctx context.Context, row *project_entity.ProjectAgent) error {
	if row.JoinedAt == 0 {
		row.JoinedAt = time.Now().UnixMilli()
	}
	row.EnsureSyncID()
	return db.Ctx(ctx).Create(row).Error
}

func (r *projectAgentRepo) Remove(ctx context.Context, projectID, agentID int64) error {
	return db.Ctx(ctx).
		Where("project_id = ? AND agent_id = ?", projectID, agentID).
		Delete(&project_entity.ProjectAgent{}).Error
}

func (r *projectAgentRepo) ListByProject(ctx context.Context, projectID int64) ([]*project_entity.ProjectAgent, error) {
	var rows []*project_entity.ProjectAgent
	err := db.Ctx(ctx).
		Where("project_id = ?", projectID).
		Order("joined_at ASC, agent_id ASC").
		Find(&rows).Error
	return rows, err
}

// ListByProjects 批量查每个项目的直接成员，避免父项目链上溯时的 N+1。
func (r *projectAgentRepo) ListByProjects(ctx context.Context, projectIDs []int64) (map[int64][]*project_entity.ProjectAgent, error) {
	out := make(map[int64][]*project_entity.ProjectAgent, len(projectIDs))
	if len(projectIDs) == 0 {
		return out, nil
	}
	var rows []*project_entity.ProjectAgent
	err := db.Ctx(ctx).
		Where("project_id IN ?", projectIDs).
		Order("project_id ASC, joined_at ASC, agent_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ProjectID] = append(out[row.ProjectID], row)
	}
	return out, nil
}

func (r *projectAgentRepo) ListByAgent(ctx context.Context, agentID int64) ([]*project_entity.ProjectAgent, error) {
	var rows []*project_entity.ProjectAgent
	err := db.Ctx(ctx).
		Where("agent_id = ?", agentID).
		Order("project_id ASC").
		Find(&rows).Error
	return rows, err
}
