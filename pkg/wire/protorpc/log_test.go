package protorpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

type hostCtxKey struct{}

// 宿主（桌面、agentred）在启动时用 SetLogger 交出它自己的「按 ctx 取 logger」：protorpc
// 不绑任何宿主的日志框架，日志的去向与 ctx 上的关联字段由宿主决定。
func TestSetLogger_GivenAHostLogger_WhenAHandlerPanics_ThenThePanicIsLoggedThroughItWithTheRequestCtx(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	var sawHostValue bool
	protorpc.SetLogger(func(ctx context.Context) *zap.Logger {
		if ctx.Value(hostCtxKey{}) == "req" {
			sawHostValue = true
		}
		return zap.New(core)
	})
	t.Cleanup(func() { protorpc.SetLogger(nil) })

	clientTransport, serverTransport := pipePair()
	reg := protorpc.NewRegistry()
	protorpc.RegisterMethod(reg, mcpProxyMethodID,
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) { panic("boom") })
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), hostCtxKey{}, "req"))
	defer cancel()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, reg)
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, mcpProxyMethodID, &agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.Error(t, err)
	require.Eventually(t, func() bool {
		return logs.FilterMessage("protorpc: rpc handler panic").Len() == 1
	}, 2*time.Second, 10*time.Millisecond)
	require.True(t, sawHostValue, "the host logger is asked with the request's ctx")
	require.Equal(t, zapcore.ErrorLevel, logs.FilterMessage("protorpc: rpc handler panic").All()[0].Level)
}

// 没有宿主钩子时（agrctl 这类瘦客户端）日志静默丢弃，引擎照常工作。
func TestSetLogger_GivenNoHostLogger_ThenTheEngineStillWorks(t *testing.T) {
	protorpc.SetLogger(nil)
	clientTransport, serverTransport := pipePair()
	reg := protorpc.NewRegistry()
	protorpc.RegisterMethod(reg, mcpProxyMethodID,
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) { panic("boom") })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	go client.Serve(ctx)
	go protorpc.NewConn(serverTransport, reg).Serve(ctx)
	_, err := protorpc.CallMethod(ctx, client, mcpProxyMethodID, &agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, protorpc.CodeInternal, rpcErr.Code)
}
