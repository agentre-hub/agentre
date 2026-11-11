package memory_svc

import (
	"context"
	"slices"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// 会话里的 AI 写入者（agrctl 记忆命令）的范围。决策 7 的两条硬规则在服务边界上把关：
// AI 只能写它所属的层——也就是本会话看得到的层；AI 的删除是归档（Delete 不收写入者，
// 只给用户用）。用户写入不受这里的限制。

// SessionOwners AI 写入者所属的层，顺序同 Visible：全局、部门（最上级在前）、agent、
// 会话所在项目（有项目时）。actor 必须是 AI；项目从会话本身读，不信调用方给的。
func (s *memorySvc) SessionOwners(ctx context.Context, actor memory_entity.Actor) ([]memory_entity.Owner, error) {
	if actor.Kind != memory_entity.ActorAI || !actor.Valid() {
		return nil, i18n.NewError(ctx, code.MemoryInvalidActor)
	}
	sess, err := chat_repo.Session().Find(ctx, actor.SessionID)
	if err != nil {
		return nil, err
	}
	var projectID int64
	if sess != nil {
		projectID = sess.ProjectID
	}
	return s.visibleOwners(ctx, actor.AgentID, projectID)
}

// SessionVisible AI 写入者所在会话看得到的记忆：SessionOwners 的各层，逐层按预算分好。
func (s *memorySvc) SessionVisible(ctx context.Context, actor memory_entity.Actor) (*VisibleMemory, error) {
	owners, err := s.SessionOwners(ctx, actor)
	if err != nil {
		return nil, err
	}
	return visibleIn(ctx, owners)
}

// requireOwnLayer AI 写入者只能写它所属的层；用户不受限。
func (s *memorySvc) requireOwnLayer(ctx context.Context, actor memory_entity.Actor, owners ...memory_entity.Owner) error {
	if actor.Kind != memory_entity.ActorAI {
		return nil
	}
	own, err := s.SessionOwners(ctx, actor)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if !slices.Contains(own, o) {
			return i18n.NewError(ctx, code.MemoryNotOwnLayer)
		}
	}
	return nil
}
