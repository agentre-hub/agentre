package remote

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type budgetPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func budgetPipePair() (*budgetPipe, *budgetPipe) {
	a, b := make(chan []byte, 8), make(chan []byte, 8)
	done := make(chan struct{})
	once := &sync.Once{}
	return &budgetPipe{a, b, done, once}, &budgetPipe{b, a, done, once}
}

func (p *budgetPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}

func (p *budgetPipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}
func (p *budgetPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *budgetPipe) Done() <-chan struct{} { return p.done }

type budgetConnection struct{ conn *protorpc.Conn }

func (c *budgetConnection) Conn() *protorpc.Conn    { return c.conn }
func (c *budgetConnection) Closed() <-chan struct{} { return c.conn.Done() }
func (c *budgetConnection) Close() error            { return c.conn.Close() }

// runtime.run 在 daemon 侧是同步的:它要解析后端、准备远端工作区(可能是一次 clone)
// 再把 CLI 拉起来,几分钟是正常的。它必须豁免连接的默认请求预算 —— 被截断的话桌面
// 判这一轮失败,而 daemon 那边这一轮已经开跑并继续记日志、继续烧 token。
func TestCallRun_GivenARunSlowerThanTheCallBudget_WhenDispatched_ThenItIsNotTruncated(t *testing.T) {
	desktopSide, daemonSide := budgetPipePair()
	daemonRegistry := protorpc.NewRegistry()
	protorpc.RegisterMethod(daemonRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN),
		func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} },
		func(ctx context.Context, _ *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
			select {
			case <-time.After(200 * time.Millisecond): // 比预算长的"准备工作区"
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &agentrewire.RuntimeRunResponse{ConversationId: convOf(7)}, nil
		})
	desktopConn := protorpc.NewConn(desktopSide, protorpc.NewRegistry(),
		protorpc.WithCallTimeout(50*time.Millisecond))
	daemonConn := protorpc.NewConn(daemonSide, daemonRegistry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go desktopConn.Serve(ctx)
	go daemonConn.Serve(ctx)

	rt := New(&budgetConnection{conn: desktopConn}, WithConversationIDResolver(convOf))
	defer func() { _ = rt.Close() }()

	ack, err := rt.callRun(context.Background(), wire.RunParams{
		ConversationID: convOf(7), AgentID: 1, Backend: json.RawMessage(`{}`),
	})

	require.NoError(t, err)
	require.Equal(t, convOf(7), ack.ConversationID)
}

// SelfFingerprint 满足 client.ProtobufConnection:本端在这条连接上出示的设备指纹。
func (c *budgetConnection) SelfFingerprint() string { return "" }
