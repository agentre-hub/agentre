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

// relayEndpointServer 起一个假的中转服务端:校验 Bearer 头,把任何路径升级成
// websocket,对 auth.account 请求回成功。用于验证 server_svc 的 relay 拨号。
func relayEndpointServer(t *testing.T, bearer string) *httptest.Server {
	t.Helper()
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
		serveProtobufAccount(ws)
	}))
	return srv
}

func serveProtobufAccount(ws *websocket.Conn) {
	_, payload, err := ws.ReadMessage()
	if err != nil {
		return
	}
	var frame agentrewire.RpcFrame
	if proto.Unmarshal(payload, &frame) != nil {
		return
	}
	response, _ := proto.Marshal(&agentrewire.AuthAccountResponse{Ok: true, InstanceUuid: "uuid-1", ProtocolVersion: wireversion.Protocol})
	payload, _ = proto.Marshal(&agentrewire.RpcFrame{Id: frame.GetId(), Body: &agentrewire.RpcFrame_Response{Response: &agentrewire.Response{MethodId: frame.GetRequest().GetMethodId(), EncodedPayload: response}}})
	_ = ws.WriteMessage(websocket.BinaryMessage, payload)
}

// setupRelaySvc wires a logged-in server_svc with the given repo row + access token.
func setupRelaySvc(t *testing.T, row *server_state_entity.ServerState, token string) server_svc.ServerSvc {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
	server_state_repo.RegisterServerState(mRepo)
	if row != nil {
		mRepo.EXPECT().Get(gomock.Any()).Return(row, nil)
	}
	keychain.SetDefault(keychain.NewMemory())
	svc := server_svc.New(server_svc.NewHTTPClient("http://relay.hub", token), nil)
	return svc
}

func TestDialDaemonRelay_NotLoggedIn(t *testing.T) {
	Convey("DialDaemonRelay with no persisted login → ErrNotLoggedIn, no dial", t, func() {
		svc := setupRelaySvc(t, &server_state_entity.ServerState{ID: 1}, "")
		_, err := svc.DialDaemonRelay(context.Background(), "sha256:daemon", "sha256:desktop")
		So(errors.Is(err, server_svc.ErrNotLoggedIn), ShouldBeTrue)
	})
}

// Given server 部署在一个带路径前缀的 baseURL 下(反代常态:https://host/agentre),
// When 桌面端走账号中转拨号,Then 它打的是 <前缀>/v1/relay/client —— 与同一个
// baseURL 上的 HTTP 调用(serverClient.do 用 baseURL+path)以及 daemon 侧的
// hubEndpoint(它保留前缀后拼 /v1/relay/daemon)一致。丢掉前缀会打到反代根下不存在
// 的路径,server 从没见过这次请求,拨号却被归类成「这台 daemon 从未登记过」——
// 用户被指向「先去认领这台机器」,而机器一直是认领着的。
func TestDialDaemonRelay_PreservesServerBasePath(t *testing.T) {
	Convey("baseURL 带路径前缀时,中转拨号必须打到前缀下的 /v1/relay/client", t, func() {
		const prefix = "/agentre"
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			if r.URL.Path != prefix+"/v1/relay/client" {
				http.NotFound(w, r)
				return
			}
			up := &websocket.Upgrader{Subprotocols: []string{protorpc.Subprotocol}}
			ws, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = ws.Close() }()
			serveProtobufAccount(ws)
		}))
		defer srv.Close()

		base := srv.URL + prefix
		row := &server_state_entity.ServerState{
			ID: 1, ServerURL: base, DeviceID: 1, ServerUserID: 1,
			KeychainAccount: "agentre.server.refresh_token",
		}
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(row, nil)
		keychain.SetDefault(keychain.NewMemory())
		svc := server_svc.New(server_svc.NewHTTPClient(base, "tok-9"), nil)

		c, err := svc.DialDaemonRelay(context.Background(), "sha256:daemon", "sha256:desktop")
		So(err, ShouldBeNil)
		So(c, ShouldNotBeNil)
		So(gotPath, ShouldEqual, prefix+"/v1/relay/client")
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

func TestDialDesktopRelay_GivenTargetDesktopAppIsNotRunning_ThenItDoesNotReuseAgentredOfflineError(t *testing.T) {
	Convey("desktop relay offline is exposed as a distinct typed error", t, func() {
		relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "relay target offline", http.StatusConflict)
		}))
		defer relay.Close()

		loggedIn := &server_state_entity.ServerState{
			ID: 1, ServerURL: relay.URL, DeviceID: 1, ServerUserID: 1,
			KeychainAccount: "agentre.server.refresh_token",
		}
		ctrl := gomock.NewController(t)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedIn, nil)
		svc := server_svc.New(server_svc.NewHTTPClient(relay.URL, "tok-9"), nil)

		_, err := svc.DialDesktopRelay(context.Background(), "sha256:desktop", "sha256:caller")
		So(errors.Is(err, server_svc.ErrDesktopAppNotRunning), ShouldBeTrue)
		So(errors.Is(err, client.ErrRelayDaemonOffline), ShouldBeFalse)
	})
}

func TestDialDaemonRelay_LoggedInDialAndHandshake(t *testing.T) {
	Convey("logged in: relay dial authenticates with the access token and completes auth.account", t, func() {
		srv := relayEndpointServer(t, "tok-9")
		defer srv.Close()

		row := &server_state_entity.ServerState{
			ID: 1, ServerURL: srv.URL, DeviceID: 1, ServerUserID: 1,
			KeychainAccount: "agentre.server.refresh_token",
		}
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		mRepo.EXPECT().Get(gomock.Any()).Return(row, nil)
		keychain.SetDefault(keychain.NewMemory())
		svc := server_svc.New(server_svc.NewHTTPClient(srv.URL, "tok-9"), nil)

		c, err := svc.DialDaemonRelay(context.Background(), "sha256:daemon", "sha256:desktop")
		So(err, ShouldBeNil)
		So(c, ShouldNotBeNil)
		So(c.Closed(), ShouldNotBeNil)
		_ = c.Close()
	})
}
