package daemon

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

func deviceFactsDaemon(t *testing.T, probes *atomic.Int32) *Daemon {
	t.Helper()
	d, err := New(Options{
		DataDir: t.TempDir(),
		DeviceFactsRuntimes: func(context.Context) []syncwire.RuntimeFact {
			n := probes.Add(1)
			version := "0.40.0"
			if n > 1 {
				version = "0.46.0"
			}
			return []syncwire.RuntimeFact{{Runtime: "codex", Found: true, Path: "/nonexistent/codex", Version: version, NativeLogin: syncwire.NativeLoginLoggedOut}}
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { closeDB(d.db) })
	return d
}

func callDeviceFacts(t *testing.T, d *Daemon, authState protorpc.AuthState, refresh bool) (*agentrewire.DeviceFactsResponse, error) {
	t.Helper()
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, d.protobufRegistry.Clone())
	server.SetAuth(authState)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)
	return protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_DEVICE_FACTS),
		&agentrewire.DeviceFactsRequest{Refresh: refresh}, func() *agentrewire.DeviceFactsResponse { return &agentrewire.DeviceFactsResponse{} })
}

// Given 一台未登录账号的 agentred 与一个已配对的桌面端连接,
// When 桌面端调 device.facts,Then 拿到这台机器的运行时情况;要求刷新时重新探测。
func TestDeviceFacts_GivenPairedDesktop_WhenReadingFacts_ThenTheDaemonAnswersAndRefreshReprobes(t *testing.T) {
	var probes atomic.Int32
	d := deviceFactsDaemon(t, &probes)
	paired := protorpc.AuthState{Authenticated: true, DeviceFingerprint: "sha256:desktop"}

	first, err := callDeviceFacts(t, d, paired, false)
	require.NoError(t, err)
	require.Len(t, first.Runtimes, 1)
	require.Equal(t, "0.40.0", first.Runtimes[0].Version)
	require.Equal(t, syncwire.NativeLoginLoggedOut, first.Runtimes[0].NativeLogin)

	again, err := callDeviceFacts(t, d, paired, false)
	require.NoError(t, err)
	require.Equal(t, "0.40.0", again.Runtimes[0].Version, "不刷新时交回记下的那份")
	require.Equal(t, int32(1), probes.Load())

	refreshed, err := callDeviceFacts(t, d, paired, true)
	require.NoError(t, err)
	require.Equal(t, "0.46.0", refreshed.Runtimes[0].Version)
	require.Equal(t, int32(2), probes.Load())
}

// 没有通过握手的连接读不到设备情况。
func TestDeviceFacts_GivenUnauthenticatedConnection_ThenRefused(t *testing.T) {
	var probes atomic.Int32
	d := deviceFactsDaemon(t, &probes)
	_, err := callDeviceFacts(t, d, protorpc.AuthState{}, false)
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, int32(-32001), rpcErr.Code)
	require.Equal(t, int32(0), probes.Load())
}

// 个人 token 只要 read 范围就能读设备情况(范围表那一行注释「device facts」)。
func TestDeviceFacts_GivenPersonalTokenWithReadScope_ThenAllowed(t *testing.T) {
	scope, err := auth.AuthorizeMethod(protorpc.AuthState{Kind: auth.KindPersonalToken, Scopes: []string{string(auth.ScopeRead)}},
		uint32(agentrewire.RpcMethod_RPC_METHOD_DEVICE_FACTS))
	require.NoError(t, err)
	require.Equal(t, auth.ScopeRead, scope)
}
