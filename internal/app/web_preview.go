package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/pkg/wire/portforwardhost"
)

// 网页预览预检的结论。WKWebView 里父页面拿不到 iframe 被 X-Frame-Options / frame-ancestors
// 拦下的信号(load 照常触发、没有 error),也拿不到证书失败与连接被拒,所以由 Go 侧先探一次。
const (
	// webPreviewFrameBlocked:目标站禁止被嵌入。
	webPreviewFrameBlocked = "frameBlocked"
	// webPreviewCertificate:https 证书没通过校验(从不忽略证书错误)。
	webPreviewCertificate = "certificate"
	// webPreviewUnreachable:连不上、超时或重定向失控。
	webPreviewUnreachable = "unreachable"
	// webPreviewForwardFailed:端口转发这一层自己没能完成(映射停用 / 不在了、上游断开……)。
	webPreviewForwardFailed = "forwardFailed"

	webPreviewTimeout      = 8 * time.Second
	webPreviewMaxRedirects = 5
)

// WebPreviewProbe 是网页预览预检的结果。不含响应正文。
type WebPreviewProbe struct {
	// URL 是 iframe 该加载的地址:远端会话的 localhost 地址已换成本机转发地址。
	URL string `json:"url"`
	// Forwarded 为 true 表示 URL 是经端口转发换过的。
	Forwarded  bool   `json:"forwarded"`
	Embeddable bool   `json:"embeddable"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail"`
}

// ProbeSessionWebPreview 先按会话解析地址(远端会话的 localhost 经端口转发),再对解析后的
// 地址做一次内嵌预检。只认 http / https;不返回正文。
func (a *App) ProbeSessionWebPreview(sessionID int64, rawURL string) (*WebPreviewProbe, error) {
	if err := validateWebPreviewURL(rawURL); err != nil {
		return nil, err
	}
	resolved, err := a.ResolveSessionURL(sessionID, rawURL)
	if err != nil {
		return nil, err
	}
	forwarded := resolved != rawURL
	probe, err := probeWebPreview(a.ctx, resolved, forwarded)
	if err != nil {
		return nil, err
	}
	probe.URL = resolved
	probe.Forwarded = forwarded
	if !probe.Embeddable {
		logger.Ctx(a.ctx).Debug("app.ProbeSessionWebPreview: not embeddable",
			zap.Int64("sessionId", sessionID), zap.String("reason", probe.Reason), zap.Bool("forwarded", forwarded))
	}
	return probe, nil
}

func validateWebPreviewURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("web preview: only http(s) URLs are supported")
	}
	return nil
}

// probeWebPreview 对 rawURL 发一次 GET(HEAD 对开发服务器不可靠),跟随有界的重定向,
// 只看最终响应头,立刻丢弃正文。forwarded 标明 rawURL 是本机转发地址:那时转发层自己的
// 失败(带 portforwardhost.FailureKindHeader)按它说的种类报原因。
func probeWebPreview(ctx context.Context, rawURL string, forwarded bool) (*WebPreviewProbe, error) {
	if err := validateWebPreviewURL(rawURL); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, webPreviewTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           nil,
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) > webPreviewMaxRedirects {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	defer client.CloseIdleConnections()

	resp, err := client.Do(req)
	if err != nil {
		return failedProbe(err), nil
	}
	_ = resp.Body.Close()

	if forwarded {
		if kind := resp.Header.Get(portforwardhost.FailureKindHeader); kind != "" {
			return forwardFailureProbe(kind), nil
		}
	}
	if blocked, detail := frameBlocked(resp.Header); blocked {
		return &WebPreviewProbe{Reason: webPreviewFrameBlocked, Detail: detail}, nil
	}
	return &WebPreviewProbe{Embeddable: true}, nil
}

// forwardFailureProbe 把转发层报的失败种类(portforwardhost.FailureKind.Token)翻成预检原因。
// 证书没过单独成一类(spec「网页预览工具窗」https:显示失败原因与「打开端口转发设置」);
// 目标连不上与设备够不着说成访问不到;其余是转发这一层没能完成。
func forwardFailureProbe(kind string) *WebPreviewProbe {
	switch kind {
	case "tls-verification":
		return &WebPreviewProbe{Reason: webPreviewCertificate,
			Detail: "the https target's certificate failed verification on the execution device, and this port forward does not ignore certificate errors"}
	case "no-listener":
		return &WebPreviewProbe{Reason: webPreviewUnreachable, Detail: "nothing accepted the connection on the execution device"}
	case "name-resolution":
		return &WebPreviewProbe{Reason: webPreviewUnreachable, Detail: "the target host name did not resolve on the execution device"}
	case "device-unreachable":
		return &WebPreviewProbe{Reason: webPreviewUnreachable, Detail: "the execution device is not reachable right now"}
	}
	return &WebPreviewProbe{Reason: webPreviewForwardFailed, Detail: "port forward: " + kind}
}

func failedProbe(err error) *WebPreviewProbe {
	var (
		verifyErr    *tls.CertificateVerificationError
		authorityErr x509.UnknownAuthorityError
		hostnameErr  x509.HostnameError
		invalidErr   x509.CertificateInvalidError
	)
	reason := webPreviewUnreachable
	if errors.As(err, &verifyErr) || errors.As(err, &authorityErr) ||
		errors.As(err, &hostnameErr) || errors.As(err, &invalidErr) {
		reason = webPreviewCertificate
	}
	return &WebPreviewProbe{Reason: reason, Detail: err.Error()}
}

// frameBlocked 判最终响应是否禁止被嵌入。frame-ancestors 存在时覆盖 X-Frame-Options。
// 父页面的来源是 wails://wails,所以只有 `*` 或点名 wails 的列表放行,'self' / 'none' / 具体
// 站点都不放行。
func frameBlocked(h http.Header) (bool, string) {
	if sources, ok := frameAncestors(h.Values("Content-Security-Policy")); ok {
		if allowsWails(sources) {
			return false, ""
		}
		return true, "Content-Security-Policy frame-ancestors " + strings.Join(sources, " ")
	}
	for _, v := range h.Values("X-Frame-Options") {
		for _, part := range strings.Split(v, ",") {
			switch strings.ToUpper(strings.TrimSpace(part)) {
			case "DENY", "SAMEORIGIN":
				return true, "X-Frame-Options: " + strings.TrimSpace(part)
			}
		}
	}
	return false, ""
}

// frameAncestors 从若干条 CSP 头里找出 frame-ancestors 的来源列表。多条策略并存时取
// 最严的那条(任何一条拒绝即拒绝):这里只返回第一条不放行的,都放行才返回放行的那条。
func frameAncestors(headers []string) ([]string, bool) {
	var found []string
	ok := false
	for _, header := range headers {
		for _, policy := range strings.Split(header, ",") {
			for _, directive := range strings.Split(policy, ";") {
				fields := strings.Fields(directive)
				if len(fields) == 0 || !strings.EqualFold(fields[0], "frame-ancestors") {
					continue
				}
				sources := fields[1:]
				if !ok || !allowsWails(sources) {
					found = sources
				}
				ok = true
			}
		}
	}
	return found, ok
}

func allowsWails(sources []string) bool {
	for _, s := range sources {
		switch strings.ToLower(s) {
		case "*", "wails:", "wails://wails", "wails://*":
			return true
		}
	}
	return false
}
