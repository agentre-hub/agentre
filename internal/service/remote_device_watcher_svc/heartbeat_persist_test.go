package remote_device_watcher_svc_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// memFrames 是 protorpc.FrameConn 的内存实现:两端各持一个,成对共享 done。
type memFrames struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func memFramePair() (*memFrames, *memFrames) {
	a, b := make(chan []byte, 16), make(chan []byte, 16)
	done := make(chan struct{})
	once := &sync.Once{}
	return &memFrames{in: a, out: b, done: done, once: once}, &memFrames{in: b, out: a, done: done, once: once}
}

func (m *memFrames) ReadFrame() ([]byte, error) {
	select {
	case b := <-m.in:
		return b, nil
	case <-m.done:
		return nil, io.EOF
	}
}

func (m *memFrames) WriteFrame(b []byte) error {
	select {
	case m.out <- b:
		return nil
	case <-m.done:
		return protorpc.ErrConnClosed
	}
}

func (m *memFrames) Close() error {
	m.once.Do(func() { close(m.done) })
	return nil
}

func (m *memFrames) Done() <-chan struct{} { return m.done }

// pingPeer 是对端的 health.ping 处理器。每次 ping 进入处理器时通知 entered,
// 然后阻塞到测试放行 release 才应答 —— 让测试能在「这一次心跳的落库」之前推进
// FakeClock,避免时钟竞态。failAt > 0 时第 failAt 次 ping 回错误(模拟心跳失败)。
type pingPeer struct {
	entered chan struct{}
	release chan struct{}
	failAt  int
	mu      sync.Mutex
	n       int
}

func (p *pingPeer) handle(ctx context.Context, _ *agentrewire.HealthPingRequest) (*agentrewire.HealthPingResponse, error) {
	p.mu.Lock()
	p.n++
	n := p.n
	p.mu.Unlock()
	p.entered <- struct{}{}
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if n == p.failAt {
		return nil, errors.New("daemon went away")
	}
	return &agentrewire.HealthPingResponse{}, nil
}

// livePingConnection 是一条真的握过手(能应答 health.ping)的 watcher 连接。
type livePingConnection struct{ conn *protorpc.Conn }

func (c *livePingConnection) Conn() *protorpc.Conn    { return c.conn }
func (c *livePingConnection) Closed() <-chan struct{} { return c.conn.Done() }
func (c *livePingConnection) Close() error            { return c.conn.Close() }
func (c *livePingConnection) SelfFingerprint() string { return "sha256:test-self" }

// openLivePing 为每次拨号造一对内存连接:客户端给 watcher,服务端挂上 peer 的处理器。
func openLivePing(ctx context.Context, peer *pingPeer) client.ProtobufConnection {
	a, b := memFramePair()
	reg := protorpc.NewRegistry()
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING),
		func() *agentrewire.HealthPingRequest { return &agentrewire.HealthPingRequest{} }, peer.handle)
	clientConn := protorpc.NewConn(a, protorpc.NewRegistry())
	serverConn := protorpc.NewConn(b, reg)
	go clientConn.Serve(ctx)
	go serverConn.Serve(ctx)
	return &livePingConnection{conn: clientConn}
}

// recordedWrite 是一次 UpdateLastSeen 落库。
type recordedWrite struct {
	lastSeenMs int64
	lastError  string
}

type writeLog struct {
	mu     sync.Mutex
	writes []recordedWrite
}

func (l *writeLog) add(w recordedWrite) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes = append(l.writes, w)
}

func (l *writeLog) snapshot() []recordedWrite {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]recordedWrite(nil), l.writes...)
}

// heartbeatCfg 把心跳节拍压到 1ms(真实时间,只为让 ticker 尽快触发);
// 虚拟时间由 FakeClock 推进,与心跳节拍无关。
var heartbeatCfg = remote_device_watcher_svc.WatcherConfig{
	HeartbeatInterval: time.Millisecond,
	CallTimeout:       10 * time.Second,
	Backoff: remote_device_watcher_svc.BackoffConfig{
		Initial: time.Second, Max: 30 * time.Second,
		Multiplier: 2.0, Jitter: 0,
	},
}

const heartbeatStepSeconds = 5

func TestWatcher_GivenTwelveHeartbeatsFiveSecondsApart_ThenLastSeenIsPersistedAtMostTwice(t *testing.T) {
	Convey("12 次成功心跳(每次虚拟 5 s):last_seen 落库不超过 2 次(上线一次 + 60 s 节流一次)", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		peer := &pingPeer{entered: make(chan struct{}, 64), release: make(chan struct{}, 64)}
		log := &writeLog{}

		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(fixtureRow(), nil).AnyTimes()
		kc.EXPECT().Get("agentre-daemon-token-7").Return("tok", nil).AnyTimes()
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil).AnyTimes()
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).
			DoAndReturn(func(context.Context, remote_device_watcher_svc.OpenArgs) (client.ProtobufConnection, error) {
				return openLivePing(ctx, peer), nil
			}).AnyTimes()
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ int64, lastSeenMs int64, lastError string) error {
				log.add(recordedWrite{lastSeenMs: lastSeenMs, lastError: lastError})
				return nil
			}).AnyTimes()

		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, heartbeatCfg, clock, nil)
		go w.Run(ctx)

		waitForPing := func() {
			t.Helper()
			select {
			case <-peer.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("heartbeat ping never arrived")
			}
		}
		// 第 i 次 ping 进入处理器时,watcher 正阻塞在对端里,时钟此刻稳定;推进 5 s 后放行,
		// 直到下一次 ping 进入,才说明上一次心跳的落库(若有)已经完成。
		for i := 0; i < 12; i++ {
			waitForPing()
			clock.Advance(heartbeatStepSeconds * time.Second)
			peer.release <- struct{}{}
		}
		waitForPing() // 第 12 次心跳的落库已完成;第 13 次 ping 阻塞在对端,不会再写。

		So(len(log.snapshot()), ShouldBeLessThanOrEqualTo, 2)
		cancel()
		w.Wait()
	})
}

func TestWatcher_GivenAHeartbeatFailsInsideTheWindow_ThenErrorAndRecoveryAreWrittenImmediately(t *testing.T) {
	Convey("60 s 节流窗口内心跳失败再恢复:错误与恢复上线都立即落库,不等窗口结束", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		peer := &pingPeer{entered: make(chan struct{}, 64), release: make(chan struct{}, 64), failAt: 2}
		log := &writeLog{}

		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(fixtureRow(), nil).AnyTimes()
		kc.EXPECT().Get("agentre-daemon-token-7").Return("tok", nil).AnyTimes()
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil).AnyTimes()
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).
			DoAndReturn(func(context.Context, remote_device_watcher_svc.OpenArgs) (client.ProtobufConnection, error) {
				return openLivePing(ctx, peer), nil
			}).AnyTimes()
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ int64, lastSeenMs int64, lastError string) error {
				log.add(recordedWrite{lastSeenMs: lastSeenMs, lastError: lastError})
				return nil
			}).AnyTimes()

		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, heartbeatCfg, clock, nil)
		go w.Run(ctx)

		waitForPing := func() {
			t.Helper()
			select {
			case <-peer.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("heartbeat ping never arrived")
			}
		}
		waitForPing() // 第 1 次心跳:成功
		peer.release <- struct{}{}
		waitForPing() // 第 2 次心跳:失败 -> 错误立即落库,然后退避 1 s
		peer.release <- struct{}{}
		// 退避 1 s 的等待者可能晚于 Advance 才注册,所以推进到重连为止;
		// 整段仍在 60 s 节流窗口之内。
		for i := 0; ; i++ {
			clock.Advance(time.Second)
			select {
			case <-peer.entered:
			case <-time.After(20 * time.Millisecond):
				if i > 50 {
					t.Fatal("watcher never redialled after backoff")
				}
				continue
			}
			break
		}
		peer.release <- struct{}{} // 重连后第 3 次心跳:上线已在拨号时立即落库

		writes := log.snapshot()
		errIdx := -1
		for i, wr := range writes {
			if strings.HasPrefix(wr.lastError, "dial_failed:") {
				errIdx = i
				break
			}
		}
		So(errIdx, ShouldBeGreaterThanOrEqualTo, 0)
		So(writes[len(writes)-1].lastError, ShouldEqual, "")
		So(len(writes)-1, ShouldBeGreaterThan, errIdx)
		cancel()
		w.Wait()
	})
}
