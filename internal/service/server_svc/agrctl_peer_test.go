package server_svc_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agrctlcred"
)

// peerServer 是一台假 server：记录代签与吊销请求，其余路径按登录 / 刷新 / 设备列表作答。
type peerServer struct {
	*httptest.Server
	mu         sync.Mutex
	mintBodies []map[string]any
	mintAuth   []string
	revokeAuth []string
	mintStatus int
	hits       chan string // 代签 / 吊销各投一条，供异步断言等待
}

func newPeerServer(t *testing.T) *peerServer {
	p := &peerServer{mintStatus: http.StatusOK, hits: make(chan string, 16)}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/oauth/device/token":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"access_token":"a1","refresh_token":"r1","token_type":"Bearer","expires_in":3600,"refresh_expires_in":7776000,"device_id":42}}`))
		case "/v1/auth/me":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"user_id":7,"email":"u@e.com","device_id":42}}`))
		case "/v1/oauth/token/refresh":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"access_token":"a2","refresh_token":"r2","token_type":"Bearer","expires_in":3600,"refresh_expires_in":7776000,"device_id":42}}`))
		case "/v1/devices/cli-children":
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			p.mu.Lock()
			p.mintBodies = append(p.mintBodies, body)
			p.mintAuth = append(p.mintAuth, r.Header.Get("Authorization"))
			status := p.mintStatus
			p.mu.Unlock()
			if status != http.StatusOK {
				w.WriteHeader(status)
				p.hits <- "mint"
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"access_token":"ca1","token_type":"Bearer","expires_in":3600,"refresh_token":"cr1","refresh_expires_in":7776000,"device_id":99}}`))
			p.hits <- "mint"
		case "/v1/oauth/token/revoke":
			p.mu.Lock()
			p.revokeAuth = append(p.revokeAuth, r.Header.Get("Authorization"))
			p.mu.Unlock()
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{}}`))
			p.hits <- "revoke"
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *peerServer) waitHit(want string) bool {
	select {
	case got := <-p.hits:
		return got == want
	case <-time.After(3 * time.Second):
		return false
	}
}

func (p *peerServer) mints() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.mintBodies)
}

func dataDir(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dir)
	return dir
}

func waitCreds(dir string) *agrctlcred.Credentials {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := agrctlcred.Load(dir); err == nil {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func TestProvision_OnLogin(t *testing.T) {
	Convey("Given a desktop login succeeds, Then a child cli credential is minted with the desktop token and written as delegated", t, func() {
		dir := dataDir(t)
		p := newPeerServer(t)
		svc, mRepo, _ := setupServerSvc(t, p.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1, ServerURL: p.URL, DeviceFingerprint: "fp-abc"}, nil)
		mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)

		ok, err := svc.PollLoginToken(context.Background(), "dc")
		So(err, ShouldBeNil)
		So(ok, ShouldBeTrue)
		So(p.waitHit("mint"), ShouldBeTrue)

		c := waitCreds(dir)
		So(c, ShouldNotBeNil)
		So(c.DelegatedBy, ShouldNotBeEmpty)
		So(c.DeviceID, ShouldEqual, int64(99))
		So(c.RefreshToken, ShouldEqual, "cr1")
		So(c.AccessToken, ShouldEqual, "ca1")
		So(c.AccountID, ShouldEqual, int64(7))
		So(c.ServerURL, ShouldEqual, p.URL)
		So(strings.HasPrefix(c.Fingerprint, "sha256:"), ShouldBeTrue)
		p.mu.Lock()
		defer p.mu.Unlock()
		So(p.mintAuth[0], ShouldEqual, "Bearer a1")
		So(p.mintBodies[0]["fingerprint"], ShouldEqual, c.Fingerprint)
	})

	Convey("Given the mint endpoint fails, Then login still succeeds and no credential is written", t, func() {
		dir := dataDir(t)
		p := newPeerServer(t)
		p.mintStatus = http.StatusInternalServerError
		svc, mRepo, _ := setupServerSvc(t, p.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1, ServerURL: p.URL, DeviceFingerprint: "fp-abc"}, nil)
		mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)

		ok, err := svc.PollLoginToken(context.Background(), "dc")
		So(err, ShouldBeNil)
		So(ok, ShouldBeTrue)
		So(p.waitHit("mint"), ShouldBeTrue)
		time.Sleep(100 * time.Millisecond)
		_, lerr := agrctlcred.Load(dir)
		So(lerr, ShouldNotBeNil)
	})

	Convey("Given a manual agrctl login exists, Then the desktop login does not mint or overwrite", t, func() {
		dir := dataDir(t)
		So(agrctlcred.Save(dir, &agrctlcred.Credentials{ServerURL: "https://x", DeviceID: 5, RefreshToken: "manual"}), ShouldBeNil)
		p := newPeerServer(t)
		svc, mRepo, _ := setupServerSvc(t, p.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{ID: 1, ServerURL: p.URL, DeviceFingerprint: "fp-abc"}, nil)
		mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)

		ok, err := svc.PollLoginToken(context.Background(), "dc")
		So(err, ShouldBeNil)
		So(ok, ShouldBeTrue)
		time.Sleep(200 * time.Millisecond)
		So(p.mints(), ShouldEqual, 0)
		c, _ := agrctlcred.Load(dir)
		So(c.RefreshToken, ShouldEqual, "manual")
		So(c.DelegatedBy, ShouldBeEmpty)
	})
}

func TestProvision_OnStartup(t *testing.T) {
	Convey("Given logged in at startup with no agrctl credentials, Then RefreshWithBackoff provisions one", t, func() {
		dir := dataDir(t)
		p := newPeerServer(t)
		svc, mRepo, kc := setupServerSvc(t, p.URL)
		_ = kc.Set("agentre.server.refresh_token", "rt")
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInRow(p.URL), nil).AnyTimes()
		mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		svc.RefreshWithBackoff(context.Background())
		So(p.mints(), ShouldEqual, 1)
		c, err := agrctlcred.Load(dir)
		So(err, ShouldBeNil)
		So(c.DelegatedBy, ShouldNotBeEmpty)
	})

	Convey("Given credentials already exist (manual or delegated), Then startup does not mint", t, func() {
		for _, delegated := range []string{"", "desktop:42"} {
			dir := dataDir(t)
			So(agrctlcred.Save(dir, &agrctlcred.Credentials{ServerURL: "https://x", DeviceID: 5, RefreshToken: "tok", DelegatedBy: delegated}), ShouldBeNil)
			p := newPeerServer(t)
			svc, mRepo, kc := setupServerSvc(t, p.URL)
			_ = kc.Set("agentre.server.refresh_token", "rt")
			mRepo.EXPECT().Get(gomock.Any()).Return(loggedInRow(p.URL), nil).AnyTimes()
			mRepo.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			svc.RefreshWithBackoff(context.Background())
			So(p.mints(), ShouldEqual, 0)
		}
	})
}

func TestRevoke_OnLogoutAndForcedLogout(t *testing.T) {
	delegated := &agrctlcred.Credentials{ServerURL: "", DeviceID: 99, RefreshToken: "cr1", AccessToken: "ca1", DelegatedBy: "desktop:42"}

	Convey("Given delegated credentials, When the user logs out, Then the child revokes itself and the file is deleted", t, func() {
		dir := dataDir(t)
		p := newPeerServer(t)
		c := *delegated
		c.ServerURL = p.URL
		So(agrctlcred.Save(dir, &c), ShouldBeNil)
		svc, mRepo, _ := setupServerSvc(t, p.URL)
		mRepo.EXPECT().ClearLoginFields(gomock.Any()).Return(nil)

		So(svc.Logout(context.Background()), ShouldBeNil)
		p.mu.Lock()
		So(p.revokeAuth, ShouldContain, "Bearer ca1")
		p.mu.Unlock()
		_, err := agrctlcred.Load(dir)
		So(err, ShouldNotBeNil)
	})

	Convey("Given delegated credentials, When the login is force-cleared, Then the file is deleted and the child revoked", t, func() {
		dir := dataDir(t)
		p := newPeerServer(t)
		c := *delegated
		c.ServerURL = p.URL
		So(agrctlcred.Save(dir, &c), ShouldBeNil)
		svc, mRepo, _ := setupServerSvc(t, p.URL)
		mRepo.EXPECT().ClearLoginFields(gomock.Any()).Return(nil)

		So(svc.ClearLogin(context.Background()), ShouldBeNil)
		p.mu.Lock()
		So(p.revokeAuth, ShouldContain, "Bearer ca1")
		p.mu.Unlock()
		_, err := agrctlcred.Load(dir)
		So(err, ShouldNotBeNil)
	})

	Convey("Given a manual login, When the desktop logs out or is force-cleared, Then the file is untouched and nothing is revoked for it", t, func() {
		for _, forced := range []bool{false, true} {
			dir := dataDir(t)
			p := newPeerServer(t)
			So(agrctlcred.Save(dir, &agrctlcred.Credentials{ServerURL: p.URL, DeviceID: 5, RefreshToken: "manual", AccessToken: "man-a"}), ShouldBeNil)
			svc, mRepo, _ := setupServerSvc(t, p.URL)
			mRepo.EXPECT().ClearLoginFields(gomock.Any()).Return(nil)
			if forced {
				So(svc.ClearLogin(context.Background()), ShouldBeNil)
			} else {
				So(svc.Logout(context.Background()), ShouldBeNil)
			}
			p.mu.Lock()
			So(p.revokeAuth, ShouldNotContain, "Bearer man-a")
			p.mu.Unlock()
			c, err := agrctlcred.Load(dir)
			So(err, ShouldBeNil)
			So(c.RefreshToken, ShouldEqual, "manual")
		}
	})
}

func TestListDevices_ExcludesCliDevices(t *testing.T) {
	Convey("ListDevices never returns cli devices, so panels and pickers cannot show them", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"devices":[
				{"id":42,"name":"m1","kind":"desktop","status":1,"is_this_device":true},
				{"id":99,"name":"agrctl","kind":"cli","status":1},
				{"id":43,"name":"m2","kind":"agentred","status":1}
			]}}`))
		}))
		defer srv.Close()
		svc, mRepo, _ := setupServerSvc(t, srv.URL)
		mRepo.EXPECT().Get(gomock.Any()).Return(loggedInRow(srv.URL), nil)
		out, err := svc.ListDevices(context.Background())
		So(err, ShouldBeNil)
		So(len(out), ShouldEqual, 2)
		for _, d := range out {
			So(d.Kind, ShouldNotEqual, "cli")
		}
	})
}
