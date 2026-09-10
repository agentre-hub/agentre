package wireinbound

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 外围方法族的注册是**两种执行端共用**的一处(agentred 经 daemon、桌面端经 peer),
// 而两种执行端拿得出的端口并不一样:桌面端不带 workspacefs、也不带 transcriptimport。
//
// 「这台机器没有这个能力」在协议上只有一种说法 —— method not found。它必须对每一个
// 方法族都成立:调用方据此判定「换台机器」,而 -32603 internal 只会让它去查那台机器
// 的日志,查到的还是一句 panic。
type peripheralPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func peripheralPipePair() (*peripheralPipe, *peripheralPipe) {
	a, b := make(chan []byte, 4), make(chan []byte, 4)
	done := make(chan struct{})
	once := &sync.Once{}
	return &peripheralPipe{a, b, done, once}, &peripheralPipe{b, a, done, once}
}

func (p *peripheralPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}

func (p *peripheralPipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}

func (p *peripheralPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *peripheralPipe) Done() <-chan struct{} { return p.done }

// dialPeripheral 起一对已鉴权的连接,注册面只挂 deps 给出的那些端口。
func dialPeripheral(t *testing.T, deps PeripheralDeps) (context.Context, *protorpc.Conn) {
	t.Helper()
	registry := protorpc.NewRegistry()
	RegisterPeripheralMethods(registry, deps)
	clientTransport, serverTransport := peripheralPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)
	// 鉴权与本用例无关:每个外围方法都裹在 Authenticated 里,不先握手的话所有断言
	// 都会停在 -32001,分不出「没这个能力」与「没鉴权」。
	server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "peripheral-test"})
	return ctx, client
}

// Given 一个不带 transcriptimport 端口的宿主(桌面端就是这样组装的)
// When 对端调 transcriptImport.scan
// Then 应答是 method not found —— 与 workspacefs 缺席时的说法一致。
//
// 修之前这里回的是 -32603:那一族的注册**没有 nil 守卫**(另外三族都有),于是挂上去
// 的 handler 持有一个 nil *Handlers,进去就在 h.sources() 上解空指针。
func TestPeripheral_GivenNoTranscriptImportPort_WhenCalled_ThenMethodNotFound(t *testing.T) {
	t.Parallel()

	ctx, client := dialPeripheral(t, PeripheralDeps{})

	_, err := protorpc.CallMethod(ctx, client,
		uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_SCAN),
		&agentrewire.TranscriptImportScanRequest{},
		func() *agentrewire.TranscriptImportScanResponse { return &agentrewire.TranscriptImportScanResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, protorpc.CodeMethodNotFound, rpcErr.Code,
		"端口缺席只有一种说法:method not found。回 internal 会让调用方去查那台机器的日志")
}

// 同一条判据对整族成立,不只 scan 那一个入口 —— execute 是这族唯一写库的,更不能
// 让它以 panic 的形式「差一点就跑起来了」。
func TestPeripheral_GivenNoTranscriptImportPort_WhenAnyMethodCalled_ThenMethodNotFound(t *testing.T) {
	t.Parallel()

	ctx, client := dialPeripheral(t, PeripheralDeps{})

	for name, call := range map[string]func() error{
		"open": func() error {
			_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_OPEN),
				&agentrewire.TranscriptImportOpenRequest{}, func() *agentrewire.TranscriptImportOpenResponse { return &agentrewire.TranscriptImportOpenResponse{} })
			return err
		},
		"turns": func() error {
			_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_TURNS),
				&agentrewire.TranscriptImportTurnsRequest{}, func() *agentrewire.TranscriptImportTurnsResponse { return &agentrewire.TranscriptImportTurnsResponse{} })
			return err
		},
		"execute": func() error {
			_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_EXECUTE),
				&agentrewire.TranscriptImportExecuteRequest{}, func() *agentrewire.TranscriptImportExecuteResponse {
					return &agentrewire.TranscriptImportExecuteResponse{}
				})
			return err
		},
	} {
		var rpcErr *protorpc.Error
		require.ErrorAs(t, call(), &rpcErr, name)
		require.Equal(t, protorpc.CodeMethodNotFound, rpcErr.Code, name)
	}
}
