package remote_device_watcher_svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc/mock_remote_device_watcher_svc"
)

func setupService(t *testing.T) (
	*mock_remote_device_watcher_svc.MockPairedAgentredReader,
	*mock_remote_device_watcher_svc.MockDaemonDialPort,
	*mock_remote_device_watcher_svc.MockKeychainPort,
	*spyEmitter,
	remote_device_watcher_svc.Service,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_remote_device_watcher_svc.NewMockPairedAgentredReader(ctrl)
	dial := mock_remote_device_watcher_svc.NewMockDaemonDialPort(ctrl)
	kc := mock_remote_device_watcher_svc.NewMockKeychainPort(ctrl)
	emit := &spyEmitter{}
	svc := remote_device_watcher_svc.New(
		repo, dial, kc, emit, testCfg,
		remote_device_watcher_svc.NewFakeClock(time.Unix(0, 0)),
		nil, // no provider recorder in these tests
	)
	return repo, dial, kc, emit, svc
}

func TestService_StartAll_SkipsInactive(t *testing.T) {
	Convey("StartAll 跳过 status!=ACTIVE 的设备", t, func() {
		repo, dial, kc, _, svc := setupService(t)
		repo.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 1, Name: "a", URL: "ws://a/rpc", DaemonFingerprint: "x", TLSMode: "default", Status: 1},
			{ID: 2, Name: "b", URL: "ws://b/rpc", DaemonFingerprint: "x", TLSMode: "default", Status: 0}, // inactive
		}, nil)
		// 只对 id=1 拨号:Get/Get 凭证/Open 都用 AnyTimes 让 watcher 状态机随便走。
		repo.EXPECT().Get(gomock.Any(), int64(1)).Return(&paired_agentred_entity.PairedAgentred{
			ID: 1, Name: "a", URL: "ws://a/rpc", DaemonFingerprint: "x", TLSMode: "default", Status: 1,
		}, nil).AnyTimes()
		kc.EXPECT().Get(gomock.Any()).Return("x", nil).AnyTimes()
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(newWatcherTestConnection(), nil).AnyTimes()
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(1), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		So(svc.StartAll(context.Background()), ShouldBeNil)
		// gomock Strict 会在 Cleanup 阶段炸如果 id=2 有意外调用。
		svc.StopAll()
	})
}

func TestService_StartIdempotent(t *testing.T) {
	Convey("重复 Start 同一 id 是 no-op,不 panic 不二次拨号", t, func() {
		repo, dial, kc, emit, svc := setupService(t)
		// 只允许 dial.Open 1 次 + Get / keychain / UpdateLastSeen AnyTimes
		repo.EXPECT().Get(gomock.Any(), int64(5)).Return(&paired_agentred_entity.PairedAgentred{
			ID: 5, Name: "x", URL: "ws://x/rpc", DaemonFingerprint: "y", TLSMode: "default", Status: 1,
		}, nil).AnyTimes()
		kc.EXPECT().Get(gomock.Any()).Return("x", nil).AnyTimes()
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(newWatcherTestConnection(), nil).Times(1)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(5), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		So(svc.Start(context.Background(), 5), ShouldBeNil)
		So(svc.Start(context.Background(), 5), ShouldBeNil) // 第二次 no-op
		// 等第一个 watcher 进 online,断言只 dial 一次。
		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		svc.StopAll()
	})
}

func TestService_Restart(t *testing.T) {
	Convey("Restart 停旧 watcher 然后再启一个", t, func() {
		repo, dial, kc, emit, svc := setupService(t)
		row := &paired_agentred_entity.PairedAgentred{
			ID: 5, Name: "r", URL: "ws://r/rpc", DaemonFingerprint: "x",
			TLSMode: "default", Status: 1,
		}
		repo.EXPECT().Get(gomock.Any(), int64(5)).Return(row, nil).AnyTimes()
		kc.EXPECT().Get(gomock.Any()).Return("x", nil).AnyTimes()
		// 每次 dial 失败 → watcher emit 一次 offline event；两轮拨号 → emit >= 2
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(nil, errors.New("conn")).MinTimes(2)
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(5), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		So(svc.Start(context.Background(), 5), ShouldBeNil)
		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 }) // 第一轮拨号已发生
		So(svc.Restart(context.Background(), 5), ShouldBeNil)        // 不 panic / 不死锁
		waitFor(t, func() bool { return len(emit.snapshot()) >= 2 }) // 第二轮拨号已发生
		svc.StopAll()
	})
}
