package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrCtlServerUnavailable:这台 agentred 还没有登录账号(没有 server 地址或设备凭据),
// 控制台拥有的会话无处执行 ctl 请求。
var ErrCtlServerUnavailable = errors.New("agentred is not signed in to an Agentre account")

func isCtlServerUnavailable(err error) bool { return errors.Is(err, ErrCtlServerUnavailable) }

// errCtlServerUnauthorized 标出「值得刷新一次再试」的那一种失败。
var errCtlServerUnauthorized = errors.New("ctl server rejected the device access token")

// CtlServerClient 是 server 执行者(`POST <server>/v1/ctl/*`)的客户端:以这台 agentred
// 自己的设备 access token 作 Bearer。三个函数每次请求现取(与中继、目录同步同一份解析),
// 401 走共享的单飞刷新入口刷新一次再试。请求与应答正文可能含密钥明文,从不进日志。
type CtlServerClient struct {
	HTTP        *http.Client
	ServerURL   func() string
	AccessToken func() string
	// Refresh 刷新设备凭据一次(credentialRefresher.refreshNow);nil = 不刷新。
	Refresh func(context.Context) error
}

// ctlServerTimeout 是单次 server 往返的上限;审批的等待不在这一跳里。
const ctlServerTimeout = 30 * time.Second

// Post 把一次 ctl 请求交给 server,返回它的状态码与正文(非 2xx 也原样交回,由代理透传)。
func (c *CtlServerClient) Post(ctx context.Context, pathAndQuery string, body []byte) (int, []byte, error) {
	return c.withRefresh(ctx, func() (int, []byte, error) {
		return c.do(ctx, http.MethodPost, pathAndQuery, bytes.NewReader(body))
	})
}

// Get 是 Post 的只读版本(同一份设备 Bearer 凭据,同样的单飞 401 刷新),用于
// serverDevicesPath 这类不写正文的请求。
func (c *CtlServerClient) Get(ctx context.Context, pathAndQuery string) (int, []byte, error) {
	return c.withRefresh(ctx, func() (int, []byte, error) {
		return c.do(ctx, http.MethodGet, pathAndQuery, nil)
	})
}

// withRefresh 发一次;被拒为设备凭据失效时刷新一次再发一次。
func (c *CtlServerClient) withRefresh(ctx context.Context, send func() (int, []byte, error)) (int, []byte, error) {
	status, raw, err := send()
	if errors.Is(err, errCtlServerUnauthorized) && c.Refresh != nil {
		if rerr := c.Refresh(ctx); rerr != nil {
			return 0, nil, fmt.Errorf("refresh device credential: %w", rerr)
		}
		status, raw, err = send()
	}
	return status, raw, err
}

func (c *CtlServerClient) do(ctx context.Context, method, pathAndQuery string, body io.Reader) (int, []byte, error) {
	var base, token string
	if c.ServerURL != nil {
		base = strings.TrimRight(strings.TrimSpace(c.ServerURL()), "/")
	}
	if c.AccessToken != nil {
		token = c.AccessToken()
	}
	if base == "" || token == "" {
		return 0, nil, ErrCtlServerUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, method, base+pathAndQuery, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: ctlServerTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, ctlMaxBody*4))
	if err != nil {
		return 0, nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return 0, nil, errCtlServerUnauthorized
	}
	return resp.StatusCode, raw, nil
}
