package remote_device_watcher_svc_test

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
)

// fastHeartbeatCfg 只把心跳节拍压到毫秒级(其余与生产同形),让「先成功一次心跳、
// 再断掉」这条时序在单测里跑得完。
var fastHeartbeatCfg = remote_device_watcher_svc.WatcherConfig{
	HeartbeatInterval: 20 * time.Millisecond,
	CallTimeout:       100 * time.Millisecond,
	Backoff: remote_device_watcher_svc.BackoffConfig{
		Initial: time.Second, Max: 30 * time.Second,
		Multiplier: 2.0, Jitter: 0,
	},
}

// Given 一台已经握手成功、last_seen 被推到当下的设备,When 这条连接断掉(中转通道
// 随账号服务一起没了,心跳收到 protorpc: connection closed),Then 落库的 last_seen
// 必须停在「最后一次真的见到它」那一刻,而不能被倒拨回拨号那一刻读到的旧值。
//
// 倒拨是 F11 里「面板瞬间 0 在线、发消息被判『这台 agentred 当前离线』」的成因:
// DeviceView.Online 与 exec_target_svc 的可用性判据都从这一列推出来(5 分钟窗口),
// 把它写回一个几小时前的值,等于在中转断开的那一秒直接宣告这台机器不可用 ——
// 而它此刻在同一个局域网里活得好好的。
func TestWatcher_GivenAHeartbeatAlreadySucceeded_WhenTheConnectionDrops_ThenLastSeenIsNotRewound(t *testing.T) {
	Convey("心跳成功过之后连接断掉:last_seen 不被倒拨回拨号时读到的旧值", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		row := fixtureRow()
		row.LastSeenAt = 900_000 // 拨号时读到的旧值(上一次进程退出时留下的)
		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(row, nil).AnyTimes()
		kc.EXPECT().Get("agentre-daemon-token-7").Return("tok", nil).AnyTimes()
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil).AnyTimes()
		// 一条已经断掉的连接:握手成功、但第一次 health.ping 就收到
		// protorpc: connection closed —— 正是账号服务停掉时中转通道的形状。
		dead := newWatcherTestConnection()
		_ = dead.Close()
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(dead, nil).AnyTimes()
		// 时钟在 1_000_000:online 与随后的失败都只能写这个值,900_000 不许再出现。
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), int64(1_000_000), gomock.Any()).
			Return(nil).AnyTimes()

		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, fastHeartbeatCfg, clock, nil)
		go w.Run(ctx)

		waitFor(t, func() bool {
			for _, e := range emit.snapshot() {
				if !e.Online {
					return true
				}
			}
			return false
		})
		var offline remote_device_watcher_svc.StateEvent
		for _, e := range emit.snapshot() {
			if !e.Online {
				offline = e
				break
			}
		}
		So(offline.LastSeenAt, ShouldEqual, int64(1_000_000))
		cancel()
		w.Wait()
	})
}

// Given 账号服务不可达、中转拨号就此挂住不返回(residentRelay.waitConnected 等物理
// 链路重连,没有期限),When watcher 重连,Then 这一次拨号必须在期限内收场并继续重试
// —— 否则整条探活循环卡在中转上,同一个局域网里那条直连再也没有机会被拨出去。
func TestWatcher_GivenTheDialHangs_WhenTheDeadlinePasses_ThenItReportsAndKeepsRetrying(t *testing.T) {
	Convey("拨号挂住:到期收场、继续重试,不把探活循环钉死在账号服务上", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(fixtureRow(), nil).AnyTimes()
		kc.EXPECT().Get("agentre-daemon-token-7").Return("tok", nil).AnyTimes()
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil).AnyTimes()
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), gomock.Any(), gomock.Any()).
			Return(nil).AnyTimes()
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, _ remote_device_watcher_svc.OpenArgs) (client.ProtobufConnection, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}).AnyTimes()

		cfg := fastHeartbeatCfg
		cfg.DialTimeout = 50 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, cfg, clock, nil)
		go w.Run(ctx)

		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		So(emit.snapshot()[0].Online, ShouldBeFalse)
		// 到期之后走的是既有的退避重试,而不是永远挂着。
		clock.Advance(time.Second)
		waitFor(t, func() bool { return len(emit.snapshot()) >= 2 })
		cancel()
		w.Wait()
	})
}
