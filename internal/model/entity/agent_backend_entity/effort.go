package agent_backend_entity

// ReasoningEffort 思考力度档位。
//
// 六档：空串 = 不设置（走模型/CLI 自身默认），其余五档 low / medium / high / xhigh / max
// 与 cago provider.ThinkingEffort、claude CLI 的 --effort 取值完全对齐。
// codex CLI 支持到 xhigh；max 会在 agentruntime/codex 启动层 clamp 到 high；
// 但 entity 层不拦截，存原值，便于 backend 类型切换时保留语义。

// IsValidReasoningEffort 用于 service / 前端预校验，与 entity.Check 行为一致。
// 注意：大小写敏感，前后空格不容忍——上游应规范化后再调用。
func IsValidReasoningEffort(s string) bool {
	switch s {
	case "", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}
