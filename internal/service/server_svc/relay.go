package server_svc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
)

// AccessToken returns the current hub access token (empty when not logged in).
// The relay dial uses it to authenticate GET /v1/relay/client (device JWT Bearer).
func (s *service) AccessToken() string {
	return s.getClient().AccessToken()
}

// DialDaemonRelay 经账号中转连接指定指纹的 agentred。它保留既有的
// client.ErrRelayDaemonOffline 语义和 wording；desktop 目标走 DialDesktopRelay。
func (s *service) DialDaemonRelay(ctx context.Context, daemonFingerprint, peerFingerprint string) (client.ProtobufConnection, error) {
	return s.dialRelay(ctx, daemonFingerprint, peerFingerprint)
}

// NewInboundHubLink returns the protocol-agnostic registration transport for
// this desktop. AccessToken is resolved on every reconnect so startup may begin
// before ServerBoot finishes refreshing a persisted login.
func (s *service) NewInboundHubLink(ctx context.Context) (*relaytransport.HubLink, error) {
	row, err := server_state_repo.ServerState().Get(ctx)
	if err != nil {
		return nil, err
	}
	if row == nil || !row.IsLoggedIn() || row.ServerURL == "" {
		return nil, ErrNotLoggedIn
	}
	return relaytransport.NewHubLink(relaytransport.HubLinkOptions{
		ServerURL:           row.ServerURL,
		AccessTokenProvider: s.AccessToken,
	}), nil
}

// DialDesktopRelay connects to a desktop target over the same authenticated
// relay path, but maps its absent App process to the desktop-specific sentinel.
func (s *service) DialDesktopRelay(ctx context.Context, desktopFingerprint, peerFingerprint string) (client.ProtobufConnection, error) {
	connection, err := s.dialRelay(ctx, desktopFingerprint, peerFingerprint)
	if errors.Is(err, client.ErrRelayDaemonOffline) {
		// Do not wrap the agentred sentinel: callers must choose the desktop App
		// recovery action rather than the existing machine-offline wording.
		return nil, fmt.Errorf("%w: %v", ErrDesktopAppNotRunning, err)
	}
	return connection, err
}

func (s *service) dialRelay(ctx context.Context, targetFingerprint, peerFingerprint string) (client.ProtobufConnection, error) {
	row, err := server_state_repo.ServerState().Get(ctx)
	if err != nil {
		return nil, err
	}
	if row == nil || !row.IsLoggedIn() {
		return nil, ErrNotLoggedIn
	}
	if targetFingerprint == "" || peerFingerprint == "" {
		return nil, errors.New("server_svc.dialRelay: empty fingerprint")
	}
	c := s.getClient()
	if c.AccessToken() == "" {
		return nil, ErrNotLoggedIn
	}
	return client.DialRelayProtobuf(ctx, client.RelayOptions{
		URL:               relayClientURL(c.baseURL, targetFingerprint),
		AccessToken:       c.AccessToken(),
		DeviceFingerprint: peerFingerprint,
	})
}

// relayClientURL 把 server 的 HTTP baseURL 换成 ws(s):// 并拼上客户端接入端点
// /v1/relay/client?daemon_fingerprint=<fp>。
func relayClientURL(baseURL, daemonFingerprint string) string {
	return websocketURL(baseURL, "/v1/relay/client", url.Values{
		"daemon_fingerprint": {daemonFingerprint},
	})
}

// websocketURL 把 server 的 HTTP baseURL 换成 ws(s):// 并拼上一个 websocket 端点。
// server 上的每一条 websocket 都从这里拼(中继两条 + 账号级实时通道),拼法只此一处。
//
// 端点**追加**在 baseURL 已有的路径后面,不覆盖它:server 部署在反代的路径前缀下
// (https://host/agentre)是常态,同一个 baseURL 上的 HTTP 调用走 serverClient.do 的
// baseURL+path,daemon 侧的 rpc.hubEndpoint 也是保留前缀后拼 /v1/relay/daemon。
// 这里若写死成绝对路径,前缀就被丢掉,请求打到反代根下不存在的路径,而 404 会被
// classifyRelayDialError 归成 ErrRelayDaemonNotFound —— 用户被告知「先认领这台机器」,
// 可机器一直是认领着的。
func websocketURL(baseURL, endpoint string, query url.Values) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return ""
	}
	q := u.Query()
	for key := range query {
		q.Set(key, query.Get(key))
	}
	u.Path = strings.TrimRight(u.Path, "/") + endpoint
	u.RawPath = ""
	u.RawQuery = q.Encode()
	return u.String()
}
