// agent-backends 面板三个子模块（列表行 / 徽章与结果条 / 字段控件）与面板本体共用的
// 类型、常量与纯函数。只放跨模块共用的那些，单个模块独有的仍留在它自己那里。
import type { useUiTranslation as useTranslation } from "../i18n";

import {
  resolveModelTarget,
  type ResolvedModelTarget,
} from "./agent-backends-utils";
import type { PickerProvider } from "./model-target-picker";
import type { agent_backend_svc, llm_provider_svc } from "./port-bridge";

export type Backend = agent_backend_svc.BackendItem;
export type Provider = llm_provider_svc.ProviderItem;
export type BackendType =
  | "builtin"
  | "claudecode"
  | "codex"
  | "piagent"
  | "openclaw";
export type Translate = ReturnType<typeof useTranslation>["t"];

// probing → 请求在飞；installed/missing → 目标机 $PATH 的结论；
// failed → 远端不可达（离线 / 超时 / 探测报错），此时不能谎报「未安装」。
export type CLIProbeState = "probing" | "installed" | "missing" | "failed";
export type CLIProbe = { state: CLIProbeState; path: string };

export type FlashState =
  | { kind: "ok"; text: string }
  | { kind: "err"; text: string }
  | null;

export type SandboxValue =
  | ""
  | "read-only"
  | "workspace-write"
  | "danger-full-access";
export type ApprovalValue = "" | "untrusted" | "on-request" | "never";
export type ReasoningEffortValue =
  | ""
  | "low"
  | "medium"
  | "high"
  | "xhigh"
  | "max";
// RouteTarget 是 Claude Tier Route 的结构化目标（与后端 DTO 同形）：
// providerKey 空 = inherit-main；modelKey 空 = provider-default。
export type RouteTarget = { providerKey: string; modelKey: string };

export type EnvEntry = { key: string; value: string };

export const CLAUDE_TIERS = ["OPUS", "SONNET", "HAIKU"] as const;
export type ClaudeTier = (typeof CLAUDE_TIERS)[number];

export const RESERVED_ENV_KEYS = new Set([
  "ANTHROPIC_BASE_URL",
  "ANTHROPIC_API_KEY",
  "ANTHROPIC_AUTH_TOKEN",
  "ANTHROPIC_MODEL",
  "ANTHROPIC_DEFAULT_OPUS_MODEL",
  "ANTHROPIC_DEFAULT_SONNET_MODEL",
  "ANTHROPIC_DEFAULT_HAIKU_MODEL",
  "OPENAI_API_KEY",
  "OPENAI_BASE_URL",
  "OPENAI_API_BASE",
  "PI_OFFLINE",
  "PI_CODING_AGENT_DIR",
  "PI_CODING_AGENT_SESSION_DIR",
]);

export function isCliBackend(t: BackendType): boolean {
  return t === "claudecode" || t === "codex" || t === "piagent";
}

export function cliBinaryName(t: BackendType): string {
  if (t === "claudecode") return "claude";
  if (t === "piagent") return "pi";
  return "codex";
}

export function routeConclusion(
  t: Translate,
  route: RouteTarget,
  main: ResolvedModelTarget,
  catalog: PickerProvider[],
): string {
  if (!route.providerKey) {
    return t("agentBackends.modelRoutes.inheritsSummary", {
      target:
        main.mode === "native"
          ? t("agentBackends.binding.cliLogin")
          : main.modelId || main.providerName,
    });
  }
  const resolved = resolveModelTarget(
    route.providerKey,
    route.modelKey,
    catalog,
  );
  if (resolved.mode === "invalid")
    return t("agentBackends.modelRoutes.invalid");
  return t("agentBackends.modelRoutes.fixedSummary", {
    target: resolved.modelId || resolved.providerName,
  });
}
