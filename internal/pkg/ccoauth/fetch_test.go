package ccoauth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

func TestFetch(t *testing.T) {
	Convey("Fetch 先尝试 Keychain 命中后调用 API", t, func() {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"five_hour":{"utilization":20,"resets_at":"2026-05-28T13:00:00Z"}}`))
		}))
		defer srv.Close()

		kc := newFakeKeychain()
		kc.put("Claude Code-credentials", "codfrm", `{"claudeAiOauth":{"accessToken":"kc-tok"}}`)

		rl, err := ccoauth.Fetch(context.Background(), ccoauth.FetcherConfig{
			Keychain:           kc,
			EnvClaudeConfigDir: "", // 默认 service name
			ClaudeConfigDir:    t.TempDir(),
			Username:           "codfrm",
			HTTPClient:         ccoauth.NewClient(srv.URL),
		})
		So(err, ShouldBeNil)
		So(rl.FiveHourPercent, ShouldEqual, 20)
		So(gotAuth, ShouldEqual, "Bearer kc-tok")
	})

	Convey("Fetch 在 Keychain 缺失时回退到 .credentials.json", t, func() {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"five_hour":{"utilization":10,"resets_at":"2026-05-28T13:00:00Z"}}`))
		}))
		defer srv.Close()

		dir := t.TempDir()
		So(os.WriteFile(filepath.Join(dir, ".credentials.json"),
			[]byte(`{"claudeAiOauth":{"accessToken":"file-tok"}}`), 0o600), ShouldBeNil)

		rl, err := ccoauth.Fetch(context.Background(), ccoauth.FetcherConfig{
			Keychain:        newFakeKeychain(), // 全空
			ClaudeConfigDir: dir,
			Username:        "codfrm",
			HTTPClient:      ccoauth.NewClient(srv.URL),
		})
		So(err, ShouldBeNil)
		So(rl.FiveHourPercent, ShouldEqual, 10)
		So(gotAuth, ShouldEqual, "Bearer file-tok")
	})

	Convey("Fetch 在 Keychain 和文件都缺失时返回 ErrNoCredentials", t, func() {
		dir := t.TempDir() // 没创建 .credentials.json
		_, err := ccoauth.Fetch(context.Background(), ccoauth.FetcherConfig{
			Keychain:        newFakeKeychain(),
			ClaudeConfigDir: dir,
			Username:        "codfrm",
			HTTPClient:      ccoauth.NewClient("http://unused"),
		})
		So(errors.Is(err, ccoauth.ErrNoCredentials), ShouldBeTrue)
	})

	Convey("Fetch 在 keychain 为 nil 时只用文件", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"five_hour":{"utilization":5,"resets_at":"2026-05-28T13:00:00Z"}}`))
		}))
		defer srv.Close()

		dir := t.TempDir()
		So(os.WriteFile(filepath.Join(dir, ".credentials.json"),
			[]byte(`{"claudeAiOauth":{"accessToken":"file-only"}}`), 0o600), ShouldBeNil)

		rl, err := ccoauth.Fetch(context.Background(), ccoauth.FetcherConfig{
			Keychain:        nil,
			ClaudeConfigDir: dir,
			HTTPClient:      ccoauth.NewClient(srv.URL),
		})
		So(err, ShouldBeNil)
		So(rl.FiveHourPercent, ShouldEqual, 5)
	})

	Convey("Fetch 在 token 有效但 API 返回 401 时透传 ErrAuthExpired", t, func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		kc := newFakeKeychain()
		kc.put("Claude Code-credentials", "codfrm", `{"claudeAiOauth":{"accessToken":"stale"}}`)

		_, err := ccoauth.Fetch(context.Background(), ccoauth.FetcherConfig{
			Keychain:        kc,
			ClaudeConfigDir: t.TempDir(),
			Username:        "codfrm",
			HTTPClient:      ccoauth.NewClient(srv.URL),
		})
		So(errors.Is(err, ccoauth.ErrAuthExpired), ShouldBeTrue)
	})

	Convey("Fetch 在 CLAUDE_CONFIG_DIR 非空时用 hash 后缀 service 名", t, func() {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"five_hour":{"utilization":1,"resets_at":"2026-05-28T13:00:00Z"}}`))
		}))
		defer srv.Close()

		hashedSvc := ccoauth.KeychainServiceName("/custom/dir")
		kc := newFakeKeychain()
		kc.put(hashedSvc, "codfrm", `{"claudeAiOauth":{"accessToken":"hashed-tok"}}`)

		rl, err := ccoauth.Fetch(context.Background(), ccoauth.FetcherConfig{
			Keychain:           kc,
			EnvClaudeConfigDir: "/custom/dir",
			ClaudeConfigDir:    t.TempDir(),
			Username:           "codfrm",
			HTTPClient:         ccoauth.NewClient(srv.URL),
		})
		So(err, ShouldBeNil)
		So(rl.FiveHourPercent, ShouldEqual, 1)
		So(gotAuth, ShouldEqual, "Bearer hashed-tok")
	})
}
