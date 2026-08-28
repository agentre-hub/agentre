package protorpc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestGenericTypedMethodRegistrationAndCall(t *testing.T) {
	terminalWriteID := uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_WRITE)
	a, b := pipePair()
	reg := protorpc.NewRegistry()
	protorpc.RegisterMethod(reg, terminalWriteID, func() *agentrewire.TerminalWriteRequest { return &agentrewire.TerminalWriteRequest{} }, func(_ context.Context, request *agentrewire.TerminalWriteRequest) (*agentrewire.Empty, error) {
		require.Equal(t, []byte{0, 255}, request.Data)
		return &agentrewire.Empty{}, nil
	})
	client, server := protorpc.NewConn(a, protorpc.NewRegistry()), protorpc.NewConn(b, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)
	response, err := protorpc.CallMethod(ctx, client, terminalWriteID, &agentrewire.TerminalWriteRequest{Data: []byte{0, 255}}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.NoError(t, err)
	require.NotNil(t, response)
}

func TestGenericMethodRejectsResponseForAnotherMethod(t *testing.T) {
	const methodID uint32 = 99
	a, b := pipePair()
	client := protorpc.NewConn(a, protorpc.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go func() {
		requestBytes, _ := b.ReadFrame()
		var request agentrewire.RpcFrame
		require.NoError(t, proto.Unmarshal(requestBytes, &request))
		responseBytes, _ := proto.Marshal(&agentrewire.RpcFrame{Id: request.Id, Body: &agentrewire.RpcFrame_Response{Response: &agentrewire.Response{MethodId: methodID + 1}}})
		require.NoError(t, b.WriteFrame(responseBytes))
	}()
	_, err := protorpc.CallMethod(ctx, client, methodID, &agentrewire.Empty{}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.ErrorIs(t, err, protorpc.ErrResponseType)
}

// The peer answered for the right method, but with a body that is not this
// response message at all. Given such a frame, When the caller decodes it, Then it
// must surface ErrResponseType rather than a silently empty response.
func TestGenericMethodRejectsUndecodableResponsePayload(t *testing.T) {
	const methodID uint32 = 99
	a, b := pipePair()
	client := protorpc.NewConn(a, protorpc.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go func() {
		requestBytes, _ := b.ReadFrame()
		var request agentrewire.RpcFrame
		require.NoError(t, proto.Unmarshal(requestBytes, &request))
		responseBytes, _ := proto.Marshal(&agentrewire.RpcFrame{Id: request.Id, Body: &agentrewire.RpcFrame_Response{Response: &agentrewire.Response{
			MethodId: methodID, EncodedPayload: []byte{0xFF},
		}}})
		require.NoError(t, b.WriteFrame(responseBytes))
	}()
	_, err := protorpc.CallMethod(ctx, client, methodID, &agentrewire.Empty{}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.ErrorIs(t, err, protorpc.ErrResponseType)
}
