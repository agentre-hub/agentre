package memory_svc

import (
	"cmp"
	"context"
	"slices"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
)

// Visible 合成一个会话可见的记忆（决策 2）：全局 + agent 所在部门及其全部上级部门 +
// 该 agent + 会话所在项目。已归档的不在其中；每层各自按预算分成带上与超出两组。
func (s *memorySvc) Visible(ctx context.Context, agentID, projectID int64) (*VisibleMemory, error) {
	owners, err := s.visibleOwners(ctx, agentID, projectID)
	if err != nil {
		return nil, err
	}
	return visibleIn(ctx, owners)
}

// ownerKindOrder 是 AllLayers 里各层的先后，与会话可见的层同序：全局、部门、agent、项目。
var ownerKindOrder = map[memory_entity.OwnerKind]int{
	memory_entity.OwnerGlobal: 0, memory_entity.OwnerDepartment: 1, memory_entity.OwnerAgent: 2, memory_entity.OwnerProject: 3,
}

// AllLayers 见 MemorySvc 接口注释。
func (s *memorySvc) AllLayers(ctx context.Context) (*VisibleMemory, error) {
	counts, err := memory_repo.Memory().CountNormalByOwner(ctx)
	if err != nil {
		return nil, err
	}
	owners := []memory_entity.Owner{memory_entity.GlobalOwner()}
	for _, c := range counts {
		if c.Owner.Kind != memory_entity.OwnerGlobal {
			owners = append(owners, c.Owner)
		}
	}
	slices.SortStableFunc(owners[1:], func(a, b memory_entity.Owner) int {
		if d := ownerKindOrder[a.Kind] - ownerKindOrder[b.Kind]; d != 0 {
			return d
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return visibleIn(ctx, owners)
}

// visibleIn 取 owners 各层的正常条目，按 owners 的顺序逐层按预算分好。
func visibleIn(ctx context.Context, owners []memory_entity.Owner) (*VisibleMemory, error) {
	rows, err := memory_repo.Memory().ListNormalByOwners(ctx, owners)
	if err != nil {
		return nil, err
	}
	byOwner := make(map[memory_entity.Owner][]*memory_entity.Memory, len(owners))
	for _, m := range rows {
		byOwner[m.Owner()] = append(byOwner[m.Owner()], m)
	}
	out := &VisibleMemory{Layers: make([]*VisibleLayer, 0, len(owners))}
	for _, owner := range owners {
		carried, over := memory_entity.SplitByBudget(byOwner[owner], memory_entity.LayerBudgetChars)
		out.Layers = append(out.Layers, &VisibleLayer{
			Owner:      owner,
			Budget:     memory_entity.LayerBudgetChars,
			Carried:    toItems(carried),
			OverBudget: toItems(over),
		})
	}
	return out, nil
}

// visibleOwners 按固定顺序列出可见的层：全局、部门（最上级在前）、agent、项目。
func (s *memorySvc) visibleOwners(ctx context.Context, agentID, projectID int64) ([]memory_entity.Owner, error) {
	departmentID, err := s.agentDepartment(ctx, agentID)
	if err != nil {
		return nil, err
	}
	chain, err := departmentChain(ctx, departmentID)
	if err != nil {
		return nil, err
	}
	owners := []memory_entity.Owner{memory_entity.GlobalOwner()}
	for _, id := range chain {
		owners = append(owners, memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: id})
	}
	owners = append(owners, memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: agentID})
	if projectID > 0 {
		owners = append(owners, memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: projectID})
	}
	return owners, nil
}

// agentDepartment agent 所在的部门；agent 挂在上级 agent 之下时，沿上级找到最近的
// 部门。找不到（系统 agent、链断了或成环）返回 0。agent 本身不存在时报错。
func (s *memorySvc) agentDepartment(ctx context.Context, agentID int64) (int64, error) {
	seen := map[int64]bool{}
	for id := agentID; id > 0 && !seen[id]; {
		seen[id] = true
		a, err := agent_repo.Agent().Find(ctx, id)
		if err != nil {
			return 0, err
		}
		if a == nil {
			if id == agentID {
				return 0, i18n.NewError(ctx, code.MemoryOwnerNotFound)
			}
			return 0, nil
		}
		if a.DepartmentID > 0 {
			return a.DepartmentID, nil
		}
		id = a.ParentAgentID
	}
	return 0, nil
}

// departmentChain 从 departmentID 一路向上到顶级部门，返回最上级在前的 id 列表。
// 遇到不存在的部门或成环即停。
func departmentChain(ctx context.Context, departmentID int64) ([]int64, error) {
	var chain []int64
	seen := map[int64]bool{}
	for id := departmentID; id > 0 && !seen[id]; {
		seen[id] = true
		d, err := department_repo.Department().Find(ctx, id)
		if err != nil {
			return nil, err
		}
		if d == nil {
			break
		}
		chain = append(chain, d.ID)
		id = d.ParentID
	}
	slices.Reverse(chain)
	return chain, nil
}
