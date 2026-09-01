package server_svc

import (
	"context"
	"errors"
	"fmt"

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

// dialRelay 在这台桌面端唯一的常驻中继客户端连接（ensureRelay，决策 13）上开一条
// 新虚拟通道,目标声明为 machine:<targetFingerprint>（决策 11:从机器轴点进去的
// 走机器寻址,而 DialDaemonRelay/DialDesktopRelay 正是机器轴——ConnPool 按
// deviceID 借连接,不知道对话)。
//
// URL 上不再出现 daemon_fingerprint(决策 10):目标从连接级降到了通道级。
// peerFingerprint 从决策 8 起就不出现在线上任何地方(auth.account 的对端身份由
// 服务端从已验签的凭据里取,不是客户端自报的)——这里仍然校验它非空,只是延续
// dialRelay 原有的「两个参数都不许空」契约,不是把它发出去。
func (s *service) dialRelay(ctx context.Context, targetFingerprint, peerFingerprint string) (client.ProtobufConnection, error) {
	if targetFingerprint == "" || peerFingerprint == "" {
		return nil, errors.New("server_svc.dialRelay: empty fingerprint")
	}
	relay, err := s.ensureRelay(ctx)
	if err != nil {
		return nil, err
	}
	c := s.getClient()
	if c == nil || c.AccessToken() == "" {
		return nil, ErrNotLoggedIn
	}
	return relay.openTarget(ctx, targetPrefixMachine+targetFingerprint, c.AccessToken())
}

// targetPrefixMachine 与 agentre-server relay_svc.TargetPrefixMachine
// 逐字同值（决策 11）。两个仓库各自维护一份常量的理由同 SignalChannelID 的注释:
// Go 后端之间不允许反向 import。
const targetPrefixMachine = "machine:"
