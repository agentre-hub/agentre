package remote_device_watcher_svc_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
)

// 设备面板只读 lastError,而 watcher 每隔几秒就重写一次它 —— 拨号被对端以
// 「协议版本对不上」拒掉时若照样落 "dial_failed:…",refresh.go 刚写下的
// protocol_mismatch 活不过一个退避周期,「版本太旧,连不上」那条强提示于是
// 永远出不来,用户看到的是一句泛泛的连接失败。
func TestWatcher_GivenTheDaemonSpeaksAnotherProtocolVersion_WhenDialing_ThenReportsTheProtocolCode(t *testing.T) {
	Convey("拨号被协议版本拒绝:落 protocol_mismatch", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)

		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(fixtureRow(), nil).AnyTimes()
		kc.EXPECT().Get("agentre-daemon-token-7").Return("tok", nil).AnyTimes()
		kc.EXPECT().Get("agentre-device-fingerprint").Return("fp", nil).AnyTimes()
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).
			Return(nil, errors.New("remote agentred speaks another agentre protocol version")).AnyTimes()
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), gomock.Any(), "protocol_mismatch").
			Return(nil).AnyTimes()

		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, testCfg, clock, nil)
		go w.Run(ctx)

		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		got := emit.snapshot()[0]
		cancel()
		w.Wait()

		So(got.Online, ShouldBeFalse)
		So(got.LastError, ShouldEqual, "protocol_mismatch")
	})
}
