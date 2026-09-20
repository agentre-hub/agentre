package app

import (
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
)

// ListAgentBackends 列出全部 Agent 后端（已 join LLM 供应商摘要）。
func (a *App) ListAgentBackends() (*agent_backend_svc.ListBackendsResponse, error) {
	return agent_backend_svc.AgentBackend().List(a.ctx, &agent_backend_svc.ListBackendsRequest{})
}

// CreateAgentBackend 新建 Agent 后端。不同 Type 的字段约束由 service/entity 校验。
func (a *App) CreateAgentBackend(req *agent_backend_svc.CreateBackendRequest) (*agent_backend_svc.CreateBackendResponse, error) {
	return agent_backend_svc.AgentBackend().Create(a.ctx, req)
}

// CreateOpenClawAgentBackend accepts a transient token as a separate binding
// argument. It is never represented in a Wails request/response model.
func (a *App) CreateOpenClawAgentBackend(req *agent_backend_svc.CreateBackendRequest, token string) (*agent_backend_svc.CreateBackendResponse, error) {
	return agent_backend_svc.AgentBackend().CreateOpenClaw(a.ctx, req, token)
}

// UpdateAgentBackend 更新 Agent 后端。Type 不可变。
func (a *App) UpdateAgentBackend(req *agent_backend_svc.UpdateBackendRequest) (*agent_backend_svc.UpdateBackendResponse, error) {
	return agent_backend_svc.AgentBackend().Update(a.ctx, req)
}

func (a *App) UpdateOpenClawAgentBackend(req *agent_backend_svc.UpdateBackendRequest, token string, clearToken bool) (*agent_backend_svc.UpdateBackendResponse, error) {
	return agent_backend_svc.AgentBackend().UpdateOpenClaw(a.ctx, req, token, clearToken)
}

// DeleteAgentBackend 软删除 Agent 后端。
func (a *App) DeleteAgentBackend(req *agent_backend_svc.DeleteBackendRequest) (*agent_backend_svc.DeleteBackendResponse, error) {
	return agent_backend_svc.AgentBackend().Delete(a.ctx, req)
}

// TestAgentBackend 跑一次连通性自检。OK=false 时 Message 含错误文案,不通过 error 返回。
func (a *App) TestAgentBackend(req *agent_backend_svc.TestBackendRequest) (*agent_backend_svc.TestBackendResponse, error) {
	return agent_backend_svc.AgentBackend().Test(a.ctx, req)
}

func (a *App) TestOpenClawAgentBackend(req *agent_backend_svc.TestBackendRequest, token string) (*agent_backend_svc.TestBackendResponse, error) {
	return agent_backend_svc.AgentBackend().TestOpenClaw(a.ctx, req, token)
}

// CancelTestAgentBackend 中断一个还在跑的 TestAgentBackend。
// 前端在用户点取消时调用，传入与 TestAgentBackend 时一致的 RequestID。
func (a *App) CancelTestAgentBackend(req *agent_backend_svc.CancelTestBackendRequest) (*agent_backend_svc.CancelTestBackendResponse, error) {
	return agent_backend_svc.AgentBackend().CancelTest(a.ctx, req)
}

// ListAgentBackendCLIOverlays lists overlay statuses only; absolute paths remain
// behind the local get/set methods below.
func (a *App) ListAgentBackendCLIOverlays() (*agent_backend_svc.ListCLIOverlaysResponse, error) {
	return agent_backend_svc.AgentBackend().ListCLIOverlays(a.ctx, &agent_backend_svc.ListCLIOverlaysRequest{})
}

func (a *App) GetAgentBackendCLIOverlay(req *agent_backend_svc.GetCLIOverlayRequest) (*agent_backend_svc.GetCLIOverlayResponse, error) {
	return agent_backend_svc.AgentBackend().GetCLIOverlay(a.ctx, req)
}

func (a *App) SetAgentBackendCLIOverlay(req *agent_backend_svc.SetCLIOverlayRequest) (*agent_backend_svc.SetCLIOverlayResponse, error) {
	return agent_backend_svc.AgentBackend().SetCLIOverlay(a.ctx, req)
}

// ResolveAgentBackendCLIPath 用 $PATH 查找 claudecode / codex 后端的可执行文件路径，
// 让前端 BackendEditor 在用户切换类型时自动填入识别到的绝对路径。
func (a *App) ResolveAgentBackendCLIPath(req *agent_backend_svc.ResolveCLIPathRequest) (*agent_backend_svc.ResolveCLIPathResponse, error) {
	return agent_backend_svc.AgentBackend().ResolveCLIPath(a.ctx, req)
}

// ScanAndCreateAgentBackends 扫描系统 PATH 中的 Claude Code / Codex / Pi Agent CLI，
// 并为命中的 binary 自动创建对应的 Agent 后端配置。
func (a *App) ScanAndCreateAgentBackends() (*agent_backend_svc.ScanAndCreateAgentBackendsResponse, error) {
	return agent_backend_svc.AgentBackend().ScanAndCreateAgentBackends(a.ctx, &agent_backend_svc.ScanAndCreateAgentBackendsRequest{})
}

// ListHermesAuthProviders 读取一个 hermes serve 支持的认证 provider（供弹窗下拉，不硬编码）。
func (a *App) ListHermesAuthProviders(req *agent_backend_svc.ListHermesAuthProvidersRequest) (*agent_backend_svc.ListHermesAuthProvidersResponse, error) {
	return agent_backend_svc.AgentBackend().ListHermesAuthProviders(a.ctx, req)
}

// LoginHermesBackend 跑原生 PKCE 登录：只把 refresh token 写入 keychain，绝不存密码。
// 失败带业务码（codedError），前端据此区分「密码错 / 限速 / 需要浏览器」等出路。
func (a *App) LoginHermesBackend(req *agent_backend_svc.LoginHermesRequest) (*agent_backend_svc.LoginHermesResponse, error) {
	resp, err := agent_backend_svc.AgentBackend().LoginHermes(a.ctx, req)
	if err != nil {
		return nil, codedError(err)
	}
	return resp, nil
}

// GetBackendCredentialStatus 问后端绑定的那台设备:凭据存了没、登录成了谁。
// 只回状态,凭据本身永远不出那台设备。设备离线 / 未配对时返回带业务码的错误,
// 编辑器据此显示「登录状态未知」。
func (a *App) GetBackendCredentialStatus(req *agent_backend_svc.BackendCredentialStatusRequest) (*agent_backend_svc.BackendCredentialStatusResponse, error) {
	resp, err := agent_backend_svc.AgentBackend().BackendCredentialStatus(a.ctx, req)
	if err != nil {
		return nil, codedError(err)
	}
	return resp, nil
}

// LogoutHermesBackend 删除凭据并清掉后端上两个展示字段。
func (a *App) LogoutHermesBackend(req *agent_backend_svc.LogoutHermesRequest) (*agent_backend_svc.LogoutHermesResponse, error) {
	resp, err := agent_backend_svc.AgentBackend().LogoutHermes(a.ctx, req)
	if err != nil {
		return nil, codedError(err)
	}
	return resp, nil
}
