package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

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
//
// Close 与 relay 那一半同一份承诺:关掉它开过的**每一条**通道。relay 通道随链路一起关,
// 进程内通道没有链路可关,只能由这里记着逐条关 —— 它在这台 agentred 这一侧是一条完整的
// 连接(按连接的 runtime 会话表、会话认领、接管后的推送订阅),ctlaccount 的 stop / 插话
// 等路径不自己关它,漏关就留在进程里直到 agentred 退出。
type ctlSelfAwarePeer struct {
	relay    ctlaccount.Peer
	self     string
	dialSelf func(ctx context.Context) (*relaypeer.Conn, error)
	hosts    func(ctx context.Context, conversationID string) bool

	mu     sync.Mutex
	closed bool
	opened []*relaypeer.Conn
}

func (p *ctlSelfAwarePeer) Open(ctx context.Context, target string) (*relaypeer.Conn, error) {
	if p.dialSelf != nil && p.local(ctx, target) {
		return p.openSelf(ctx)
	}
	return p.relay.Open(ctx, target)
}

func (p *ctlSelfAwarePeer) openSelf(ctx context.Context) (*relaypeer.Conn, error) {
	conn, err := p.dialSelf(ctx)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: closed", relaypeer.ErrRelayUnavailable)
	}
	// 已经结束的那些不再记着:一个跟到子会话收口的卡片会反复重连,表不该跟着长。
	live := p.opened[:0]
	for _, c := range p.opened {
		select {
		case <-c.Done():
		default:
			live = append(live, c)
		}
	}
	p.opened = append(live, conn)
	return conn, nil
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

func (p *ctlSelfAwarePeer) Close() {
	p.mu.Lock()
	p.closed = true
	opened := p.opened
	p.opened = nil
	p.mu.Unlock()
	for _, c := range opened {
		_ = c.Close()
	}
	p.relay.Close()
}

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
