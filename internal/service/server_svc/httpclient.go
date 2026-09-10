package server_svc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// serverClient 是一个薄薄的 net/http 封装，给 server_svc 各方法复用。
// 责任：组装 Authorization 头、JSON 编解码、把网络错误归一到 ErrServerUnreachable、
// 把 4xx/5xx 业务响应包成 *httpErr（供 401-重试逻辑使用）。
//
// 线程安全：accessToken 通过 RWMutex 保护，其余字段创建后不再写。
type serverClient struct {
	baseURL string
	http    *http.Client

	mu          sync.RWMutex
	accessToken string
}

// NewHTTPClient 构造一个 serverClient。baseURL 末尾的 "/" 会被裁掉。
func NewHTTPClient(baseURL, accessToken string) *serverClient {
	return &serverClient{
		baseURL:     strings.TrimRight(baseURL, "/"),
		http:        &http.Client{Timeout: 15 * time.Second},
		accessToken: accessToken,
	}
}

// SetAccessToken 在 token 刷新后更新；线程安全。
func (c *serverClient) SetAccessToken(tok string) {
	c.mu.Lock()
	c.accessToken = tok
	c.mu.Unlock()
}

// AccessToken 返回当前 token，可能为空字符串。
func (c *serverClient) AccessToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.accessToken
}

// envelope 与 cago 默认响应壳对齐：{"code":0,"msg":"...","data":...}
type envelope[T any] struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

// httpErr 描述一次 4xx/5xx 响应;HTTPStatus 供 401 重试逻辑消费。
type httpErr struct {
	status int
	body   string
}

func (e *httpErr) Error() string   { return e.body }
func (e *httpErr) HTTPStatus() int { return e.status }

// do 发起一次请求；out 若非 nil，会把响应 JSON 解码进去。返回 HTTP 状态码与错误。
// 网络层失败统一返回 ErrServerUnreachable，便于上层一处处理。
func (c *serverClient) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	started := time.Now()
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return 0, err
	}

	var reqBody io.Reader
	if body != nil {
		b, mErr := json.Marshal(body)
		if mErr != nil {
			return 0, mErr
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if tok := c.AccessToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) ||
			strings.Contains(err.Error(), "connection refused") ||
			strings.Contains(err.Error(), "no such host") ||
			strings.Contains(err.Error(), "EOF") {
			return 0, ErrServerUnreachable
		}
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if out != nil {
		if dErr := json.NewDecoder(resp.Body).Decode(out); dErr != nil {
			if resp.StatusCode >= 400 {
				// Status drove the decode failure — surface that as an httpErr so
				// callers get a clean signal instead of a raw json.SyntaxError.
				return resp.StatusCode, &httpErr{status: resp.StatusCode, body: http.StatusText(resp.StatusCode)}
			}
			return resp.StatusCode, dErr
		}
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, &httpErr{status: resp.StatusCode, body: http.StatusText(resp.StatusCode)}
	}
	logger.Ctx(ctx).Debug("server_svc.serverClient.do: request completed",
		zap.String("method", method), zap.String("path", u.Path),
		zap.Int("statusCode", resp.StatusCode), zap.Duration("duration", time.Since(started)))
	return resp.StatusCode, nil
}
