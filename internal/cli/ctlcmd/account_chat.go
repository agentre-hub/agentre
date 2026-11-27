package ctlcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 账号模式的会话命令（send / wait / stop / list / get / messages / tail / pending / answer）：
// agrctl 自己是一个账号对端，经账号 server 的 relay 直连承载会话的那台机器（agentred 或桌面）。
// 派发与查询的逻辑在 ctlaccount（agentred 的 ctl 代理为控制台拥有的会话用的是同一份）；
// 这里只接上 agrctl 的凭据、目录与 stderr。flag、输出与退出码与桌面模式相同。

// accountPollInterval 是账号模式 wait / tail 复查会话状态的间隔。状态一律以执行端的会话行
// 为准，断线重连后再查一次既不重复也不遗漏。
var accountPollInterval = time.Second

// newAccountChat 为一次调用造账号执行者：一个 Ctl* 目录（名字解析）、一条 relay 链路。
func newAccountChat(s *sys, x *executor, args []string) *ctlaccount.Client {
	tokens := &tokenDirectory{acct: x.acct}
	rejected := ctlclient.ErrLoginExpired
	if x.acct.PersonalToken() {
		rejected = ctlclient.ErrTokenRejected
	}
	return ctlaccount.New(ctlaccount.Options{
		Server: x.acct,
		Peer: relaypeer.New(relaypeer.Options{
			ServerURL: x.acct.Server, Token: x.acct.Bearer, Reauthorize: x.acct.Reauthorize,
		}),
		Catalog:            accountCatalog{cat: newCatalog(x, args)},
		ServerURL:          x.acct.Server,
		DeviceName:         x.acct.DeviceName(),
		CredentialRejected: rejected,
		Warn:               func(msg string) { _, _ = fmt.Fprintf(s.stderr, "warning: %s\n", msg) },
		SelfFingerprint:    tokens.selfFingerprint,
		TokenName:          tokens.tokenNameOf,
		ConsolePeer:        tokens.consolePeer,
		PollInterval:       accountPollInterval,
		ReconnectDelay:     reconnectDelay,
	})
}

// tokenDirectory 是账号模式认发起方要用的 token 读路（个人 token 的 /self、登录态的 token
// 列表）。一次 agrctl 调用里答案不变，成功读到的那份就留着：get session 认一次发起方要问
// 「我是谁」「控制台是谁」「这个 token 叫什么」，三问共用一次读取。读失败不留，下一问再试。
type tokenDirectory struct {
	acct *ctlclient.Account

	mu   sync.Mutex
	self *selfTokenInfo
	list *tokenListInfo
}

// selfFingerprint 是调用方发起的会话记在谁名下：登录态是这台 cli 设备的指纹，个人 token 问
// server 自己的指纹。
func (d *tokenDirectory) selfFingerprint(ctx context.Context) (string, error) {
	if !d.acct.PersonalToken() {
		return d.acct.Fingerprint(), nil
	}
	self, err := d.selfToken(ctx)
	return self.PeerFingerprint, err
}

// tokenNameOf 是对端指纹 peer 属于哪枚个人 token：登录态读账号的 token 列表，个人 token 只看得到自己。
func (d *tokenDirectory) tokenNameOf(ctx context.Context, peer string) (string, error) {
	if d.acct.PersonalToken() {
		self, err := d.selfToken(ctx)
		if err != nil || self.PeerFingerprint != peer {
			return "", err
		}
		return self.Item.Name, nil
	}
	list, err := d.tokenList(ctx)
	if err != nil {
		return "", err
	}
	for _, t := range list.Tokens {
		if t.PeerFingerprint == peer {
			return t.Name, nil
		}
	}
	return "", nil
}

// consolePeer 是本账号控制台的对端指纹（server 按账号派生；token 列表与 self 都带）。
func (d *tokenDirectory) consolePeer(ctx context.Context) (string, error) {
	if d.acct.PersonalToken() {
		self, err := d.selfToken(ctx)
		return self.ConsolePeerFingerprint, err
	}
	list, err := d.tokenList(ctx)
	return list.ConsolePeerFingerprint, err
}

type tokenListInfo struct {
	Tokens []struct {
		Name            string `json:"name"`
		PeerFingerprint string `json:"peer_fingerprint"`
	} `json:"tokens"`
	ConsolePeerFingerprint string `json:"console_peer_fingerprint"`
}

func (d *tokenDirectory) tokenList(ctx context.Context) (tokenListInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.list != nil {
		return *d.list, nil
	}
	var out tokenListInfo
	raw, err := d.acct.GetRaw(ctx, "/v1/personal-tokens")
	if err != nil {
		return out, serverErr(err)
	}
	if err := cagoenvelope.Decode(raw, &out); err != nil {
		return out, fmt.Errorf("decode the token list: %w", err)
	}
	d.list = &out
	return out, nil
}

type selfTokenInfo struct {
	Item struct {
		Name string `json:"name"`
	} `json:"item"`
	PeerFingerprint        string `json:"peer_fingerprint"`
	ConsolePeerFingerprint string `json:"console_peer_fingerprint"`
}

func (d *tokenDirectory) selfToken(ctx context.Context) (selfTokenInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.self != nil {
		return *d.self, nil
	}
	var out selfTokenInfo
	raw, err := d.acct.GetRaw(ctx, "/v1/personal-tokens/self")
	if err != nil {
		return out, serverErr(err)
	}
	if err := cagoenvelope.Decode(raw, &out); err != nil {
		return out, fmt.Errorf("decode the personal token: %w", err)
	}
	d.self = &out
	return out, nil
}

// accountCatalog 把 agrctl 的目录交给 ctlaccount：定位走 agrctl 自己的 locate（歧义时给出可
// 照抄的示例命令），歧义标成 ctlaccount.AmbiguousError。
type accountCatalog struct{ cat *catalog }

func (c accountCatalog) List(kind agentrewire.CtlKind) ([]*agentrewire.CtlResource, error) {
	return c.cat.list(kind)
}

func (c accountCatalog) ByID(kind agentrewire.CtlKind, id int64) *agentrewire.CtlResource {
	return c.cat.byID(kind, id)
}

func (c accountCatalog) Path(kind agentrewire.CtlKind, id int64) string { return c.cat.path(kind, id) }

func (c accountCatalog) Locate(kind agentrewire.CtlKind, ref string) (*agentrewire.CtlResource, error) {
	spec := kindByProto(kind)
	if spec == nil {
		return nil, fmt.Errorf("unsupported resource kind %s", kind)
	}
	it, err := c.cat.locate(spec, ref)
	var ce *cliError
	if errors.As(err, &ce) && strings.Contains(ce.msg, "ambiguous") {
		return nil, &ctlaccount.AmbiguousError{Err: err}
	}
	return it, err
}

// serverErr 把账号 server 的拒绝原样透出。
var serverErr = ctlaccount.ServerErr

// kindByProto 是 Ctl* 资源类型对应的 agrctl 资源说明。
func kindByProto(kind agentrewire.CtlKind) *kindSpec {
	for _, k := range allKinds {
		if k.kind == kind {
			return k
		}
	}
	return nil
}

// accountErr 把 ctlaccount 的用法错误与「等待输入」换算成 agrctl 的退出码。
func accountErr(err error) error {
	var ue *ctlaccount.UsageError
	var we *ctlaccount.WaitingError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ue):
		return &cliError{code: exitUsage, msg: ue.Msg}
	case errors.As(err, &we):
		return &cliError{code: exitWaiting, msg: we.Error()}
	}
	return err
}
