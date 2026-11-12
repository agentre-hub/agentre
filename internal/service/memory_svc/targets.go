package memory_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

// 一个执行目标的会话带多少记忆。取值与 chat_svc.MemoryLevel 一致，由宿主注入的 TierFunc 给出；
// memory_svc 不能 import chat_svc（chat_svc 依赖 memory_svc），判档因此走函数端口。
const (
	TierFull      = "full"
	TierSummaries = "summaries"
	TierNone      = "none"
)

// 受限原因（spec 2026-11-11「让用户知道哪里不带」的三种）。
const (
	ReasonBuiltinSummaries   = "builtin_summaries"   // 内置后端只能读摘要
	ReasonUnsupportedBackend = "unsupported_backend" // 这个后端不支持
	ReasonNeedsUpgrade       = "needs_upgrade"       // 那台设备需要升级
)

// TierFunc 按执行目标的能力集与后端类型判档，返回 TierFull / TierSummaries / TierNone。
type TierFunc func(caps capability.Capabilities, backendType string) string

// ExecTarget 是 agent 的一个执行目标（后端档）及判档所需的事实。
type ExecTarget struct {
	BackendID   int64
	BackendName string
	BackendType string
	// DeviceName 非空表示这档在另一台设备上执行（远端）；本机档为空。
	DeviceName string
	Remote     bool
	Caps       capability.Capabilities
	// TypeSupportsMemory：这种后端类型本身带记忆能力（本机 runtime 声明了 CapMemory）。
	// 区分「设备旧了没报」与「这个后端本就不支持」。
	TypeSupportsMemory bool
}

// RestrictedTarget 一个不带记忆或只读摘要的执行目标及原因。
type RestrictedTarget struct {
	BackendID   int64  `json:"backendId"`
	BackendName string `json:"backendName"`
	DeviceName  string `json:"deviceName"`
	Level       string `json:"level"`
	Reason      string `json:"reason"`
}

// TargetAdvisor 列出一个 agent 哪些执行目标带不全记忆。
type TargetAdvisor struct {
	source TargetSource
	tier   TierFunc
}

func NewTargetAdvisor(source TargetSource, tier TierFunc) *TargetAdvisor {
	return &TargetAdvisor{source: source, tier: tier}
}

// Restricted 只返回受限的目标；全部完整支持时返回空切片。
func (a *TargetAdvisor) Restricted(ctx context.Context, agentID int64) ([]RestrictedTarget, error) {
	targets, err := a.source.ExecTargets(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := []RestrictedTarget{}
	for _, t := range targets {
		level := a.tier(t.Caps, t.BackendType)
		if level == TierFull {
			continue
		}
		out = append(out, RestrictedTarget{
			BackendID: t.BackendID, BackendName: t.BackendName, DeviceName: t.DeviceName,
			Level: level, Reason: reasonFor(level, t),
		})
	}
	return out, nil
}

func reasonFor(level string, t ExecTarget) string {
	switch {
	case level == TierSummaries:
		return ReasonBuiltinSummaries
	case t.Remote && t.TypeSupportsMemory:
		return ReasonNeedsUpgrade
	default:
		return ReasonUnsupportedBackend
	}
}
