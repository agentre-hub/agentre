package devicefactsreport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/syncwire"
)

func report() *syncwire.DeviceFactsReport {
	return &syncwire.DeviceFactsReport{
		CollectedAt: 42,
		Runtimes:    []syncwire.RuntimeFact{{Runtime: "codex", Found: true, Version: "0.46.0"}},
	}
}

// Given 登录了账号的 agentred,When 上报设备情况,Then 用设备凭据 POST 到账号 server 的
// 设备情况端点,请求体就是那份情况。
func TestUploader_GivenLoggedInDaemon_WhenUploading_ThenPostsTheReportWithItsDeviceCredential(t *testing.T) {
	var got syncwire.DeviceFactsReport
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/prefix"+syncwire.PathDeviceFacts, r.URL.Path)
		assert.Equal(t, "Bearer device-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, _ := io.ReadAll(r.Body)
		assert.NoError(t, json.Unmarshal(body, &got))
		_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{}}`)
	}))
	t.Cleanup(server.Close)

	u := Uploader{
		LoggedIn: func() bool { return true }, ServerURL: func() string { return server.URL + "/prefix/" },
		AccessToken: func() string { return "device-token" }, HTTP: server.Client(),
	}
	require.NoError(t, u.Upload(context.Background(), report()))
	assert.Equal(t, *report(), got)
}

// server 还没有这个端点(404)或答了业务错误:上报失败但只是一个错误,由调用方下次重试。
func TestUploader_GivenServerWithoutEndpoint_ThenUploadFailsWithoutPanicking(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"404": func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"business error": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"code":10001,"msg":"nope"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			u := Uploader{
				LoggedIn: func() bool { return true }, ServerURL: func() string { return server.URL },
				AccessToken: func() string { return "t" }, HTTP: server.Client(),
			}
			assert.Error(t, u.Upload(context.Background(), report()))
		})
	}
}

// 未登录账号的 agentred 只供已配对桌面端直读:一个请求都不发。
func TestUploader_GivenLoggedOutDaemon_ThenSendsNothing(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	t.Cleanup(server.Close)
	u := Uploader{
		LoggedIn: func() bool { return false }, ServerURL: func() string { return server.URL },
		AccessToken: func() string { return "t" }, HTTP: server.Client(),
	}
	assert.ErrorIs(t, u.Upload(context.Background(), report()), ErrNotLoggedIn)
	assert.False(t, called)
}
