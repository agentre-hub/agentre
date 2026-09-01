package server_svc_test

import (
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo/mock_server_state_repo"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 本文件的假中转服务端说的是决策 10/13/14 之后的协议:一条连接、信封承载的虚拟
// 通道、目标声明为通道的第一帧载荷、失败按通道级错误帧作答——不再是旧版一条连接
// 一个目标、失败靠 HTTP 4xx/5xx 状态码。

// wrapRelayEnvelope / unwrapRelayEnvelope 复现 relaytransport 的信封格式(2 字节
// 大端长度 + 通道 ID + 载荷),与 agentre-server relay_svc.WrapEnvelope 同一格式。
func wrapRelayEnvelope(channelID string, payload []byte) []byte {
	id := []byte(channelID)
	out := make([]byte, 2+len(id)+len(payload))
	binary.BigEndian.PutUint16(out, uint16(len(id)))
	copy(out[2:], id)
	copy(out[2+len(id):], payload)
	return out
}

func unwrapRelayEnvelope(t *testing.T, envelope []byte) (string, []byte) {
	t.Helper()
	if len(envelope) < 2 {
		t.Fatalf("envelope too short: %d bytes", len(envelope))
	}
	length := int(binary.BigEndian.Uint16(envelope[:2]))
	if len(envelope) < 2+length {
		t.Fatalf("envelope shorter than its declared channel ID length")
	}
	return string(envelope[2 : 2+length]), envelope[2+length:]
}

// relayClientTarget 是假服务端针对一次「开通道」请求要给出的应答:成功时完成
// auth.account 握手,失败时按 channel_code 写一帧通道级错误再关掉通道。
type relayClientTarget struct {
	// channelCode 非零时,这条通道以该通道级错误码失败(决策 10)。
	channelCode int32
}

// relayClientEndpointServer 起一个假的中继**客户端**入口(/v1/relay/client):校验
// Bearer,升级之后按信封协议逐条读帧;每条新通道的第一帧是目标声明,respond
// 决定这条通道成功(完成 auth.account 握手)还是以给定的通道级错误码失败。
func relayClientEndpointServer(t *testing.T, bearer string, respond func(target string) relayClientTarget) (*httptest.Server, *[]string) {
	t.Helper()
	var gotTargets []string
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
		opened := map[string]bool{}
		for {
			messageType, payload, readErr := ws.ReadMessage()
			if readErr != nil {
				return
			}
			if messageType != websocket.BinaryMessage {
				continue
			}
			channelID, inner := unwrapRelayEnvelope(t, payload)
			if !opened[channelID] {
				opened[channelID] = true
				gotTargets = append(gotTargets, string(inner))
				outcome := relayClientTarget{}
				if respond != nil {
					outcome = respond(string(inner))
				}
				if outcome.channelCode != 0 {
					errFrame, _ := proto.Marshal(&agentrewire.RpcFrame{Body: &agentrewire.RpcFrame_Error{
						Error: &agentrewire.RpcError{Code: outcome.channelCode, Message: "channel failed"},
					}})
					_ = ws.WriteMessage(websocket.BinaryMessage, wrapRelayEnvelope(channelID, errFrame))
					_ = ws.WriteMessage(websocket.BinaryMessage, wrapRelayEnvelope(channelID, nil))
					delete(opened, channelID)
				}
				continue
			}
			// 第二帧:auth.account 请求。
			var frame agentrewire.RpcFrame
			if proto.Unmarshal(inner, &frame) != nil {
				continue
			}
			response, _ := proto.Marshal(&agentrewire.AuthAccountResponse{
				Ok: true, InstanceUuid: "uuid-1", ProtocolVersion: wireversion.Protocol,
				MinSupportedProtocolVersion: wireversion.MinSupported, PeerFingerprint: "sha256:desktop",
			})
			respFrame, _ := proto.Marshal(&agentrewire.RpcFrame{Id: frame.GetId(), Body: &agentrewire.RpcFrame_Response{
				Response: &agentrewire.Response{MethodId: frame.GetRequest().GetMethodId(), EncodedPayload: response},
			}})
			_ = ws.WriteMessage(websocket.BinaryMessage, wrapRelayEnvelope(channelID, respFrame))
		}
	}))
	return srv, &gotTargets
}

func TestDialDaemonRelay_NotLoggedIn(t *testing.T) {
	Convey("DialDaemonRelay with no persisted login → ErrNotLoggedIn, no dial", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1}, nil)
		keychain.SetDefault(keychain.NewMemory())
		svc := server_svc.New(server_svc.NewHTTPClient("http://relay.hub", ""), nil)

		_, err := svc.DialDaemonRelay(context.Background(), "sha256:daemon", "sha256:desktop")
		So(errors.Is(err, server_svc.ErrNotLoggedIn), ShouldBeTrue)
	})
}

// Given server 部署在一个带路径前缀的 baseURL 下(反代常态:https://host/agentre),
// When 桌面端走账号中转拨号,Then 它打的是 <前缀>/v1/relay/client —— 与同一个
// baseURL 上的 HTTP 调用(serverClient.do 用 baseURL+path)以及 daemon 侧的
// hubEndpoint(它保留前缀后拼 /v1/relay/daemon)一致。丢掉前缀会打到反代根下不存在
// 的路径,server 从没见过这次请求。
func TestDialDaemonRelay_PreservesServerBasePath(t *testing.T) {
	Convey("baseURL 带路径前缀时,中转拨号必须打到前缀下的 /v1/relay/client", t, func() {
		const prefix = "/agentre"
		var gotPath string
		relaySrv, gotTargets := relayClientEndpointServer(t, "tok-9", nil)
		defer relaySrv.Close()
		// httptest.NewServer 本身没有路径前缀参数;用一层反代式包装记录路径并转发,
		// 与旧测试的做法保持一致的断言口径(gotPath),同时复用上面按信封协议应答的
		// 真实 handler。
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			if r.URL.Path != prefix+"/v1/relay/client" {
				http.NotFound(w, r)
				return
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/v1/relay/client"
			relaySrv.Config.Handler.ServeHTTP(w, r2)
		}))
		defer proxy.Close()

		base := proxy.URL + prefix
		row := &server_state_entity.ServerState{
			ID: 1, ServerURL: base, DeviceID: 1, ServerUserID: 1,
			KeychainAccount: "agentre.server.refresh_token",
		}
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(row, nil).AnyTimes()
		keychain.SetDefault(keychain.NewMemory())
		svc := server_svc.New(server_svc.NewHTTPClient(base, "tok-9"), nil)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := svc.DialDaemonRelay(ctx, "sha256:daemon", "sha256:desktop")
		So(err, ShouldBeNil)
		So(c, ShouldNotBeNil)
		So(gotPath, ShouldEqual, prefix+"/v1/relay/client")
		So(*gotTargets, ShouldContain, "machine:sha256:daemon")
		_ = c.Close()
	})
}

func TestNewInboundHubLink_GivenLoggedInDesktop_WhenRun_ThenRegistersWithItsCurrentAccessToken(t *testing.T) {
	Convey("a logged-in desktop builds the protocol-agnostic inbound relay link", t, func() {
		registered := make(chan struct{}, 1)
		upgrader := websocket.Upgrader{}
		relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/relay/daemon" {
				t.Errorf("path = %q, want /v1/relay/daemon", r.URL.Path)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer desktop-token" {
				t.Errorf("Authorization = %q, want desktop bearer", got)
				return
			}
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade relay websocket: %v", err)
				return
			}
			registered <- struct{}{}
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		}))
		defer relay.Close()

		ctrl := gomock.NewController(t)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{
			ID: 1, ServerURL: relay.URL, DeviceID: 1, ServerUserID: 1,
			KeychainAccount: "agentre.server.refresh_token",
		}, nil)
		svc := server_svc.New(server_svc.NewHTTPClient(relay.URL, "desktop-token"), nil)

		link, err := svc.NewInboundHubLink(context.Background())
		So(err, ShouldBeNil)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- link.Run(ctx) }()
		select {
		case <-registered:
		case <-time.After(time.Second):
			t.Fatal("desktop did not register with the relay")
		}
		cancel()
		So(<-done, ShouldBeNil)
	})
}

// channelCodeTargetOffline 与 agentre-server relay_svc.ChannelCodeTargetOffline
// 逐字同值 —— 见 relayclient.go 顶部同一组常量的注释。
const channelCodeTargetOffline int32 = -32011

func TestDialDesktopRelay_GivenTargetDesktopAppIsNotRunning_ThenItDoesNotReuseAgentredOfflineError(t *testing.T) {
	Convey("desktop relay offline is exposed as a distinct typed error", t, func() {
		relay, _ := relayClientEndpointServer(t, "tok-9", func(string) relayClientTarget {
			return relayClientTarget{channelCode: channelCodeTargetOffline}
		})
		defer relay.Close()

		loggedIn := &server_state_entity.ServerState{
			ID: 1, ServerURL: relay.URL, DeviceID: 1, ServerUserID: 1,
			KeychainAccount: "agentre.server.refresh_token",
		}
		ctrl := gomock.NewController(t)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedIn, nil).AnyTimes()
		keychain.SetDefault(keychain.NewMemory())
		svc := server_svc.New(server_svc.NewHTTPClient(relay.URL, "tok-9"), nil)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := svc.DialDesktopRelay(ctx, "sha256:desktop", "sha256:caller")
		So(errors.Is(err, server_svc.ErrDesktopAppNotRunning), ShouldBeTrue)
		So(errors.Is(err, client.ErrRelayDaemonOffline), ShouldBeFalse)
	})
}

func TestDialDaemonRelay_LoggedInDialAndHandshake(t *testing.T) {
	Convey("logged in: relay dial opens a channel over the resident link and completes auth.account", t, func() {
		srv, gotTargets := relayClientEndpointServer(t, "tok-9", nil)
		defer srv.Close()

		row := &server_state_entity.ServerState{
			ID: 1, ServerURL: srv.URL, DeviceID: 1, ServerUserID: 1,
			KeychainAccount: "agentre.server.refresh_token",
		}
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(row, nil).AnyTimes()
		keychain.SetDefault(keychain.NewMemory())
		svc := server_svc.New(server_svc.NewHTTPClient(srv.URL, "tok-9"), nil)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := svc.DialDaemonRelay(ctx, "sha256:daemon", "sha256:desktop")
		So(err, ShouldBeNil)
		So(c, ShouldNotBeNil)
		So(c.Closed(), ShouldNotBeNil)
		So(c.SelfFingerprint(), ShouldEqual, "sha256:desktop")
		So(*gotTargets, ShouldContain, "machine:sha256:daemon")
		_ = c.Close()
	})
}
