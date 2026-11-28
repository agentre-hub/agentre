// Package devicefactsreport 把 agentred 的设备情况交给账号 server(规格「设备情况
// 上报」)。只做 HTTP:什么时候报、报哪一份由 devicefacts.Reporter 决定。
package devicefactsreport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/pkg/syncwire"
)

const (
	defaultTimeout   = 15 * time.Second
	maxResponseBytes = 64 << 10
)

// ErrNotLoggedIn 是未登录账号的 agentred:它不上报,只供已配对桌面端直读。
var ErrNotLoggedIn = errors.New("device facts report skipped: daemon is not logged in")

// HTTPDoer 是 HTTP 接缝。
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Uploader 用 daemon 自己的设备凭据把情况 POST 到 syncwire.PathDeviceFacts。
type Uploader struct {
	LoggedIn    func() bool
	ServerURL   func() string
	AccessToken func() string
	// HTTP 为 nil 时用一个带超时的默认客户端。
	HTTP HTTPDoer
}

// Upload 上报一份情况。未登录不发请求;非 2xx 或业务码非 0 都是错误(server 还没有
// 这个端点时就是一个 404),由调用方在下一次触发时重试。
func (u Uploader) Upload(ctx context.Context, report *syncwire.DeviceFactsReport) error {
	if u.LoggedIn == nil || !u.LoggedIn() {
		return ErrNotLoggedIn
	}
	baseURL, token := "", ""
	if u.ServerURL != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(u.ServerURL()), "/")
	}
	if u.AccessToken != nil {
		token = strings.TrimSpace(u.AccessToken())
	}
	if baseURL == "" || token == "" {
		return errors.New("device facts report unavailable: account endpoint or credential missing")
	}
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode device facts: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+syncwire.PathDeviceFacts, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build device facts request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := u.HTTP
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("device facts request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read device facts response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("device facts endpoint returned %s", resp.Status)
	}
	var envelope struct {
		Code int `json:"code"`
	}
	if len(bytes.TrimSpace(payload)) > 0 {
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return fmt.Errorf("parse device facts response: %w", err)
		}
	}
	if envelope.Code != 0 {
		return fmt.Errorf("device facts report rejected with code %d", envelope.Code)
	}
	return nil
}
