package ctlcmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

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
		PollInterval:       accountPollInterval,
		ReconnectDelay:     reconnectDelay,
	})
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
