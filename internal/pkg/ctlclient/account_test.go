package ctlclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agrctlcred"
)

// fakeAccountServer 是 server 的 OAuth 与 /v1/ctl/resources 的假实现：refresh token 一次性
// 轮换，重放旧的就吊销整条链（与真 server 同一规则），响应套 cago 的 {code,msg,data} 外壳。
type fakeAccountServer struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	authorize  map[string]any
	pollQueue  []string // 依次回给 /device/token 的 OAuth error 码；空串 = 成功
	access     string
	refresh    string
	gen        int
	revoked    bool
	refreshes  int
	replayed   bool
	revokeBody map[string]any
	resources  int
	lastBearer string
}

func newFakeAccountServer(t *testing.T) *fakeAccountServer {
	t.Helper()
	f := &fakeAccountServer{t: t, access: "access-0", refresh: "refresh-0"}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth/device/authorize", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewDecoder(r.Body).Decode(&f.authorize)
		envelope(w, map[string]any{
			"device_code": "dc-1", "user_code": "ABCD-EFGH",
			"verification_uri": "https://server.example/device", "verification_uri_complete": "https://server.example/device?user_code=ABCD-EFGH",
			"interval": 1, "expires_in": 600,
		})
	})
	mux.HandleFunc("/v1/oauth/device/token", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.pollQueue) > 0 {
			code := f.pollQueue[0]
			f.pollQueue = f.pollQueue[1:]
			if code != "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 400, "msg": code, "data": nil, "error": code})
				return
			}
		}
		envelope(w, map[string]any{"access_token": f.access, "token_type": "Bearer", "expires_in": 900,
			"refresh_token": f.refresh, "refresh_expires_in": 86400, "device_id": 42})
	})
	mux.HandleFunc("/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(w, r) {
			return
		}
		envelope(w, map[string]any{"user_id": 7, "email": "alice@example.com", "display_name": "Alice", "device_id": 42})
	})
	mux.HandleFunc("/v1/oauth/token/refresh", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.refreshes++
		if f.revoked || body.RefreshToken != f.refresh {
			if !f.revoked {
				f.replayed = true // 重放旧的 refresh token：整条链吊销
			}
			f.revoked = true
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 400, "msg": "invalid grant", "data": nil, "error": "invalid_grant"})
			return
		}
		f.gen++
		f.access, f.refresh = "access-"+strconv.Itoa(f.gen), "refresh-"+strconv.Itoa(f.gen)
		envelope(w, map[string]any{"access_token": f.access, "expires_in": 900, "refresh_token": f.refresh, "refresh_expires_in": 86400})
	})
	mux.HandleFunc("/v1/oauth/token/revoke", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewDecoder(r.Body).Decode(&f.revokeBody)
		f.revoked = true
		envelope(w, map[string]any{})
	})
	mux.HandleFunc("/v1/ctl/resources", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(w, r) {
			return
		}
		f.mu.Lock()
		f.resources++
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"list":{"items":[]}}`)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func envelope(w http.ResponseWriter, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": data})
}

// authed 只认当前那枚 access token；吊销后一律 401（cago 外壳，不是 {"error"}）。
func (f *fakeAccountServer) authed(w http.ResponseWriter, r *http.Request) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastBearer = r.Header.Get("Authorization")
	if f.revoked || f.lastBearer != "Bearer "+f.access {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"code":401,"msg":"unauthorized","data":null}`)
		return false
	}
	return true
}

func (f *fakeAccountServer) rotate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	f.access = "access-" + strconv.Itoa(f.gen)
}

// loggedIn 在 dir 里放一份与假 server 当前状态一致的登录凭据。
func (f *fakeAccountServer) loggedIn(t *testing.T, dir string, accessExpiresAt int64) {
	t.Helper()
	f.mu.Lock()
	c := &agrctlcred.Credentials{ServerURL: f.srv.URL, AccountID: 7, Account: "alice@example.com", DeviceID: 42,
		DeviceName: "box", Fingerprint: "sha256:fp", RefreshToken: f.refresh, AccessToken: f.access, AccessExpiresAt: accessExpiresAt}
	f.mu.Unlock()
	if err := agrctlcred.Save(dir, c); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceFlow_GivenAnApprovedLoginThenItAuthorizesAsACliDeviceWithItsOwnFingerprint(t *testing.T) {
	f := newFakeAccountServer(t)
	f.pollQueue = []string{"authorization_pending", "slow_down", ""}
	ctx := context.Background()

	auth, err := AuthorizeDevice(ctx, f.srv.URL, DeviceLoginRequest{Fingerprint: "sha256:own", Platform: "linux", Version: "v1", Name: "box"})
	if err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	if f.authorize["device_kind"] != "cli" || f.authorize["fingerprint"] != "sha256:own" || f.authorize["name"] != "box" {
		t.Fatalf("authorize body = %v, want device_kind cli with the given fingerprint and name", f.authorize)
	}
	if auth.UserCode != "ABCD-EFGH" || auth.DeviceCode != "dc-1" || auth.Interval != 1 {
		t.Fatalf("authorization = %+v", auth)
	}
	for _, want := range []error{ErrAuthorizationPending, ErrSlowDown} {
		if _, err := PollDeviceToken(ctx, f.srv.URL, auth.DeviceCode); !errors.Is(err, want) {
			t.Fatalf("poll = %v, want %v", err, want)
		}
	}
	tok, err := PollDeviceToken(ctx, f.srv.URL, auth.DeviceCode)
	if err != nil {
		t.Fatalf("poll after approval: %v", err)
	}
	if tok.AccessToken != "access-0" || tok.RefreshToken != "refresh-0" || tok.DeviceID != 42 || tok.ExpiresIn != 900 {
		t.Fatalf("tokens = %+v", tok)
	}
	me, err := FetchIdentity(ctx, f.srv.URL, tok.AccessToken)
	if err != nil || me.UserID != 7 || me.Label() != "alice@example.com" {
		t.Fatalf("identity = %+v, %v", me, err)
	}
}

func TestDeviceFlow_GivenADeniedOrExpiredAuthorizationThenTheyAreTold(t *testing.T) {
	f := newFakeAccountServer(t)
	f.pollQueue = []string{"access_denied", "expired_token"}
	if _, err := PollDeviceToken(context.Background(), f.srv.URL, "dc-1"); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("denied poll = %v, want ErrAccessDenied", err)
	}
	if _, err := PollDeviceToken(context.Background(), f.srv.URL, "dc-1"); !errors.Is(err, ErrAuthorizationExpired) {
		t.Fatalf("expired poll = %v, want ErrAuthorizationExpired", err)
	}
}

func TestDeviceFlow_GivenAnUnreachableServerThenTheErrorSaysSo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	_ = ln.Close()
	_, err = AuthorizeDevice(context.Background(), url, DeviceLoginRequest{Fingerprint: "sha256:own"})
	var ue *UnreachableError
	if !errors.As(err, &ue) {
		t.Fatalf("authorize against a closed port = %v, want *UnreachableError", err)
	}
}

func TestAccount_GivenAValidCachedAccessTokenThenItIsUsedWithoutRefreshing(t *testing.T) {
	f := newFakeAccountServer(t)
	dir := t.TempDir()
	f.loggedIn(t, dir, time.Now().Add(time.Hour).Unix())
	a, err := LoginAccount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`)); err != nil {
		t.Fatalf("PostRaw: %v", err)
	}
	if f.refreshes != 0 || f.lastBearer != "Bearer access-0" {
		t.Fatalf("refreshes = %d, bearer = %q; want the cached access token, no refresh", f.refreshes, f.lastBearer)
	}
}

func TestAccount_GivenAnExpiredAccessTokenThenItRefreshesOnceAndPersistsTheRotatedPair(t *testing.T) {
	f := newFakeAccountServer(t)
	dir := t.TempDir()
	f.loggedIn(t, dir, time.Now().Add(-time.Minute).Unix())
	a, err := LoginAccount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`)); err != nil {
		t.Fatalf("PostRaw: %v", err)
	}
	if f.refreshes != 1 || f.lastBearer != "Bearer access-1" {
		t.Fatalf("refreshes = %d, bearer = %q; want one refresh then the new access token", f.refreshes, f.lastBearer)
	}
	c, err := agrctlcred.Load(dir)
	if err != nil || c.RefreshToken != "refresh-1" || c.AccessToken != "access-1" || c.AccessExpiresAt <= time.Now().Unix() {
		t.Fatalf("stored credentials = %+v, %v; want the rotated pair persisted", c, err)
	}
	// 下一个进程读到的是新的一对，不会拿旧的 refresh token 去重放。
	b, _ := LoginAccount(dir)
	if _, err := b.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`)); err != nil {
		t.Fatalf("second PostRaw: %v", err)
	}
	if f.refreshes != 1 || f.replayed {
		t.Fatalf("refreshes = %d, replayed = %v; want the fresh pair reused", f.refreshes, f.replayed)
	}
}

func TestAccount_GivenA401OnACachedTokenThenItRefreshesAndRetries(t *testing.T) {
	f := newFakeAccountServer(t)
	dir := t.TempDir()
	f.loggedIn(t, dir, time.Now().Add(time.Hour).Unix())
	f.rotate() // server 端那枚 access token 已经失效（例如签名密钥轮换）
	a, _ := LoginAccount(dir)
	if _, err := a.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`)); err != nil {
		t.Fatalf("PostRaw: %v", err)
	}
	if f.refreshes != 1 || f.resources != 1 {
		t.Fatalf("refreshes = %d, served = %d; want one refresh and the retried request served", f.refreshes, f.resources)
	}
}

func TestAccount_GivenConcurrentProcessesWithAnExpiredTokenThenOnlyOneRefreshes(t *testing.T) {
	f := newFakeAccountServer(t)
	dir := t.TempDir()
	f.loggedIn(t, dir, time.Now().Add(-time.Minute).Unix())
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			a, err := LoginAccount(dir)
			if err == nil {
				_, err = a.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`))
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent PostRaw: %v", err)
		}
	}
	if f.refreshes != 1 || f.replayed {
		t.Fatalf("refreshes = %d, replayed = %v; want exactly one refresh and no replay", f.refreshes, f.replayed)
	}
}

func TestAccount_GivenARevokedDeviceThenErrLoginExpiredAndTheLoginIsMarkedExpired(t *testing.T) {
	f := newFakeAccountServer(t)
	dir := t.TempDir()
	f.loggedIn(t, dir, time.Now().Add(-time.Minute).Unix())
	f.revoked = true
	a, _ := LoginAccount(dir)
	_, err := a.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`))
	if !errors.Is(err, ErrLoginExpired) {
		t.Fatalf("PostRaw on a revoked device = %v, want ErrLoginExpired", err)
	}
	c, lerr := agrctlcred.Load(dir)
	if lerr != nil || !c.Expired() || c.AccessToken != "" || c.Account != "alice@example.com" {
		t.Fatalf("stored credentials = %+v, %v; want the file kept with the login marked expired", c, lerr)
	}
	// 已标记作废：不再去 server 碰运气。
	before := f.refreshes
	if _, err := a.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`)); !errors.Is(err, ErrLoginExpired) {
		t.Fatalf("second PostRaw = %v, want ErrLoginExpired", err)
	}
	if f.refreshes != before {
		t.Fatal("an expired login still called the refresh endpoint")
	}
}

func TestAccount_GivenAnUnreachableServerDuringRefreshThenTheLoginIsKept(t *testing.T) {
	f := newFakeAccountServer(t)
	dir := t.TempDir()
	f.loggedIn(t, dir, time.Now().Add(-time.Minute).Unix())
	f.srv.Close()
	a, _ := LoginAccount(dir)
	_, err := a.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`))
	var ue *UnreachableError
	if !errors.As(err, &ue) {
		t.Fatalf("PostRaw against a stopped server = %v, want *UnreachableError", err)
	}
	if c, _ := agrctlcred.Load(dir); c == nil || c.Expired() {
		t.Fatalf("stored credentials = %+v; an outage must not end the login", c)
	}
}

func TestAccount_GivenAServerRefusalThenItIsAServerErrorWithTheMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ctl/resources", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"token lacks scope resources:write"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	a := PersonalTokenAccount(srv.URL, "agr_pat_x")
	_, err := a.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`))
	var se *ServerError
	if !errors.As(err, &se) || se.Status != http.StatusForbidden || se.Message != "token lacks scope resources:write" {
		t.Fatalf("PostRaw = %v, want ServerError 403 with the scope message", err)
	}
}

func TestAccount_GivenARejectedPersonalTokenThenErrTokenRejectedWithoutRefresh(t *testing.T) {
	f := newFakeAccountServer(t)
	a := PersonalTokenAccount(f.srv.URL, "agr_pat_revoked")
	_, err := a.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`))
	if !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("PostRaw with a rejected personal token = %v, want ErrTokenRejected", err)
	}
	if f.lastBearer != "Bearer agr_pat_revoked" || f.refreshes != 0 {
		t.Fatalf("bearer = %q, refreshes = %d; want the personal token used as-is, never refreshed", f.lastBearer, f.refreshes)
	}
}

func TestAccount_RevokeNamesThisDevice(t *testing.T) {
	f := newFakeAccountServer(t)
	dir := t.TempDir()
	f.loggedIn(t, dir, time.Now().Add(time.Hour).Unix())
	a, _ := LoginAccount(dir)
	if err := a.Revoke(context.Background(), 42); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if f.revokeBody["device_id"] != float64(42) || !f.revoked {
		t.Fatalf("revoke body = %v, want device_id 42", f.revokeBody)
	}
}
