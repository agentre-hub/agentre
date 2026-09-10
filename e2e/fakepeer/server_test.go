package fakepeer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

const (
	testDeviceFingerprint    = "sha256:e2e-desktop"
	testDeviceAuthValue      = "e2e-device-auth-value"
	testInstanceUUID         = "e2e-fake-peer-instance"
	testControlAuthorization = "e2e-control-auth-value"
)

func startTestServer(t *testing.T) *Server {
	t.Helper()
	server, err := Start(context.Background(), Options{DeviceFingerprint: testDeviceFingerprint, DeviceToken: testDeviceAuthValue, InstanceUUID: testInstanceUUID, ControlToken: testControlAuthorization})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Close()) })
	return server
}

func authenticatedClient(t *testing.T, server *Server) *client.ProtobufClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	cli, err := client.DialProtobuf(ctx, client.Options{URL: server.URL()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	response, err := cli.AuthConnect(ctx, &agentrewire.AuthConnectRequest{DeviceFingerprint: testDeviceFingerprint, DeviceToken: testDeviceAuthValue, ExpectedDaemonFingerprint: identity.DaemonFingerprint(testInstanceUUID)})
	require.NoError(t, err)
	require.True(t, response.Ok)
	return cli
}

func TestServerGivenTypedRuntimeRunThenStreamsAndJournalsBinaryNotifications(t *testing.T) {
	server := startTestServer(t)
	cli := authenticatedClient(t, server)
	events := make(chan *agentrewire.RpcNotification, 8)
	unsubscribe := cli.Conn().Registry().SubscribeNotification(func(_ context.Context, notification *agentrewire.RpcNotification) error {
		events <- notification
		return nil
	})
	defer unsubscribe()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := protorpc.CallMethod(ctx, cli.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), &agentrewire.RuntimeRunRequest{ConversationId: convID(42), UserText: "hello"}, func() *agentrewire.RuntimeRunResponse { return &agentrewire.RuntimeRunResponse{} })
	require.NoError(t, err)
	assert.Equal(t, convID(42), response.ConversationId)
	finished := false
	for !finished {
		select {
		case notification := <-events:
			finished = notification.GetRunResultDone() != nil
		case <-ctx.Done():
			t.Fatal("typed run did not finish")
		}
	}
	pull, err := protorpc.CallMethod(ctx, cli.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL), &agentrewire.SessionPullRequest{ConversationId: convID(42)}, func() *agentrewire.SessionPullResponse { return &agentrewire.SessionPullResponse{} })
	require.NoError(t, err)
	require.NotEmpty(t, pull.Notifications)
	assert.NotNil(t, pull.Notifications[len(pull.Notifications)-1].Payload.GetRunResultDone())
}

func TestServerGivenRecoverableDisconnectWhenClientReconnectsThenJournalCanBeAttachedAndPulledToTerminal(t *testing.T) {
	server := startTestServer(t)
	server.SetNextRunFault(FaultRecoverableDisconnect)
	cli := authenticatedClient(t, server)
	closed := cli.Closed()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := protorpc.CallMethod(ctx, cli.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), &agentrewire.RuntimeRunRequest{ConversationId: convID(43), UserText: "recover"}, func() *agentrewire.RuntimeRunResponse { return &agentrewire.RuntimeRunResponse{} })
	require.NoError(t, err)
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("recoverable fault did not disconnect the owning client")
	}

	reconnected := authenticatedClient(t, server)
	var attached *agentrewire.SessionAttachResponse
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var callErr error
		attached, callErr = protorpc.CallMethod(ctx, reconnected.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH), &agentrewire.SessionAttachRequest{ConversationId: convID(43)}, func() *agentrewire.SessionAttachResponse { return &agentrewire.SessionAttachResponse{} })
		assert.NoError(collect, callErr)
		if attached != nil {
			assert.Equal(collect, wire.SessionLifecycleIdle, attached.LifecycleState)
			assert.Equal(collect, int64(5), attached.LatestSeq)
		}
	}, time.Second, 10*time.Millisecond)

	pull, err := protorpc.CallMethod(ctx, reconnected.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL), &agentrewire.SessionPullRequest{ConversationId: convID(43)}, func() *agentrewire.SessionPullResponse { return &agentrewire.SessionPullResponse{} })
	require.NoError(t, err)
	require.Len(t, pull.Notifications, 5)
	assert.Equal(t, int64(5), pull.Cursor)
	assert.Equal(t, "remote-peer-", pull.Notifications[0].Payload.GetRuntimeEvent().GetTextDelta().Text)
	assert.NotNil(t, pull.Notifications[len(pull.Notifications)-1].Payload.GetRunResultDone())
}

func TestServerGivenWrongTypedCredentialThenReturnsStableUnauthorized(t *testing.T) {
	server := startTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cli, err := client.DialProtobuf(ctx, client.Options{URL: server.URL()})
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()
	_, err = cli.AuthConnect(ctx, &agentrewire.AuthConnectRequest{DeviceFingerprint: testDeviceFingerprint, DeviceToken: "wrong", ExpectedDaemonFingerprint: server.DaemonFingerprint()})
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, int32(-32001), rpcErr.Code)
}

// convID 把一个短会话号折成一条**格式合法**的 conversation_id,只在测试里用:
// 线上身份是 uuid,而这些用例真正要断言的是"同一个值原样往返"与"两条不同的对话
// 互不并轨",一个可读、可复现的映射比随机 uuid 更好读。
func convID(n int64) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
}
