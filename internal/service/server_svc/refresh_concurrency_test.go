package server_svc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo/mock_server_state_repo"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// rotatingHub 复刻 agentre-server 的刷新语义：refresh_token 一次性，用过即轮转
// （device_svc 把旧行撤销、同毫秒写出新行）；再拿已被消费的那枚来换，按 OAuth
// 规范答 invalid_grant —— 那是**重放检测**，不是「这份登录没了」。
//
// 业务接口 /v1/devices 只认当前这枚 access token，其余一律 401，桌面端崩溃重启后
// 多路请求同时撞 401 的现场就是这样。
type rotatingHub struct {
	mu sync.Mutex
	// beforeReject 在判定重放、回 invalid_grant 之前跑，用来模拟「别的进程刚刚
	// 刷新成功并把新 token 写进了 keychain」。
	beforeReject func()
	refreshTok   string
	accessTok    string
	refreshHits  int
	seq          int
}

func newRotatingHub(refreshTok string) *rotatingHub {
	return &rotatingHub{refreshTok: refreshTok, accessTok: "at-boot"}
}

func (h *rotatingHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/oauth/token/refresh":
		h.serveRefresh(w, r)
	case "/v1/devices":
		h.serveDevices(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *rotatingHub) serveRefresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	h.mu.Lock()
	h.refreshHits++
	current := body.RefreshToken == h.refreshTok
	if current {
		h.seq++
		h.refreshTok = fmt.Sprintf("rt-%d", h.seq)
		h.accessTok = fmt.Sprintf("at-%d", h.seq)
	}
	access, refresh, onReject := h.accessTok, h.refreshTok, h.beforeReject
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if !current {
		if onReject != nil {
			onReject()
		}
		rejectRefresh(w)
		return
	}
	_, _ = fmt.Fprintf(w, `{"code":0,"msg":"ok","data":{"access_token":%q,"expires_in":900,`+
		`"refresh_token":%q,"refresh_expires_in":86400}}`, access, refresh)
}

func (h *rotatingHub) serveDevices(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	want := "Bearer " + h.accessTok
	h.mu.Unlock()
	if r.Header.Get("Authorization") != want {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"devices":[` +
		`{"id":42,"name":"m1","kind":"desktop","status":1,"is_this_device":true}]}}`))
}

func (h *rotatingHub) hits() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.refreshHits
}

func (h *rotatingHub) storedRefresh() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.refreshTok
}

func (h *rotatingHub) onReject(fn func()) {
	h.mu.Lock()
	h.beforeReject = fn
	h.mu.Unlock()
}

// 崩溃重启后 catch-up / sync / 设备清单几乎同时命中 401，各自拿着 keychain 里
// 同一枚 refresh_token 去刷新：服务端轮转之后其余几条一律被判重放。把那份重放
// 当成「登录失效」就会把刚刚续上的登录整个清掉（F10）。
func TestWithAuth_ConcurrentUnauthorizedCallersShareASingleRefresh(t *testing.T) {
	Convey("Given several authenticated calls hit 401 at the same moment, When they refresh, Then exactly one refresh reaches the server and every caller succeeds with the rotated credential", t, func() {
		hub := newRotatingHub("rt-boot")
		srv := httptest.NewServer(hub)
		defer srv.Close()

		svc, mRepo, kc := setupServerSvc(t, srv.URL)
		_ = kc.Set("agentre.server.refresh_token", "rt-boot")
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInRow(srv.URL), nil).AnyTimes()
		// ClearLoginFields 未声明 —— 被调用一次 gomock 当场判错，那正是用户看到的
		// 「崩溃重启后莫名掉登录」。

		const callers = 8
		errs := make([]error, callers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_, err := svc.ListDevices(context.Background())
				errs[i] = err
			}(i)
		}
		close(start)
		wg.Wait()

		for _, err := range errs {
			So(err, ShouldBeNil)
		}
		So(hub.hits(), ShouldEqual, 1)
		tok, _ := kc.Get("agentre.server.refresh_token")
		So(tok, ShouldEqual, hub.storedRefresh())
		So(tok, ShouldNotEqual, "rt-boot")
	})
}

// 同一枚 token 也可能被本机之外的东西（另一个进程、上一次运行残留的请求）换掉。
// 判据必须落在事实上：我们提交的那枚**是否仍是**当前存着的那一枚。不是了，就说明
// 别人刷成功了，该拿新的重来，而不是判这份登录死了。
func TestWithAuth_RejectionOfAnAlreadyRotatedTokenDoesNotClearTheLogin(t *testing.T) {
	Convey("Given the stored refresh token was rotated away while our request was in flight, When the server rejects the token we submitted, Then the login is kept and the call retries with the stored one", t, func() {
		hub := newRotatingHub("rt-elsewhere") // 服务端手里已经是别人刚换出来的那枚
		var kcRef atomic.Value
		srv := httptest.NewServer(hub)
		defer srv.Close()
		hub.onReject(func() {
			// 我们提交的是旧的那枚；与此同时别人的刷新已经落盘。
			if k, ok := kcRef.Load().(keychain.Keychain); ok {
				_ = k.Set("agentre.server.refresh_token", "rt-elsewhere")
			}
		})

		svc, mRepo, kc := setupServerSvc(t, srv.URL)
		kcRef.Store(kc)
		_ = kc.Set("agentre.server.refresh_token", "rt-stale")
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInRow(srv.URL), nil).AnyTimes()
		// ClearLoginFields 未声明：重放被拒不等于登录失效。

		out, err := svc.ListDevices(context.Background())
		So(err, ShouldBeNil)
		So(len(out), ShouldEqual, 1)
		So(hub.hits(), ShouldEqual, 2) // 被拒一次，换成当前那枚再来一次
		tok, _ := kc.Get("agentre.server.refresh_token")
		So(tok, ShouldEqual, hub.storedRefresh())
	})
}

// lockedKeychain 模拟 macOS 系统钥匙串在刷新途中上锁：取要提交的那枚时还读得到，
// 之后的回读开始失败。生产上这是真实场景，不是理论情形。
type lockedKeychain struct {
	inner    keychain.Keychain
	mu       sync.Mutex
	gets     int
	failFrom int
}

func (k *lockedKeychain) Get(account string) (string, error) {
	k.mu.Lock()
	k.gets++
	locked := k.gets >= k.failFrom
	k.mu.Unlock()
	if locked {
		return "", errors.New("keychain: user interaction is not allowed")
	}
	return k.inner.Get(account)
}

func (k *lockedKeychain) Set(account, secret string) error { return k.inner.Set(account, secret) }
func (k *lockedKeychain) Delete(account string) error      { return k.inner.Delete(account) }

// 「凭据还在不在」这个事实必须真的读出来才算数。读不到（钥匙串上锁 / 后端暂时
// 不可用）时，服务端的 invalid_grant 单独说明不了这份登录死了 —— 登录态必须留着，
// 与函数开头那次读不到 keychain 的口径一致。
func TestWithAuth_RejectionWithAnUnreadableKeychainKeepsTheLogin(t *testing.T) {
	Convey("Given the server rejects the token and the keychain cannot be read back, When the call refreshes, Then the login is kept and it is not reported as a credential rejection", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/devices":
				w.WriteHeader(http.StatusUnauthorized)
			case "/v1/oauth/token/refresh":
				rejectRefresh(w)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		svc, mRepo, kc := setupServerSvc(t, srv.URL)
		_ = kc.Set("agentre.server.refresh_token", "rt-maybe-alive")
		// failFrom=2：要提交的那枚读得到，被拒之后的回读读不到。
		locked := &lockedKeychain{inner: kc, failFrom: 2}
		keychain.SetDefault(locked)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInRow(srv.URL), nil).AnyTimes()
		// ClearLoginFields 未声明：读不出事实 ≠ 凭据没了。

		_, err := svc.ListDevices(context.Background())
		So(err, ShouldNotBeNil)
		So(server_svc.IsCredentialRejected(err), ShouldBeFalse)
		tok, _ := kc.Get("agentre.server.refresh_token")
		So(tok, ShouldEqual, "rt-maybe-alive")
	})
}

// 真·凭据失效时，善后只该做一次：清登录、发一次 logged_out。共享同一个失败的其余
// 并发调用方只拿错误，不重复清、也不重复通知前端。
func TestWithAuth_ConcurrentGenuineRejectionClearsTheLoginExactlyOnce(t *testing.T) {
	Convey("Given many concurrent calls share one genuinely rejected refresh, When they fail, Then the login is cleared once and logged_out is emitted once", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/devices":
				w.WriteHeader(http.StatusUnauthorized)
			case "/v1/oauth/token/refresh":
				rejectRefresh(w)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
		server_state_repo.RegisterServerState(mRepo)
		kc := keychain.NewMemory()
		keychain.SetDefault(kc)
		_ = kc.Set("agentre.server.refresh_token", "rt-dead")
		var loggedOut atomic.Int32
		svc := server_svc.New(server_svc.NewHTTPClient(srv.URL, ""), func(payload any) {
			if m, ok := payload.(map[string]any); ok && m["kind"] == "logged_out" {
				loggedOut.Add(1)
			}
		})
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInRow(srv.URL), nil).AnyTimes()
		mRepo.EXPECT().ClearLoginFields(gomock.Any()).Return(nil).Times(1)

		const callers = 8
		errs := make([]error, callers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_, err := svc.ListDevices(context.Background())
				errs[i] = err
			}(i)
		}
		close(start)
		wg.Wait()

		for _, err := range errs {
			So(err, ShouldNotBeNil)
		}
		So(loggedOut.Load(), ShouldEqual, int32(1))
		tok, _ := kc.Get("agentre.server.refresh_token")
		So(tok, ShouldEqual, "")
	})
}
