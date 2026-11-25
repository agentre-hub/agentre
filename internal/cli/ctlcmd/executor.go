package ctlcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/agrctlcred"
	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

// accountResourcesPath 是账号 server 上的资源接口，正文与桌面的 resourcesPath 同一份 Ctl* 契约。
const accountResourcesPath = "/v1/ctl/resources"

// executorKind 是一次调用连到的执行者（spec「执行者与会话引用」）。
type executorKind int

const (
	execSession executorKind = iota // Agentre 注入的会话 token
	execDesktop                     // 本机桌面（握手文件）
	execAccount                     // 账号 server：个人 token 或本机登录态
)

// executor 是选中的执行者。会话与桌面走 ep，账号走 acct。
type executor struct {
	kind executorKind
	ep   ctlclient.Endpoint
	acct *ctlclient.Account
}

func endpointExecutor(ep ctlclient.Endpoint) *executor {
	if ep.TokenFromEnv {
		return &executor{kind: execSession, ep: ep}
	}
	return &executor{kind: execDesktop, ep: ep}
}

// selectExecutor 按 spec 的顺序选执行者：
//  1. 会话注入的 AGENTRE_CTL_ENDPOINT / AGENTRE_CTL_TOKEN（或显式 --endpoint）；
//  2. 显式 --desktop：本机桌面，桌面没在运行就报错，不回落；
//  3. 个人 token（AGENTRE_TOKEN，或会话命令上不带 --endpoint 的 --token），或本机已登录：账号；
//  4. 否则本机桌面，与此前相同。
//
// 已作废的登录（server 证实吊销 / 过期）仍选中账号：命令据此提示重新登录，不悄悄换成本机桌面。
func selectExecutor(s *sys, flagEndpoint, flagToken string) (*executor, error) {
	if flagEndpoint != "" || envSet(s, "AGENTRE_CTL_ENDPOINT") || envSet(s, "AGENTRE_CTL_TOKEN") {
		token := flagToken
		if flagEndpoint == "" {
			token = "" // 没有 --endpoint 时 --token 是个人 token，不是会话端点的凭据
		}
		ep, err := ctlclient.Resolve(flagEndpoint, token, s.lookupEnv)
		if err != nil {
			return nil, err
		}
		return endpointExecutor(ep), nil
	}
	if s.desktop {
		ep, err := ctlclient.ResolveDesktop()
		if err != nil {
			return nil, err
		}
		return &executor{kind: execDesktop, ep: ep}, nil
	}
	token := flagToken
	if token == "" {
		token, _ = s.lookupEnv("AGENTRE_TOKEN")
	}
	if token = strings.TrimSpace(token); token != "" {
		return &executor{kind: execAccount, acct: ctlclient.PersonalTokenAccount(personalTokenServer(s), token)}, nil
	}
	if dir, err := paths.AppDataDir(); err == nil {
		acct, lerr := ctlclient.LoginAccount(dir)
		switch {
		case lerr == nil:
			return &executor{kind: execAccount, acct: acct}, nil
		case !errors.Is(lerr, os.ErrNotExist):
			return nil, lerr
		}
	}
	ep, err := ctlclient.Resolve("", "", s.lookupEnv)
	if err != nil {
		return nil, &defaultDesktopUnavailable{err: err}
	}
	return endpointExecutor(ep), nil
}

// defaultDesktopUnavailable 是第 4 步（默认的本机桌面）解析不到端点：执行者已经确定是桌面，
// 只是它现在不在。whoami 据此照样报出执行者；其余命令把原因原样报给用户。
type defaultDesktopUnavailable struct{ err error }

func (e *defaultDesktopUnavailable) Error() string { return e.err.Error() }
func (e *defaultDesktopUnavailable) Unwrap() error { return e.err }

func envSet(s *sys, key string) bool {
	v, ok := s.lookupEnv(key)
	return ok && v != ""
}

// personalTokenServer 是个人 token 连的 server：AGENTRE_SERVER_URL > 本机登录的 server > 默认。
func personalTokenServer(s *sys) string {
	if v, ok := s.lookupEnv("AGENTRE_SERVER_URL"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimRight(strings.TrimSpace(v), "/")
	}
	if dir, err := paths.AppDataDir(); err == nil {
		if c, err := agrctlcred.Load(dir); err == nil && c.ServerURL != "" {
			return c.ServerURL
		}
	}
	return ctlclient.DefaultServerURL
}

// postResources 把一次 Ctl* 资源请求交给执行者。
func (x *executor) postResources(ctx context.Context, body []byte) ([]byte, error) {
	if x.kind == execAccount {
		return x.acct.PostRaw(ctx, accountResourcesPath, body)
	}
	return x.ep.PostRaw(ctx, resourcesPath, body)
}

// isHuman 是 agrctl 现有的「人在终端」判定：stdin 是 TTY，且环境里没有已知 agent CLI 的标记。
func isHuman(s *sys) bool {
	return s.stdinIsTTY && !hasAgentEnvMarker(s.lookupEnv)
}

// refuseExternal 是账号登录态的客户端护栏（spec 决策 17）：登录态 + 外部程序不能发起 / 继续 /
// 停止对话、回答待处理项或写资源，要用个人 token。它防误用，不是安全边界——安全边界是个人
// token 的范围，由 server / agentred 检查。
func (x *executor) refuseExternal(s *sys, what string) error {
	if x.kind != execAccount || x.acct.PersonalToken() || isHuman(s) {
		return nil
	}
	return fmt.Errorf("%s with agrctl's logged-in account needs a person at a terminal; "+
		"for a program or an agent CLI, issue a personal token and pass it in AGENTRE_TOKEN", what)
}

// errAccountConversations：这几个会话命令（tail / messages / pending / answer / 会话查询）的账号
// 执行者还没有接上；send / wait / stop 已经经 relay 走到承载会话的那台机器。
var errAccountConversations = errors.New(
	"this session command is not available against the account yet; use --desktop for this desktop's sessions")

// conversationExecutor 为会话命令选执行者。chat 为 true 的命令（send / stop / answer）在账号
// 登录态下先过外部程序护栏。
func conversationExecutor(s *sys, p parsedArgs, chat bool, what string) (*executor, error) {
	x, err := selectExecutor(s, last(p, "endpoint"), last(p, "token"))
	if err != nil {
		return nil, err
	}
	if chat {
		if err := x.refuseExternal(s, what); err != nil {
			return nil, err
		}
	}
	return x, nil
}

// conversationEndpoint 是还只接了会话 / 桌面执行者的会话命令用的入口。
func conversationEndpoint(s *sys, p parsedArgs, chat bool, what string) (ctlclient.Endpoint, error) {
	x, err := conversationExecutor(s, p, chat, what)
	if err != nil {
		return ctlclient.Endpoint{}, err
	}
	if x.kind == execAccount {
		return ctlclient.Endpoint{}, errAccountConversations
	}
	return x.ep, nil
}

// extractDesktopFlag 取出全局 flag --desktop（`--` 之后的不算），交回其余参数。
func extractDesktopFlag(args []string) ([]string, bool, error) {
	out := make([]string, 0, len(args))
	desktop := false
	for i, a := range args {
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		switch a {
		case "--desktop", "--desktop=true":
			desktop = true
		case "--desktop=false":
		default:
			if strings.HasPrefix(a, "--desktop=") {
				return nil, false, usageErrorf("flag --desktop takes no value (or =true/=false)")
			}
			out = append(out, a)
		}
	}
	return out, desktop, nil
}
