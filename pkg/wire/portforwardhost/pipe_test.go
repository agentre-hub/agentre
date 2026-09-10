package portforwardhost_test

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 本文件是用例的脚手架:一对真的 protorpc 连接。
//
// 连接是真的而不是打桩的,因为这一层要证的事里有一半发生在线上(open 的参数、三条
// 通知的分流、ack 的累计量)—— 方法号配错、oneof 撞了、通知没被分流,打桩的用例
// 照样绿。
//
// 桌面仓 internal/pkg/portforward 的 harness_test.go 里有一份同形的管道,那边还带一格
// 每帧时延(端到端那两条用例要靠它钉住横跨收尾的竞态)。两份不共用是因为 pkg/wire 是
// 独立 module,不能反向依赖桌面 module —— 而把测试脚手架从产品代码里导出去,代价比
// 这十几行重复更大。

type memPipe struct {
	in   chan []byte
	out  chan []byte
	done chan struct{}
	once *sync.Once
}

func memPipePair() (*memPipe, *memPipe) {
	a, b := make(chan []byte, 64), make(chan []byte, 64)
	done := make(chan struct{})
	once := &sync.Once{}
	return &memPipe{in: a, out: b, done: done, once: once},
		&memPipe{in: b, out: a, done: done, once: once}
}

func (p *memPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}

func (p *memPipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}

func (p *memPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *memPipe) Done() <-chan struct{} { return p.done }

// connPair 起一条宿主 ↔ 设备的连接,两端都在跑各自的读循环。
func connPair(t *testing.T) (host, device *protorpc.Conn) {
	t.Helper()
	hostSide, deviceSide := memPipePair()
	host = protorpc.NewConn(hostSide, protorpc.NewRegistry())
	device = protorpc.NewConn(deviceSide, protorpc.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go host.Serve(ctx)
	go device.Serve(ctx)
	return host, device
}
