package daemon

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/daemon/connection"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	statepkg "github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestProtobufAuthAccountResponsePreservesInstanceUUID(t *testing.T) {
	response := protobufAuthAccountResponse(&auth.AccountResult{
		OK: true, InstanceUUID: "daemon-instance", PeerFingerprint: "sha256:from-credential",
	})
	require.True(t, response.GetOk())
	require.Equal(t, "daemon-instance", response.GetInstanceUuid())
	// 决策 8:应答回写 daemon 认定的对端身份 —— 调用方本端指纹的唯一来源。
	require.Equal(t, "sha256:from-credential", response.GetPeerFingerprint())
}

type protobufTestPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func protobufTestPipePair() (*protobufTestPipe, *protobufTestPipe) {
	a, b := make(chan []byte, 4), make(chan []byte, 4)
	done := make(chan struct{})
	once := &sync.Once{}
	return &protobufTestPipe{in: a, out: b, done: done, once: once}, &protobufTestPipe{in: b, out: a, done: done, once: once}
}
func (p *protobufTestPipe) ReadFrame() ([]byte, error) {
	select {
	case frame := <-p.in:
		return frame, nil
	case <-p.done:
		return nil, io.EOF
	}
}
func (p *protobufTestPipe) WriteFrame(frame []byte) error {
	select {
	case p.out <- append([]byte(nil), frame...):
		return nil
	case <-p.done:
		return io.EOF
	}
}
func (p *protobufTestPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *protobufTestPipe) Done() <-chan struct{} { return p.done }

func TestProtobufRegistryPairsAndAuthenticatesConnection(t *testing.T) {
	t.Run("given a valid pairing code, when auth pair is called, then the protobuf connection becomes authenticated", func(t *testing.T) {
		daemon, err := New(Options{DataDir: t.TempDir()})
		require.NoError(t, err)
		t.Cleanup(func() { closeDB(daemon.db) })
		code, err := daemon.pairing.Generate()
		require.NoError(t, err)
		clientTransport, serverTransport := protobufTestPipePair()
		client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
		server := protorpc.NewConn(serverTransport, daemon.protobufRegistry.Clone())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go client.Serve(ctx)
		go server.Serve(ctx)

		response, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR), &agentrewire.AuthPairRequest{
			Code: code, DeviceName: "desktop", DeviceFingerprint: "device-1", ProtocolVersion: wireversion.Protocol,
		}, func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })
		require.NoError(t, err)
		require.NotEmpty(t, response.DeviceToken)
		require.Equal(t, "device-1", server.Auth().DeviceFingerprint)
		require.True(t, server.Auth().Authenticated)
		daemon.conns.mu.Lock()
		require.Contains(t, daemon.conns.live, connection.Protobuf(server))
		daemon.conns.mu.Unlock()
	})

	t.Run("given an empty device fingerprint, when auth pair is called, then invalid params is returned", func(t *testing.T) {
		daemon, err := New(Options{DataDir: t.TempDir()})
		require.NoError(t, err)
		t.Cleanup(func() { closeDB(daemon.db) })
		clientTransport, serverTransport := protobufTestPipePair()
		client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
		server := protorpc.NewConn(serverTransport, daemon.protobufRegistry.Clone())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go client.Serve(ctx)
		go server.Serve(ctx)

		_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR),
			&agentrewire.AuthPairRequest{Code: "123456", ProtocolVersion: wireversion.Protocol}, func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })
		var rpcErr *protorpc.Error
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, protorpc.CodeInvalidParams, rpcErr.Code)
		require.False(t, server.Auth().Authenticated)
	})
}

func TestProtobufRegistryRequiresAuthAndListsMaskedProviders(t *testing.T) {
	daemon, err := New(Options{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { closeDB(daemon.db) })
	daemon.state.Mutate(func(value *statepkg.State) {
		value.LLMProviders["provider-1"] = statepkg.LLMProviderMeta{Name: "Provider", Type: "openai", APIKey: "secret-value", UpdatedAt: 7}
	})
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, daemon.protobufRegistry.Clone())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	call := func() (*agentrewire.LLMListResponse, error) {
		return protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_LLM_LIST),
			&agentrewire.LLMListRequest{}, func() *agentrewire.LLMListResponse { return &agentrewire.LLMListResponse{} })
	}
	_, err = call()
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, int32(-32001), rpcErr.Code)

	server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "device-1"})
	response, err := call()
	require.NoError(t, err)
	require.Len(t, response.Providers, 1)
	require.Equal(t, "...alue", response.Providers[0].MaskedTail)
}

func TestProtobufConnectionRegistersPerConnectionRuntimeMethods(t *testing.T) {
	daemon, err := New(Options{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { closeDB(daemon.db) })
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, daemon.protobufRegistry.Clone())
	daemon.bindProtobufConn(server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	call := func() (*agentrewire.RuntimeCapabilitiesResponse, error) {
		return protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES),
			&agentrewire.RuntimeCapabilitiesRequest{BackendType: "claudecode"}, func() *agentrewire.RuntimeCapabilitiesResponse { return &agentrewire.RuntimeCapabilitiesResponse{} })
	}
	_, err = call()
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, int32(-32001), rpcErr.Code)

	server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "device-1"})
	response, err := call()
	require.NoError(t, err)
	require.NotEmpty(t, response.Capabilities)
}
