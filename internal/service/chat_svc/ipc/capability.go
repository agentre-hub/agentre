// Package ipc 收纳 chat_svc 暴露给 Wails 前端的 IPC handler。当前住在这里的
// 是 GetSessionCapabilities + GetBackendCapabilities;其它 IPC
// (AnswerUserQuestion / AnswerToolPermission / SendChat 等)仍住在 chat_svc 顶层。
package ipc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"

	// 显式 blank import 触发 NEW runtime 子包 init() 注册到 RuntimeFor。
	// 不直接调子包的 Runtime 类型,只通过 agentruntime.RuntimeFor 反查。
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/builtin"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/claudecode"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/codex"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/openclaw"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// GetSessionCapabilitiesRequest 前端按 sessionID 询问当前会话的 backend 能力矩阵。
type GetSessionCapabilitiesRequest struct {
	SessionID int64 `json:"sessionId"`
}

// GetSessionCapabilitiesResponse 返回 wire 形态的 caps:
//   - capabilities 列字符串(前端按需查 set membership)
//   - permissionModeMeta 给前端 PermissionModePill / capability gating 用
type GetSessionCapabilitiesResponse struct {
	Capabilities       []string                       `json:"capabilities"`
	PermissionModeMeta GetSessionCapabilitiesModeMeta `json:"permissionModeMeta"`
}

type GetSessionCapabilitiesModeMeta struct {
	AllowedModes         []string `json:"allowedModes,omitempty"`
	DefaultMode          string   `json:"defaultMode,omitempty"`
	SwitchableDuringTurn bool     `json:"switchableDuringTurn"`
	Order                []string `json:"order,omitempty"`
}

// GetSessionCapabilities 读 session backend 对应 runtime 的 Capabilities,
// 返给前端做 UI gating。本函数从 agentruntime.RuntimeFor 注册表反查;NEW runtime
// 子包 init() 已自注册,顶部 blank import 保证依赖图触达。
//
// remote backend 暂未接 Capabilities IPC(remote runner 持 DaemonClientPort,
// 构造代价高且 caps 由远端决定),返空 caps;后续加远端 caps 透传。
func GetSessionCapabilities(ctx context.Context, req *GetSessionCapabilitiesRequest) (*GetSessionCapabilitiesResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	sess, err := capabilitySessions.Find(ctx, req.SessionID)
	if err != nil || sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	a, err := capabilityAgents.Find(ctx, sess.AgentID)
	if err != nil || a == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	if a.AgentBackendID <= 0 {
		return nil, i18n.NewError(ctx, code.ChatAgentNoBackend)
	}
	be, err := capabilityBackends.Find(ctx, a.AgentBackendID)
	if err != nil || be == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
	}

	caps := capabilitiesFor(agent_backend_entity.BackendType(be.Type))

	return &GetSessionCapabilitiesResponse{
		Capabilities: capListToStrings(caps),
		PermissionModeMeta: GetSessionCapabilitiesModeMeta{
			AllowedModes:         caps.PermissionModeMeta.AllowedModes,
			DefaultMode:          caps.PermissionModeMeta.DefaultMode,
			SwitchableDuringTurn: caps.PermissionModeMeta.SwitchableDuringTurn,
			Order:                caps.PermissionModeMeta.Order,
		},
	}, nil
}

// GetBackendCapabilitiesRequest 前端在「新对话还没建 session」阶段按 backend type
// 询问能力矩阵 — 用来决定是否渲染 PermissionModePill / 起手 mode 等。
type GetBackendCapabilitiesRequest struct {
	BackendType string `json:"backendType"`
}

// GetBackendCapabilities 按 backend type 返同形态的 caps,响应复用
// GetSessionCapabilitiesResponse。BackendType 空报参数错误(前端逻辑应保证有值);
// 未知 type 返空 caps + 空 meta,不报错(向前兼容未来新增 backend)。
func GetBackendCapabilities(ctx context.Context, req *GetBackendCapabilitiesRequest) (*GetSessionCapabilitiesResponse, error) {
	if req == nil || req.BackendType == "" {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	caps := capabilitiesFor(agent_backend_entity.BackendType(req.BackendType))
	return &GetSessionCapabilitiesResponse{
		Capabilities: capListToStrings(caps),
		PermissionModeMeta: GetSessionCapabilitiesModeMeta{
			AllowedModes:         caps.PermissionModeMeta.AllowedModes,
			DefaultMode:          caps.PermissionModeMeta.DefaultMode,
			SwitchableDuringTurn: caps.PermissionModeMeta.SwitchableDuringTurn,
			Order:                caps.PermissionModeMeta.Order,
		},
	}, nil
}

// capabilitiesFor 按 backend type 反查 agentruntime.RuntimeFor 注册表里的
// Capabilities()。未注册(包括 remote / 未来新增 backend)返空 caps。
func capabilitiesFor(bt agent_backend_entity.BackendType) capability.Capabilities {
	r := agentruntime.RuntimeFor(bt)
	if r == nil {
		return capability.Capabilities{}
	}
	return r.Capabilities()
}

func capListToStrings(c capability.Capabilities) []string {
	out := make([]string, 0, len(c.Set))
	for k, v := range c.Set {
		if v {
			out = append(out, string(k))
		}
	}
	return out
}
