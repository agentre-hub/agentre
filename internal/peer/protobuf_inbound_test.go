package peer

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/protobufadapter"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/daemon/remotefs"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type peerProtoPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func peerProtoPipePair() (*peerProtoPipe, *peerProtoPipe) {
	a, b := make(chan []byte, 4), make(chan []byte, 4)
	done := make(chan struct{})
	once := &sync.Once{}
	return &peerProtoPipe{a, b, done, once}, &peerProtoPipe{b, a, done, once}
}
func (p *peerProtoPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}
func (p *peerProtoPipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}
func (p *peerProtoPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *peerProtoPipe) Done() <-chan struct{} { return p.done }

func TestProtobufInboundRegistryAuthenticatesAndReusesPeripheralAdapters(t *testing.T) {
	registry := NewProtobufInboundRegistry(ProtobufInboundDeps{
		Peripheral:              protobufadapter.PeripheralDeps{RemoteFS: remotefs.NewHandlers(remotefs.Options{})},
		VerifyAccountCredential: func(context.Context, string) (string, error) { return "peer-1", nil },
	})
	clientTransport, serverTransport := peerProtoPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_LIST_DIR), &agentrewire.RemoteFsListDirRequest{}, func() *agentrewire.RemoteFsListDirResponse { return &agentrewire.RemoteFsListDirResponse{} })
	var unauthorized *protorpc.Error
	require.ErrorAs(t, err, &unauthorized)
	require.Equal(t, int32(-32001), unauthorized.Code)

	auth, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		&agentrewire.AuthAccountRequest{Credential: "credential", ProtocolVersion: wireversion.Protocol}, func() *agentrewire.AuthAccountResponse { return &agentrewire.AuthAccountResponse{} })
	require.NoError(t, err)
	require.True(t, auth.Ok)
	// 身份来自验证器交出的那个值(请求体里已经没有可自报的字段),并原样回写给调用方。
	require.Equal(t, "peer-1", server.Auth().DeviceFingerprint)
	require.Equal(t, "peer-1", auth.GetPeerFingerprint())

	_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_LIST_DIR), &agentrewire.RemoteFsListDirRequest{}, func() *agentrewire.RemoteFsListDirResponse { return &agentrewire.RemoteFsListDirResponse{} })
	require.NoError(t, err)
}

func TestProtobufInboundRegistryRejectsIncompleteAccountAuth(t *testing.T) {
	registry := NewProtobufInboundRegistry(ProtobufInboundDeps{
		VerifyAccountCredential: func(context.Context, string) (string, error) { return "peer-1", nil },
	})
	clientTransport, serverTransport := peerProtoPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		&agentrewire.AuthAccountRequest{ProtocolVersion: wireversion.Protocol}, func() *agentrewire.AuthAccountResponse { return &agentrewire.AuthAccountResponse{} })
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, protorpc.CodeInvalidParams, rpcErr.Code)
	require.False(t, server.Auth().Authenticated)
}

func TestProtobufInboundRegistryServesPeerSessionList(t *testing.T) {
	registry := NewProtobufInboundRegistry(ProtobufInboundDeps{ListSessions: func(context.Context, string) (*remotewire.SessionListResult, error) {
		return &remotewire.SessionListResult{Sessions: []remotewire.SessionSummary{{ConversationID: convID(7), Title: "remote"}}}, nil
	}})
	clientTransport, serverTransport := peerProtoPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "sha256:caller"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	response, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST), &agentrewire.SessionListRequest{}, func() *agentrewire.SessionListResponse { return &agentrewire.SessionListResponse{} })
	require.NoError(t, err)
	require.Len(t, response.Sessions, 1)
	require.Equal(t, convID(7), response.Sessions[0].ConversationId)
}

func TestProtobufInboundRegistryServesPeerSessionControlMethods(t *testing.T) {
	var steered remotewire.SteerParams
	deps := ProtobufInboundDeps{
		AttachSession: func(_ context.Context, p remotewire.SessionAttachParams, _ chat_svc.PeerSessionSubscriber) (remotewire.SessionAttachResult, error) {
			require.Equal(t, convID(7), p.ConversationID)
			return remotewire.SessionAttachResult{ConversationID: convID(7), LatestSeq: 12}, nil
		},
		PullSession: func(_ context.Context, p remotewire.SessionPullParams, _ chat_svc.PeerSessionSubscriber) (remotewire.SessionPullResult, error) {
			require.Equal(t, int64(3), p.Cursor)
			return remotewire.SessionPullResult{Cursor: 4, OldestSeq: 1}, nil
		},
		RunSession: func(_ context.Context, p remotewire.RunParams, source chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error) {
			require.True(t, p.FreshSession)
			require.Equal(t, "sha256:caller", source.Device)
			return &chat_svc.SendResponse{SessionID: 42}, nil
		},
		SteerSession: func(_ context.Context, p remotewire.SteerParams, _ chat_svc.PeerSessionSource) error {
			steered = p
			return nil
		},
		SubmitAnswer: func(_ context.Context, p remotewire.SubmitAnswerParams) (chat_svc.PeerSessionControlResult, error) {
			require.Equal(t, "answer", p.RequestID)
			return chat_svc.PeerSessionControlResult{AlreadyHandled: true}, nil
		},
		SubmitToolPermission: func(_ context.Context, p remotewire.SubmitToolPermissionParams) (chat_svc.PeerSessionControlResult, error) {
			require.True(t, p.Allow)
			return chat_svc.PeerSessionControlResult{AlreadyHandled: true}, nil
		},
	}
	registry := NewProtobufInboundRegistry(deps)
	clientTransport, serverTransport := peerProtoPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "sha256:caller"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	attach, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH), &agentrewire.SessionAttachRequest{ConversationId: convID(7)}, func() *agentrewire.SessionAttachResponse { return &agentrewire.SessionAttachResponse{} })
	require.NoError(t, err)
	require.Equal(t, int64(12), attach.LatestSeq)
	pull, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL), &agentrewire.SessionPullRequest{ConversationId: convID(7), Cursor: 3}, func() *agentrewire.SessionPullResponse { return &agentrewire.SessionPullResponse{} })
	require.NoError(t, err)
	require.Equal(t, int64(4), pull.Cursor)
	run, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), &agentrewire.RuntimeRunRequest{ConversationId: convID(99), FreshSession: true, UserText: "go"}, func() *agentrewire.RuntimeRunResponse { return &agentrewire.RuntimeRunResponse{} })
	require.NoError(t, err)
	require.Equal(t, convID(99), run.ConversationId, "对话身份是发起端铸的那一个,对端不得改写")
	_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER), &agentrewire.RuntimeSteerRequest{ConversationId: convID(7), Text: "continue"}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.NoError(t, err)
	require.Equal(t, "continue", steered.Text)
	answer, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER), &agentrewire.RuntimeSubmitAnswerRequest{ConversationId: convID(7), RequestId: "answer"}, func() *agentrewire.PeerSessionControlResponse { return &agentrewire.PeerSessionControlResponse{} })
	require.NoError(t, err)
	require.True(t, answer.AlreadyHandled)
	permission, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION), &agentrewire.RuntimeSubmitToolPermissionRequest{ConversationId: convID(7), RequestId: "permission", Allow: true}, func() *agentrewire.PeerSessionControlResponse { return &agentrewire.PeerSessionControlResponse{} })
	require.NoError(t, err)
	require.True(t, permission.AlreadyHandled)
}

// TestProtobufInboundRegistry_GivenAnUnverifiableCredential_ThenRejectsTheHandshake
// 钉住决策 8 在**入站对端**这条 Mode C 上的形态:凭据非空不等于凭据成立。没有验证
// 能力(没装配 VerifyAccountCredential)的注册表必须拒绝握手 —— 而不是采信请求体里
// 那个自报指纹,把一个谁都能编出来的字符串写进 AuthState。
func TestProtobufInboundRegistry_GivenAnUnverifiableCredential_ThenRejectsTheHandshake(t *testing.T) {
	registry := NewProtobufInboundRegistry(ProtobufInboundDeps{})
	clientTransport, serverTransport := peerProtoPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		&agentrewire.AuthAccountRequest{Credential: "not-a-real-credential", ProtocolVersion: wireversion.Protocol}, //nolint:gosec // G101: 这就是本用例的主角——一个编出来的凭据。
		func() *agentrewire.AuthAccountResponse { return &agentrewire.AuthAccountResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, int32(-32001), rpcErr.Code)
	require.False(t, server.Auth().Authenticated)
	require.Empty(t, server.Auth().DeviceFingerprint)
}

// TestProtobufInboundRegistry_GivenAVerifierThatRejects_ThenRefusesTheHandshake
// 验证器说不行就是不行:与「没有验证器」同一形态被拒,连接不留任何身份。
func TestProtobufInboundRegistry_GivenAVerifierThatRejects_ThenRefusesTheHandshake(t *testing.T) {
	registry := NewProtobufInboundRegistry(ProtobufInboundDeps{
		VerifyAccountCredential: func(context.Context, string) (string, error) {
			return "", errors.New("account credential signature invalid")
		},
	})
	clientTransport, serverTransport := peerProtoPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		&agentrewire.AuthAccountRequest{Credential: "forged", ProtocolVersion: wireversion.Protocol},
		func() *agentrewire.AuthAccountResponse { return &agentrewire.AuthAccountResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, int32(-32001), rpcErr.Code)
	require.False(t, server.Auth().Authenticated)
	require.Empty(t, server.Auth().DeviceFingerprint)
}
