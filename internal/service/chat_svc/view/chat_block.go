// Package view 持有 chat_svc 的 canonical wire DTO:CanonicalDTO 及其构造函数
// FromCanonical。
//
// 唯一消费方是 chat_svc.ChatBlock.Canonical(types.go)—— 它直接引用这里的
// CanonicalDTO,前端按 kind 分发 discriminated union。
package view

import (
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/canonical"
)

// CanonicalDTO wire 形态,与前端 TS discriminated union 一一对应。
type CanonicalDTO struct {
	Kind           canonical.Kind                `json:"kind"`
	FileWrite      *canonical.FileWrite          `json:"fileWrite,omitempty"`
	FileEdit       *canonical.FileEdit           `json:"fileEdit,omitempty"`
	UserAsk        *canonical.UserAsk            `json:"userAsk,omitempty"`
	PlanUpdate     *canonical.PlanUpdate         `json:"planUpdate,omitempty"`
	PlanApprove    *canonical.PlanApproveRequest `json:"planApprove,omitempty"`
	AgentSpawn     *canonical.AgentSpawn         `json:"agentSpawn,omitempty"`
	ToolPermission *canonical.ToolPermission     `json:"toolPermission,omitempty"`
}
