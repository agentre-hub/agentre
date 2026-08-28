package agent_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

//go:generate mockgen -source exec_target_override.go -destination mock_agent_repo/mock_exec_target_override.go

// AgentExecTargetOverrideRepo 本端（这台桌面端安装）执行目标顺序覆盖（R14）的
// 持久化访问。它是**纯本地**数据：不同步、不进同步队列、不上行 —— 只改这一端。
// 一个 Agent 至多一行，按 agent_id upsert。
type AgentExecTargetOverrideRepo interface {
	// Get 取某个 Agent 的本端覆盖；没有时返回 (nil, nil) —— 「没覆盖 = 用账号默认」
	// 是本端解析的正常状态，不是错误。
	Get(ctx context.Context, agentID int64) (*agent_entity.AgentExecTargetOverride, error)
	// Save 按 agent_id upsert 一行覆盖（没有则插入、有则覆盖 order_json）。
	Save(ctx context.Context, o *agent_entity.AgentExecTargetOverride) error
	// Delete 清掉某个 Agent 的本端覆盖（「恢复为账号默认顺序」）；行不存在不是错误。
	Delete(ctx context.Context, agentID int64) error
}

// 注意：这个仓储的默认单例不走 internal/bootstrap（本任务的 owned path 不含
// bootstrap），因此在这里就地初始化成 GORM 实现；测试经 RegisterAgentExecTargetOverride
// 替换成 mock。行为与其它 bootstrap 注册的仓储完全一致 —— 只差注册位置。
var defaultAgentExecTargetOverride = NewAgentExecTargetOverride()

// AgentExecTargetOverride 取默认仓储单例。
func AgentExecTargetOverride() AgentExecTargetOverrideRepo { return defaultAgentExecTargetOverride }

// RegisterAgentExecTargetOverride 测试注入用；调用方负责还原。
func RegisterAgentExecTargetOverride(impl AgentExecTargetOverrideRepo) {
	defaultAgentExecTargetOverride = impl
}

// NewAgentExecTargetOverride 构造 GORM 实现。
func NewAgentExecTargetOverride() AgentExecTargetOverrideRepo { return &agentExecTargetOverrideRepo{} }

type agentExecTargetOverrideRepo struct{}

func (r *agentExecTargetOverrideRepo) Get(ctx context.Context, agentID int64) (*agent_entity.AgentExecTargetOverride, error) {
	out := &agent_entity.AgentExecTargetOverride{}
	err := db.Ctx(ctx).Where("agent_id = ?", agentID).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *agentExecTargetOverrideRepo) Save(ctx context.Context, o *agent_entity.AgentExecTargetOverride) error {
	if o == nil || o.AgentID <= 0 {
		return nil
	}
	return db.Ctx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "agent_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"order_json", "updatetime"}),
	}).Create(o).Error
}

func (r *agentExecTargetOverrideRepo) Delete(ctx context.Context, agentID int64) error {
	if agentID <= 0 {
		return nil
	}
	return db.Ctx(ctx).Where("agent_id = ?", agentID).Delete(&agent_entity.AgentExecTargetOverride{}).Error
}
