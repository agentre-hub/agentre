package server_svc_test

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo/mock_server_state_repo"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	agentrewire "github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// accountSignalFrame 编一帧账号信号（sync_version），与服务端广播、
// syncwire.DecodeAccountChannelFrame 解码的是同一份 WireFrame 编码。
func accountSignalFrame(t *testing.T, version uint64) []byte {
	t.Helper()
	payload, err := proto.Marshal(&agentrewire.WireFrame{
		Body: &agentrewire.WireFrame_Notification{Notification: &agentrewire.Notification{
			Payload: &agentrewire.Notification_AccountSyncVersion{
				AccountSyncVersion: &agentrewire.AccountSyncVersion{Version: version},
			},
		}},
	})
	require.NoError(t, err)
	return payload
}

func signalEnvelope(channelID string, frame []byte) []byte {
	payload := make([]byte, 2+len(channelID)+len(frame))
	binary.BigEndian.PutUint16(payload, uint16(len(channelID)))
	copy(payload[2:], channelID)
	copy(payload[2+len(channelID):], frame)
	return payload
}

// signalRelayServer 起一台只做一件事的假中继：升级之后**由服务端主动开**那条保留
// 通道（决策 13/14 的「本节唯一的新机制」），按 25ms 一拍重复送同一条账号信号，
// 直到 closeChannel 关闭；那之后写一帧空载荷（= 这条通道关了，与普通通道同一个
// 约定）并**保持物理连接**——通道级的失败绝不能靠断开整条 socket 来表达，否则测
// 到的就是链路级收尾而不是通道级的那一路。
//
// 重复送同一条信号是有意的：信号「可合并、可丢弃、重复无害」（accountchan_svc 的
// 设计前提），重复因此消掉了「服务端抢在客户端 mux 连上之前写第一帧」那个竞态，
// 不必靠 sleep 去猜时序。
// 交回的第二个值在这条物理连接的读循环结束时关闭 —— 也就是对端把 socket 收掉的
// 那一刻，「常驻只在登录态成立」那条规则的可观测点。
func signalRelayServer(t *testing.T, bearer string, closeChannel <-chan struct{}) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	socketGone := make(chan struct{})
	var goneOnce sync.Once
	// 帧在**测试 goroutine**里编好：handler 跑在别的 goroutine 上，断言不能在那里做。
	signal := accountSignalFrame(t, 7)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+bearer {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		up := &websocket.Upgrader{Subprotocols: []string{protorpc.Subprotocol}}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		gone := make(chan struct{})
		go func() {
			defer goneOnce.Do(func() { close(socketGone) })
			defer close(gone)
			for {
				if _, _, readErr := ws.ReadMessage(); readErr != nil {
					return
				}
			}
		}()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-gone:
				return
			case <-closeChannel:
				_ = ws.WriteMessage(websocket.BinaryMessage,
					signalEnvelope(relaytransport.SignalChannelID, nil))
				<-gone
				return
			case <-ticker.C:
				if writeErr := ws.WriteMessage(websocket.BinaryMessage,
					signalEnvelope(relaytransport.SignalChannelID, signal)); writeErr != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, socketGone
}

func signalTestService(t *testing.T, serverURL, token string) sync_svc.AccountChannelDialer {
	t.Helper()
	return signalTestServiceWithRepo(t, serverURL, token, nil).(sync_svc.AccountChannelDialer)
}

func signalTestServiceWithRepo(
	t *testing.T, serverURL, token string, expect func(*mock_server_state_repo.MockServerStateRepo),
) server_svc.ServerSvc {
	t.Helper()
	row := &server_state_entity.ServerState{
		ID: 1, ServerURL: serverURL, DeviceID: 1, ServerUserID: 1,
		KeychainAccount: "agentre.server.refresh_token",
	}
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
	server_state_repo.RegisterServerState(mRepo)
	mRepo.EXPECT().Get(gomock.Any()).Return(row, nil).AnyTimes()
	if expect != nil {
		expect(mRepo)
	}
	keychain.SetDefault(keychain.NewMemory())
	// 生产里 sync_svc 也是这样拿到它的：DialAccountChannel 是
	// sync_svc.AccountChannelDialer 这一小块能力，不在 ServerSvc 的主接口上。
	return server_svc.New(server_svc.NewHTTPClient(serverURL, token), nil)
}

// TestDialAccountChannel_GivenTheReservedChannelIsClosedByTheServer_ThenTheStreamEndsSoTheCallerFallsBackToPolling
// 是规格「账号信号并入同一条连接」那句失败行为的守卫：
//
//	「订阅建不起来时按通道级错误作答，整条连接照常服务 RPC，客户端只把信号那一路
//	 标为不可用并退回 30 秒轮询。」
//
// 服务端在这种情况下只关掉保留通道（signalUnavailable 写一帧通道级错误 + 一帧空
// 载荷），物理连接照常。桌面端因此**必须**把这条路标为不可用——也就是让
// DialAccountChannel 交出的那条流关闭，sync_svc.consumeAccountSignals 据此返回、
// 退回轮询并择机重订。
//
// RED 之前：serveSignal 读到空载荷就直接 return，订阅者一个都没关，
// consumeAccountSignals 于是永远阻塞在一条再也不会有帧的流上，直到**物理**连接
// 断开为止——失败被无声吞掉，而不是被标出来。
func TestDialAccountChannel_GivenTheReservedChannelIsClosedByTheServer_ThenTheStreamEndsSoTheCallerFallsBackToPolling(t *testing.T) {
	Convey("服务端只关保留通道时，信号流关闭而物理连接留着", t, func() {
		closeChannel := make(chan struct{})
		relay, _ := signalRelayServer(t, "tok-signal", closeChannel)
		svc := signalTestService(t, relay.URL, "tok-signal")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		signals, err := svc.DialAccountChannel(ctx)
		So(err, ShouldBeNil)

		// 正路先立住：账号信号确实从保留通道抵达（决策 13 在桌面端这一侧唯一的
		// 端到端覆盖）。
		var got syncwire.AccountChannelFrame
		select {
		case frame, ok := <-signals:
			So(ok, ShouldBeTrue)
			got = frame
		case <-time.After(5 * time.Second):
			t.Fatal("保留通道上的信号没有抵达")
		}
		So(got.Type, ShouldEqual, syncwire.AccountChannelSyncVersion)
		So(got.Version, ShouldEqual, int64(7))

		close(closeChannel)

		// 关掉之前压在缓冲里的重复信号照收不误（重复无害），要紧的是这条流**会**
		// 走到关闭，而不是永远悬着。
		deadline := time.After(5 * time.Second)
		for {
			select {
			case _, ok := <-signals:
				if !ok {
					So(ok, ShouldBeFalse)
					return
				}
			case <-deadline:
				t.Fatal("服务端关掉了保留通道，信号流却还开着：这一路没有被标为不可用，" +
					"调用方会永远阻塞在一条死流上而不是退回 30 秒轮询")
			}
		}
	})
}

// TestLogout_GivenAResidentRelayConnection_ThenItIsTornDownInsteadOfDialingForever
// 是规格「常驻与空闲宽限的冲突要裁决」那一句的另一半：
//
//	「只要账号处于登录态就保持这一条连接。」
//
// 常驻的条件是**登录态**，不是「进程活着」。登出之后这条连接既没有可用的凭据、
// 也不该继续存在：它挂在 context.Background() 上，凭据提供者此后交回空串，于是
// 每一次重拨都是一次注定 401 的、不带身份的网络请求 —— 而这台桌面端已经登出了。
//
// RED 之前：ensureRelay 建出来的 residentRelay 没有任何拆卸路径，s.relay 也从不
// 清空，登出之后 HubLink 的重连循环按退避一直拨下去。
func TestLogout_GivenAResidentRelayConnection_ThenItIsTornDownInsteadOfDialingForever(t *testing.T) {
	Convey("登出把常驻中继连接收掉", t, func() {
		closeChannel := make(chan struct{})
		relay, socketGone := signalRelayServer(t, "tok-signal", closeChannel)
		svc := signalTestServiceWithRepo(t, relay.URL, "tok-signal",
			func(m *mock_server_state_repo.MockServerStateRepo) {
				m.EXPECT().ClearLoginFields(gomock.Any()).Return(nil).AnyTimes()
			})
		dialer, ok := svc.(sync_svc.AccountChannelDialer)
		require.True(t, ok)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		signals, err := dialer.DialAccountChannel(ctx)
		So(err, ShouldBeNil)
		// 先等这条常驻连接真的建起来：没建起来就谈不上「登出把它收掉」。
		select {
		case _, alive := <-signals:
			So(alive, ShouldBeTrue)
		case <-time.After(5 * time.Second):
			t.Fatal("常驻中继连接没有建起来")
		}

		So(svc.Logout(ctx), ShouldBeNil)

		select {
		case <-socketGone:
		case <-time.After(5 * time.Second):
			t.Fatal("登出之后那条常驻中继连接还在：它会带着一个空凭据一直重拨下去，" +
				"而常驻的条件是账号处于登录态")
		}
	})
}
