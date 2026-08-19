package server_svc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-ai/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-ai/agentre/internal/pkg/keychain"
	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
	"github.com/agentre-ai/agentre/internal/repository/server_state_repo"
	"github.com/agentre-ai/agentre/internal/repository/server_state_repo/mock_server_state_repo"
	"github.com/agentre-ai/agentre/internal/service/server_svc"
	"github.com/agentre-ai/agentre/internal/service/sync_svc"
)

// 账号级实时通道（server 的 GET /v1/account/channel）在桌面端这一侧的出入口。
// 它与中继的两条连接彼此独立：不指定目标 daemon，只收信号、不发帧。

// accountChannelDialer 取出这条通道的出入口。取法本身就是守卫：同步引擎正是按
// 这个可选接口拿到它的，签名对不上时通道会**静默消失**、只剩 30 秒轮询，
// 编译器一声不吭。
func accountChannelDialer(t *testing.T, svc server_svc.ServerSvc) sync_svc.AccountChannelDialer {
	t.Helper()
	dialer, ok := svc.(sync_svc.AccountChannelDialer)
	So(ok, ShouldBeTrue)
	return dialer
}

// accountChannelServer 起一个假的账号级通道服务端：校验 Bearer 与路径，升级之后
// 把 send 里的字节逐条发出去，然后按 keepOpen 决定挂着还是断开。
func accountChannelServer(t *testing.T, bearer, wantPath string, send [][]byte, keepOpen chan struct{}) (*httptest.Server, *string) {
	t.Helper()
	gotPath := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.URL.Path
		if r.URL.Path != wantPath {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+bearer {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		for _, payload := range send {
			if err := ws.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		}
		if keepOpen != nil {
			<-keepOpen
		}
	}))
	t.Cleanup(srv.Close)
	return srv, gotPath
}

func loggedInChannelSvc(t *testing.T, base, token string) server_svc.ServerSvc {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
	server_state_repo.RegisterServerState(mRepo)
	mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{
		ID: 1, ServerURL: base, ServerUserID: 7, DeviceID: 42,
		KeychainAccount: "agentre.server.refresh_token",
	}, nil).AnyTimes()
	keychain.SetDefault(keychain.NewMemory())
	return server_svc.New(server_svc.NewHTTPClient(base, token), nil)
}

func awaitSignal(t *testing.T, signals <-chan syncwire.AccountChannelFrame) (syncwire.AccountChannelFrame, bool) {
	t.Helper()
	select {
	case frame, ok := <-signals:
		return frame, ok
	case <-time.After(3 * time.Second):
		t.Fatal("等不到信号")
		return syncwire.AccountChannelFrame{}, false
	}
}

// TestDialAccountChannel_DeliversSyncVersionSignals 通道的**全部**线上契约：一个
// 两字段的 JSON 文本帧。这里发的是 server 侧 accountchan_svc.Frame 编出来的**原字节**
// ——两仓没有共享 Go 模块，桌面端这一份是手抄件，逐字节对得上才算数。
//
// 端点同样**追加**在 baseURL 已有的路径后面（反代路径前缀是常态），与中继拨号同理。
func TestDialAccountChannel_DeliversSyncVersionSignals(t *testing.T) {
	Convey("账号级通道把 sync_version 信号交给同步引擎", t, func() {
		const prefix = "/agentre"
		keepOpen := make(chan struct{})
		srv, gotPath := accountChannelServer(t, "tok-9", prefix+"/v1/account/channel",
			[][]byte{[]byte(`{"type":"sync_version","version":42}`)}, keepOpen)

		base := srv.URL + prefix
		svc := loggedInChannelSvc(t, base, "tok-9")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		signals, err := accountChannelDialer(t, svc).DialAccountChannel(ctx)
		So(err, ShouldBeNil)

		frame, ok := awaitSignal(t, signals)
		So(ok, ShouldBeTrue)
		So(frame.Type, ShouldEqual, syncwire.AccountChannelSyncVersion)
		So(frame.Version, ShouldEqual, int64(42))
		So(*gotPath, ShouldEqual, prefix+"/v1/account/channel")

		// 服务端断开 → 信号流关闭，调用方据此重连并主动 Pull 一次。
		close(keepOpen)
		_, ok = awaitSignal(t, signals)
		So(ok, ShouldBeFalse)
	})
}

// TestDialAccountChannel_GivenMalformedFrame_SkipsItAndStaysUp 一帧读不懂不该把整条
// 通道弄断：通道是优化，断了就退回 30 秒轮询，代价比丢一帧大得多。
func TestDialAccountChannel_GivenMalformedFrame_SkipsItAndStaysUp(t *testing.T) {
	Convey("不成形的帧被跳过，后面的正经信号照常到达", t, func() {
		keepOpen := make(chan struct{})
		defer close(keepOpen)
		srv, _ := accountChannelServer(t, "tok-9", "/v1/account/channel", [][]byte{
			[]byte(`not json at all`),
			[]byte(`{"type":"sync_version","version":7}`),
		}, keepOpen)

		svc := loggedInChannelSvc(t, srv.URL, "tok-9")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		signals, err := accountChannelDialer(t, svc).DialAccountChannel(ctx)
		So(err, ShouldBeNil)
		frame, ok := awaitSignal(t, signals)
		So(ok, ShouldBeTrue)
		So(frame.Version, ShouldEqual, int64(7))
	})
}

// TestDialAccountChannel_GivenServerRefuses_ReportsIt 服务端不提供这条通道（旧版本、
// 部署时关掉、反代拦下）：如实报错，调用方退回轮询。不阻塞、不重试到底。
func TestDialAccountChannel_GivenServerRefuses_ReportsIt(t *testing.T) {
	Convey("服务端拒绝升级时如实报错", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not available", http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		svc := loggedInChannelSvc(t, srv.URL, "tok-9")
		signals, err := accountChannelDialer(t, svc).DialAccountChannel(context.Background())
		So(err, ShouldNotBeNil)
		So(signals, ShouldBeNil)
	})
}

// TestDialAccountChannel_GivenNotLoggedIn_DoesNotDial 未登录时同步侧的一切都不存在
// （R12）：一个请求都不该发出去。
func TestDialAccountChannel_GivenNotLoggedIn_DoesNotDial(t *testing.T) {
	Convey("未登录时不拨号", t, func() {
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits++
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1}, nil).AnyTimes()
		keychain.SetDefault(keychain.NewMemory())
		svc := server_svc.New(server_svc.NewHTTPClient(srv.URL, ""), nil)

		_, err := accountChannelDialer(t, svc).DialAccountChannel(context.Background())
		So(errors.Is(err, server_svc.ErrNotLoggedIn), ShouldBeTrue)
		So(hits, ShouldEqual, 0)
	})
}

// TestDialAccountChannel_GivenContextDone_ClosesTheStream 收工时连接与信号流都要收掉
// ——否则退出登录 / 关窗之后还挂着一条常连。
func TestDialAccountChannel_GivenContextDone_ClosesTheStream(t *testing.T) {
	Convey("ctx 结束时信号流关闭", t, func() {
		keepOpen := make(chan struct{})
		defer close(keepOpen)
		srv, _ := accountChannelServer(t, "tok-9", "/v1/account/channel", nil, keepOpen)

		svc := loggedInChannelSvc(t, srv.URL, "tok-9")
		ctx, cancel := context.WithCancel(context.Background())
		signals, err := accountChannelDialer(t, svc).DialAccountChannel(ctx)
		So(err, ShouldBeNil)

		cancel()
		_, ok := awaitSignal(t, signals)
		So(ok, ShouldBeFalse)
	})
}
