package notifier_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/notifier"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type notifierPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func notifierPipePair() (*notifierPipe, *notifierPipe) {
	a, b := make(chan []byte, 8), make(chan []byte, 8)
	done := make(chan struct{})
	once := &sync.Once{}
	return &notifierPipe{a, b, done, once}, &notifierPipe{b, a, done, once}
}

func (p *notifierPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}

func (p *notifierPipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}
func (p *notifierPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *notifierPipe) Done() <-chan struct{} { return p.done }

// mcp.proxy 把一次 MCP 工具调用经隧道代理回桌面 gateway 再执行,一个工具跑几分钟是
// 正常的 —— 它必须豁免连接的默认请求预算,否则一条长工具调用会在预算到点时被截断成
// 一个模型看不懂的失败,而桌面那边工具其实还在跑。
func TestProtobufNotifierRequest_GivenAToolCallSlowerThanTheCallBudget_WhenProxied_ThenItIsNotTruncated(t *testing.T) {
	daemonSide, desktopSide := notifierPipePair()
	desktopRegistry := protorpc.NewRegistry()
	protorpc.RegisterMethod(desktopRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY),
		func() *agentrewire.MCPProxyRequest { return &agentrewire.MCPProxyRequest{} },
		func(ctx context.Context, _ *agentrewire.MCPProxyRequest) (*agentrewire.MCPProxyResponse, error) {
			select {
			case <-time.After(200 * time.Millisecond): // 比预算长的"慢工具"
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &agentrewire.MCPProxyResponse{Status: 200, Body: []byte(`{"ok":true}`)}, nil
		})
	daemonConn := protorpc.NewConn(daemonSide, protorpc.NewRegistry(),
		protorpc.WithCallTimeout(50*time.Millisecond))
	desktopConn := protorpc.NewConn(desktopSide, desktopRegistry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go daemonConn.Serve(ctx)
	go desktopConn.Serve(ctx)

	var response wire.MCPProxyResponse
	err := notifier.NewProtobuf(daemonConn).Request(context.Background(), wire.MethodMCPProxy,
		wire.MCPProxyRequest{Path: "/mcp", Body: []byte(`{}`)}, &response)

	require.NoError(t, err)
	require.Equal(t, 200, response.Status)
}
