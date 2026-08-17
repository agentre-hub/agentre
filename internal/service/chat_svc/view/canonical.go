package view

import (
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/canonical"
)

// FromCanonical 把 agentruntime.canonical 类型转成 wire CanonicalDTO。
// nil-safe(返 nil)。
//
// Live emit 路径:ToolCall handler 拿 ev.Canonical 直接调这里。
// Replay 路径不重算 canonical —— canonical 是 runtime translator 的翻译产物,
// chat_svc.toChatMessage 重建历史时只透传已落库的结果。
func FromCanonical(c canonical.CanonicalTool) *CanonicalDTO {
	if c == nil {
		return nil
	}
	dto := &CanonicalDTO{Kind: canonical.KindOf(c)}
	switch t := c.(type) {
	case canonical.FileWrite:
		dto.FileWrite = &t
	case *canonical.FileWrite:
		dto.FileWrite = t
	case canonical.FileEdit:
		dto.FileEdit = &t
	case *canonical.FileEdit:
		dto.FileEdit = t
	case canonical.UserAsk:
		dto.UserAsk = &t
	case *canonical.UserAsk:
		dto.UserAsk = t
	case canonical.PlanUpdate:
		dto.PlanUpdate = &t
	case *canonical.PlanUpdate:
		dto.PlanUpdate = t
	case canonical.PlanApproveRequest:
		dto.PlanApprove = &t
	case *canonical.PlanApproveRequest:
		dto.PlanApprove = t
	case canonical.AgentSpawn:
		dto.AgentSpawn = &t
	case *canonical.AgentSpawn:
		dto.AgentSpawn = t
	case canonical.ToolPermission:
		dto.ToolPermission = &t
	case *canonical.ToolPermission:
		dto.ToolPermission = t
	}
	return dto
}
