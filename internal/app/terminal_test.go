package app

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc/mocks"
	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
)

// TestApp_TerminalOpen_NilService locks the nil-guard: TerminalOpen must not
// panic before the service is wired (Startup not run yet).
func TestApp_TerminalOpen_NilService(t *testing.T) {
	a := &App{}
	require.ErrorIs(t, a.TerminalOpen("t1", 7, "", 80, 24), errTerminalSvcNotInitialized)
}

// TestApp_TerminalOpen_ResolvesProjectCwdThenOpens locks the app-layer glue that
// the service itself can't see: TerminalOpen resolves the project cwd and threads
// it into terminal_svc.Open's pty.Spec. This is the only place that wiring is
// exercised end-to-end (the service is handed a pre-resolved cwd).
func TestApp_TerminalOpen_ResolvesProjectCwdThenOpens(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockProj := mock_project_repo.NewMockProjectRepo(ctrl)
	project_repo.RegisterProject(mockProj)
	mockProj.EXPECT().Find(gomock.Any(), int64(7)).Return(
		&project_entity.Project{ID: 7, Path: "/repo", Status: consts.ACTIVE}, nil)

	mockBE := mocks.NewMockPTYBackend(ctrl)
	mockH := mocks.NewMockHandle(ctrl)
	mockH.EXPECT().Data().AnyTimes().Return(make(chan []byte))
	mockH.EXPECT().Exit().AnyTimes().Return(make(chan pty.ExitInfo))
	mockH.EXPECT().Close().AnyTimes().Return(nil)
	var gotSpec pty.Spec
	mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, spec pty.Spec) (pty.Handle, error) {
			gotSpec = spec
			return mockH, nil
		})

	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(mockBE, nil), terminal_svc.NoopEmitter{})
	defer svc.Shutdown()
	a := &App{ctx: context.Background(), terminalSvc: svc}

	require.NoError(t, a.TerminalOpen("t1", 7, "", 80, 24))
	// cwd came from ResolveProjectCwd (local project.Path), dims passed through.
	assert.Equal(t, pty.Spec{TerminalID: "t1", Cwd: "/repo", Cols: 80, Rows: 24}, gotSpec)
}

// TestApp_TerminalOpen_PropagatesResolveErrorWithoutOpening locks that a cwd
// resolution failure (e.g. project deleted) surfaces as an error and never
// reaches the backend — no PTY is spawned for a project we can't locate.
func TestApp_TerminalOpen_PropagatesResolveErrorWithoutOpening(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockProj := mock_project_repo.NewMockProjectRepo(ctrl)
	project_repo.RegisterProject(mockProj)
	mockProj.EXPECT().Find(gomock.Any(), int64(7)).Return(nil, nil) // not found → ProjectNotFound

	// No Open expectation: backend.Open must NOT be called when cwd resolution fails.
	mockBE := mocks.NewMockPTYBackend(ctrl)
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(mockBE, nil), terminal_svc.NoopEmitter{})
	a := &App{ctx: context.Background(), terminalSvc: svc}

	require.Error(t, a.TerminalOpen("t1", 7, "", 80, 24))
}

// stubExecTarget 只实现 ResolveSessionWorkspace;嵌入的接口留 nil,碰到别的方法当场 panic。
type stubExecTarget struct {
	exec_target_svc.ExecTargetSvc

	deviceID int64
	cwd      string
}

func (s *stubExecTarget) ResolveSessionWorkspace(context.Context, int64) (int64, string, error) {
	return s.deviceID, s.cwd, nil
}

// openForSessionRig 装好会话终端用到的三个接缝:执行设备/cwd、已认领工作根、PTY 后端,
// 并给出「后端实际收到的 spec / 设备」的读取口。
type openForSessionRig struct {
	app      *App
	opened   func() (pty.Spec, int)
	deviceID func() string
}

func newOpenForSessionRig(t *testing.T, ws *stubExecTarget, roots []workspace_fs_svc.WorkRootView) *openForSessionRig {
	t.Helper()
	ctrl := gomock.NewController(t)

	origET := exec_target_svc.ExecTarget()
	exec_target_svc.RegisterExecTarget(ws)
	t.Cleanup(func() { exec_target_svc.RegisterExecTarget(origET) })

	origWS := workspace_fs_svc.Default()
	workspace_fs_svc.RegisterWorkspaceFs(&stubWorkspaceFs{roots: roots})
	t.Cleanup(func() { workspace_fs_svc.RegisterWorkspaceFs(origWS) })

	mockH := mocks.NewMockHandle(ctrl)
	mockH.EXPECT().Data().AnyTimes().Return(make(chan []byte))
	mockH.EXPECT().Exit().AnyTimes().Return(make(chan pty.ExitInfo))
	mockH.EXPECT().Close().AnyTimes().Return(nil)

	var (
		gotSpec  pty.Spec
		opens    int
		gotDevID string
	)
	mockBE := mocks.NewMockPTYBackend(ctrl)
	mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, spec pty.Spec) (pty.Handle, error) {
			gotSpec = spec
			opens++
			return mockH, nil
		})
	remote := func(deviceID string) (terminal_svc.PTYBackend, error) {
		gotDevID = deviceID
		return mockBE, nil
	}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(mockBE, remote), terminal_svc.NoopEmitter{})
	t.Cleanup(svc.Shutdown)

	return &openForSessionRig{
		app:      &App{ctx: context.Background(), terminalSvc: svc},
		opened:   func() (pty.Spec, int) { return gotSpec, opens },
		deviceID: func() string { return gotDevID },
	}
}

func TestApp_TerminalOpenForSession(t *testing.T) {
	roots := []workspace_fs_svc.WorkRootView{
		{Path: "/repo", Name: "repo", IsPrimary: true},
		{Path: "/repo/.worktrees/x", Name: "x", IsWorktree: true},
	}

	t.Run("empty root starts in the session cwd", func(t *testing.T) {
		rig := newOpenForSessionRig(t, &stubExecTarget{cwd: "/repo"}, roots)
		require.NoError(t, rig.app.TerminalOpenForSession("t1", 42, "", 80, 24))
		spec, _ := rig.opened()
		assert.Equal(t, pty.Spec{TerminalID: "t1", Cwd: "/repo", Cols: 80, Rows: 24}, spec)
	})

	t.Run("a claimed worktree root is the start directory", func(t *testing.T) {
		rig := newOpenForSessionRig(t, &stubExecTarget{cwd: "/repo"}, roots)
		require.NoError(t, rig.app.TerminalOpenForSession("t1", 42, "/repo/.worktrees/x", 100, 30))
		spec, _ := rig.opened()
		assert.Equal(t, pty.Spec{TerminalID: "t1", Cwd: "/repo/.worktrees/x", Cols: 100, Rows: 30}, spec)
	})

	t.Run("a remote session opens on the executing device at the device path", func(t *testing.T) {
		remoteRoots := []workspace_fs_svc.WorkRootView{
			{Path: "/srv/app", Name: "app", IsPrimary: true},
			{Path: "/srv/app-wt", Name: "app-wt", IsWorktree: true},
		}
		rig := newOpenForSessionRig(t, &stubExecTarget{deviceID: 9, cwd: "/srv/app"}, remoteRoots)
		require.NoError(t, rig.app.TerminalOpenForSession("t1", 42, "/srv/app-wt", 80, 24))
		spec, opens := rig.opened()
		assert.Equal(t, 1, opens)
		assert.Equal(t, "9", rig.deviceID())
		assert.Equal(t, "/srv/app-wt", spec.Cwd)
	})

	t.Run("an unclaimed root is refused and nothing is spawned", func(t *testing.T) {
		rig := newOpenForSessionRig(t, &stubExecTarget{cwd: "/repo"}, roots)
		require.Error(t, rig.app.TerminalOpenForSession("t1", 42, "/etc", 80, 24))
		_, opens := rig.opened()
		assert.Zero(t, opens)
	})

	t.Run("a sub directory of a claimed root is not a claimed root", func(t *testing.T) {
		rig := newOpenForSessionRig(t, &stubExecTarget{cwd: "/repo"}, roots)
		require.Error(t, rig.app.TerminalOpenForSession("t1", 42, "/repo/sub", 80, 24))
		_, opens := rig.opened()
		assert.Zero(t, opens)
	})

	t.Run("a session without a work directory cannot open a terminal", func(t *testing.T) {
		rig := newOpenForSessionRig(t, &stubExecTarget{cwd: ""}, nil)
		require.Error(t, rig.app.TerminalOpenForSession("t1", 42, "", 80, 24))
		_, opens := rig.opened()
		assert.Zero(t, opens)
	})

	t.Run("nil service is guarded", func(t *testing.T) {
		require.ErrorIs(t, (&App{}).TerminalOpenForSession("t1", 42, "", 80, 24), errTerminalSvcNotInitialized)
	})
}
