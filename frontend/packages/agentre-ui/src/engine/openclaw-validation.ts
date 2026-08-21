// @ts-nocheck
// OpenClaw 后端草稿的前端校验 + 错误码到 i18n key 的映射。
//
// 后端 entity.Check 对所有配置问题一律返回 code.InvalidParameter,跨 Wails 边界只剩
// 一句后端 i18n 的中文「参数错误」——既分不清错在哪个字段,又会把中文糊进英文 UI。
// 服务端现在对 OpenClaw 草稿返回结构化 Code(见 agent_backend_svc.openClawDraftIssue),
// 这里负责把 Code 翻成本地化文案;同时在提交前用同一套规则做一次前端预校验,
// 避免 Create/Update 这条仍然只回字符串的路径把中文错误漏出来。
// 规则必须与 agent_backend_entity.NormalizeOpenClawGatewayURL 保持一致。

export const OPENCLAW_SESSION_MODE = "per-agentre-session";

export type OpenClawIssueCode =
  | "OPENCLAW_NAME_REQUIRED"
  | "OPENCLAW_URL_REQUIRED"
  | "OPENCLAW_URL_INVALID"
  | "OPENCLAW_URL_SCHEME"
  | "OPENCLAW_URL_HOST"
  | "OPENCLAW_URL_CREDENTIALS"
  | "OPENCLAW_URL_PLAINTEXT_REMOTE"
  | "OPENCLAW_SESSION_MODE_INVALID";

// 结构化错误码 → agentBackends.openclaw.errors.<key>。探测响应码与草稿校验码共用一张表。
export const OPENCLAW_ERROR_KEY_BY_CODE: Record<string, string> = {
  OPENCLAW_SCOPE_MISSING: "scopeMissing",
  OPENCLAW_PROTOCOL_MISMATCH: "protocolMismatch",
  OPENCLAW_AGENT_NOT_FOUND: "agentNotFound",
  OPENCLAW_MODEL_NOT_FOUND: "modelNotFound",
  OPENCLAW_METHOD_MISSING: "methodMissing",
  OPENCLAW_EVENT_MISSING: "eventMissing",
  OPENCLAW_SECRET_UNAVAILABLE: "secretUnavailable",
  OPENCLAW_REMOTE_SECRET_UNAVAILABLE: "remoteUnavailable",
  OPENCLAW_PROBE_CANCELED: "canceled",
  OPENCLAW_PROBE_TIMEOUT: "timeout",
  OPENCLAW_CONNECTION_FAILED: "connectionFailed",
  OPENCLAW_NOT_PAIRED: "notPaired",
  OPENCLAW_NAME_REQUIRED: "nameRequired",
  OPENCLAW_URL_REQUIRED: "urlRequired",
  OPENCLAW_URL_INVALID: "urlInvalid",
  OPENCLAW_URL_SCHEME: "urlScheme",
  OPENCLAW_URL_HOST: "urlHost",
  OPENCLAW_URL_CREDENTIALS: "urlCredentials",
  OPENCLAW_URL_PLAINTEXT_REMOTE: "urlPlaintextRemote",
  OPENCLAW_SESSION_MODE_INVALID: "sessionModeInvalid",
  AUTH_FAILED: "authFailed",
  UNAUTHORIZED: "authFailed",
  FORBIDDEN: "authFailed",
  NOT_PAIRED: "notPaired",
};

function isLoopbackHostname(hostname: string): boolean {
  const host = hostname.toLowerCase();
  if (host === "localhost" || host === "::1" || host === "[::1]") return true;
  if (host === "0.0.0.0") return false;
  // IPv4 环回段 127.0.0.0/8。
  const v4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host);
  if (v4) return v4[1] === "127";
  return false;
}

// 校验 Gateway URL,返回错误码;合法返回 null。规则与后端一致:
// 只允许 ws/wss、必须有 host、不允许 userinfo/query/fragment、明文 ws 仅限环回。
export function openClawGatewayURLIssue(raw: string): OpenClawIssueCode | null {
  const value = raw.trim();
  if (!value) return "OPENCLAW_URL_REQUIRED";
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return "OPENCLAW_URL_INVALID";
  }
  const scheme = parsed.protocol.replace(/:$/, "").toLowerCase();
  if (scheme !== "ws" && scheme !== "wss") return "OPENCLAW_URL_SCHEME";
  if (!parsed.hostname) return "OPENCLAW_URL_HOST";
  if (parsed.username || parsed.password || parsed.search || parsed.hash) {
    return "OPENCLAW_URL_CREDENTIALS";
  }
  if (scheme === "ws" && !isLoopbackHostname(parsed.hostname)) {
    return "OPENCLAW_URL_PLAINTEXT_REMOTE";
  }
  return null;
}

export function openClawDraftIssue(draft: {
  name: string;
  gatewayURL: string;
  sessionMode: string;
}): OpenClawIssueCode | null {
  if (!draft.name.trim()) return "OPENCLAW_NAME_REQUIRED";
  const urlIssue = openClawGatewayURLIssue(draft.gatewayURL);
  if (urlIssue) return urlIssue;
  if (draft.sessionMode.trim() !== OPENCLAW_SESSION_MODE) {
    return "OPENCLAW_SESSION_MODE_INVALID";
  }
  return null;
}
