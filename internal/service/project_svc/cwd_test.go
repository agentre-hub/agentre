package project_svc_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
)

func setupCwdTest(t *testing.T) (context.Context, *mock_project_repo.MockProjectRepo, project_svc.ProjectSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockProj := mock_project_repo.NewMockProjectRepo(ctrl)
	mockPA := mock_project_repo.NewMockProjectAgentRepo(ctrl)
	project_repo.RegisterProject(mockProj)
	project_repo.RegisterProjectAgent(mockPA)
	return context.Background(), mockProj, project_svc.New()
}

func TestResolveSessionCwd(t *testing.T) {
	convey.Convey("ResolveSessionCwd", t, func() {
		convey.Convey("自由会话 (project_id=0) 回退到 AgentCwd", func() {
			ctx, _, svc := setupCwdTest(t)
			// AgentCwd 会创建 <AppDataDir>/agents/<id>/，AGENTRE_DATA_DIR 可覆盖。
			tmp := t.TempDir()
			t.Setenv("AGENTRE_DATA_DIR", tmp)

			cwd, err := svc.ResolveSessionCwd(ctx, &chat_entity.Session{
				ID:        1,
				AgentID:   42,
				ProjectID: 0,
			})
			require.NoError(t, err)
			convey.So(cwd, convey.ShouldEqual, fmt.Sprintf("%s/agents/42", tmp))
			info, err := os.Stat(cwd)
			require.NoError(t, err)
			convey.So(info.IsDir(), convey.ShouldBeTrue)
		})

		convey.Convey("项目会话直接返回 project.Path", func() {
			ctx, mockProj, svc := setupCwdTest(t)
			mockProj.EXPECT().Find(ctx, int64(7)).Return(&project_entity.Project{
				ID: 7, Path: "/Users/foo/Code/agentre",
			}, nil)

			cwd, err := svc.ResolveSessionCwd(ctx, &chat_entity.Session{
				ID: 9, AgentID: 1, ProjectID: 7,
			})
			require.NoError(t, err)
			convey.So(cwd, convey.ShouldEqual, "/Users/foo/Code/agentre")
		})

		convey.Convey("project 已软删除 (Find 返回 nil) 兜底到 AgentCwd", func() {
			ctx, mockProj, svc := setupCwdTest(t)
			tmp := t.TempDir()
			t.Setenv("AGENTRE_DATA_DIR", tmp)
			mockProj.EXPECT().Find(ctx, int64(7)).Return(nil, nil)

			cwd, err := svc.ResolveSessionCwd(ctx, &chat_entity.Session{
				ID: 9, AgentID: 42, ProjectID: 7,
			})
			require.NoError(t, err)
			convey.So(cwd, convey.ShouldEqual, fmt.Sprintf("%s/agents/42", tmp))
		})

		// R10/R11: 本机未配置路径是显式状态位，解析点必须返回一个与
		// ProjectLocationMissing、WorkspaceFsNoCwd 都可区分的确定错误，而不是
		// ("", nil)（决策 21）。
		convey.Convey("本机未配置路径 (R10) → 确定错误而不是空串加 nil", func() {
			ctx, mockProj, svc := setupCwdTest(t)
			mockProj.EXPECT().Find(ctx, int64(7)).Return(&project_entity.Project{
				ID: 7, Path: "", LocalPathMissing: true,
			}, nil)

			cwd, err := svc.ResolveSessionCwd(ctx, &chat_entity.Session{
				ID: 9, AgentID: 1, ProjectID: 7,
			})
			convey.So(cwd, convey.ShouldEqual, "")
			convey.So(err, convey.ShouldNotBeNil)
			var httpErr *httputils.Error
			require.ErrorAs(t, err, &httpErr)
			convey.So(httpErr.Code, convey.ShouldEqual, code.ProjectLocalPathMissing)
			convey.So(httpErr.Code, convey.ShouldNotEqual, code.ProjectLocationMissing)
			convey.So(httpErr.Code, convey.ShouldNotEqual, code.WorkspaceFsNoCwd)
		})

		// 用户指定路径后状态解除：同一解析点对「已配置」的项目行为不变。
		convey.Convey("指定路径后状态解除 → 恢复正常解析", func() {
			ctx, mockProj, svc := setupCwdTest(t)
			mockProj.EXPECT().Find(ctx, int64(7)).Return(&project_entity.Project{
				ID: 7, Path: "/Users/foo/Code/agentre", LocalPathMissing: false,
			}, nil)

			cwd, err := svc.ResolveSessionCwd(ctx, &chat_entity.Session{
				ID: 9, AgentID: 1, ProjectID: 7,
			})
			require.NoError(t, err)
			convey.So(cwd, convey.ShouldEqual, "/Users/foo/Code/agentre")
		})
	})
}
