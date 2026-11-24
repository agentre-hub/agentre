package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/service/port_forward_svc"
)

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func withHeaders(kv ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i+1 < len(kv); i += 2 {
			w.Header().Add(kv[i], kv[i+1])
		}
		_, _ = w.Write([]byte("<html>page body that must never be returned</html>"))
	}
}

// Given 一个目标站, When 预检, Then 按 X-Frame-Options / frame-ancestors 判能否内嵌。
func TestProbeWebPreviewFrameHeaders(t *testing.T) {
	cases := []struct {
		name       string
		headers    []string
		embeddable bool
		reason     string
	}{
		{"plain page is embeddable", nil, true, ""},
		{"XFO DENY refused", []string{"X-Frame-Options", "DENY"}, false, webPreviewFrameBlocked},
		{"XFO SAMEORIGIN refused", []string{"X-Frame-Options", "sameorigin"}, false, webPreviewFrameBlocked},
		{"frame-ancestors none refused", []string{"Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'"}, false, webPreviewFrameBlocked},
		{"frame-ancestors self refused", []string{"Content-Security-Policy", "frame-ancestors 'self'"}, false, webPreviewFrameBlocked},
		{"frame-ancestors overrides XFO", []string{"X-Frame-Options", "DENY", "Content-Security-Policy", "frame-ancestors *"}, true, ""},
		{"frame-ancestors none beats a lax XFO", []string{"X-Frame-Options", "ALLOW-FROM https://x", "Content-Security-Policy", "frame-ancestors 'none'"}, false, webPreviewFrameBlocked},
		{"CSP without frame-ancestors leaves XFO in charge", []string{"X-Frame-Options", "DENY", "Content-Security-Policy", "default-src 'self'"}, false, webPreviewFrameBlocked},
		{"frame-ancestors naming wails is allowed", []string{"Content-Security-Policy", "frame-ancestors wails://wails"}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := serve(t, withHeaders(c.headers...))
			got, err := probeWebPreview(context.Background(), srv.URL, false)
			require.NoError(t, err)
			assert.Equal(t, c.embeddable, got.Embeddable)
			assert.Equal(t, c.reason, got.Reason)
			if !c.embeddable {
				assert.NotEmpty(t, got.Detail)
			}
		})
	}
}

// Given 目标重定向到一个禁止嵌入的页面, Then 判的是最终响应。
func TestProbeWebPreviewFollowsRedirects(t *testing.T) {
	final := serve(t, withHeaders("X-Frame-Options", "DENY"))
	mid := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/landing", http.StatusFound)
	})
	got, err := probeWebPreview(context.Background(), mid.URL, false)
	require.NoError(t, err)
	assert.False(t, got.Embeddable)
	assert.Equal(t, webPreviewFrameBlocked, got.Reason)
}

// Given 无限重定向, Then 有界跟随后报 unreachable,不挂死。
func TestProbeWebPreviewBoundsRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/again", http.StatusFound)
	})
	got, err := probeWebPreview(context.Background(), srv.URL, false)
	require.NoError(t, err)
	assert.False(t, got.Embeddable)
	assert.Equal(t, webPreviewUnreachable, got.Reason)
}

// Given 自签名证书的 https 站, Then 不忽略证书错误,报 certificate。
func TestProbeWebPreviewReportsCertificateFailure(t *testing.T) {
	srv := httptest.NewTLSServer(withHeaders())
	t.Cleanup(srv.Close)
	got, err := probeWebPreview(context.Background(), srv.URL, false)
	require.NoError(t, err)
	assert.False(t, got.Embeddable)
	assert.Equal(t, webPreviewCertificate, got.Reason)
	assert.NotEmpty(t, got.Detail)
}

// Given 没有服务监听的端口, Then 报 unreachable。
func TestProbeWebPreviewReportsRefusedPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	got, perr := probeWebPreview(context.Background(), "http://"+addr+"/", false)
	require.NoError(t, perr)
	assert.False(t, got.Embeddable)
	assert.Equal(t, webPreviewUnreachable, got.Reason)
}

// Given 转发出来的 https 目标, When 转发层答 502(它没连上目标,常见是证书没过), Then 报 forwardFailed。
func TestProbeWebPreviewForwardedHTTPSBadGateway(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	got, err := probeWebPreview(context.Background(), srv.URL, true)
	require.NoError(t, err)
	assert.False(t, got.Embeddable)
	assert.Equal(t, webPreviewForwardFailed, got.Reason)

	plain, err := probeWebPreview(context.Background(), srv.URL, false)
	require.NoError(t, err)
	assert.True(t, plain.Embeddable, "非转发的 502 是应用自己的答复,照常内嵌")
}

// Given 非 http(s) 地址, Then 拒绝,什么都不请求。
func TestProbeWebPreviewRejectsNonHTTPSchemes(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "ftp://127.0.0.1/x", "javascript:alert(1)", "gopher://127.0.0.1:70/", "http://", "not a url", ""} {
		_, err := probeWebPreview(context.Background(), raw, false)
		assert.Error(t, err, raw)
	}
}

// Given 本机会话, When 经绑定预检, Then 只探会话解析后的地址并回带它。
func TestProbeSessionWebPreviewBinding(t *testing.T) {
	srv := serve(t, withHeaders("X-Frame-Options", "DENY"))
	stub := &stubSessionProcessChat{}
	a := withStubSessionProcessChat(t, stub)

	got, err := a.ProbeSessionWebPreview(42, srv.URL+"/x")
	require.NoError(t, err)
	assert.Equal(t, int64(42), stub.gotSID)
	assert.Equal(t, srv.URL+"/x", got.URL)
	assert.False(t, got.Forwarded)
	assert.False(t, got.Embeddable)

	_, err = a.ProbeSessionWebPreview(42, "file:///etc/passwd")
	assert.Error(t, err)
}

// Given 远端会话的 localhost 地址, When 预检, Then 探的是转发地址,且标记 Forwarded。
func TestProbeSessionWebPreviewRemoteUsesForward(t *testing.T) {
	pf := &stubPortForwardSvc{mapping: &port_forward_svc.MappingView{ID: "11", Port: 3000, Enabled: true}}
	a, _ := withRemoteSession(t, pf)

	got, err := a.ProbeSessionWebPreview(42, "http://localhost:3000/docs")
	require.NoError(t, err)
	assert.True(t, got.Forwarded)
	assert.NotContains(t, got.URL, "localhost:3000")
	assert.Contains(t, got.URL, "/docs")
}
