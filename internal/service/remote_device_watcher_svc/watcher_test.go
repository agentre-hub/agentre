package remote_device_watcher_svc_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc/mock_remote_device_watcher_svc"
)

type watcherTestConnection struct{ conn *protorpc.Conn }

func newWatcherTestConnection() client.ProtobufConnection {
	return &watcherTestConnection{conn: protorpc.NewConn(nil, protorpc.NewRegistry())}
}
func (c *watcherTestConnection) Conn() *protorpc.Conn    { return c.conn }
func (c *watcherTestConnection) Closed() <-chan struct{} { return c.conn.Done() }
func (c *watcherTestConnection) Close() error            { return c.conn.Close() }

type spyEmitter struct {
	mu     sync.Mutex
	events []remote_device_watcher_svc.StateEvent
}

func (s *spyEmitter) Emit(p remote_device_watcher_svc.StateEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, p)
}
func (s *spyEmitter) snapshot() []remote_device_watcher_svc.StateEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]remote_device_watcher_svc.StateEvent, len(s.events))
	copy(out, s.events)
	return out
}

func fixtureRow() *paired_agentred_entity.PairedAgentred {
	return &paired_agentred_entity.PairedAgentred{
		ID: 7, Name: "lab", URL: "ws://lab/rpc",
		DaemonFingerprint: "sha256:abc", InstanceUUID: "u",
		TLSMode: "default", Status: 1, // ACTIVE
	}
}

func setupWatcher(t *testing.T) (
	*mock_remote_device_watcher_svc.MockPairedAgentredReader,
	*mock_remote_device_watcher_svc.MockDaemonDialPort,
	*mock_remote_device_watcher_svc.MockKeychainPort,
	*spyEmitter,
	*remote_device_watcher_svc.FakeClock,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	return mock_remote_device_watcher_svc.NewMockPairedAgentredReader(ctrl),
		mock_remote_device_watcher_svc.NewMockDaemonDialPort(ctrl),
		mock_remote_device_watcher_svc.NewMockKeychainPort(ctrl),
		&spyEmitter{},
		remote_device_watcher_svc.NewFakeClock(time.Unix(1_000, 0))
}

// 默认 watcher 参数(测试用,放大节拍便于推进 FakeClock)。
var testCfg = remote_device_watcher_svc.WatcherConfig{
	HeartbeatInterval: 5 * time.Second,
	CallTimeout:       3 * time.Second,
	Backoff: remote_device_watcher_svc.BackoffConfig{
		Initial: time.Second, Max: 30 * time.Second,
		Multiplier: 2.0, Jitter: 0,
	},
}

func TestWatcher_DialOK_EmitsOnline_WritesLastSeen(t *testing.T) {
	Convey("dial 成功 -> emit online + UpdateLastSeen(nowMs,'')", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(fixtureRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-7").Return("tok", nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(newWatcherTestConnection(), nil)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), int64(1_000_000), "").Return(nil)

		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, testCfg, clock, nil)
		go w.Run(ctx)

		// 等 watcher 进 online 状态(events 长度 == 1)
		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		So(emit.snapshot()[0].Online, ShouldBeTrue)
		So(emit.snapshot()[0].LastError, ShouldEqual, "")
		So(emit.snapshot()[0].Name, ShouldEqual, "lab")
		cancel()
		w.Wait()
	})
}

func TestWatcher_DialTransientErr_BackoffThenRetry(t *testing.T) {
	Convey("dial 失败 → emit offline,1s 后重试", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		gomock.InOrder(
			repo.EXPECT().Get(gomock.Any(), int64(7)).Return(fixtureRow(), nil),
			repo.EXPECT().Get(gomock.Any(), int64(7)).Return(fixtureRow(), nil),
		)
		kc.EXPECT().Get("agentre-daemon-token-7").Return("tok", nil).Times(2)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil).Times(2)
		gomock.InOrder(
			dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(nil, errors.New("ECONNREFUSED")),
			dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(newWatcherTestConnection(), nil),
		)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), int64(0), gomock.Any()).Return(nil)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), gomock.Any(), "").Return(nil)

		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, testCfg, clock, nil)
		go w.Run(ctx)

		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		So(emit.snapshot()[0].Online, ShouldBeFalse)
		So(emit.snapshot()[0].LastError, ShouldStartWith, "dial_failed:")
		clock.Advance(time.Second)
		waitFor(t, func() bool { return len(emit.snapshot()) >= 2 })
		So(emit.snapshot()[1].Online, ShouldBeTrue)
		cancel()
		w.Wait()
	})
}

func TestWatcher_RelayOnlyDeviceWithoutLocalPairingToken_UsesAccountRelay(t *testing.T) {
	Convey("Server 收编的 relay-only 设备没有本地配对 token 时仍通过账号中转恢复 online", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		row := fixtureRow()
		row.URL = ""
		opened := make(chan remote_device_watcher_svc.OpenArgs, 1)
		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(row, nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("desktop-fp", nil)
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, args remote_device_watcher_svc.OpenArgs) (client.ProtobufConnection, error) {
				opened <- args
				return newWatcherTestConnection(), nil
			},
		)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), int64(1_000_000), "").Return(nil)

		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, testCfg, clock, nil)
		go w.Run(ctx)

		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		args := <-opened
		So(args.URL, ShouldEqual, "")
		So(args.DeviceFingerprint, ShouldEqual, "desktop-fp")
		So(args.DeviceToken, ShouldEqual, "")
		So(emit.snapshot()[0].Online, ShouldBeTrue)
		So(emit.snapshot()[0].LastError, ShouldEqual, "")
		cancel()
		w.Wait()
	})
}

func TestWatcher_StopReleases(t *testing.T) {
	Convey("ctx cancel 后 Wait() 返回,goroutine 退出", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(fixtureRow(), nil).AnyTimes()
		kc.EXPECT().Get(gomock.Any()).Return("x", nil).AnyTimes()
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(nil, errors.New("conn")).AnyTimes()
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, testCfg, clock, nil)
		go w.Run(ctx)
		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		cancel()
		done := make(chan struct{})
		go func() { w.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("watcher didn't stop in 2s")
		}
	})
}

func TestWatcher_PermanentErr_DegradedNoRetry(t *testing.T) {
	Convey("permanent err -> emit offline + 不再拨号", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(fixtureRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-7").Return("tok", nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil)
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).
			Return(nil, errors.New("tofu mismatch: sha256:foo vs sha256:bar"))
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), int64(0), "tofu_mismatch").Return(nil)

		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, testCfg, clock, nil)
		go w.Run(ctx)
		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		So(emit.snapshot()[0].LastError, ShouldEqual, "tofu_mismatch")
		// 推 60s 虚拟时间,确认 watcher 不重拨。
		clock.Advance(60 * time.Second)
		time.Sleep(20 * time.Millisecond)
		So(len(emit.snapshot()), ShouldEqual, 1)
		cancel()
		w.Wait()
	})
}

func TestWatcher_Backoff_GrowsAcrossRetries(t *testing.T) {
	Convey("连续 dial 失败,等待时间 1s -> 2s", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(fixtureRow(), nil).Times(3)
		kc.EXPECT().Get("agentre-daemon-token-7").Return("tok", nil).Times(3)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil).Times(3)
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).
			Return(nil, errors.New("ECONNREFUSED")).Times(3)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), int64(0), gomock.Any()).
			Return(nil).AnyTimes()

		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, testCfg, clock, nil)
		go w.Run(ctx)

		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		// 第 1 次失败后,backoff = 1s — 推 999ms 不应触发第 2 次拨号。
		clock.Advance(999 * time.Millisecond)
		time.Sleep(20 * time.Millisecond)
		So(len(emit.snapshot()), ShouldEqual, 1)
		clock.Advance(2 * time.Millisecond)
		waitFor(t, func() bool { return len(emit.snapshot()) >= 2 })
		// 第 2 次失败后,backoff = 2s — 推 1.9s 不应触发第 3 次拨号。
		clock.Advance(1900 * time.Millisecond)
		time.Sleep(20 * time.Millisecond)
		So(len(emit.snapshot()), ShouldEqual, 2)
		clock.Advance(200 * time.Millisecond)
		waitFor(t, func() bool { return len(emit.snapshot()) >= 3 })
		cancel()
		w.Wait()
	})
}

// waitFor 是简单 busy-wait,用真实时间(测试时长 ~ms 量级)。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor timed out")
}

// SelfFingerprint 满足 client.ProtobufConnection:本端在这条连接上出示的设备指纹。
func (c *watcherTestConnection) SelfFingerprint() string { return "sha256:test-self" }
