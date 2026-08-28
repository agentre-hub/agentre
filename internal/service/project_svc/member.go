package project_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
)

// aggregateMembers 按 parent_id 链向上聚合成员：直接成员 + 全部祖先继承成员。
//
// 算法：
//  1. 先沿 parent_id 链一路找到所有祖先项目 id（含自身）；
//  2. 一次性 ListByProjects 批量查；
//  3. 自身命中的归 direct；其余归 inherited，去重时按 (agent_id) 优先保留更"近"的来源
//     —— 即较深层级的；如果同一 agent 既是直接成员又被祖先包含，仍只算 direct（不重复）。
//
// 算法复杂度：O(depth) 次 Find + 1 次 ListByProjects；不入库继承字段（spec §3.3 决议 3）。
func (s *projectSvc) aggregateMembers(
	ctx context.Context,
	p *project_entity.Project,
) (direct, inherited []*ProjectAgentMember, err error) {
	// 1. 收集 self + ancestors，记录每个项目的 name 供 UI 展示。
	ancestors := []*project_entity.Project{p}
	cur := p
	for cur != nil && cur.ParentID > 0 {
		parent, ferr := project_repo.Project().Find(ctx, cur.ParentID)
		if ferr != nil {
			return nil, nil, ferr
		}
		if parent == nil {
			break
		}
		ancestors = append(ancestors, parent)
		cur = parent
	}

	ids := make([]int64, 0, len(ancestors))
	nameByID := make(map[int64]string, len(ancestors))
	for _, a := range ancestors {
		ids = append(ids, a.ID)
		nameByID[a.ID] = a.Name
	}

	// 2. 批量查每个项目的直接成员。
	rows, err := project_repo.ProjectAgent().ListByProjects(ctx, ids)
	if err != nil {
		return nil, nil, err
	}

	// 3. 拆分 direct vs inherited，去重保留最近的来源。
	direct = make([]*ProjectAgentMember, 0)
	// 用 set 记录已经做 direct 的 agent_id；继承成员里跳过这些。
	directSet := make(map[int64]struct{})
	for _, row := range rows[p.ID] {
		direct = append(direct, &ProjectAgentMember{
			AgentID:       row.AgentID,
			JoinedAt:      row.JoinedAt,
			FromProjectID: p.ID,
		})
		directSet[row.AgentID] = struct{}{}
	}

	// inheritedSeen 记录第一次出现某 agent 时的来源 —— 从最近的祖先（自身父）开始遍历，
	// 第一次记下的来源就是 nearest，后续更远的祖先重复就跳过。
	inherited = make([]*ProjectAgentMember, 0)
	inheritedSeen := make(map[int64]struct{})
	for i := 1; i < len(ancestors); i++ { // 跳过自身 ancestors[0]
		ancestor := ancestors[i]
		for _, row := range rows[ancestor.ID] {
			if _, ok := directSet[row.AgentID]; ok {
				continue
			}
			if _, ok := inheritedSeen[row.AgentID]; ok {
				continue
			}
			inheritedSeen[row.AgentID] = struct{}{}
			inherited = append(inherited, &ProjectAgentMember{
				AgentID:       row.AgentID,
				JoinedAt:      row.JoinedAt,
				FromProjectID: ancestor.ID,
				FromName:      nameByID[ancestor.ID],
			})
		}
	}
	return direct, inherited, nil
}

func (s *projectSvc) hydrateMemberAgents(ctx context.Context, groups ...[]*ProjectAgentMember) error {
	agents, err := s.agents.List(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]*agent_entity.Agent, len(agents))
	for _, a := range agents {
		byID[a.ID] = a
	}
	for _, group := range groups {
		for _, member := range group {
			if a := byID[member.AgentID]; a != nil {
				member.AgentName = a.Name
				member.AvatarColor = a.AvatarColor
				member.AvatarIcon = a.AvatarIcon
				member.AvatarDataURL = a.AvatarDataURL
			}
		}
	}
	return nil
}
