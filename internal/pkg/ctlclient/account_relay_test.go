package ctlclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 账号执行者经 relay 走会话命令时要的三件事：GET 一条 cago 外壳的接口、交出 relay 握手用的
// 那枚 access token、以及 relay 拒收（401）时换一枚新的。

func TestAccount_GivenAGetThenItCarriesTheBearerAndRefreshesOnA401(t *testing.T) {
	f := newFakeAccountServer(t)
	f.srv.Config.Handler.(*http.ServeMux).HandleFunc("/v1/workspace/dispatch-target", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(w, r) {
			return
		}
		envelope(w, map[string]any{"q": r.URL.Query().Get("agent_sync_id")})
	})
	dir := t.TempDir()
	f.loggedIn(t, dir, time.Now().Add(time.Hour).Unix())
	f.rotate()
	a, _ := LoginAccount(dir)
	raw, err := a.GetRaw(context.Background(), "/v1/workspace/dispatch-target?agent_sync_id=A")
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if !strings.Contains(string(raw), `"q":"A"`) || f.refreshes != 1 {
		t.Fatalf("raw = %s, refreshes = %d; want the body after one refresh", raw, f.refreshes)
	}
}

func TestAccount_GivenALoginThenBearerAndReauthorizeHandOutTheRelayCredential(t *testing.T) {
	f := newFakeAccountServer(t)
	dir := t.TempDir()
	f.loggedIn(t, dir, time.Now().Add(time.Hour).Unix())
	a, _ := LoginAccount(dir)
	ctx := context.Background()
	tok, err := a.Bearer(ctx)
	if err != nil || tok != "access-0" {
		t.Fatalf("Bearer = %q, %v; want the cached access token", tok, err)
	}
	fresh, err := a.Reauthorize(ctx, tok)
	if err != nil || fresh != "access-1" || f.refreshes != 1 {
		t.Fatalf("Reauthorize = %q, %v (refreshes %d); want a rotated access token", fresh, err, f.refreshes)
	}
	if a.DeviceName() != "box" {
		t.Fatalf("DeviceName = %q, want the cli device's name", a.DeviceName())
	}
}

func TestAccount_GivenAPersonalTokenThenReauthorizeSaysItWasRejected(t *testing.T) {
	a := PersonalTokenAccount("http://127.0.0.1:1", "agr_pat_x")
	tok, err := a.Bearer(context.Background())
	if err != nil || tok != "agr_pat_x" {
		t.Fatalf("Bearer = %q, %v", tok, err)
	}
	if _, err := a.Reauthorize(context.Background(), tok); !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("Reauthorize with a personal token = %v, want ErrTokenRejected", err)
	}
	if a.DeviceName() != "agrctl" {
		t.Fatalf("DeviceName = %q, want agrctl for a personal token", a.DeviceName())
	}
}
