package remote_device_svc

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/pkg/wire/protorpc"

	. "github.com/smartystreets/goconvey/convey"
)

type fakeClient struct {
	mu     sync.Mutex
	closed chan struct{}
}

func newFakeClient() *fakeClient { return &fakeClient{closed: make(chan struct{})} }

func (f *fakeClient) Call(context.Context, string, any, any) error { return nil }
func (f *fakeClient) Notify(string, any) error                     { return nil }
func (f *fakeClient) Handle(string, func(context.Context, json.RawMessage) (any, error)) {
}
func (f *fakeClient) Closed() <-chan struct{} { return f.closed }
func (f *fakeClient) Conn() *protorpc.Conn    { return protorpc.NewConn(nil, protorpc.NewRegistry()) }
func (f *fakeClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
	return nil
}

// idleEvictableClient 包一层 fakeClient,在 Closed() 被调用时发信号。
// watchClient 阻塞在 <-c.Closed() 上之前会先调这个方法,信号到了就说明
// watchClient 已经捕获 client 并在等待,从而让用例不必靠 sleep 表达时序。
type idleEvictableClient struct {
	*fakeClient
	closedRead chan struct{}
	once       sync.Once
}

func (c *idleEvictableClient) Closed() <-chan struct{} {
	c.once.Do(func() { close(c.closedRead) })
	return c.fakeClient.Closed()
}

// capturePoolLogs 把 logger.Default() 换成 observer,返回可查日志,用例结束还原。
func capturePoolLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	oldLogger := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(oldLogger) })
	return logs
}

func daemonDropWarns(logs *observer.ObservedLogs) int {
	return logs.FilterMessageSnippet("daemon connection dropped").Len()
}

func expectEntryEvictedAfterDrop(t *testing.T, p *pool, e *entry, fc *fakeClient) {
	t.Helper()
	go p.watchClient(e)
	_ = fc.Close()

	select {
	case <-e.closedCh:
	case <-time.After(time.Second):
		t.Fatal("entry.closedCh not closed")
	}
	p.mu.Lock()
	_, stillIn := p.entries[e.deviceID]
	p.mu.Unlock()
	So(stillIn, ShouldBeFalse)
}

func TestPool_WatchClient_EvictsOnDrop(t *testing.T) {
	Convey("daemon drop closes closedCh and evicts entry", t, func() {
		p := &pool{
			entries:     map[int64]*entry{},
			idleTimeout: time.Second,
		}
		fc := newFakeClient()
		e := &entry{
			deviceID: 7,
			client:   fc,
			closedCh: make(chan struct{}),
			refcount: 1,
		}
		p.entries[7] = e
		expectEntryEvictedAfterDrop(t, p, e, fc)
	})
}

func TestPool_Close_EvictsAllAndIdempotent(t *testing.T) {
	Convey("Close cleans up all entries, idempotent", t, func() {
		p := &pool{entries: map[int64]*entry{}, idleTimeout: time.Second}
		fc1, fc2 := newFakeClient(), newFakeClient()
		for id, fc := range map[int64]*fakeClient{1: fc1, 2: fc2} {
			e := &entry{
				deviceID: id, client: fc,
				closedCh: make(chan struct{}), refcount: 1,
			}
			p.entries[id] = e
		}
		So(p.Close(), ShouldBeNil)
		So(p.Close(), ShouldBeNil) // idempotent

		select {
		case <-fc1.Closed():
		case <-time.After(time.Second):
			t.Fatal("fc1 not closed by Pool.Close")
		}
		select {
		case <-fc2.Closed():
		case <-time.After(time.Second):
			t.Fatal("fc2 not closed by Pool.Close")
		}
	})
}

func TestPool_BorrowAfterClose(t *testing.T) {
	Convey("Borrow after Close → ErrPoolClosed", t, func() {
		p := &pool{entries: map[int64]*entry{}, closed: true}
		_, err := p.Borrow(context.Background(), 1)
		So(err, ShouldEqual, ErrPoolClosed)
	})
}

func TestPool_RedialsAfterDrop(t *testing.T) {
	Convey("after watchClient evicts, entry is removed from map", t, func() {
		p := &pool{
			entries:     map[int64]*entry{},
			idleTimeout: time.Second,
		}
		fc := newFakeClient()
		e := &entry{
			deviceID: 9,
			client:   fc,
			closedCh: make(chan struct{}),
			refcount: 1,
		}
		p.entries[9] = e
		expectEntryEvictedAfterDrop(t, p, e, fc)
	})
}

func TestPool_WatchClient_IdleEvictionDoesNotWarn(t *testing.T) {
	Convey("idle 回收由我方关闭连接,watchClient 不该报「远端断了」", t, func() {
		logs := capturePoolLogs(t)
		p := &pool{entries: map[int64]*entry{}, idleTimeout: time.Second}
		fc := newFakeClient()
		wrapped := &idleEvictableClient{fakeClient: fc, closedRead: make(chan struct{})}
		e := &entry{
			deviceID: 7,
			client:   wrapped,
			closedCh: make(chan struct{}),
			refcount: 0,
		}
		p.entries[7] = e

		done := make(chan struct{})
		go func() { p.watchClient(e); close(done) }()
		<-wrapped.closedRead // watchClient 已在 <-c.Closed() 上等待
		p.tryEvictIdle(e)
		<-done

		So(e.isEvicted(), ShouldBeTrue)
		So(daemonDropWarns(logs), ShouldEqual, 0)
	})
}

func TestPool_WatchClient_RemoteDropWarns(t *testing.T) {
	Convey("远端单方面失效才该报「远端断了」", t, func() {
		logs := capturePoolLogs(t)
		p := &pool{entries: map[int64]*entry{}, idleTimeout: time.Second}
		fc := newFakeClient()
		wrapped := &idleEvictableClient{fakeClient: fc, closedRead: make(chan struct{})}
		e := &entry{
			deviceID: 8,
			client:   wrapped,
			closedCh: make(chan struct{}),
			refcount: 1,
		}
		p.entries[8] = e

		done := make(chan struct{})
		go func() { p.watchClient(e); close(done) }()
		<-wrapped.closedRead
		_ = fc.Close() // 远端自己断开
		<-done

		So(daemonDropWarns(logs), ShouldEqual, 1)
	})
}

// SelfFingerprint 满足 client.ProtobufConnection:本端在这条连接上出示的设备指纹。
func (c *fakeClient) SelfFingerprint() string { return "sha256:test-self" }
