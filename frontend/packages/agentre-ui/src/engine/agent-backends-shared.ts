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
  | "openclaw"
  | "hermes"
  | "antigravity";
export type Translate = ReturnType<typeof useTranslation>["t"];

// probing → 请求在飞；installed/missing → 目标机 $PATH 的结论；
// failed → 远端不可达（离线 / 超时 / 探测报错），此时不能谎报「未安装」。
export type CLIProbeState = "probing" | "installed" | "missing" | "failed";
export type CLIProbe = { state: CLIProbeState; path: string };

/**
 * 一次失败给人看的两层：`text` 是本地化的事实句，`detail` 是机器原话（后端 / CLI
 * 的错误串），只进折叠的「详情」——原话不当正文。
 */
export type FailureNotice = { text: string; detail?: string };

export type FlashState =
  | { kind: "ok"; text: string }
  | ({ kind: "err" } & FailureNotice)
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

const RESERVED_ENV_KEYS = new Set([
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

// antigravity 一轮由 Agentre 写入这两个键（会话网关 token 与本机网关根地址），
// 所以只对它保留；通用 ACP 后端照旧可以在 env_json 里填 GEMINI_API_KEY 认证。
// 与 kinds.go 的 antigravityReservedEnvKeys 对齐。
const ANTIGRAVITY_RESERVED_ENV_KEYS = new Set([
  ...RESERVED_ENV_KEYS,
  "GEMINI_API_KEY",
  "GOOGLE_GEMINI_BASE_URL",
]);

// reservedEnvKeysFor 回答「这个后端的 env_json 里哪些键不许填」：公共保留键对
// 所有类型生效，antigravity 另加它自己的两个。
export function reservedEnvKeysFor(t: BackendType): ReadonlySet<string> {
  return t === "antigravity"
    ? ANTIGRAVITY_RESERVED_ENV_KEYS
    : RESERVED_ENV_KEYS;
}

export function isCliBackend(t: BackendType): boolean {
  return (
    t === "claudecode" ||
    t === "codex" ||
    t === "piagent" ||
    t === "hermes" ||
    t === "antigravity"
  );
}

// isCLIPathBackend 回答「这个后端会不会 spawn 一个本地 CLI，从而需要探测 / 填写
// 可执行文件路径」。hermes 属于 isCliBackend（走设备前缀、仍可编辑 env_json），
// 但它连的是一个已在运行的 `hermes serve`，没有 CLI 路径可探。
export function isCLIPathBackend(t: BackendType): boolean {
  return (
    t === "claudecode" ||
    t === "codex" ||
    t === "piagent" ||
    t === "antigravity"
  );
}

// consumesAgentreProvider 回答「这个后端会不会读 Agentre 的 LLMProvider」。
// openclaw 走 Gateway 自身认证，hermes 自带 provider/model/凭证配置，两者不参与
// 供应商绑定；其余后端至少可以关联一个 Agentre provider。
export function consumesAgentreProvider(t: BackendType): boolean {
  return t !== "openclaw" && t !== "hermes";
}

// requiresAgentreProvider 回答「不绑 Agentre provider 能不能保存」。builtin 没有
// 别的模型来源；antigravity 的 LLM 流量只走网关，没有 CLI 自身登录这条退路。
// 其余 CLI 后端可走 CLI 登录态，OpenClaw / Hermes 自带认证。
export function requiresAgentreProvider(t: BackendType): boolean {
  return t === "builtin" || t === "antigravity";
}

// supportsReasoningEffort 回答「思考力度档位有没有下发通道」：OpenClaw 不吃，
// antigravity 走 ACP 也没有这条通道（agy 自己的思考档位不在 Agentre 里显示）。
// 草稿据此清空，编辑器据此（再叠加 consumesAgentreProvider）决定是否渲染字段。
export function supportsReasoningEffort(t: BackendType): boolean {
  return t !== "openclaw" && t !== "antigravity";
}

export function cliBinaryName(t: BackendType): string {
  if (t === "claudecode") return "claude";
  if (t === "piagent") return "pi";
  if (t === "hermes") return "hermes";
  if (t === "antigravity") return "agy";
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
          : main.modelLabel || main.providerName,
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
    target: resolved.modelLabel || resolved.providerName,
  });
}
