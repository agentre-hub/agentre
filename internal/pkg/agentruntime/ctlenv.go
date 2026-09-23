package agentruntime

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

func (c CtlCredentials) complete() bool { return c.Endpoint != "" && c.Token != "" }

// CtlCredentialSource 为 (agent, session) 签发会话凭证；签不了时返回零值。
type CtlCredentialSource func(agentID, sessionID int64) CtlCredentials

// ctlCredentialSource 是进程级注入点（与 RegisterSessionCursor 同一种接线）：桌面端由
// ctl_svc 注册；没注册的进程（例如尚无 ctl 代理的 agentred）不注入。
var ctlCredentialSource CtlCredentialSource

// RegisterCtlCredentialSource 注入会话凭证来源；传 nil 清空(测试用)。
func RegisterCtlCredentialSource(f CtlCredentialSource) { ctlCredentialSource = f }

// CtlCredentials 返回本轮 CLI 子进程该带的会话凭证。没有 agent 或会话身份、或进程
// 没注册来源时为零值。
func (r RunRequest) CtlCredentials() CtlCredentials {
	if ctlCredentialSource == nil || r.AgentID <= 0 || r.SessionID <= 0 {
		return CtlCredentials{}
	}
	return ctlCredentialSource(r.AgentID, r.SessionID)
}

// applyCtlEnv 把会话凭证写进 env。在用户 env_json 之后调用：会话身份不能被后端配置盖掉。
func applyCtlEnv(env map[string]string, c CtlCredentials) {
	if !c.complete() {
		return
	}
	env[CtlEndpointEnv] = c.Endpoint
	env[CtlTokenEnv] = c.Token
}
