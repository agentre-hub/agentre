package portforward_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/portforward"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 本文件是两个用例文件共用的脚手架:一对真的 protorpc 连接 + 一个假的连接池。
//
// 连接是真的而不是打桩的,因为这一层要证的事里有一半发生在线上(open 的参数、三条
// 通知的分流、ack 的累计量)。假的只有「连接池怎么把它交出来」这一件事 —— 那是
// remote_device_svc 的职责,不在本包的判定范围内。

type memPipe struct {
	in   chan []byte
	out  chan []byte
	done chan struct{}
	once *sync.Once

	// latency 是每帧交付前的等待,默认 0(同进程,快到没有时延)。
	// 只有需要一条**像真链路**的连接的用例才把它拨上去,见 memPipePairWithLatency。
	latency time.Duration
}

// memPipePairWithLatency 造一对管道;latency 为 0 就是原先那对同进程的快管道。
//
// 时延不是为了「慢一点」,而是为了把真链路上那件**决定性**的事补回来:帧是排着队走的,
// 一条通知要等它前面积压的那些帧都交付完才轮得到。同进程的管道没有这一段,收尾通知
// 几乎与最后一块数据同时到达,横跨「收尾」的竞态就退化成了偶发。
func memPipePairWithLatency(latency time.Duration) (*memPipe, *memPipe) {
	a, b := make(chan []byte, 64), make(chan []byte, 64)
	done := make(chan struct{})
	once := &sync.Once{}
	return &memPipe{in: a, out: b, done: done, once: once, latency: latency},
		&memPipe{in: b, out: a, done: done, once: once, latency: latency}
}

func (p *memPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		if p.latency > 0 {
			time.Sleep(p.latency)
		}
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

// fakeLease 是连接池那一侧交出来的租约。closed 关掉 = 那台设备掉线。
type fakeLease struct {
	conn     *protorpc.Conn
	closed   chan struct{}
	releases *int32
	mu       *sync.Mutex
}

func (l *fakeLease) Conn() *protorpc.Conn    { return l.conn }
func (l *fakeLease) Closed() <-chan struct{} { return l.closed }
func (l *fakeLease) Release() {
	l.mu.Lock()
	*l.releases++
	l.mu.Unlock()
}

// fakeDevices 按设备号交出租约。
type fakeDevices struct {
	mu       sync.Mutex
	leases   map[int64]*fakeLease
	releases map[int64]*int32
	err      error
}

func (d *fakeDevices) Borrow(_ context.Context, deviceID int64) (portforward.DeviceConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	lease, ok := d.leases[deviceID]
	if !ok {
		return nil, errNoSuchDevice
	}
	return lease, nil
}

var errNoSuchDevice = errors.New("fakeDevices: 没登记过这台设备")

// deviceGoesOffline 关掉这台设备的租约失效信号 —— 生产上由连接池在 daemon 掉线时做。
func (d *fakeDevices) deviceGoesOffline(deviceID int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	close(d.leases[deviceID].closed)
}

func (d *fakeDevices) releaseCount(deviceID int64) int32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return *d.releases[deviceID]
}

// newDevices 为每个设备号起一对连接;返回设备那一侧的 Conn 供用例注册假的设备行为。
func newDevices(t *testing.T, deviceIDs ...int64) (*fakeDevices, map[int64]*protorpc.Conn) {
	t.Helper()
	return newDevicesWithLatency(t, 0, deviceIDs...)
}

// newDevicesWithLatency 同上,但连接带一段每帧时延(见 memPipePairWithLatency)。
func newDevicesWithLatency(
	t *testing.T, latency time.Duration, deviceIDs ...int64,
) (*fakeDevices, map[int64]*protorpc.Conn) {
	t.Helper()
	devices := &fakeDevices{leases: map[int64]*fakeLease{}, releases: map[int64]*int32{}}
	far := map[int64]*protorpc.Conn{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	for _, id := range deviceIDs {
		hostSide, deviceSide := memPipePairWithLatency(latency)
		host := protorpc.NewConn(hostSide, protorpc.NewRegistry())
		device := protorpc.NewConn(deviceSide, protorpc.NewRegistry())
		go host.Serve(ctx)
		go device.Serve(ctx)
		var releases int32
		devices.leases[id] = &fakeLease{
			conn: host, closed: make(chan struct{}), releases: &releases, mu: &devices.mu,
		}
		devices.releases[id] = &releases
		far[id] = device
	}
	return devices, far
}

// dialable 报告这条地址此刻还接不接得上 TCP。
func dialable(t *testing.T, address string) bool {
	t.Helper()
	parsed, err := url.Parse(address)
	require.NoError(t, err)
	conn, err := net.DialTimeout("tcp", parsed.Host, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// requireGone 等这条监听真的不再受理连接。close 是异步的(要等 Serve 循环退出),
// 所以是 Eventually 而不是一次断言。
func requireGone(t *testing.T, address string) {
	t.Helper()
	require.Eventually(t, func() bool { return !dialable(t, address) },
		3*time.Second, 10*time.Millisecond, "这条专属监听本该被关掉,但它还在受理连接: "+address)
}
