package ctlclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agrctlcred"
)

// langRecorder 记下每条请求的路径与 Accept-Language，并按路径回一个 cago 外壳的成功响应。
type langRecorder struct {
	mu    sync.Mutex
	langs map[string]string
}

func (r *langRecorder) record(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.langs == nil {
		r.langs = map[string]string{}
	}
	r.langs[req.Method+" "+req.URL.Path] = req.Header.Get("Accept-Language")
}

func (r *langRecorder) get(key string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.langs[key]
	return v, ok
}

func newLangServer(t *testing.T, rec *langRecorder, errorBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	ok := func(path string, data any) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			if errorBody != "" && path == "/v1/ctl/resources" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, errorBody)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": data})
		})
	}
	ok("/v1/oauth/device/authorize", map[string]any{"device_code": "dc", "user_code": "UC", "expires_in": 600})
	ok("/v1/oauth/device/token", map[string]any{"access_token": "a", "expires_in": 900, "refresh_token": "r", "refresh_expires_in": 86400, "device_id": 42})
	ok("/v1/oauth/token/refresh", map[string]any{"access_token": "a2", "expires_in": 900, "refresh_token": "r2", "refresh_expires_in": 86400, "device_id": 42})
	ok("/v1/ctl/resources", map[string]any{"items": []any{}})
	ok("/v1/devices/42", map[string]any{"id": 42})
	ok("/v1/personal-tokens", map[string]any{"id": 1})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAccountRequests_GivenAnyAccountRequestThenItAsksTheServerForEnglish(t *testing.T) {
	rec := &langRecorder{}
	srv := newLangServer(t, rec, "")
	ctx := context.Background()

	// 个人 token：资源、设备、个人 token 三类请求都带 Accept-Language: en。
	pat := PersonalTokenAccount(srv.URL, "agr_pat_x")
	if _, err := pat.GetRaw(ctx, "/v1/ctl/resources"); err != nil {
		t.Fatal(err)
	}
	if _, err := pat.PatchRaw(ctx, "/v1/devices/42", []byte(`{"display_name":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pat.PostRaw(ctx, "/v1/personal-tokens", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}

	// Device Flow 登录 + 登录态刷新。
	if _, err := AuthorizeDevice(ctx, srv.URL, DeviceLoginRequest{Fingerprint: "sha256:fp", Platform: "darwin", Version: "1", Name: "box"}); err != nil {
		t.Fatal(err)
	}
	if _, err := PollDeviceToken(ctx, srv.URL, "dc"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := agrctlcred.Save(dir, &agrctlcred.Credentials{ServerURL: srv.URL, AccountID: 7, DeviceID: 42,
		RefreshToken: "r", RefreshExpiresAt: time.Now().Add(time.Hour).Unix(), AccessToken: "stale", AccessExpiresAt: 1}); err != nil {
		t.Fatal(err)
	}
	acc, err := LoginAccount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acc.GetRaw(ctx, "/v1/ctl/resources"); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		"GET /v1/ctl/resources", "PATCH /v1/devices/42", "POST /v1/personal-tokens",
		"POST /v1/oauth/device/authorize", "POST /v1/oauth/device/token", "POST /v1/oauth/token/refresh",
	} {
		if got, ok := rec.get(key); !ok || got != "en" {
			t.Errorf("%s Accept-Language = %q (seen=%v), want \"en\"", key, got, ok)
		}
	}
}

func TestAccountRequests_GivenAServerErrorThenItsBodyMessageIsPrintedAsReturned(t *testing.T) {
	const zhMsg = "该设备运行的发行版与服务器不兼容"
	rec := &langRecorder{}
	srv := newLangServer(t, rec, `{"code":400,"msg":"`+zhMsg+`","data":null}`)
	pat := PersonalTokenAccount(srv.URL, "agr_pat_x")

	_, err := pat.PostRaw(context.Background(), "/v1/ctl/resources", []byte(`{}`))
	var se *ServerError
	if !errors.As(err, &se) || se.Status != http.StatusBadRequest || se.Message != zhMsg {
		t.Fatalf("PostRaw = %v, want ServerError 400 carrying the server message verbatim", err)
	}
	if got, _ := rec.get("POST /v1/ctl/resources"); got != "en" {
		t.Errorf("Accept-Language = %q, want \"en\"", got)
	}
}
