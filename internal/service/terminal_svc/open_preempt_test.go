package terminal_svc_test

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc/mocks"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestService_Open_SuccessAfterClose_ClosesHandleNotRegistered covers the race
// where Close cancels an in-flight Open, but backend.Open still returns a live
// handle (the spawn won the race against cancellation). The service must close
// that handle rather than register it — otherwise, for a remote backend, the
// daemon-side PTY leaks (the user clicked close, but the shell keeps running).
func TestService_Open_SuccessAfterClose_ClosesHandleNotRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBE := mocks.NewMockPTYBackend(ctrl)
	mockH := mocks.NewMockHandle(ctrl)

	started := make(chan struct{})
	proceed := make(chan struct{})
	mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ pty.Spec) (pty.Handle, error) {
			close(started)
			<-proceed
			return mockH, nil // spawn succeeded despite the concurrent Close
		})
	// The preempted handle must be closed and never registered. No pump should
	// start, so Data()/Exit() must NOT be consumed.
	mockH.EXPECT().Close().Return(nil)

	sel := terminal_svc.NewBackendSelector(mockBE, nil)
	svc := terminal_svc.NewService(sel, terminal_svc.NoopEmitter{})

	openErr := make(chan error, 1)
	go func() { openErr <- svc.Open(context.Background(), "t1", "", "/tmp", 80, 24) }()

	<-started
	require.NoError(t, svc.Close(context.Background(), "t1")) // preempt the in-flight Open
	close(proceed)                                            // backend.Open now returns success
	require.NoError(t, <-openErr)

	// The handle must not be registered.
	require.ErrorIs(t, svc.Write(context.Background(), "t1", "x"), terminal_svc.ErrTerminalClosed)
}
