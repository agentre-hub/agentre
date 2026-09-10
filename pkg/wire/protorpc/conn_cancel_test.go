package protorpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Given a long-lived connection, When Cancel frames keep arriving for requests
// that were never dispatched (or already finished), Then no pre-cancel entry may
// survive: request IDs increase monotonically per connection, so an entry that is
// never consumed is leaked for the lifetime of the daemon connection.
func TestConnCancel_GivenCancelForUndispatchedRequest_WhenFramesArrive_ThenNoPreCancelEntryIsRetained(t *testing.T) {
	conn := NewConn(newDisconnectedFrameConn(), NewRegistry())

	for id := uint64(1); id <= 1000; id++ {
		conn.cancel(id)
	}

	conn.inflightMu.Lock()
	defer conn.inflightMu.Unlock()
	require.Empty(t, conn.canceled, "cancel frames for undispatched requests leaked pre-cancel entries")
}

// Given the read loop dispatched a request but its handler goroutine has not
// registered yet, When the Cancel frame for that ID is processed, Then the handler
// must still observe a canceled context once it starts.
func TestConnCancel_GivenCancelBeforeHandlerRegisters_WhenHandlerStarts_ThenItSeesACanceledContext(t *testing.T) {
	methodID := uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY)
	registry := NewRegistry()
	handlerCtxErr := make(chan error, 1)
	RegisterMethod(
		registry,
		methodID,
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(ctx context.Context, _ *agentrewire.Empty) (*agentrewire.Empty, error) {
			handlerCtxErr <- ctx.Err()
			return &agentrewire.Empty{}, nil
		},
	)
	conn := NewConn(newDisconnectedFrameConn(), registry)
	payload, err := proto.Marshal(&agentrewire.Empty{})
	require.NoError(t, err)

	// Exactly the order Serve produces: the request frame marks the ID dispatched,
	// the cancel frame arrives while the handler goroutine is still unscheduled.
	const requestID = uint64(7)
	conn.markDispatched(requestID)
	conn.cancel(requestID)
	conn.handle(context.Background(), requestID, &agentrewire.Request{MethodId: methodID, EncodedPayload: payload})

	require.ErrorIs(t, <-handlerCtxErr, context.Canceled)
	conn.inflightMu.Lock()
	defer conn.inflightMu.Unlock()
	require.Empty(t, conn.canceled)
	require.Empty(t, conn.inflight)
}
