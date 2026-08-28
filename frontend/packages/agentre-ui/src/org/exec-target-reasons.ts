import type { TFunction } from "i18next";

import type { OrgBackendModel } from "./types";

// orgExecTargetReasonLabel 把 chat_svc.BlockReason(后端结构化枚举字符串,见
// internal/service/chat_svc/types.go 的 BlockReason 常量 + exec_pick.go 新增的
// 三个 exec-target-* 值)翻成本地化短标签，供 R15 执行目标列表的每档状态徽标用。
//
// 刻意不直接展示后端 ExecTargetAvailabilityView.Hint 字段——那是 Go 侧写死的中文
// 提示串，不是 i18n 资源，展示它会违反「新可见 UI 文案一律走 i18n」。Reason 是稳定
// 的类型化取值，前端按值查表即可双语呈现；未知取值兜底成「未知原因」。
const REASON_I18N_KEY: Record<string, string> = {
  "no-backend": "orphanBackend",
  "backend-requires-provider": "backendRequiresProvider",
  "provider-inactive": "providerInactive",
  "remote-provider-missing": "remoteProviderMissing",
  "gateway-not-running": "gatewayNotRunning",
  "remote-openclaw-unavailable": "remoteOpenclawUnavailable",
  "unknown-backend": "unknownBackend",
  "exec-target-unpaired": "unpaired",
  "exec-target-offline": "offline",
  "exec-target-project-path-missing": "projectPathMissing",
};

export function orgExecTargetReasonLabel(reason: string, t: TFunction): string {
  const key = REASON_I18N_KEY[reason] ?? "unknownBackend";
  return t(`org.agent.execTargets.reasons.${key}`);
}

// 不可用原因里哪些是"供应商/配置类"问题（红色强调），哪些是"连通性类"问题
// （中性提示）——与 R15 的判据分组一致（决策 37 的既有 BlockReason vs R15b 新增的
// exec-target-* 三类）。
export const ORG_EXEC_TARGET_DESTRUCTIVE_REASONS = new Set([
  "no-backend",
  "backend-requires-provider",
  "provider-inactive",
  "remote-provider-missing",
  "gateway-not-running",
  "remote-openclaw-unavailable",
  "unknown-backend",
]);

const BACKEND_TYPE_LABEL: Record<string, string> = {
  claudecode: "Claude Code",
  "claude-code": "Claude Code",
  codex: "Codex",
  builtin: "Built-in",
  openclaw: "OpenClaw",
  piagent: "Pi Agent",
};

export function orgBackendTypeLabel(type: string): string {
  return BACKEND_TYPE_LABEL[type] ?? type;
}

/**
 * 这一档跑在哪台机器上。没有 deviceId = 本机；有 deviceId 却解析不出机器名时给
 * 「未配对」而不是把那串内部指纹当机器名显示出来。
 */
export function orgExecTargetMachineLabel(
  backend: OrgBackendModel | undefined,
  localLabel: string,
  unresolvedLabel: string,
): string {
  if (!backend || !backend.deviceId) return localLabel;
  return backend.deviceName || unresolvedLabel;
}
