// Package exec_target_svc 拥有「这条会话的活在哪儿干」这一个域：
// 按 R14/R15 顺序为 Agent 挑执行目标档、逐档判定可用性、把会话解析成
// {设备, 工作目录} 作用域。它只读仓储与设备/网关状态，不驱动任何一轮执行。
package exec_target_svc

import "github.com/agentre-hub/agentre/pkg/wire/devicefp"

// BlockReason 是不可对话 Agent 的结构化原因枚举；空串 = 可对话（与 Chattable=true 一致）。
// 由 ListAgents 在与 Chattable/ChattableHint 相同的判定点位设置，取值见下。
// 前端（spec docs/specs/2026-08-08-setup-guidance.md 决策表）按它映射引导文案与
// 主按钮跳转；ChattableHint 保留为兜底展示字段。
type BlockReason string

const (
	// BlockReasonNoBackend 该 Agent 没绑后端（含 CEO）。
	BlockReasonNoBackend BlockReason = "no-backend"
	// BlockReasonBackendRequiresProvider 后端需要但找不到绑定的 LLM 供应商。
	BlockReasonBackendRequiresProvider BlockReason = "backend-requires-provider"
	// BlockReasonProviderInactive 后端绑的供应商存在但未激活/缺 Key。
	BlockReasonProviderInactive BlockReason = "provider-inactive"
	// BlockReasonRemoteProviderMissing 远端 agentred 未配置该供应商。
	BlockReasonRemoteProviderMissing BlockReason = "remote-provider-missing"
	// BlockReasonGatewayNotRunning 本地网关未启动，CLI 后端暂不可用。
	BlockReasonGatewayNotRunning BlockReason = "gateway-not-running"
	// BlockReasonRemoteOpenClawUnavailable 远端 OpenClaw 暂不可用。
	BlockReasonRemoteOpenClawUnavailable BlockReason = "remote-openclaw-unavailable"
	// BlockReasonUnknownBackend 未知 Agent 后端类型。
	BlockReasonUnknownBackend BlockReason = "unknown-backend"

	// 以下三个是 R15 执行目标挑选专用的原因，与上面几个「backend 自身不可用」的判据
	// 正交：它们描述的是这一档所在的机器 / 项目路径，不是 backend 配置本身。

	// BlockReasonExecTargetUnpaired 本机没有配对这一档指向的那台 agentred（R2b：判据
	// 是本地配对表里有没有这一行，不是有没有配对令牌）。
	BlockReasonExecTargetUnpaired BlockReason = "exec-target-unpaired"
	// BlockReasonExecTargetOffline 已配对，但该 agentred 当前不在线。
	BlockReasonExecTargetOffline BlockReason = "exec-target-offline"
	// BlockReasonExecTargetDesktopNotRunning 目标是一台具名桌面端，且它的 Agentre App
	// 没有运行（R2：与「机器离线」是两种说法——一个是开应用，一个是开机）。
	BlockReasonExecTargetDesktopNotRunning BlockReason = "exec-target-desktop-not-running"
	// BlockReasonExecTargetProjectPathMissing 会话绑定了项目，但这一档所在的机器上
	// 没有配置这个项目的路径（决策 34）。不绑项目的会话不受这一项约束。
	BlockReasonExecTargetProjectPathMissing BlockReason = "exec-target-project-path-missing"
)

// LocalCommandScope 是本地命令历史与命令执行共享的稳定设备/cwd 作用域。
// DeviceID 为空表示本机；Cwd 为空表示目标设备上的默认 Agent 工作目录。
type LocalCommandScope struct {
	DeviceID devicefp.Carrier `json:"deviceId"`
	Cwd      string           `json:"cwd"`
}

// ResolveLocalCommandScopeRequest 接受且只接受一种目标：已有 SessionID，或尚未
// 持久化的 AgentID + ProjectID（ProjectID=0 表示自由会话）。
type ResolveLocalCommandScopeRequest struct {
	SessionID int64 `json:"sessionId"`
	AgentID   int64 `json:"agentId"`
	ProjectID int64 `json:"projectId"`
}
