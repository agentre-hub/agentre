package project_svc_test

import (
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// 本文件锁住 S4「项目合并结果预览」：MergePreview 只读地告诉前端，真的合并时哪一行
// 留下、哪一行消失——判据与 Merge 是同一份 chooseMergeWinner，与用户选的方向无关。
// 预览里每一边带名称、父路径（根 → 直接父级的名称链）与是否账号侧已认领，用来分清
// 不同父项目下的同名项目。
//
// setupMergeTest 的仓储桩是严格的：预览路径上出现任何一次写入（Update / Delete /
// 改挂）都会让 gomock 直接判失败，因此这里不必再单独断言「没写库」。

func TestProjectSvcMergePreview_GivenAccountSideTarget_ThenKeepsTargetWithParentPaths(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)

	// 两个同名的 api 分属不同父项目；target 已被账号认领，source 先创建但只在本机。
	org := &project_entity.Project{ID: 10, Name: "agentre-hub", Createtime: 1}
	server := &project_entity.Project{ID: 11, ParentID: 10, Name: "agentre-server", Createtime: 2}
	desktop := &project_entity.Project{ID: 12, Name: "agentre", Createtime: 3}
	source := &project_entity.Project{ID: 1, ParentID: 12, Name: "api", Createtime: 100}
	target := &project_entity.Project{
		ID: 2, ParentID: 11, Name: "api", Createtime: 200,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "sync-2", SyncAccountID: 7},
	}
	m.project.EXPECT().Find(ctx, int64(1)).Return(source, nil)
	m.project.EXPECT().Find(ctx, int64(2)).Return(target, nil)
	m.project.EXPECT().List(ctx).Return([]*project_entity.Project{org, server, desktop, source, target}, nil)

	got, err := svc.MergePreview(ctx, 1, 2)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(2), got.Keep.ID)
	assert.Equal(t, "api", got.Keep.Name)
	assert.Equal(t, []string{"agentre-hub", "agentre-server"}, got.Keep.ParentPath)
	assert.True(t, got.Keep.Claimed)
	assert.Equal(t, int64(1), got.Drop.ID)
	assert.Equal(t, "api", got.Drop.Name)
	assert.Equal(t, []string{"agentre"}, got.Drop.ParentPath)
	assert.False(t, got.Drop.Claimed)
}

// 两边都没认领：先创建的那个留下——即使它是用户选中的「源」，方向不决定结果。
func TestProjectSvcMergePreview_GivenNeitherClaimed_ThenOlderSourceIsKept(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)

	source := &project_entity.Project{ID: 1, Name: "docs", Createtime: 100}
	target := &project_entity.Project{ID: 2, Name: "docs-old", Createtime: 200}
	m.project.EXPECT().Find(ctx, int64(1)).Return(source, nil)
	m.project.EXPECT().Find(ctx, int64(2)).Return(target, nil)
	// 两边都在顶层：父路径不需要读整棵树，读不读交给实现。
	m.project.EXPECT().List(ctx).Return([]*project_entity.Project{source, target}, nil).AnyTimes()

	got, err := svc.MergePreview(ctx, 1, 2)

	require.NoError(t, err)
	assert.Equal(t, int64(1), got.Keep.ID)
	assert.Equal(t, "docs", got.Keep.Name)
	// 顶层是空链而不是 nil：经 Wails 落到前端是 []，不是 null。
	assert.Equal(t, []string{}, got.Keep.ParentPath)
	assert.False(t, got.Keep.Claimed)
	assert.Equal(t, int64(2), got.Drop.ID)
	assert.Equal(t, "docs-old", got.Drop.Name)
	assert.Equal(t, []string{}, got.Drop.ParentPath)
}

// 与 Merge 同一条入参口径：同一个项目、非正 id 一律是参数错误，一次库都不读。
func TestProjectSvcMergePreview_GivenInvalidIDs_ThenRejectsWithoutReading(t *testing.T) {
	cases := map[string][2]int64{
		"same project": {3, 3},
		"zero source":  {0, 3},
		"zero target":  {3, 0},
	}
	for name, ids := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, _, svc := setupMergeTest(t)

			got, err := svc.MergePreview(ctx, ids[0], ids[1])

			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, code.InvalidParameter, httpErr.Code)
			assert.Nil(t, got)
		})
	}
}

// 其中一边已经不在了（被删 / 被别处合并掉）：ProjectNotFound，不给半份预览。
func TestProjectSvcMergePreview_GivenMissingProject_ThenProjectNotFound(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)

	m.project.EXPECT().Find(ctx, int64(1)).Return(&project_entity.Project{ID: 1, Name: "api"}, nil)
	m.project.EXPECT().Find(ctx, int64(404)).Return(nil, nil)

	got, err := svc.MergePreview(ctx, 1, 404)

	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, code.ProjectNotFound, httpErr.Code)
	assert.Nil(t, got)
}
