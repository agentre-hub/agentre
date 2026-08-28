package terminal_svc_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/pty/remote"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type remoteCloseClient struct {
	data         chan protocol.TerminalDataEvent
	exit         chan protocol.TerminalExitEvent
	closeCalls   atomic.Int32
	closeResults []error
}

func (c *remoteCloseClient) Call(_ context.Context, method string, params any, out any) error {
	switch method {
	case "terminal.open":
		terminalID := params.(protocol.TerminalOpenParams).TerminalID
		*(out.(*protocol.TerminalOpenResult)) = protocol.TerminalOpenResult{TerminalID: terminalID}
	case "terminal.close":
		call := int(c.closeCalls.Add(1))
		if call <= len(c.closeResults) {
			return c.closeResults[call-1]
		}
	}
	return nil
}

func (c *remoteCloseClient) Subscribe(string) remote.Subscription {
	return remote.Subscription{Data: c.data, Exit: c.exit}
}

func (c *remoteCloseClient) Unsubscribe(string, remote.Subscription) {}
func (c *remoteCloseClient) Abort() error                            { return nil }

func TestService_GivenRunningRemoteCommandWhenCloseSucceedsThenPreventsFuturePumpEmissions(t *testing.T) {
	client := &remoteCloseClient{
		data: make(chan protocol.TerminalDataEvent),
		exit: make(chan protocol.TerminalExitEvent),
	}
	remoteBackend := remote.NewBackend(client)
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(
		terminal_svc.NewBackendSelector(nil, func(deviceID string) (terminal_svc.PTYBackend, error) {
			assert.Equal(t, "device-1", deviceID)
			return remoteBackend, nil
		}),
		emitter,
	)

	require.NoError(t, svc.OpenCommand(
		context.Background(), "terminal-1", "device-1", "/remote", "go test ./...", 80, 24,
	))
	require.NoError(t, svc.Close(context.Background(), "terminal-1"))
	settledEvents := len(emitter.Snapshot())
	require.Never(t, func() bool {
		return len(emitter.Snapshot()) > settledEvents
	}, 100*time.Millisecond, time.Millisecond,
		"a confirmed Close must retire the captured entry before late pump output")
	assert.ErrorIs(t, svc.Write(context.Background(), "terminal-1", "x"), terminal_svc.ErrTerminalClosed)
	assert.Equal(t, int32(1), client.closeCalls.Load())
}

func TestService_GivenRemoteCloseRPCFailsWhenRetriedThenRetainsSameSessionUntilSuccess(t *testing.T) {
	rpcErr := errors.New("terminal.close unavailable")
	client := &remoteCloseClient{
		data:         make(chan protocol.TerminalDataEvent),
		exit:         make(chan protocol.TerminalExitEvent),
		closeResults: []error{rpcErr, nil},
	}
	remoteBackend := remote.NewBackend(client)
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(
		terminal_svc.NewBackendSelector(nil, func(string) (terminal_svc.PTYBackend, error) {
			return remoteBackend, nil
		}),
		emitter,
	)

	require.NoError(t, svc.OpenCommand(
		context.Background(), "terminal-retry", "device-1", "/remote", "go test ./...", 80, 24,
	))
	require.ErrorIs(t, svc.Close(context.Background(), "terminal-retry"), rpcErr)
	require.NoError(t, svc.Write(context.Background(), "terminal-retry", "x"),
		"failed close must retain the same live remote handle for retry")
	client.data <- protocol.TerminalDataEvent{
		TerminalID: "terminal-retry",
		Data:       []byte("still-active"),
	}
	require.Eventually(t, func() bool {
		return len(emitter.Snapshot()) == 1
	}, time.Second, time.Millisecond,
		"failed Close must retain active pump emissions")
	require.Equal(t, []byte("still-active"), recordedData(t, emitter.Snapshot()[0]))

	require.NoError(t, svc.Close(context.Background(), "terminal-retry"))
	settledEvents := len(emitter.Snapshot())
	require.Never(t, func() bool {
		return len(emitter.Snapshot()) > settledEvents
	}, 100*time.Millisecond, time.Millisecond,
		"successful retry must retire the entry before late killed exit")
	assert.ErrorIs(t, svc.Write(context.Background(), "terminal-retry", "x"), terminal_svc.ErrTerminalClosed)
	assert.Equal(t, int32(2), client.closeCalls.Load())
}
