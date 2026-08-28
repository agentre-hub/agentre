package handlers_test

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestTerminal_CloseAll_ClosesLiveHandlesAndClearsMap verifies that CloseAll
// terminates every live PTY and empties the registry. The daemon calls this
// when a desktop connection drops, so orphaned remote shells don't leak.
func TestTerminal_CloseAll_ClosesLiveHandlesAndClearsMap(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mbe := mock_handlers.NewMockPTYBackend(ctrl)
	mh1 := mock_handlers.NewMockPTYHandle(ctrl)
	mh2 := mock_handlers.NewMockPTYHandle(ctrl)
	for _, mh := range []*mock_handlers.MockPTYHandle{mh1, mh2} {
		mh.EXPECT().Data().AnyTimes().Return(make(chan []byte))
		mh.EXPECT().Exit().AnyTimes().Return(make(chan pty.ExitInfo))
		mh.EXPECT().Close().Return(nil)
	}
	gomock.InOrder(
		mbe.EXPECT().Open(gomock.Any(), gomock.Any()).Return(mh1, nil),
		mbe.EXPECT().Open(gomock.Any(), gomock.Any()).Return(mh2, nil),
	)

	h := handlers.NewTerminalHandlers(mbe, &recordingEmitter{})
	r1, _ := h.Open(context.Background(), protocol.TerminalOpenParams{Cols: 80, Rows: 24})
	r2, _ := h.Open(context.Background(), protocol.TerminalOpenParams{Cols: 80, Rows: 24})

	h.CloseAll()

	_, err := h.Write(context.Background(), protocol.TerminalWriteParams{TerminalID: r1.TerminalID})
	require.ErrorIs(t, err, handlers.ErrTerminalNotFound)
	_, err = h.Write(context.Background(), protocol.TerminalWriteParams{TerminalID: r2.TerminalID})
	require.ErrorIs(t, err, handlers.ErrTerminalNotFound)
}
