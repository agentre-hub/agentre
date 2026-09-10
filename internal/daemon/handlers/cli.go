package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/cliprober"
)

// cliProbeTokenTTL daemon 自签 token 仅供这一次 probe 子进程使用，30s 足矣，
// 加之路径上 defer RevokeToken，过期前也会显式回收。
const cliProbeTokenTTL = 30 * time.Second

// resolveCLIPathFunc / probeFunc 是包级注入点：生产代码走 cliprober 实现，
// 单测通过 SetXxxFunc 替换为 fake，避免在 handler 测试里真的 fork CLI 或扫 PATH。
var (
	resolveCLIPathFunc = cliprober.ResolveCLIPath
	probeFunc          = cliprober.Probe
)

// SetResolveCLIPathFunc 仅供测试替换 PATH 扫描；用 ResetResolveCLIPathFunc 还原。
func SetResolveCLIPathFunc(f func(string) (string, bool, error)) { resolveCLIPathFunc = f }

// ResetResolveCLIPathFunc 还原成 cliprober.ResolveCLIPath。
func ResetResolveCLIPathFunc() { resolveCLIPathFunc = cliprober.ResolveCLIPath }

// SetProbeFunc 仅供测试替换 CLI 子进程 fork；用 ResetProbeFunc 还原。
func SetProbeFunc(f func(context.Context, cliprober.ProbeRequest) (*cliprober.ProbeResponse, error)) {
	probeFunc = f
}

// ResetProbeFunc 还原成 cliprober.Probe。
func ResetProbeFunc() { probeFunc = cliprober.Probe }

// CLIResolvePathParams cli.resolvePath RPC 入参。
type CLIResolvePathParams struct {
	Type string `json:"type"`
}

// CLIResolvePathResult cli.resolvePath RPC 出参。
type CLIResolvePathResult struct {
	Path  string `json:"path,omitempty"`
	Found bool   `json:"found"`
}

// CLIProbeParams cli.probe RPC 入参。daemon 在远端自己装 env / gateway，
// 所以这里只传 backend 快照，不传 token / gatewayURL。
type CLIProbeParams struct {
	BackendType    string `json:"backendType"`
	LLMProviderKey string `json:"llmProviderKey"`
	CLIPath        string `json:"cliPath,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
	Approval       string `json:"approval,omitempty"`
	Model          string `json:"model,omitempty"`
}

// CLIProbeResult cli.probe RPC 出参。
type CLIProbeResult struct {
	Text string `json:"text"`
}

// CLIHandlers groups the cli.* RPC methods.
//
// ResolvePath 纯 PATH 扫描，不查 provider、不调 gateway；
// Probe 当 LLMProviderKey 非空时查 daemon-side provider、用 gateway 签短 token、
// 装 env 后 fork CLI 子进程；LLMProviderKey 为空时跳过 provider / gateway，
// 让 CLI 自身 login 状态生效。
type CLIHandlers struct {
	gw        GatewayPort
	providers LLMProviderLookupPort
}

// NewCLIHandlers 注入 gateway 与 provider lookup 两个端口。
// 二者只在 Probe 路径用到；ResolvePath 走纯 PATH 扫描不依赖它们。
func NewCLIHandlers(gw GatewayPort, providers LLMProviderLookupPort) *CLIHandlers {
	return &CLIHandlers{gw: gw, providers: providers}
}

// ResolvePath 在 daemon 本机 $PATH 中查 CLI binary 绝对路径。
// 错误（如 cliprober.ErrInvalidType）原样返回，由 RPC 框架转成 JSON 错误体。
func (h *CLIHandlers) ResolvePath(_ context.Context, p CLIResolvePathParams) (CLIResolvePathResult, error) {
	path, found, err := resolveCLIPathFunc(p.Type)
	if err != nil {
		return CLIResolvePathResult{}, err
	}
	return CLIResolvePathResult{Path: path, Found: found}, nil
}

// Probe 在 daemon 本机 fork CLI 子进程跑一轮 ping，回末轮 assistant 文本。
// 关联 LLM provider 时走 daemon 自己的 gateway 签短 token；未关联时让 CLI
// 自身 login 状态生效（不签 token、不装 gateway env）。
func (h *CLIHandlers) Probe(ctx context.Context, p CLIProbeParams) (CLIProbeResult, error) {
	// 先做 backend type 合法性判断，免去后续 provider / gateway 调用都白跑。
	bt := strings.TrimSpace(p.BackendType)
	switch bt {
	case string(agent_backend_entity.TypeClaudeCode), string(agent_backend_entity.TypeCodex), string(agent_backend_entity.TypePiAgent):
		// ok
	default:
		return CLIProbeResult{}, fmt.Errorf("invalid backend type: %q", p.BackendType)
	}

	be := &agent_backend_entity.AgentBackend{
		Type:           bt,
		LLMProviderKey: p.LLMProviderKey,
		CLIPath:        p.CLIPath,
		Sandbox:        p.Sandbox,
		Approval:       p.Approval,
		// ModelRoutes / EnvJSON / ReasoningEffort 等 daemon 侧不持有,
		// 留空默认即可,env builder 内部对空值有 fallback。
	}

	deps := agentruntime.CLIDeps{}
	if p.LLMProviderKey != "" {
		prov, err := h.providers.FindByKey(ctx, p.LLMProviderKey)
		if err != nil {
			return CLIProbeResult{}, fmt.Errorf("provider lookup: %w", err)
		}
		if prov == nil {
			return CLIProbeResult{}, errors.New("provider not found")
		}
		gatewayURL := h.gw.URL()
		if gatewayURL == "" {
			return CLIProbeResult{}, errors.New("gateway not running")
		}
		tok, err := h.gw.IssueToken(ctx, be, cliProbeTokenTTL)
		if err != nil {
			return CLIProbeResult{}, fmt.Errorf("gateway issue token: %w", err)
		}
		defer h.gw.RevokeToken(tok)
		deps.Token = tok
		deps.GatewayURL = gatewayURL
	}

	var (
		env          map[string]string
		codexConfigs []string
		envErr       error
	)
	switch bt {
	case string(agent_backend_entity.TypeClaudeCode):
		env, envErr = agentruntime.BuildClaudeCodeEnv(be, deps)
	case string(agent_backend_entity.TypeCodex):
		env, envErr = agentruntime.BuildCodexEnv(be, deps)
		if envErr == nil {
			codexConfigs = agentruntime.BuildCodexConfig(deps)
		}
	case string(agent_backend_entity.TypePiAgent):
		env, envErr = agentruntime.BuildPiAgentEnv(be)
	}
	if envErr != nil {
		return CLIProbeResult{}, fmt.Errorf("env build: %w", envErr)
	}

	resp, err := probeFunc(ctx, cliprober.ProbeRequest{
		Type:         bt,
		CLIPath:      p.CLIPath,
		Sandbox:      p.Sandbox,
		Approval:     p.Approval,
		Model:        p.Model,
		Env:          env,
		CodexConfigs: codexConfigs,
	})
	if err != nil {
		return CLIProbeResult{}, fmt.Errorf("cli probe: %w", err)
	}
	return CLIProbeResult{Text: resp.Text}, nil
}
