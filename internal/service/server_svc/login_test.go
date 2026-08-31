package server_svc_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo/mock_server_state_repo"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// setupServerSvc builds a ServerSvc wired to:
//   - a fresh httptest server URL (caller-provided)
//   - a fresh mock_server_state_repo (registered as the default)
//   - a fresh in-memory keychain (registered as the default)
//
// Returns (svc, mockRepo, keychain). emitState is captured via a *atomic.Pointer
// caller can inspect.
func setupServerSvc(t *testing.T, srvURL string) (server_svc.ServerSvc, *mock_server_state_repo.MockServerStateRepo, keychain.Keychain) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
	server_state_repo.RegisterServerState(mRepo)
	kc := keychain.NewMemory()
	keychain.SetDefault(kc)
	svc := server_svc.New(server_svc.NewHTTPClient(srvURL, ""), nil)
	return svc, mRepo, kc
}

func TestStartLogin_Success(t *testing.T) {
	Convey("StartLogin healthchecks then issues device-flow authorize and persists server_state", t, func() {
		var authorizeHits atomic.Int32
		var authorizeBody atomic.Pointer[map[string]any]
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/healthz":
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"version":"v0.1.0","status":"ok","db_ping":true,"redis":true}}`))
			case "/v1/oauth/device/authorize":
				authorizeHits.Add(1)
				// 请求体只在这里捞出来，断言留到 Convey 块里做：handler 跑在
				// 另一条 goroutine 上，So 在那儿会因为拿不到 convey 上下文而 panic。
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				authorizeBody.Store(&body)
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"device_code":"dc","user_code":"A4F-7Q2","verification_uri":"http://h/device","verification_uri_complete":"http://h/device?user_code=A4F-7Q2","interval":5,"expires_in":600}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		svc, mRepo, kc := setupServerSvc(t, srv.URL)
		gomock.InOrder(
			mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1, DeviceFingerprint: "fp-existing"}, nil),
			// 1) early fingerprint resync (ServerURL still empty) — R5: synced to keychain.
			mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, s *server_state_entity.ServerState) error {
					kcFP, _ := kc.Get("agentre-device-fingerprint")
					So(s.ServerURL, ShouldEqual, "")
					So(s.DeviceFingerprint, ShouldEqual, kcFP)
					return nil
				},
			),
			// 2) post-authorize save (hub_url now set; fingerprint unchanged).
			mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, s *server_state_entity.ServerState) error {
					So(s.ServerURL, ShouldEqual, srv.URL)
					kcFP, _ := kc.Get("agentre-device-fingerprint")
					So(s.DeviceFingerprint, ShouldEqual, kcFP)
					return nil
				},
			),
		)

		res, err := svc.StartLogin(context.Background(), srv.URL)
		So(err, ShouldBeNil)
		So(res.UserCode, ShouldEqual, "A4F-7Q2")
		So(res.VerificationURIComplete, ShouldEqual, "http://h/device?user_code=A4F-7Q2")
		So(authorizeHits.Load(), ShouldEqual, int32(1))

		body := authorizeBody.Load()
		So(body, ShouldNotBeNil)
		So((*body)["device_kind"], ShouldEqual, "desktop")
		// 主机名是账号侧拿到这台机器名字的唯一途径：设备流不带它，服务端只能拿
		// 指纹缩写当名字，设备列表里每台机器就都长得一样了。
		wantHostname, _ := os.Hostname()
		So((*body)["name"], ShouldEqual, wantHostname)
		// 能力概念已从账号侧移除，桌面端不再自报一份没人校验的清单。
		_, hasCaps := (*body)["capabilities"]
		So(hasCaps, ShouldBeFalse)
	})
}

func TestStartLogin_FreshInstall_PersistsFingerprintEarly(t *testing.T) {
	Convey("StartLogin on fresh install persists a generated fingerprint BEFORE authorize", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/healthz":
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"version":"v0.1.0"}}`))
			case "/v1/oauth/device/authorize":
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"device_code":"dc","user_code":"X","verification_uri":"http://h/device","verification_uri_complete":"http://h/device?user_code=X","interval":5,"expires_in":600}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)

		gomock.InOrder(
			mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1}, nil),
			// 1) early fingerprint persist (fp non-empty, hub_url still empty)
			mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, s *server_state_entity.ServerState) error {
					So(s.DeviceFingerprint, ShouldNotBeEmpty)
					So(s.ServerURL, ShouldEqual, "")
					return nil
				},
			),
			// 2) post-authorize save (hub_url now set; fingerprint unchanged)
			mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, s *server_state_entity.ServerState) error {
					So(s.ServerURL, ShouldEqual, srv.URL)
					So(s.DeviceFingerprint, ShouldNotBeEmpty)
					return nil
				},
			),
		)

		_, err := svc.StartLogin(context.Background(), srv.URL)
		So(err, ShouldBeNil)
	})
}

func TestStartLogin_ReusesKeychainFingerprint(t *testing.T) {
	Convey("StartLogin presents the LAN pairing keychain fingerprint, not a random one (R5)", t, func() {
		var authorizeFP atomic.Value
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/healthz":
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"version":"v0.1.0"}}`))
			case "/v1/oauth/device/authorize":
				var req struct {
					Fingerprint string `json:"fingerprint"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				authorizeFP.Store(req.Fingerprint)
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"device_code":"dc","user_code":"X","verification_uri":"http://h/device","verification_uri_complete":"http://h/device?user_code=X","interval":5,"expires_in":600}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		svc, mRepo, kc := setupServerSvc(t, srv.URL)
		// LAN 配对已先落地同一把 keychain 指纹。
		_ = kc.Set("agentre-device-fingerprint", "sha256:lan-paired")

		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1}, nil)
		mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).AnyTimes()

		_, err := svc.StartLogin(context.Background(), srv.URL)
		So(err, ShouldBeNil)
		So(authorizeFP.Load().(string), ShouldEqual, "sha256:lan-paired")
		kcFP, _ := kc.Get("agentre-device-fingerprint")
		So(kcFP, ShouldEqual, "sha256:lan-paired") // 未覆盖既有 LAN 指纹
	})
}

func TestAccessToken_ReturnsCurrentToken(t *testing.T) {
	Convey("AccessToken returns the current hub access token (relay auth)", t, func() {
		svc := server_svc.New(server_svc.NewHTTPClient("http://hub", "tok-1"), nil)
		So(svc.AccessToken(), ShouldEqual, "tok-1")
	})
}

func TestPollLoginToken_Pending(t *testing.T) {
	Convey("PollLoginToken returns (false, nil) when authorization_pending", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":30200,"msg":"authorization pending","data":null,"error":"authorization_pending"}`))
		}))
		defer srv.Close()

		svc, _, _ := setupServerSvc(t, srv.URL)
		done, err := svc.PollLoginToken(context.Background(), "dc")
		So(err, ShouldBeNil)
		So(done, ShouldBeFalse)
	})
}

func TestPollLoginToken_Success(t *testing.T) {
	Convey("PollLoginToken persists token to keychain + updates server_state + emits logged_in", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/oauth/device/token":
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"access_token":"a1","refresh_token":"r1","token_type":"Bearer","expires_in":3600,"refresh_expires_in":7776000,"device_id":42}}`))
			case "/v1/auth/me":
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"user_id":7,"email":"u@e.com","display_name":"u","avatar_url":"https://avatars/x","github_login":"u","device_id":42}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		svc, mRepo, kc := setupServerSvc(t, srv.URL)

		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1, ServerURL: srv.URL, DeviceFingerprint: "fp-abc"}, nil)
		mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, s *server_state_entity.ServerState) error {
				So(s.DeviceID, ShouldEqual, int64(42))
				So(s.ServerUserID, ShouldEqual, int64(7))
				So(s.KeychainAccount, ShouldEqual, "agentre.server.refresh_token")
				return nil
			},
		)

		ok, err := svc.PollLoginToken(context.Background(), "dc")
		So(err, ShouldBeNil)
		So(ok, ShouldBeTrue)
		v, _ := kc.Get("agentre.server.refresh_token")
		So(v, ShouldEqual, "r1")
	})
}

func TestPollLoginToken_AccessDenied(t *testing.T) {
	Convey("PollLoginToken returns ErrAccessDenied when error=access_denied", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":30203,"msg":"denied","data":null,"error":"access_denied"}`))
		}))
		defer srv.Close()
		svc, _, _ := setupServerSvc(t, srv.URL)
		_, err := svc.PollLoginToken(context.Background(), "dc")
		So(err, ShouldEqual, server_svc.ErrAccessDenied)
	})
}

// ── 已登录时不许对另一套 server 发起登录 ────────────────────────────────────
//
// StartLogin 一进门就把 client 换成新地址、并把 access token 清空，authorize 成功
// 之后又立刻把新 server_url 落库——而这一切发生在用户**还没批准**之前。已登录时
// 走这条路的收场是一次静默登出：手上是 A 的 refresh_token，client 却指着 B，
// 下一次 401 触发的刷新会被 B 判成 invalid_grant（凭据被明确拒绝），
// withAuth 据此清掉本地登录。
//
// 界面现在挡着它（登录入口只在登出时出现），但那是前端在替后端守一条不变量。
// agentred 那边同源的闸门（已认领必须先 unclaim）一直在后端。
func TestStartLogin_GivenAlreadyLoggedIn_RefusesWithoutRepointingTheClient(t *testing.T) {
	Convey("a desktop that is already signed in must sign out before pointing at another server", t, func() {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		keychain.SetDefault(keychain.NewMemory())
		// 已经登录在 A 上，手上有 A 的 access token。
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{
			ID: 1, ServerURL: "https://a.example", ServerUserID: 7, DeviceID: 3, KeychainAccount: "k",
		}, nil).AnyTimes()
		svc := server_svc.New(server_svc.NewHTTPClient("https://a.example", "token-a"), nil)

		res, err := svc.StartLogin(context.Background(), srv.URL)

		So(err, ShouldEqual, server_svc.ErrAlreadyLoggedIn)
		So(res, ShouldBeNil)
		So(hits.Load(), ShouldEqual, 0) // 一个请求都不该发给新 server
		So(svc.AccessToken(), ShouldEqual, "token-a")
	})
}

// 登出之后照常能登另一套 server——闸门挡的是「已登录」，不是「换 server」本身。
func TestStartLogin_GivenLoggedOut_ProceedsAgainstAnotherServer(t *testing.T) {
	Convey("after signing out, pointing at another server is an ordinary login", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/v1/healthz":
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"version":"v0.1.0","status":"ok","db_ping":true,"redis":true}}`))
			case "/v1/oauth/device/authorize":
				_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"device_code":"dc","user_code":"A4F-7Q2","verification_uri":"http://h/device","verification_uri_complete":"http://h/device?user_code=A4F-7Q2","interval":5,"expires_in":600}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		// 登出后的那一行：server_url 还留着上一套（Logout 刻意保留它），但登录字段已清。
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{
			ID: 1, ServerURL: "https://a.example", DeviceFingerprint: "fp-existing",
		}, nil).AnyTimes()
		mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		res, err := svc.StartLogin(context.Background(), srv.URL)

		So(err, ShouldBeNil)
		So(res.DeviceCode, ShouldEqual, "dc")
	})
}
