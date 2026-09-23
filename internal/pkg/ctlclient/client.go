// Package ctlclient is the lightweight HTTP client for a running Agentre
// desktop's loopback control API (ctl_svc, mounted on the httpgateway under
// /ctl/). It is shared by agrctl's control verbs
// (list/get/create/update/delete/send) and `agrctl acp`.
//
// It depends only on net/http + the on-disk handshake file, so it stays
// importable from the slim agrctl binary (no services, no DB, no Wails).
package ctlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/ctlendpoint"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

// Endpoint 已解析的控制端点:base URL + bearer token。
type Endpoint struct {
	Base  string
	Token string
	// TokenFromEnv 为 true 表示 token 来自环境变量 AGENTRE_CTL_TOKEN —— 那是 Agentre
	// 注入给会话的会话级 token;为 false 表示来自 flag 或本机握手文件。
	TokenFromEnv bool
}

// ServerError 是控制 API 回的非 2xx 响应;Message 是执行者给出的原始错误消息。
type ServerError struct {
	Status  int
	Message string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("control error (%d): %s", e.Status, e.Message)
}

// Resolve 按优先级解析控制端点：flag > 环境变量 > AppDataDir 握手文件。
func Resolve(flagURL, flagToken string, lookupEnv func(string) (string, bool)) (Endpoint, error) {
	ep := Endpoint{Base: strings.TrimRight(flagURL, "/"), Token: flagToken}
	if ep.Base == "" {
		if v, ok := lookupEnv("AGENTRE_CTL_ENDPOINT"); ok {
			ep.Base = strings.TrimRight(v, "/")
		}
	}
	if ep.Token == "" {
		if v, ok := lookupEnv("AGENTRE_CTL_TOKEN"); ok && v != "" {
			ep.Token = v
			ep.TokenFromEnv = true
		}
	}
	if ep.Base == "" || ep.Token == "" {
		if dir, derr := paths.AppDataDir(); derr == nil {
			fe, rerr := ctlendpoint.Read(dir)
			switch {
			case rerr == nil:
				if ep.Base == "" {
					ep.Base = strings.TrimRight(fe.URL, "/")
				}
				if ep.Token == "" {
					ep.Token = fe.Token
				}
			case errors.Is(rerr, os.ErrNotExist):
				return Endpoint{}, errors.New("agentre desktop control endpoint not found — is the desktop app running?")
			default:
				return Endpoint{}, fmt.Errorf("read control endpoint: %w", rerr)
			}
		}
	}
	if ep.Base == "" || ep.Token == "" {
		return Endpoint{}, errors.New("control endpoint not configured — is the desktop app running?")
	}
	return ep, nil
}

// Get 发一个 GET 并把 JSON 响应解码进 out(out 可为 nil)。
func (e Endpoint) Get(path string, out any) error {
	return e.do(context.Background(), http.MethodGet, path, nil, out)
}

// Post 发一个 JSON POST 并把 JSON 响应解码进 out(out 可为 nil)。
func (e Endpoint) Post(path string, body any, out any) error {
	return e.do(context.Background(), http.MethodPost, path, body, out)
}

// PostContext 同 Post,但请求随 ctx 结束而取消。
func (e Endpoint) PostContext(ctx context.Context, path string, body, out any) error {
	return e.do(ctx, http.MethodPost, path, body, out)
}

// PostRaw 发一个 POST,请求体是已编码好的 JSON(例如 protojson),返回原始响应体。
func (e Endpoint) PostRaw(ctx context.Context, path string, body []byte) ([]byte, error) {
	return e.roundTrip(ctx, http.MethodPost, path, body)
}

func (e Endpoint) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = b
	}
	raw, err := e.roundTrip(ctx, method, path, payload)
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// roundTrip 发请求并读完响应体;body 为 nil 时不带请求体。非 2xx 返回 *ServerError。
func (e Endpoint) roundTrip(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, e.Base+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to desktop: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, &ServerError{Status: resp.StatusCode, Message: serverErrMsg(raw)}
	}
	return raw, nil
}

// OpenStream 发起一个长连接 GET(通常是 SSE),返回响应体供调用方逐行读取。
// 连接随 ctx 结束而关闭。非 2xx 直接读干响应体并返回错误 —— 绝不把错误响应当流读。
func (e Endpoint) OpenStream(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to desktop: %w", err)
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, &ServerError{Status: resp.StatusCode, Message: serverErrMsg(raw)}
	}
	return resp.Body, nil
}

// serverErrMsg 从 {"error": "..."} 里取消息，取不到就回原始文本。
func serverErrMsg(raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(raw))
}
