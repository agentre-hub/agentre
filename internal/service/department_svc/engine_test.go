package department_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/service/department_svc/mock_deps"
)

func setupEngineTest(t *testing.T) (context.Context, *mock_department_repo.MockDepartmentRepo, *mock_deps.MockEngineProfilePort, *departmentSvc) {
	t.Helper()
	ctx, deptMock, _, svc := setupSvc(t)
	profiles := mock_deps.NewMockEngineProfilePort(gomock.NewController(t))
	svc.engineProfiles = profiles
	return ctx, deptMock, profiles, svc
}

func dept(id, parentID, defaultProfile int64, name string) *department_entity.Department {
	return &department_entity.Department{ID: id, ParentID: parentID, DefaultEngineProfileID: defaultProfile, Name: name, Status: consts.ACTIVE}
}

func profile(id int64, name, typ string) *agent_backend_entity.EngineProfile {
	return &agent_backend_entity.EngineProfile{ID: id, Name: name, Type: typ, Status: consts.ACTIVE}
}

func requireDeptCode(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

func TestResolveDepartmentEngine(t *testing.T) {
	convey.Convey("部门的生效引擎", t, func() {
		ctx, deptMock, profiles, svc := setupEngineTest(t)

		convey.Convey("Given the department sets its own default When resolved Then it comes from that department", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(dept(3, 1, 9, "后端组"), nil)
			profiles.EXPECT().Find(gomock.Any(), int64(9)).Return(profile(9, "Codex", "codex"), nil)

			got, err := svc.ResolveDepartmentEngine(ctx, 3)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, int64(9), got.Profile.ID)
			assert.Equal(t, EngineSourceDepartment, got.Source)
			assert.Equal(t, int64(3), got.Department.ID)
		})

		convey.Convey("Given the department follows its parent When resolved Then the nearest ancestor with a default wins", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(dept(3, 1, 0, "后端组"), nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(1)).Return(dept(1, 0, 8, "工程部"), nil)
			profiles.EXPECT().Find(gomock.Any(), int64(8)).Return(profile(8, "Claude Code", "claudecode"), nil)

			got, err := svc.ResolveDepartmentEngine(ctx, 3)
			require.NoError(t, err)
			assert.Equal(t, int64(8), got.Profile.ID)
			assert.Equal(t, EngineSourceDepartment, got.Source)
			assert.Equal(t, "工程部", got.Department.Name)
		})

		convey.Convey("Given no department on the chain sets a default When resolved Then the account default is used", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(dept(3, 1, 0, "后端组"), nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(1)).Return(dept(1, 0, 0, "工程部"), nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(profile(1, "Default", "claudecode"), nil)

			got, err := svc.ResolveDepartmentEngine(ctx, 3)
			require.NoError(t, err)
			assert.Equal(t, int64(1), got.Profile.ID)
			assert.Equal(t, EngineSourceAccountDefault, got.Source)
			assert.Nil(t, got.Department)
		})

		convey.Convey("Given a department's default points at a deleted profile When resolved Then it falls through to the next level", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(dept(3, 0, 9, "后端组"), nil)
			profiles.EXPECT().Find(gomock.Any(), int64(9)).Return(nil, nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(profile(1, "Default", "claudecode"), nil)

			got, err := svc.ResolveDepartmentEngine(ctx, 3)
			require.NoError(t, err)
			assert.Equal(t, EngineSourceAccountDefault, got.Source)
		})

		convey.Convey("Given a parent cycle in the data When resolved Then the walk stops and falls back to the account default", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(dept(3, 4, 0, "A"), nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(4)).Return(dept(4, 3, 0, "B"), nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(profile(1, "Default", "claudecode"), nil)

			got, err := svc.ResolveDepartmentEngine(ctx, 3)
			require.NoError(t, err)
			assert.Equal(t, EngineSourceAccountDefault, got.Source)
		})

		convey.Convey("Given nothing anywhere When resolved Then there is no effective engine", func() {
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(nil, nil)

			got, err := svc.ResolveDepartmentEngine(ctx, 0)
			require.NoError(t, err)
			assert.Nil(t, got)
		})
	})
}

func TestDepartmentEngine_GetAndSet(t *testing.T) {
	convey.Convey("部门的成员默认引擎", t, func() {
		ctx, deptMock, profiles, svc := setupEngineTest(t)

		convey.Convey("Given a top-level department that follows When read Then it reports what it inherits from the account default", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(1)).Return(dept(1, 0, 0, "工程部"), nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(profile(1, "Default", "claudecode"), nil)

			view, err := svc.GetDepartmentEngine(ctx, &GetDepartmentEngineRequest{DepartmentID: 1})
			require.NoError(t, err)
			assert.Equal(t, int64(0), view.EngineProfileID)
			require.NotNil(t, view.Inherited)
			assert.Equal(t, EngineSourceAccountDefault, view.Inherited.Source)
			assert.Equal(t, "Default", view.Inherited.EngineProfileName)
			assert.Equal(t, view.Inherited, view.Effective)
		})

		convey.Convey("Given an active profile When set as the department default Then only that column is written and it becomes effective", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(dept(3, 1, 0, "后端组"), nil)
			profiles.EXPECT().Find(gomock.Any(), int64(9)).Return(profile(9, "Codex", "codex"), nil).Times(2)
			deptMock.EXPECT().UpdateDefaultEngineProfile(gomock.Any(), int64(3), int64(9)).Return(nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(1)).Return(dept(1, 0, 0, "工程部"), nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(profile(1, "Default", "claudecode"), nil)

			view, err := svc.SetDepartmentEngine(ctx, &SetDepartmentEngineRequest{DepartmentID: 3, EngineProfileID: 9})
			require.NoError(t, err)
			assert.Equal(t, int64(9), view.EngineProfileID)
			assert.Equal(t, EngineSourceDepartment, view.Effective.Source)
			assert.Equal(t, int64(3), view.Effective.SourceDepartmentID)
			assert.Equal(t, EngineSourceAccountDefault, view.Inherited.Source)
		})

		convey.Convey("Given a deleted profile When set as the department default Then it is rejected and nothing is written", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(dept(3, 1, 0, "后端组"), nil)
			profiles.EXPECT().Find(gomock.Any(), int64(9)).Return(nil, nil)

			_, err := svc.SetDepartmentEngine(ctx, &SetDepartmentEngineRequest{DepartmentID: 3, EngineProfileID: 9})
			requireDeptCode(t, err, code.EngineProfileNotFound)
		})

		convey.Convey("Given a missing department When read Then it reports not found", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(nil, nil)

			_, err := svc.GetDepartmentEngine(ctx, &GetDepartmentEngineRequest{DepartmentID: 3})
			requireDeptCode(t, err, code.DepartmentNotFound)
		})
	})
}
