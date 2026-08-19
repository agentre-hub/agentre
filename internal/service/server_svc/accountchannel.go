package server_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
	"github.com/agentre-ai/agentre/internal/repository/server_state_repo"
)

// 本文件是账号级实时通道在桌面端这一侧的出入口：一条常连的 websocket，服务端在
// 这个账号的同步版本推进时往上面推一个信号，桌面端收到就照常走自己的 Pull。
//
// 它与中继的两条连接（/v1/relay/daemon、/v1/relay/client）**彼此独立**，各管各的：
// 这条不指定目标 daemon，只收信号、不发帧，也不承载任何对象内容。
//
// 谁来消费在 sync_svc：引擎按可选接口 sync_svc.AccountChannelDialer 取用它，
// 取不到（或连不上）就只剩 30 秒轮询——那本身是一个完整可用的形态。

// accountChannelBuffer 是信号流的缓冲。信号只带版本号、彼此可以合并，因此缓冲只需
// 要吸收「引擎正在 Pull 时又来了几条」，不需要为慢消费者做背压。
const accountChannelBuffer = 16

// DialAccountChannel 建立账号级实时通道 GET /v1/account/channel。
//
// 返回的信号流在连接断开或 ctx 结束时**关闭**——调用方据此重连，并在重连成功后
// 主动 Pull 一次：通道不保存未送达的信号，断线期间的变更由那一次 Pull 补齐。
//
// 鉴权与中继拨号同一份凭据（Device JWT 走 Authorization 头）。未登录时一个请求都
// 不发（R12）。
func (s *service) DialAccountChannel(ctx context.Context) (<-chan syncwire.AccountChannelFrame, error) {
	row, err := server_state_repo.ServerState().Get(ctx)
	if err != nil {
		return nil, err
	}
	if row == nil || !row.IsLoggedIn() {
		return nil, ErrNotLoggedIn
	}
	c := s.getClient()
	token := c.AccessToken()
	if token == "" {
		return nil, ErrNotLoggedIn
	}
	endpoint := accountChannelURL(c.baseURL)
	if endpoint == "" {
		return nil, fmt.Errorf("server_svc.DialAccountChannel: unusable server URL %q", c.baseURL)
	}

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, endpoint, headers)
	closeDialResponse(resp)
	if err != nil {
		return nil, err
	}

	signals := make(chan syncwire.AccountChannelFrame, accountChannelBuffer)
	go readAccountChannel(ctx, conn, signals)
	return signals, nil
}

// readAccountChannel 把线上帧解成信号，直到连接断开或 ctx 结束。
func readAccountChannel(ctx context.Context, conn *websocket.Conn, signals chan<- syncwire.AccountChannelFrame) {
	defer close(signals)
	defer func() { _ = conn.Close() }()

	// gorilla 的 ReadMessage 不看 ctx：收工时踢掉连接，读循环随即以错误返回。
	reading := make(chan struct{})
	defer close(reading)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-reading:
		}
	}()

	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var frame syncwire.AccountChannelFrame
		if err := json.Unmarshal(payload, &frame); err != nil || frame.Type == "" {
			// 读不懂的一帧丢掉就是了，不断连：断了要退回 30 秒轮询，代价比丢一帧
			// 大得多，而漏一条信号本来就无害（下一条、或轮询都会补上）。
			continue
		}
		select {
		case signals <- frame:
		case <-ctx.Done():
			return
		}
	}
}

// accountChannelURL 拼出账号级通道的端点。与中继同理，端点**追加**在 baseURL 已有
// 的路径后面——server 部署在反代的路径前缀下是常态。
func accountChannelURL(baseURL string) string {
	return websocketURL(baseURL, "/v1/account/channel", nil)
}

// closeDialResponse 释放握手响应体。gorilla 在成功与失败两种结局下都会把 Body 换成
// 一个空读者，关它不会动到已经建起来的连接，只是把 HTTP 契约写明白。
func closeDialResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}
