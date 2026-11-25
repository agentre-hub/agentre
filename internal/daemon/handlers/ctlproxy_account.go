package handlers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
)

// newCtlAccount 造生产用的账号对端:账号 server 的接口走这台 agentred 的设备 Bearer(CtlServerPort,
// 与 ctl 资源同一份凭据与单飞刷新),relay 同样以设备凭据登录;目标就是本机时走进程内通道。
// 还没登录账号时交回 ErrCtlServerUnavailable。
func newCtlAccount(deps CtlProxyDeps) (CtlAccount, error) {
	r := deps.Relay
	if deps.Server == nil || r.ServerURL == nil || r.Token == nil {
		return nil, ErrCtlServerUnavailable
	}
	base := strings.TrimRight(strings.TrimSpace(r.ServerURL()), "/")
	if base == "" {
		return nil, ErrCtlServerUnavailable
	}
	server := ctlAccountServer{port: deps.Server}
	peer := &ctlSelfAwarePeer{
		relay: relaypeer.New(relaypeer.Options{ServerURL: base, Token: r.Token, Reauthorize: r.Reauthorize}),
		self:  string(deps.Self), dialSelf: r.DialSelf, hosts: r.HostsConversation,
	}
	return ctlaccount.New(ctlaccount.Options{
		Server: server, Peer: peer, Catalog: ctlaccount.NewResourceCatalog(server.postResources),
		ServerURL: base, DeviceName: ctlAccountSource, CredentialRejected: ErrCtlServerUnavailable,
		Warn: func(msg string) {
			logger.Default().Warn("handlers.ctlAccount: partial failure", zap.String("detail", msg))
		},
	}), nil
}

// ctlSelfAwarePeer 把发往这台 agentred 自己的通道留在进程内(不经 relay 自拨),其余照常经 relay。
type ctlSelfAwarePeer struct {
	relay    ctlaccount.Peer
	self     string
	dialSelf func(ctx context.Context) (*relaypeer.Conn, error)
	hosts    func(ctx context.Context, conversationID string) bool
}

func (p *ctlSelfAwarePeer) Open(ctx context.Context, target string) (*relaypeer.Conn, error) {
	if p.dialSelf != nil && p.local(ctx, target) {
		return p.dialSelf(ctx)
	}
	return p.relay.Open(ctx, target)
}

func (p *ctlSelfAwarePeer) local(ctx context.Context, target string) bool {
	if fp, ok := strings.CutPrefix(target, relaypeer.MachineTarget("")); ok {
		return p.self != "" && fp == p.self
	}
	if id, ok := strings.CutPrefix(target, relaypeer.ConversationTarget("")); ok {
		return p.hosts != nil && p.hosts(ctx, id)
	}
	return false
}

func (p *ctlSelfAwarePeer) Close() { p.relay.Close() }

// ctlAccountServer 把 CtlServerPort 包成 ctlaccount.Server:非 2xx 交回 *ctlclient.ServerError。
type ctlAccountServer struct{ port CtlServerPort }

func (s ctlAccountServer) GetRaw(ctx context.Context, pathAndQuery string) ([]byte, error) {
	return serverResult(s.port.Get(ctx, pathAndQuery))
}

func (s ctlAccountServer) PostRaw(ctx context.Context, path string, body []byte) ([]byte, error) {
	return serverResult(s.port.Post(ctx, path, body))
}

func (s ctlAccountServer) postResources(ctx context.Context, body []byte) ([]byte, error) {
	return s.PostRaw(ctx, serverResourcesPath, body)
}

func serverResult(status int, raw []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &ctlclient.ServerError{Status: status, Message: serverMessage(raw)}
	}
	return raw, nil
}

// serverMessage 取 server 拒绝的那句话:ctl 接口是 {"error": …},其余是 cago 外壳 {"msg": …}。
func serverMessage(raw []byte) string {
	var e struct {
		Error string `json:"error"`
		Msg   string `json:"msg"`
	}
	if json.Unmarshal(raw, &e) == nil {
		if e.Error != "" {
			return e.Error
		}
		if e.Msg != "" {
			return e.Msg
		}
	}
	return strings.TrimSpace(string(raw))
}
