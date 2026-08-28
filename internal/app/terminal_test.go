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
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc/mocks"
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
