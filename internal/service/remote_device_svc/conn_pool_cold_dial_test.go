package remote_device_svc_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// manualTimers 是池子空闲计时器的手动时钟:armed 的回调只在用例调 fireAll 时才跑,
// 「空闲期限到了」由用例显式表达,不靠真睡。
type manualTimers struct {
	mu    sync.Mutex
	armed []*manualTimer
}

type manualTimer struct {
	f       func()
	stopped atomic.Bool
}

func (t *manualTimer) Stop() bool { return !t.stopped.Swap(true) }

func (m *manualTimers) afterFunc(_ time.Duration, f func()) remote_device_svc.IdleTimer {
	t := &manualTimer{f: f}
	m.mu.Lock()
	m.armed = append(m.armed, t)
	m.mu.Unlock()
	return t
}

// fireAll 让此刻所有还没被 Stop 的空闲计时器到点。
func (m *manualTimers) fireAll() {
	m.mu.Lock()
	armed := m.armed
	m.armed = nil
	m.mu.Unlock()
	for _, t := range armed {
		if !t.stopped.Swap(true) {
			t.f()
		}
	}
}

// Given 一个冷池子,when 同一台设备的两次 Borrow 并发到来,then 只拨一次号,两边拿到同一条连接。
func TestPool_Borrow_ConcurrentColdBorrows_DialOnce(t *testing.T) {
	Convey("concurrent cold borrows of one device share a single dial", t, func() {
		f := newPoolFixture(t)
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil).AnyTimes()
		var dials atomic.Int32
		entered := make(chan struct{}, 2)
		gate := make(chan struct{})
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).AnyTimes().
			DoAndReturn(func(context.Context, remote_device_svc.ConnectArgs) (client.ProtobufConnection, error) {
				dials.Add(1)
				entered <- struct{}{}
				<-gate
				return stubClient(), nil
			})

		var wg sync.WaitGroup
		leases := make([]remote_device_svc.Lease, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				leases[i], errs[i] = f.pool.Borrow(context.Background(), 42)
			}()
		}
		<-entered
		// 给第二次借用一个进入拨号的机会:旧实现会在这里拨第二次。
		select {
		case <-entered:
		case <-time.After(200 * time.Millisecond):
		}
		close(gate)
		wg.Wait()

		So(errs[0], ShouldBeNil)
		So(errs[1], ShouldBeNil)
		So(dials.Load(), ShouldEqual, 1)
		So(leases[1].Client(), ShouldEqual, leases[0].Client())
	})
}

// Given 一次冷拨号正在进行,when 发起它的那次 Borrow 被取消,then 同时等着的另一次 Borrow
// 照样拿到这条连接(拨号不随领头者取消),而被取消的那份引用不泄漏——最后一个借用者归还后,
// 连接按空闲回收。
func TestPool_Borrow_CanceledLeader_DoesNotPoisonFollower(t *testing.T) {
	Convey("a canceled leader neither cancels the shared dial nor leaks a reference", t, func() {
		f := newPoolFixture(t, remote_device_svc.WithIdleTimeout(0))
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil).AnyTimes()
		var dials atomic.Int32
		entered := make(chan struct{}, 2)
		gate := make(chan struct{})
		dialCtxErr := make(chan error, 2)
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).AnyTimes().
			DoAndReturn(func(ctx context.Context, _ remote_device_svc.ConnectArgs) (client.ProtobufConnection, error) {
				dials.Add(1)
				entered <- struct{}{}
				<-gate
				dialCtxErr <- ctx.Err()
				return stubClient(), nil
			})

		leaderCtx, cancelLeader := context.WithCancel(context.Background())
		leaderErr := make(chan error, 1)
		go func() {
			_, err := f.pool.Borrow(leaderCtx, 42)
			leaderErr <- err
		}()
		<-entered
		followerDone := make(chan struct{})
		var follower remote_device_svc.Lease
		var followerErr error
		go func() {
			defer close(followerDone)
			follower, followerErr = f.pool.Borrow(context.Background(), 42)
		}()
		select {
		case <-entered:
		case <-time.After(200 * time.Millisecond):
		}
		cancelLeader()
		select {
		case err := <-leaderErr:
			So(errors.Is(err, context.Canceled), ShouldBeTrue)
		case <-time.After(2 * time.Second):
			close(gate)
			t.Fatal("canceled Borrow did not return while the shared dial was still running")
		}
		close(gate)
		<-followerDone

		So(followerErr, ShouldBeNil)
		So(dials.Load(), ShouldEqual, 1)
		So(<-dialCtxErr, ShouldBeNil)

		follower.Release()
		select {
		case <-follower.Closed():
		case <-time.After(2 * time.Second):
			t.Fatal("entry never idle-evicted: the canceled leader leaked a reference")
		}
	})
}

// Given 设备监视报告设备在线,when 借用归还后空闲期限到了,then 池子不关这条连接,下次借用
// 不重拨;设备报告离线后,空闲期限一到照常回收,下次借用重新拨号。
func TestPool_DeviceOnline_KeepsConnectionWarm(t *testing.T) {
	Convey("an online device keeps its pooled connection; offline restores idle eviction", t, func() {
		timers := &manualTimers{}
		f := newPoolFixture(t, remote_device_svc.WithIdleAfterFunc(timers.afterFunc))
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil).AnyTimes()
		var dials atomic.Int32
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).AnyTimes().
			DoAndReturn(func(context.Context, remote_device_svc.ConnectArgs) (client.ProtobufConnection, error) {
				dials.Add(1)
				return stubClient(), nil
			})
		sink, ok := f.pool.(remote_device_svc.DeviceOnlineSink)
		So(ok, ShouldBeTrue)

		sink.SetDeviceOnline(42, true)
		l1, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		l1.Release()
		timers.fireAll()
		l2, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(dials.Load(), ShouldEqual, 1)
		So(l2.Client(), ShouldEqual, l1.Client())
		l2.Release()

		sink.SetDeviceOnline(42, false)
		timers.fireAll()
		select {
		case <-l2.Closed():
		default:
			t.Fatal("offline device's idle connection was not evicted")
		}
		l3, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(dials.Load(), ShouldEqual, 2)
		l3.Release()

		// 空闲计时已经在走的时候设备上线:计时作废,连接保住。
		sink.SetDeviceOnline(42, true)
		timers.fireAll()
		select {
		case <-l3.Closed():
			t.Fatal("device came online while idle but its connection was still evicted")
		default:
		}
		_, err = f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(dials.Load(), ShouldEqual, 2)
	})
}

// Given 中转那条路拨号一直挂着(服务端不可达,只在 ctx 结束时返回)且直连立即失败,when 用一个
// 没有期限的 ctx 去 Borrow,then 它在拨号期限内返回错误,而不是永远挂起。
func TestPool_Borrow_RelayBlocks_ReturnsWithinDialTimeout(t *testing.T) {
	Convey("a hanging relay path makes Borrow fail within the dial timeout", t, func() {
		relay := stubRelayDial{open: func(ctx context.Context, _ devicefp.Carrier, _ devicefp.Initiator) (client.ProtobufConnection, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		f := newPoolFixture(t, remote_device_svc.WithRelayDial(relay),
			remote_device_svc.WithDialTimeout(50*time.Millisecond))
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil).AnyTimes()
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).AnyTimes().
			Return(nil, errors.New("connection refused"))

		done := make(chan error, 1)
		go func() {
			_, err := f.pool.Borrow(context.Background(), 42)
			done <- err
		}()
		select {
		case err := <-done:
			So(err, ShouldNotBeNil)
			So(errors.Is(err, context.DeadlineExceeded), ShouldBeTrue)
		case <-time.After(2 * time.Second):
			t.Fatal("Borrow still blocked 2s after a 50ms dial timeout")
		}
	})
}

// Given 生产默认配置,when 冷借用拨号,then 拨号带着与设备监视一致的 15 秒期限。
func TestPool_Borrow_DefaultDialTimeoutIs15s(t *testing.T) {
	Convey("the default dial deadline matches the watcher's 15s", t, func() {
		deadlines := make(chan time.Duration, 1)
		relay := stubRelayDial{open: func(ctx context.Context, _ devicefp.Carrier, _ devicefp.Initiator) (client.ProtobufConnection, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				deadlines <- -1
			} else {
				deadlines <- time.Until(deadline)
			}
			return nil, errors.New("relay unavailable")
		}}
		f := newPoolFixture(t, remote_device_svc.WithRelayDial(relay))
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil).AnyTimes()
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).AnyTimes().
			Return(nil, errors.New("connection refused"))

		_, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldNotBeNil)
		left := <-deadlines
		So(left, ShouldBeGreaterThan, 14*time.Second)
		So(left, ShouldBeLessThanOrEqualTo, 15*time.Second)
	})
}
