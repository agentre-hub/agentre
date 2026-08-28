package ctl_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
)

// TestProjectSvcGateway_CarriesLocalPathMissing 锁住 R11 的读取点语义(决策 21):
// 控制 API 的项目清单不能只把空 Path 原样透出 —— 「本机未配置路径」(R10)必须
// 显式标注,否则外部脚本(agrctl)分不清"这个项目本来就没配路径"与"字段没读到"。
func TestProjectSvcGateway_CarriesLocalPathMissing(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockProj := mock_project_repo.NewMockProjectRepo(ctrl)
	prevRepo := project_repo.Project()
	project_repo.RegisterProject(mockProj)
	t.Cleanup(func() { project_repo.RegisterProject(prevRepo) })

	prevSvc := project_svc.Default()
	project_svc.SetDefault(project_svc.New())
	t.Cleanup(func() { project_svc.SetDefault(prevSvc) })

	ctx := context.Background()
	mockProj.EXPECT().List(ctx).Return([]*project_entity.Project{
		{ID: 1, Name: "synced", Path: "", LocalPathMissing: true},
		{ID: 2, Name: "local", Path: "/repo", LocalPathMissing: false},
	}, nil)

	out, err := ProjectSvcGateway().List(ctx)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.True(t, out[0].LocalPathMissing)
	assert.Equal(t, "", out[0].Path)
	assert.False(t, out[1].LocalPathMissing)
	assert.Equal(t, "/repo", out[1].Path)
}
