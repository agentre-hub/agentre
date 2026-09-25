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
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	// baseFromHandshake 为 true 表示 Base 取自本机桌面握手文件（不是 flag 或会话注入的
	// AGENTRE_CTL_ENDPOINT）：只有这种端点拨不通时，agentred 主机上才回落到「没有会话」
	// 那句提示（dialFailure）。
	baseFromHandshake bool
}

// ServerError 是控制 API 回的非 2xx 响应;Message 是执行者给出的原始错误消息。
type ServerError struct {
	Status  int
	Message string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("control error (%d): %s", e.Status, e.Message)
}

// errAgentredHostNoSession 是 agentred 主机上给的统一提示：唯一的解释就是「这台机器上没有
// Agentre 派发的会话」——不管是压根没有桌面握手文件，还是握手文件是桌面端早先跑过、后来
// 停掉后留下的陈旧记录（cago.go 只写不删），本机连不上一个能回应的桌面端控制端点，结论都
// 一样。Resolve 与实际拨号失败（roundTrip/OpenStream）共用同一句话，别各写一遍。
var errAgentredHostNoSession = errors.New(
	"this is an agentred host — only Agentre-dispatched sessions can use agrctl here (no session token found)")

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
					ep.baseFromHandshake = true
				}
				if ep.Token == "" {
					ep.Token = fe.Token
				}
			case errors.Is(rerr, os.ErrNotExist):
				if isAgentredHost() {
					return Endpoint{}, errAgentredHostNoSession
				}
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

// isAgentredHost 判断本机是不是一台 agentred 主机 —— 检查 agentred 落盘留下的
// state.json(internal/daemon/state.Load 在它第一次启动时就地写出,此后一直留着,不依赖
// 当前是否在跑)。只做一次 os.Stat,轻量、无副作用,不会反过来让 agentred 写握手文件。
func isAgentredHost() bool {
	dir, err := paths.AgentredDataDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "state.json"))
	return err == nil
}

// dialFailure 把一次失败的拨号（http.Client.Do 本身出错，不是非 2xx 响应）翻成给用户看的
// 错误。端点取自桌面握手文件、而本机是 agentred 主机时，拨不通只说明那是桌面端停掉后留下的
// 陈旧握手文件——回落到与压根没有握手文件时同一句提示。端点来自 flag 或会话注入的环境变量
// 时（agentred 上的 Agentre 派发会话）那句「没有会话 token」是假的，保留原始拨号错误。
// 只认真正的拨号失败（net.OpError 的 dial）：连上之后被取消、超时或断开说明那头的桌面
// 是活的，握手文件并不陈旧。
func (e Endpoint) dialFailure(err error) error {
	var opErr *net.OpError
	couldNotDial := errors.As(err, &opErr) && opErr.Op == "dial" &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	if e.baseFromHandshake && couldNotDial && isAgentredHost() {
		return errAgentredHostNoSession
	}
	return fmt.Errorf("connect to desktop: %w", err)
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
		return nil, e.dialFailure(err)
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
		return nil, e.dialFailure(err)
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
