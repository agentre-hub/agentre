package server_svc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo/mock_server_state_repo"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// TestResidentRelay_GivenTwoMachinesBorrowedThenReleased_ThenTheUnderlyingSocketStaysExactlyOne
// 是决策 13 的核心可观测结果:观察 N 台机器的对话时,桌面端持有的物理 WebSocket
// 总数恒为 1——不管此刻借了几台机器、借完又都还回去了没有。旧实现(client.DialRelayProtobuf
// 每次都独立拨号+握手)下,借两台机器就是两条物理连接,这条测试在那份实现上必然是
// 2 而不是 1,RED。
//
// 同时钉住"空闲宽限只管普通通道"那半句:两个 Lease 都 Close 之后(模拟 ConnPool 的
// idle 收回),物理连接数不倒退到 0——它是常驻的,不随最后一个借用者离开而关闭。
func TestResidentRelay_GivenTwoMachinesBorrowedThenReleased_ThenTheUnderlyingSocketStaysExactlyOne(t *testing.T) {
	Convey("两台机器的通道共享同一条物理连接,借完还回去之后连接仍然常驻", t, func() {
		var upgrades atomic.Int32
		relay, _ := relayClientEndpointServer(t, "tok-9", nil)
		defer relay.Close()
		// relayClientEndpointServer 本身不计数升级次数;包一层代理来数,复用它的
		// 信封协议 handler。
		countingProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if websocket.IsWebSocketUpgrade(r) {
				upgrades.Add(1)
			}
			relay.Config.Handler.ServeHTTP(w, r)
		}))
		defer countingProxy.Close()

		row := &server_state_entity.ServerState{
			ID: 1, ServerURL: countingProxy.URL, DeviceID: 1, ServerUserID: 1,
			KeychainAccount: "agentre.server.refresh_token",
		}
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(row, nil).AnyTimes()
		keychain.SetDefault(keychain.NewMemory())
		svc := server_svc.New(server_svc.NewHTTPClient(countingProxy.URL, "tok-9"), nil)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		connA, err := svc.DialDaemonRelay(ctx, "sha256:machine-a", "sha256:desktop")
		So(err, ShouldBeNil)
		connB, err := svc.DialDaemonRelay(ctx, "sha256:machine-b", "sha256:desktop")
		So(err, ShouldBeNil)

		So(int(upgrades.Load()), ShouldEqual, 1)

		_ = connA.Close()
		_ = connB.Close()
		// 两条 Lease 都还回去之后,连接不该倒退——它是登录态下常驻的,不是随最后
		// 一个借用者离开而关闭的空闲池。再借一台机器验证:如果连接被错误地关掉了,
		// 这里会因为需要重新物理拨号而让 upgrades 变成 2。
		connC, err := svc.DialDaemonRelay(ctx, "sha256:machine-c", "sha256:desktop")
		So(err, ShouldBeNil)
		So(int(upgrades.Load()), ShouldEqual, 1)
		_ = connC.Close()
	})
}

// TestResidentRelay_GivenAccountSignalSubscription_WhenBorrowingAMachine_ThenSharesTheSameSocket
// 钉死账号信号(DialAccountChannel)与普通通道(DialDaemonRelay)共用同一条物理连接
// ——决策 13 说的"信号通道即永不释放的使用方,与普通通道共用同一条连接",不是两条
// 各自常驻的连接。
func TestResidentRelay_GivenAccountSignalSubscription_WhenBorrowingAMachine_ThenSharesTheSameSocket(t *testing.T) {
	Convey("账号信号订阅与借用一台机器共用同一条物理连接", t, func() {
		var upgrades atomic.Int32
		relay, _ := relayClientEndpointServer(t, "tok-9", nil)
		defer relay.Close()
		countingProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if websocket.IsWebSocketUpgrade(r) {
				upgrades.Add(1)
			}
			relay.Config.Handler.ServeHTTP(w, r)
		}))
		defer countingProxy.Close()

		row := &server_state_entity.ServerState{
			ID: 1, ServerURL: countingProxy.URL, DeviceID: 1, ServerUserID: 1,
			KeychainAccount: "agentre.server.refresh_token",
		}
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(row, nil).AnyTimes()
		keychain.SetDefault(keychain.NewMemory())
		svc := server_svc.New(server_svc.NewHTTPClient(countingProxy.URL, "tok-9"), nil)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		dialer, ok := svc.(sync_svc.AccountChannelDialer)
		So(ok, ShouldBeTrue)
		signals, err := dialer.DialAccountChannel(ctx)
		So(err, ShouldBeNil)
		So(signals, ShouldNotBeNil)

		conn, err := svc.DialDaemonRelay(ctx, "sha256:machine-a", "sha256:desktop")
		So(err, ShouldBeNil)
		defer func() { _ = conn.Close() }()

		So(int(upgrades.Load()), ShouldEqual, 1)
	})
}
