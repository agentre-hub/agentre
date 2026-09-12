package server_svc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo/mock_server_state_repo"
)

// backoffProbe 是一台被注入了「假等待」的 service:退避不真睡,只把时长记下来。
type backoffProbe struct {
	svc    *service
	repo   *mock_server_state_repo.MockServerStateRepo
	kc     keychain.Keychain
	mu     sync.Mutex
	slept  []time.Duration
	events []string
}

func newBackoffProbe(t *testing.T, srvURL string) *backoffProbe {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	p := &backoffProbe{repo: mock_server_state_repo.NewMockServerStateRepo(ctrl), kc: keychain.NewMemory()}
	server_state_repo.RegisterServerState(p.repo)
	keychain.SetDefault(p.kc)
	p.svc = New(NewHTTPClient(srvURL, ""), func(payload any) {
		m, ok := payload.(map[string]any)
		if !ok {
			return
		}
		p.mu.Lock()
		p.events = append(p.events, m["kind"].(string))
		p.mu.Unlock()
	}).(*service)
	p.svc.sleepFn = func(_ context.Context, d time.Duration) bool {
		p.mu.Lock()
		p.slept = append(p.slept, d)
		p.mu.Unlock()
		return true
	}
	return p
}

func (p *backoffProbe) waits() []time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Duration(nil), p.slept...)
}

func (p *backoffProbe) kinds() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.events...)
}

func TestRefreshWithBackoff(t *testing.T) {
	Convey("Given the server is down and then recovers, When the boot refresh runs, Then it backs off, keeps the login, and reports offline→online", t, func() {
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/oauth/token/refresh" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			hits++
			if hits < 3 {
				w.WriteHeader(http.StatusBadGateway) // 反代在,后端没起来
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"access_token":"at-new",` +
				`"expires_in":900,"refresh_token":"rt-new","refresh_expires_in":86400}}`))
		}))
		defer srv.Close()

		p := newBackoffProbe(t, srv.URL)
		_ = p.kc.Set(keychainAccountName, "rt-alive")
		p.repo.EXPECT().Get(gomock.Any()).
			Return(&server_state_entity.ServerState{
				ID: 1, ServerURL: srv.URL, ServerUserID: 7, DeviceID: 42,
				KeychainAccount: keychainAccountName,
			}, nil).AnyTimes()
		// ClearLoginFields 未声明 —— 一旦被调用 gomock 直接判这条用例失败。

		p.svc.RefreshWithBackoff(context.Background())

		So(hits, ShouldEqual, 3)
		So(p.waits(), ShouldResemble, []time.Duration{5 * time.Second, 10 * time.Second})
		So(p.kinds(), ShouldResemble, []string{"server_offline", "server_online"})
		So(p.svc.Offline(), ShouldBeFalse)
		tok, _ := p.kc.Get(keychainAccountName)
		So(tok, ShouldEqual, "rt-new")
	})

	Convey("Given the server rejects the stored credential, When the boot refresh runs, Then it clears the login without retrying", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":1010,"msg":"invalid","error":"invalid_grant"}`))
		}))
		defer srv.Close()

		p := newBackoffProbe(t, srv.URL)
		_ = p.kc.Set(keychainAccountName, "rt-dead")
		p.repo.EXPECT().Get(gomock.Any()).
			Return(&server_state_entity.ServerState{
				ID: 1, ServerURL: srv.URL, ServerUserID: 7, DeviceID: 42,
				KeychainAccount: keychainAccountName,
			}, nil).AnyTimes()
		p.repo.EXPECT().ClearLoginFields(gomock.Any()).Return(nil)

		p.svc.RefreshWithBackoff(context.Background())

		So(p.waits(), ShouldBeEmpty)
		So(p.kinds(), ShouldResemble, []string{"logged_out"})
	})

	Convey("Given the user logs out while the loop is waiting, When the next attempt comes around, Then the loop stops", t, func() {
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits++
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()

		p := newBackoffProbe(t, srv.URL)
		_ = p.kc.Set(keychainAccountName, "rt-alive")
		gomock.InOrder(
			p.repo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{
				ID: 1, ServerURL: srv.URL, ServerUserID: 7, DeviceID: 42,
				KeychainAccount: keychainAccountName,
			}, nil),
			// 第二轮:已登出 —— 循环必须就此收手,不能拿着空凭据无限重试。
			p.repo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1}, nil),
		)

		p.svc.RefreshWithBackoff(context.Background())

		So(hits, ShouldEqual, 1)
	})

	Convey("Given a canceled context, When the loop wants to wait, Then it gives up instead", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()

		p := newBackoffProbe(t, srv.URL)
		_ = p.kc.Set(keychainAccountName, "rt-alive")
		p.repo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{
			ID: 1, ServerURL: srv.URL, ServerUserID: 7, DeviceID: 42,
			KeychainAccount: keychainAccountName,
		}, nil).AnyTimes()
		p.svc.sleepFn = func(context.Context, time.Duration) bool { return false }

		p.svc.RefreshWithBackoff(context.Background())

		So(p.svc.Offline(), ShouldBeTrue)
	})
}

func TestNextBackoff(t *testing.T) {
	Convey("Given repeated failures, Then the wait doubles and stops growing at the cap", t, func() {
		d := refreshRetryInitial
		got := []time.Duration{d}
		for i := 0; i < 8; i++ {
			d = nextBackoff(d)
			got = append(got, d)
		}
		So(got, ShouldResemble, []time.Duration{
			5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second,
			80 * time.Second, 160 * time.Second, 300 * time.Second,
			300 * time.Second, 300 * time.Second,
		})
	})
}
