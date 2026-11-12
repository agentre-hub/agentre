package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/agentre-hub/agentre/internal/pkg/clienv"
	"github.com/agentre-hub/agentre/internal/pkg/procscan"
)

// agrctl 会话级凭证（spec「Routing and approval」会话级 token）：CLI 类 agent 子进程
// 启动时带上执行者的控制端点和绑定 (agent, session) 的 token，子进程里跑的 agrctl
// 据此以「会话调用」身份访问执行者。

const (
	// CtlEndpointEnv 是执行者控制 API 的 base URL（agrctl 的 ctlclient.Resolve 读它）。
	CtlEndpointEnv = "AGENTRE_CTL_ENDPOINT"
	// CtlTokenEnv 是会话级 token（ctlclient 据此判定调用方是会话）。
	CtlTokenEnv = "AGENTRE_CTL_TOKEN" // #nosec G101 -- env var name, not a credential value.
)

// CtlCredentials 是注入 CLI 子进程的 agrctl 会话凭证；任一字段为空 = 不注入。
type CtlCredentials struct {
	Endpoint string
	Token    string
}

// Injectable 报告这组凭证会不会真被注入子进程：applyCtlEnv 与 codex 打开沙箱网络（决策 21）
// 共用这一个判据。
func (c CtlCredentials) Injectable() bool { return c.Endpoint != "" && c.Token != "" }

// CtlCredentialSource 为 (agent, session) 签发会话凭证；签不了时返回零值。
type CtlCredentialSource func(agentID, sessionID int64) CtlCredentials

// ctlCredentialSource 是进程级注入点（与 RegisterSessionCursor 同一种接线）：桌面端由
// ctl_svc 注册；没注册的进程（例如尚无 ctl 代理的 agentred）不注入。
var ctlCredentialSource CtlCredentialSource

// RegisterCtlCredentialSource 注入会话凭证来源；传 nil 清空(测试用)。
func RegisterCtlCredentialSource(f CtlCredentialSource) { ctlCredentialSource = f }

// CtlCredentials 返回本轮 CLI 子进程该带的会话凭证。没有会话身份、或进程没注册来源时
// 为零值。不看 AgentID：跨主机派发带 agent sync id 时本地 agent id 恒为 0（浏览器派发
// 同样），会话凭证只绑会话。
func (r RunRequest) CtlCredentials() CtlCredentials {
	if ctlCredentialSource == nil || r.SessionID <= 0 {
		return CtlCredentials{}
	}
	return ctlCredentialSource(r.AgentID, r.SessionID)
}

// agrctlPath 是本进程已装 agrctl 的绝对路径；空 = 没装/没登记。
var agrctlPath atomic.Value // string

// RegisterAgrctlPath 登记已装 agrctl 的路径（桌面端 bootstrap、agentred 装好后调用），
// 供注入会话凭证时把它所在目录排到子进程 PATH 最前，让会话里的裸 `agrctl` 能跑。
// 传空串清空(测试用)。相对路径（dev 覆盖 AGENTRE_AGRCTL_PATH=build/bin/agrctl）按本进程
// cwd 定成绝对路径：原样进 PATH 会按会话 cwd（项目工作区）解析，让工作区里的同名程序顶替
// 系统命令；定不下来就不登记。
func RegisterAgrctlPath(path string) {
	if path != "" && !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = ""
		}
		path = abs
	}
	agrctlPath.Store(path)
}

func registeredAgrctlPath() string {
	p, _ := agrctlPath.Load().(string)
	return p
}

// applyCtlEnv 把会话凭证写进 env。在用户 env_json 之后调用：会话身份不能被后端配置盖掉。
func applyCtlEnv(env map[string]string, c CtlCredentials) {
	if !c.Injectable() {
		return
	}
	env[CtlEndpointEnv] = c.Endpoint
	env[CtlTokenEnv] = c.Token
	prependAgrctlDir(env)
}

// prependAgrctlDir 把已登记 agrctl 所在目录排到 env["PATH"] 最前。基底取 env_json 写的
// PATH，没写则取进程 PATH（clienv.BuildEnv 里 extra 覆盖继承的环境，这里写下的就是子进程
// 看到的 PATH）。已在最前不重复；没登记则不动。
func prependAgrctlDir(env map[string]string) {
	path := registeredAgrctlPath()
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	base, ok := clienv.PathValue(env) // Windows 上 env_json 写的 Path 也算
	if !ok {
		base = os.Getenv("PATH")
	}
	sep := string(os.PathListSeparator)
	if base == dir || strings.HasPrefix(base, dir+sep) {
		env["PATH"] = base
		return
	}
	if base == "" {
		env["PATH"] = dir
		return
	}
	env["PATH"] = dir + sep + base
}

// applySessionEnv 把会话标记写进 env。与 applyCtlEnv 一样在用户 env_json 之后调用：
// 标记是宿主认领进程的依据，不能被后端配置盖掉。空 conversation_id 不写。
func applySessionEnv(env map[string]string, conversationID string) {
	if conversationID == "" {
		return
	}
	env[procscan.MarkerEnv] = conversationID
}
