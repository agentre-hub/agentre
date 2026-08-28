package project_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
)

// 改父项目（规格 2026-08-22 B 段「基本」那一节列了「父项目」这一格）。
//
// 此前这一端根本改不了：UpdateProjectRequest 没有 ParentID，而 ReorderSiblings 的
// SQL 带 `AND parent_id = ?`（只在同一个父下排序，拿它反父级会 RowsAffected != 1
// 直接报错）。types.go 当初写明「单独走 Move 接口；当前 spec 留作下次」——就是这次。
//
// 形状照部门那份 Move（department_svc.Move）：父级存在 + active + 环检测，
// 同一条判据不该在两棵树上长出两个样子。

func active(id, parentID int64, name string) *project_entity.Project {
	return &project_entity.Project{
		ID: id, ParentID: parentID, Name: name,
		Color: "agent-1", Path: "/tmp", Status: consts.ACTIVE,
	}
}

func TestProjectSvcMove_ToRoot(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	mp.EXPECT().Find(ctx, int64(2)).Return(active(2, 1, "child"), nil)
	// 挂到根上同样要判重名：根这一层下也可能已经有一个同名项目。
	mp.EXPECT().FindByName(ctx, int64(0), "child").Return(nil, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, p *project_entity.Project) error {
			assert.Equal(t, int64(0), p.ParentID)
			return nil
		})

	got, err := svc.Move(ctx, &project_svc.MoveProjectRequest{ID: 2, NewParentID: 0})
	require.NoError(t, err)
	assert.Equal(t, int64(0), got.ParentID)
}

func TestProjectSvcMove_UnderAnother(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	mp.EXPECT().Find(ctx, int64(2)).Return(active(2, 0, "b"), nil)
	mp.EXPECT().Find(ctx, int64(1)).Return(active(1, 0, "a"), nil)
	mp.EXPECT().List(ctx).Return([]*project_entity.Project{active(1, 0, "a"), active(2, 0, "b")}, nil)
	mp.EXPECT().FindByName(ctx, int64(1), "b").Return(nil, nil)
	mp.EXPECT().Update(ctx, gomock.Any()).Return(nil)

	got, err := svc.Move(ctx, &project_svc.MoveProjectRequest{ID: 2, NewParentID: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.ParentID)
}

// 指向自己是最短的那个环。
func TestProjectSvcMove_SelfIsACycle(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	mp.EXPECT().Find(ctx, int64(2)).Return(active(2, 0, "b"), nil)
	mp.EXPECT().Find(ctx, int64(2)).Return(active(2, 0, "b"), nil)
	mp.EXPECT().List(ctx).Return([]*project_entity.Project{active(2, 0, "b")}, nil)

	_, err := svc.Move(ctx, &project_svc.MoveProjectRequest{ID: 2, NewParentID: 2})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "自己或下级项目")
}

// 挂到自己的后代下面同样成环 —— 禁用下拉项拦不住直接打端点，所以判据必须在这里。
func TestProjectSvcMove_UnderOwnDescendantIsACycle(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	tree := []*project_entity.Project{
		active(1, 0, "root"), active(2, 1, "mid"), active(3, 2, "leaf"),
	}
	mp.EXPECT().Find(ctx, int64(1)).Return(tree[0], nil)
	mp.EXPECT().Find(ctx, int64(3)).Return(tree[2], nil)
	mp.EXPECT().List(ctx).Return(tree, nil)

	_, err := svc.Move(ctx, &project_svc.MoveProjectRequest{ID: 1, NewParentID: 3})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "自己或下级项目")
}

func TestProjectSvcMove_ParentNotFound(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	mp.EXPECT().Find(ctx, int64(2)).Return(active(2, 0, "b"), nil)
	mp.EXPECT().Find(ctx, int64(9)).Return(nil, nil)

	_, err := svc.Move(ctx, &project_svc.MoveProjectRequest{ID: 2, NewParentID: 9})
	require.Error(t, err)
}

// 换了父级之后同级重名要拦 —— 原来那一层下不重名，不代表新的一层下也不重名。
func TestProjectSvcMove_DuplicateNameUnderNewParent(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	mp.EXPECT().Find(ctx, int64(2)).Return(active(2, 0, "b"), nil)
	mp.EXPECT().Find(ctx, int64(1)).Return(active(1, 0, "a"), nil)
	mp.EXPECT().List(ctx).Return([]*project_entity.Project{active(1, 0, "a"), active(2, 0, "b")}, nil)
	mp.EXPECT().FindByName(ctx, int64(1), "b").Return(active(5, 1, "b"), nil)

	_, err := svc.Move(ctx, &project_svc.MoveProjectRequest{ID: 2, NewParentID: 1})
	require.Error(t, err)
}

func TestProjectSvcMove_NotFound(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	mp.EXPECT().Find(ctx, int64(9)).Return(nil, nil)

	_, err := svc.Move(ctx, &project_svc.MoveProjectRequest{ID: 9, NewParentID: 0})
	require.Error(t, err)
}
