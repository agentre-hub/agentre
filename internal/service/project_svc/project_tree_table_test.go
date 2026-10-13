package project_svc_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
)

// projectTreeTable 持有整张 projects 表，给「操作之后整棵树长什么样」的断言用。
//
// 用手写的内存假仓储而不是 gomock（与 merge_dangling_test.go 同一个理由）：要表达的
// 是一条关于**整张表**的性质——删掉的是不是整棵子树、合并后还是不是一棵树——逐次
// 调用的期望链说不出这件事，只有让假仓储持有全部行、操作完再读回来才断言得了。
//
// 读出去的都是副本：真实仓储每次查询交回一份新结构，服务改了手里那一份却没有
// Update，库里就没变。假仓储若把自己的行直接交出去，这一类漏写在这里永远是绿的。
type projectTreeTable struct {
	project_repo.ProjectRepo // 只该碰下面这几个方法；碰到别的直接 nil panic
	rows                     []*project_entity.Project
	// log 是与 projectSessionTable 共用的一本流水，断言得了两张表之间的先后。
	log *[]string
	// failDeleteOn 非 0 时，软删这一行失败（一步落库中途出错）。
	failDeleteOn int64
	// outsideTx 记下没带事务 ctx 的读写次数（仅在测试给了 txMarkerKey 时有意义）。
	outsideTx int
}

func (f *projectTreeTable) touch(ctx context.Context) {
	if ctx.Value(txMarkerKey{}) == nil {
		f.outsideTx++
	}
}

func (f *projectTreeTable) Find(ctx context.Context, id int64) (*project_entity.Project, error) {
	f.touch(ctx)
	for _, p := range f.rows {
		if p.ID == id && p.Status == consts.ACTIVE {
			cp := *p
			return &cp, nil
		}
	}
	return nil, nil
}

// List 与真实仓储同判据同次序：只出活跃行，parent_id / sort_order / id 升序。
func (f *projectTreeTable) List(ctx context.Context) ([]*project_entity.Project, error) {
	f.touch(ctx)
	out := make([]*project_entity.Project, 0, len(f.rows))
	for _, p := range f.rows {
		if p.Status == consts.ACTIVE {
			cp := *p
			out = append(out, &cp)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ParentID != out[j].ParentID {
			return out[i].ParentID < out[j].ParentID
		}
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (f *projectTreeTable) ListByParent(ctx context.Context, parentID int64) ([]*project_entity.Project, error) {
	all, err := f.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*project_entity.Project, 0, len(all))
	for _, p := range all {
		if p.ParentID == parentID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *projectTreeTable) Update(ctx context.Context, p *project_entity.Project) error {
	f.touch(ctx)
	for i, row := range f.rows {
		if row.ID == p.ID {
			cp := *p
			f.rows[i] = &cp
			return nil
		}
	}
	return fmt.Errorf("project %d is not in the table", p.ID)
}

// ReassignParent 与真实仓储同判据：WHERE 里只有 parent_id，软删行一并改挂。
func (f *projectTreeTable) ReassignParent(ctx context.Context, fromParentID, toParentID int64) error {
	f.touch(ctx)
	for _, p := range f.rows {
		if p.ParentID == fromParentID {
			p.ParentID = toParentID
		}
	}
	return nil
}

func (f *projectTreeTable) HasActiveChildren(ctx context.Context, id int64) (bool, error) {
	children, err := f.ListByParent(ctx, id)
	return len(children) > 0, err
}

func (f *projectTreeTable) Delete(ctx context.Context, id int64) error {
	f.touch(ctx)
	if id == f.failDeleteOn {
		return errProjectDeleteFailed
	}
	for _, p := range f.rows {
		if p.ID == id {
			p.Status = consts.DELETE
		}
	}
	if f.log != nil {
		*f.log = append(*f.log, fmt.Sprintf("delete:%d", id))
	}
	return nil
}

var errProjectDeleteFailed = errors.New("database is locked")

func (f *projectTreeTable) row(id int64) *project_entity.Project {
	for _, p := range f.rows {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (f *projectTreeTable) activeIDs() []int64 {
	out := make([]int64, 0, len(f.rows))
	for _, p := range f.rows {
		if p.Status == consts.ACTIVE {
			out = append(out, p.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// assertStillATree 断言存活的行仍然构成一棵（多根的）树：每一行顺着 parent_id 往上
// 走都落到根上——中途既不踩到已消失的项目（悬空的父级），也不绕回自己（环）。
func (f *projectTreeTable) assertStillATree(t *testing.T) {
	t.Helper()
	alive := make(map[int64]*project_entity.Project, len(f.rows))
	for _, p := range f.rows {
		if p.Status == consts.ACTIVE {
			alive[p.ID] = p
		}
	}
	for id := range alive {
		cur, steps := id, 0
		for alive[cur].ParentID != 0 {
			parent, ok := alive[alive[cur].ParentID]
			if !assert.True(t, ok, "项目 %d 的父链指向一个已消失的项目 %d", id, alive[cur].ParentID) {
				break
			}
			cur = parent.ID
			steps++
			if !assert.LessOrEqual(t, steps, len(alive), "项目 %d 的父链成环，永远走不到根", id) {
				break
			}
		}
	}
}

// projectSessionTable 是会话那一侧的内存表：删项目只该碰 SessionPort 的两个方法。
type projectSessionTable struct {
	rows      []*chat_entity.Session
	log       *[]string
	outsideTx int
}

func (f *projectSessionTable) touch(ctx context.Context) {
	if ctx.Value(txMarkerKey{}) == nil {
		f.outsideTx++
	}
}

// CountActiveByProject 与真实仓储同判据：活跃行里 agent_status 命中给定集合的条数。
func (f *projectSessionTable) CountActiveByProject(ctx context.Context, projectID int64, agentStatuses []string) (int64, error) {
	f.touch(ctx)
	var n int64
	for _, s := range f.rows {
		if s.ProjectID != projectID || s.Status != consts.ACTIVE {
			continue
		}
		for _, status := range agentStatuses {
			if s.AgentStatus == status {
				n++
				break
			}
		}
	}
	return n, nil
}

func (f *projectSessionTable) ReassignProject(ctx context.Context, fromProjectID, toProjectID int64) error {
	f.touch(ctx)
	for _, s := range f.rows {
		if s.ProjectID == fromProjectID {
			s.ProjectID = toProjectID
		}
	}
	if f.log != nil {
		*f.log = append(*f.log, fmt.Sprintf("reassign:%d", fromProjectID))
	}
	return nil
}

func (f *projectSessionTable) projectOf(sessionID int64) int64 {
	for _, s := range f.rows {
		if s.ID == sessionID {
			return s.ProjectID
		}
	}
	return -1
}
