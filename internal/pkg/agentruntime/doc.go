// Package agentruntime 把"按 agent_backend.Type 跑一段对话"的差异封进 Runtime 接口,
// 由 runtimes/{builtin,claudecode,codex,piagent,openclaw} 子包各自 init() 注册到 RuntimeFor；
// remote 是桌面端按设备创建的代理，不通过全局注册表登记。
// 表。chat_svc 通过 RuntimeFor(backendType) 取实现,面对统一的 Event 流与 RunResult,
// 不再 if-else by type。
//
// 与 agent_backend_svc/prober 的关系:prober 是单 turn / 单字符串结果;agentruntime 是
// 多 turn / 流式事件 / 跨 turn Session 复用。
package agentruntime
