package remote_device_svc

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"

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

// SelfFingerprint 满足 client.ProtobufConnection:本端在这条连接上出示的设备指纹。
func (c *fakeClient) SelfFingerprint() string { return "sha256:test-self" }
