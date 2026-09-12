package server_svc_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo/mock_server_state_repo"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

const introspectSuccessBody = `{"code":0,"msg":"ok","data":{"account_id":"7","device_id":7,"kind":"web","peer_fingerprint":"sha256:web-peer-7","expires_in":900}}`

// setupIntrospectSvc 起一台持有给定 access token 的桌面端 ServerSvc,并装配一份
// 独立的 mock server_state_repo + 内存 keychain(与 setupServerSvc 同一模式),供需要
// 触发刷新 / 清登录路径的用例声明期望。
func setupIntrospectSvc(t *testing.T, srvURL, accessToken string) (server_svc.ServerSvc, *mock_server_state_repo.MockServerStateRepo, keychain.Keychain) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
	server_state_repo.RegisterServerState(mRepo)
	kc := keychain.NewMemory()
	keychain.SetDefault(kc)
	svc := server_svc.New(server_svc.NewHTTPClient(srvURL, accessToken), nil)
	return svc, mRepo, kc
}

func TestIntrospectCredential_GivenTheServerConfirmsTheCredential_ThenReturnsTheIntrospection(t *testing.T) {
	Convey("Given the account server vouches for the presented credential, When IntrospectCredential runs, Then it returns the server's verdict using this device's own token", t, func() {
		var gotAuth atomic.Pointer[string]
		var gotPath atomic.Pointer[string]
		var gotToken atomic.Pointer[string]
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// So() panics off the request goroutine (goconvey's context is
			// bound to the test goroutine) — capture and assert after the
			// call returns, matching login_test.go's setupServerSvc pattern.
			path := r.URL.Path
			gotPath.Store(&path)
			h := r.Header.Get("Authorization")
			gotAuth.Store(&h)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			tok := body["token"]
			gotToken.Store(&tok)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(introspectSuccessBody))
		}))
		defer srv.Close()
		svc, _, _ := setupIntrospectSvc(t, srv.URL, "device-own-token")

		verified, err := svc.IntrospectCredential(context.Background(), "presented-credential")

		So(err, ShouldBeNil)
		So(verified.AccountID, ShouldEqual, "7")
		So(verified.PeerFingerprint, ShouldEqual, "sha256:web-peer-7")
		So(*gotPath.Load(), ShouldEqual, "/v1/credentials/introspect")
		So(*gotToken.Load(), ShouldEqual, "presented-credential")
		So(*gotAuth.Load(), ShouldEqual, "Bearer device-own-token")
	})
}

func TestIntrospectCredential_GivenTheServerRejectsWithABusinessError_ThenDoesNotRefresh(t *testing.T) {
	Convey("Given the account server answers a non-401 business rejection, When IntrospectCredential runs, Then it never touches the refresh endpoint", t, func() {
		var refreshHits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/credentials/introspect":
				w.WriteHeader(http.StatusBadRequest)
			case "/v1/oauth/token/refresh":
				refreshHits.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()
		svc, _, _ := setupIntrospectSvc(t, srv.URL, "device-own-token")

		_, err := svc.IntrospectCredential(context.Background(), "presented-credential")

		So(err, ShouldNotBeNil)
		So(err, ShouldEqual, auth.ErrCredentialInvalid)
		So(refreshHits.Load(), ShouldEqual, int32(0))
	})
}

func TestIntrospectCredential_GivenThisDevicesOwnTokenIs401_ThenRefreshesOnceAndRetries(t *testing.T) {
	Convey("Given the account server says this device's own token is expired, When IntrospectCredential runs, Then it refreshes once and retries with the fresh token", t, func() {
		var introspectHits atomic.Int32
		var refreshHits atomic.Int32
		var firstAuth, secondAuth atomic.Pointer[string]
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/credentials/introspect":
				n := introspectHits.Add(1)
				h := r.Header.Get("Authorization")
				if n == 1 {
					firstAuth.Store(&h)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				secondAuth.Store(&h)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(introspectSuccessBody))
			case "/v1/oauth/token/refresh":
				refreshHits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"access_token":"fresh-token","expires_in":900,"refresh_token":"rt-2","refresh_expires_in":3600}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()
		svc, _, kc := setupIntrospectSvc(t, srv.URL, "stale-token")
		_ = kc.Set("agentre.server.refresh_token", "rt-1")

		verified, err := svc.IntrospectCredential(context.Background(), "presented-credential")

		So(err, ShouldBeNil)
		So(verified.PeerFingerprint, ShouldEqual, "sha256:web-peer-7")
		So(introspectHits.Load(), ShouldEqual, int32(2))
		So(refreshHits.Load(), ShouldEqual, int32(1))
		So(*firstAuth.Load(), ShouldEqual, "Bearer stale-token")
		So(*secondAuth.Load(), ShouldEqual, "Bearer fresh-token")
	})
}

func TestIntrospectCredential_GivenThisDevicesRefreshIsRejected_ThenReturnsReceiverNotReadyAndClearsLogin(t *testing.T) {
	Convey("Given the account server rejects this device's own refresh token, When IntrospectCredential runs, Then it reports the receiver as not ready and clears the local login", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/credentials/introspect":
				w.WriteHeader(http.StatusUnauthorized)
			case "/v1/oauth/token/refresh":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":1010,"msg":"invalid","error":"invalid_grant","error_description":"refresh_token expired"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()
		svc, mRepo, kc := setupIntrospectSvc(t, srv.URL, "stale-token")
		_ = kc.Set("agentre.server.refresh_token", "rt-dead")
		mRepo.EXPECT().ClearLoginFields(gomock.Any()).Return(nil)

		_, err := svc.IntrospectCredential(context.Background(), "presented-credential")

		So(err, ShouldNotBeNil)
		So(err, ShouldEqual, auth.ErrReceiverNotReady)
	})
}

func TestIntrospectCredential_GivenTheAccountServerIsUnreachable_ThenReturnsAccountServerUnreachable(t *testing.T) {
	Convey("Given the account server cannot be reached, When IntrospectCredential runs, Then it reports the account server as unreachable", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		srv.Close() // 立刻关掉:连不上,等同 server 挂了
		svc, _, _ := setupIntrospectSvc(t, srv.URL, "device-own-token")

		_, err := svc.IntrospectCredential(context.Background(), "presented-credential")

		So(err, ShouldNotBeNil)
		So(err, ShouldEqual, auth.ErrAccountServerUnreachable)
	})
}

func TestIntrospectCredential_GivenThisDeviceHasNoOwnAccessToken_ThenReturnsReceiverNotReadyWithoutContactingTheServer(t *testing.T) {
	Convey("Given this device has no access token of its own, When IntrospectCredential runs, Then it reports the receiver as not ready without touching the network", t, func() {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		svc, _, _ := setupIntrospectSvc(t, srv.URL, "")

		_, err := svc.IntrospectCredential(context.Background(), "presented-credential")

		So(err, ShouldNotBeNil)
		So(err, ShouldEqual, auth.ErrReceiverNotReady)
		So(hits.Load(), ShouldEqual, int32(0))
	})
}

func TestIntrospectCredential_GivenTheSameTokenTwice_ThenTheSecondCallReusesTheCachedVerdict(t *testing.T) {
	Convey("Given the same credential is verified twice within the cache window, When IntrospectCredential runs, Then the second call never reaches the account server (H2, this device's Introspector must stay a singleton)", t, func() {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(introspectSuccessBody))
		}))
		defer srv.Close()
		svc, _, _ := setupIntrospectSvc(t, srv.URL, "device-own-token")

		first, err := svc.IntrospectCredential(context.Background(), "presented-credential")
		So(err, ShouldBeNil)
		second, err := svc.IntrospectCredential(context.Background(), "presented-credential")
		So(err, ShouldBeNil)

		So(second, ShouldResemble, first)
		So(hits.Load(), ShouldEqual, int32(1))
	})
}
