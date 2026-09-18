// 编辑器草稿：表单状态 → 提交给后端的 BackendDraft，以及它周边的纯函数
// （env/route 的序列化、供应商筛选与标签、OpenClaw 错误码到文案）。
// 这一层不碰 React，只做数据整形，方便单独推理与测试。
import { recordRecentTarget } from "../model-target-picker";
import { OPENCLAW_SESSION_MODE } from "../openclaw-validation";
import { OPENCLAW_ERROR_KEY_BY_CODE } from "../openclaw-validation";
import type { EngineSettingsBridge } from "../port-bridge";
import type { BackendChangedField } from "../ports";
import type { agent_backend_svc } from "../port-bridge";
import {
  CLAUDE_TIERS,
  isCLIPathBackend,
  isCliBackend,
  type ApprovalValue,
  type Backend,
  type BackendType,
  type ClaudeTier,
  type EnvEntry,
  type Provider,
  type ReasoningEffortValue,
  type RouteTarget,
  type SandboxValue,
  type Translate,
} from "../agent-backends-shared";
import type { EditorState } from "./editor-types";

export type BackendDraft = {
  type: BackendType;
  name: string;
  deviceId: string;
  llmProviderKey: string;
  // llmModelKey 主绑定目标的稳定 ModelKey（空 = provider-default）。
  llmModelKey: string;
  cliPath: string;
  // modelRoutes 类型化 Claude Tier Route target（key = OPUS/SONNET/HAIKU）。
  modelRoutes: Record<ClaudeTier, RouteTarget>;
  sandbox: string;
  approval: string;
  envJson: string;
  reasoningEffort: ReasoningEffortValue;
  defaultPermissionMode: string;
  defaultModel: string;
  openClawGatewayUrl: string;
  openClawAgentId: string;
  openClawDefaultModel: string;
  openClawSessionMode: string;
  // HermesURL 仅 hermes 使用（一个已在运行的 `hermes serve` 地址）。
  hermesUrl: string;
  // hermes gated serve 的非敏感展示字段（登录成功后的 provider / userId）。
  hermesAuthProvider: string;
  hermesUserId: string;
};

export type PendingProviderSync = {
  draft: BackendDraft;
  providerKeys: string[];
  saveAfterSync: boolean;
};

export function matchingProviders(t: BackendType, providers: Provider[]) {
  if (t === "claudecode")
    return providers.filter((p) => p.type === "anthropic");
  if (t === "codex")
    return providers.filter((p) => p.type === "openai-response");
  // piagent 三类全收（anthropic / openai-chat / openai-response）：直接全列。
  return providers;
}

// parseRoutes 把后端 DTO 的类型化 modelRoutes 解析成三档 Record。
export function parseRoutes(
  raw: Record<string, RouteTarget> | undefined,
): Record<ClaudeTier, RouteTarget> {
  const next = emptyRoutes();
  for (const tier of CLAUDE_TIERS) {
    const v = raw?.[tier];
    if (v && v.providerKey) {
      next[tier] = { providerKey: v.providerKey, modelKey: v.modelKey ?? "" };
    }
  }
  return next;
}

export function safeParseEnv(s: string): EnvEntry[] {
  try {
    const obj = JSON.parse(s || "{}");
    if (!obj || typeof obj !== "object") return [];
    return Object.entries(obj as Record<string, unknown>).map(
      ([key, value]) => ({ key, value: String(value ?? "") }),
    );
  } catch {
    return [];
  }
}

export function serializeEnv(entries: EnvEntry[]): string {
  const out: Record<string, string> = {};
  for (const e of entries) {
    const k = e.key.trim();
    if (!k) continue;
    out[k] = e.value;
  }
  return Object.keys(out).length === 0 ? "{}" : JSON.stringify(out);
}

export function emptyRoutes(): Record<ClaudeTier, RouteTarget> {
  return {
    OPUS: { providerKey: "", modelKey: "" },
    SONNET: { providerKey: "", modelKey: "" },
    HAIKU: { providerKey: "", modelKey: "" },
  };
}

// routeTargets 把非空的 tier route 收进提交用的 map（继承主绑定的空 target 不提交）。
export function routeTargetsForRequest(
  routes: Record<ClaudeTier, RouteTarget>,
): Record<string, RouteTarget> {
  const out: Record<string, RouteTarget> = {};
  for (const tier of CLAUDE_TIERS) {
    const r = routes[tier];
    if (r && r.providerKey.trim() !== "") {
      out[tier] = {
        providerKey: r.providerKey.trim(),
        modelKey: r.modelKey.trim(),
      };
    }
  }
  return out;
}

export function referencedProviderKeys(draft: BackendDraft): string[] {
  const keys = new Set<string>();
  if (draft.llmProviderKey.trim() !== "") {
    keys.add(draft.llmProviderKey.trim());
  }
  if (draft.type === "claudecode") {
    for (const tier of CLAUDE_TIERS) {
      const r = draft.modelRoutes[tier];
      if (r && r.providerKey.trim() !== "") {
        keys.add(r.providerKey.trim());
      }
    }
  }
  return Array.from(keys);
}

export function providerLabel(key: string, providers: Provider[]): string {
  const p = providers.find(
    (item) => item.providerKey === key || String(item.id) === key,
  );
  if (!p) return key;
  return p.name;
}

export function openClawProbeErrorMessage(
  code: string,
  fallback: string,
  translate: (key: string) => string,
): string {
  const normalized = code.trim().toUpperCase();
  const key = OPENCLAW_ERROR_KEY_BY_CODE[normalized];
  return key
    ? translate(`agentBackends.openclaw.errors.${key}`)
    : fallback || translate("agentBackends.openclaw.errors.connectionFailed");
}

// 登录动作走 Wails 的 codedError 前缀（`agentre-code:<业务码> <原文>`）。数值码 →
// 前端文案名；Test Connection 回的是文案名本身，名集合由这张表派生，避免两张表
// 各自维护。
const HERMES_ERROR_CODE_NUMBERS: Record<string, string> = {
  "12030": "HERMES_LOGIN_REQUIRED",
  "12031": "HERMES_LOGIN_EXPIRED",
  "12032": "HERMES_INVALID_CREDENTIALS",
  "12033": "HERMES_RATE_LIMITED",
  "12034": "HERMES_PROVIDER_UNSUPPORTED",
  "12035": "HERMES_PROVIDER_UNAVAILABLE",
  "12036": "HERMES_PROVIDER_NOT_FOUND",
  "12037": "HERMES_UNREACHABLE",
};

// Hermes test-connection codes emitted by the Go service. Unlike OpenClaw they
// share the backend's own copy namespace, so the key is the code itself.
const HERMES_ERROR_CODES = new Set(Object.values(HERMES_ERROR_CODE_NUMBERS));

// hermesProbeErrorMessage localizes a structured hermes test failure so the UI
// can say "login required" / "login expired" instead of the raw backend text.
export function hermesProbeErrorMessage(
  code: string,
  fallback: string,
  translate: (key: string) => string,
): string {
  const normalized = code.trim().toUpperCase();
  if (HERMES_ERROR_CODES.has(normalized)) {
    return translate(`agentBackends.hermes.errors.${normalized}`);
  }
  return fallback;
}

const CODED_ERROR_PREFIX = /^agentre-code:(\d+)\s?([\s\S]*)$/;

// hermesErrorMessage turns a thrown login/logout error into a readable sentence,
// preferring the localized copy over the raw `agentre-code:` envelope.
export function hermesErrorMessage(
  err: unknown,
  translate: (key: string) => string,
): string {
  const raw =
    err instanceof Error ? err.message : typeof err === "string" ? err : "";
  const match = CODED_ERROR_PREFIX.exec(raw);
  if (match) {
    const key = HERMES_ERROR_CODE_NUMBERS[match[1]];
    if (key) return translate(`agentBackends.hermes.errors.${key}`);
    return match[2] || translate("common.unknownError");
  }
  return raw || translate("common.unknownError");
}

// buildBackendDraft 把编辑器的表单字段收成一份提交草稿：每种 backend 只保留自己
// 用得上的字段，其余按类型清空，避免跨类型残留被写进库。
export type BackendDraftFields = {
  type: BackendType;
  name: string;
  deviceId: string;
  // llmProviderKey 传 effectiveLlmProviderKey（含 builtin 的自动选中）。
  llmProviderKey: string;
  llmModelKey: string;
  cliPath: string;
  routes: Record<ClaudeTier, RouteTarget>;
  sandbox: SandboxValue;
  approval: ApprovalValue;
  envEntries: EnvEntry[];
  reasoningEffort: ReasoningEffortValue;
  defaultPermissionMode: string;
  defaultModel: string;
  openClawGatewayURL: string;
  openClawAgentID: string;
  openClawDefaultModel: string;
  hermesUrl: string;
  hermesAuthProvider: string;
  hermesUserId: string;
};

export function buildBackendDraft(f: BackendDraftFields): BackendDraft {
  // 六档词表全后端统一（spec 2026-09-01「三后端下发档位的收敛」）：codex 不再在保存时
  // 把 max 二次兜底降档——此前这里与 REASONING_EFFORTS_CODEX 配对制造「保存了 max
  // 实际存的是 high」的迷惑，前提已不成立。
  const effort: ReasoningEffortValue =
    f.type === "openclaw" ? "" : f.reasoningEffort;
  return {
    type: f.type,
    name: f.name,
    // builtin 后端只能在本地运行（无 HTTP 网关路由到 daemon），强制清空以防误保存。
    deviceId: f.type === "builtin" ? "" : f.deviceId,
    llmProviderKey: f.type === "openclaw" ? "" : f.llmProviderKey,
    // openclaw 不绑定 Agentre ProviderModel（spec 决策 4/22）。
    llmModelKey: f.type === "openclaw" ? "" : f.llmModelKey.trim(),
    cliPath: isCLIPathBackend(f.type) ? f.cliPath.trim() : "",
    modelRoutes:
      f.type === "claudecode" ? routeTargetsForRequest(f.routes) : {},
    sandbox: f.type === "codex" ? f.sandbox : "",
    approval: f.type === "codex" ? f.approval : "",
    envJson: isCliBackend(f.type) ? serializeEnv(f.envEntries) : "{}",
    reasoningEffort: effort,
    defaultPermissionMode:
      f.type === "claudecode" ? f.defaultPermissionMode : "",
    defaultModel: f.type === "claudecode" ? f.defaultModel.trim() : "",
    openClawGatewayUrl:
      f.type === "openclaw" ? f.openClawGatewayURL.trim() : "",
    openClawAgentId: f.type === "openclaw" ? f.openClawAgentID.trim() : "",
    openClawDefaultModel:
      f.type === "openclaw" ? f.openClawDefaultModel.trim() : "",
    openClawSessionMode: f.type === "openclaw" ? OPENCLAW_SESSION_MODE : "",
    hermesUrl: f.type === "hermes" ? f.hermesUrl.trim() : "",
    hermesAuthProvider: f.type === "hermes" ? f.hermesAuthProvider.trim() : "",
    hermesUserId: f.type === "hermes" ? f.hermesUserId.trim() : "",
  };
}

// 草稿键 → 对外的改动字段名（ports.ts 的 BackendChangedField）。顺序即输出顺序。
const CHANGED_FIELD_BY_DRAFT_KEY: ReadonlyArray<
  readonly [keyof BackendDraft, BackendChangedField]
> = [
  ["type", "type"],
  ["name", "name"],
  ["deviceId", "deviceId"],
  ["llmProviderKey", "llmProviderKey"],
  ["llmModelKey", "llmModelKey"],
  ["cliPath", "cliPath"],
  ["envJson", "envJson"],
  ["reasoningEffort", "reasoningEffort"],
  ["modelRoutes", "config.modelRoutes"],
  ["sandbox", "config.sandbox"],
  ["approval", "config.approval"],
  ["defaultPermissionMode", "config.defaultPermissionMode"],
  ["defaultModel", "config.defaultModel"],
  ["openClawGatewayUrl", "config.openClawGatewayUrl"],
  ["openClawAgentId", "config.openClawAgentId"],
  ["openClawDefaultModel", "config.openClawDefaultModel"],
  ["openClawSessionMode", "config.openClawSessionMode"],
  ["hermesUrl", "config.hermesUrl"],
  ["hermesAuthProvider", "config.hermesAuthProvider"],
  ["hermesUserId", "config.hermesUserId"],
];

// 两份草稿都出自 buildBackendDraft，已按同一规则整形（trim、按类型清空、env 序列化、
// 路由按 tier 顺序），所以逐键比较序列化结果即可。
function draftValueKey(value: BackendDraft[keyof BackendDraft]): string {
  return typeof value === "string" ? value : JSON.stringify(value);
}

function isPopulated(
  key: keyof BackendDraft,
  value: BackendDraft[keyof BackendDraft],
): boolean {
  if (key === "envJson") return value !== "" && value !== "{}";
  if (typeof value === "string") return value !== "";
  return Object.keys(value).length > 0;
}

// changedBackendFields：baseline 为 null（新建）时列出所有有值的字段；否则列出与
// 打开时那份草稿不同的字段 —— 改了又改回去不算。
export function changedBackendFields(
  baseline: BackendDraft | null,
  draft: BackendDraft,
): BackendChangedField[] {
  return CHANGED_FIELD_BY_DRAFT_KEY.filter(([key]) =>
    baseline === null
      ? isPopulated(key, draft[key])
      : draftValueKey(baseline[key]) !== draftValueKey(draft[key]),
  ).map(([, field]) => field);
}

export type SaveBackendDraftBridge = Pick<
  EngineSettingsBridge,
  | "CreateAgentBackend"
  | "CreateOpenClawAgentBackend"
  | "UpdateAgentBackend"
  | "UpdateOpenClawAgentBackend"
>;

// saveBackendDraft 落库：create 走 Create*，edit 走 Update*（openclaw 另有带 token
// 的入口），成功后记一次「最近使用」并把成功文案交回宿主。
export async function saveBackendDraft(args: {
  draft: BackendDraft;
  // baseline：编辑器打开时那份草稿（cliPath 换成保存时所选设备已存的值），据此算
  // changedFields；新建传 null，按有值字段计。
  baseline: BackendDraft | null;
  state: EditorState;
  editing: Backend | null;
  openClawToken: string;
  clearOpenClawToken: boolean;
  bridge: SaveBackendDraftBridge;
  // 可执行文件覆盖走独立的 per-device 端口，不随 create/update 的整份草稿一起编码
  // （web 宿主日后也实现同一个端口，见 agentBackends.cliPath 的文档）。宿主没接
  // 这个端口，或本类型根本不用 CLI（isCLIPathBackend）时都不调用。
  setCliOverlay?: (
    backendSyncId: string,
    deviceId: string,
    path: string,
  ) => Promise<void>;
  onSaved: (message: string) => Promise<void> | void;
  t: Translate;
}): Promise<void> {
  const { draft, state, editing, bridge, t } = args;
  const changedFields = changedBackendFields(
    state.kind === "create" ? null : args.baseline,
    draft,
  );
  // 路径没改就不写：覆盖行按 (后端, 设备) 各自同步，把打开时读到的值原样写回会撤回
  // 其它设备在此期间对这一行的改动（规格「web 前端」：未改动字段保持服务端当前值）。
  async function writeCliOverlay(backendSyncId: string) {
    if (!args.setCliOverlay || !isCLIPathBackend(draft.type)) return;
    if (!changedFields.includes("cliPath")) return;
    await args.setCliOverlay(backendSyncId, draft.deviceId, draft.cliPath);
  }
  if (state.kind === "create") {
    let created: Backend | undefined;
    if (draft.type === "openclaw") {
      const response = await bridge.CreateOpenClawAgentBackend(
        { ...draft, changedFields } as agent_backend_svc.CreateBackendRequest,
        args.openClawToken,
      );
      created = response.item;
    } else {
      const response = await bridge.CreateAgentBackend({
        ...draft,
        changedFields,
      } as agent_backend_svc.CreateBackendRequest);
      created = response.item;
    }
    if (created) await writeCliOverlay(created.syncId);
    // 最近使用只在 target 成功持久化后记录（spec 决策 19）；native/inherit 不进入。
    recordRecentTarget("backend", draft.deviceId, {
      providerKey: draft.llmProviderKey,
      modelKey: draft.llmModelKey,
    });
    await args.onSaved(t("agentBackends.flash.created"));
  } else if (state.kind === "edit" && editing) {
    // 类型不可改，但端口契约要求带上：宿主据此判断这类后端能不能落（浏览器控制台就判）。
    const request: agent_backend_svc.UpdateBackendRequest = {
      id: editing.id,
      type: draft.type,
      name: draft.name,
      deviceId: draft.deviceId,
      llmProviderKey: draft.llmProviderKey,
      llmModelKey: draft.llmModelKey,
      cliPath: draft.cliPath,
      modelRoutes: draft.modelRoutes,
      sandbox: draft.sandbox,
      approval: draft.approval,
      envJson: draft.envJson,
      reasoningEffort: draft.reasoningEffort,
      defaultPermissionMode: draft.defaultPermissionMode,
      defaultModel: draft.defaultModel,
      openClawGatewayUrl: draft.openClawGatewayUrl,
      openClawAgentId: draft.openClawAgentId,
      openClawDefaultModel: draft.openClawDefaultModel,
      openClawSessionMode: draft.openClawSessionMode,
      hermesUrl: draft.hermesUrl,
      hermesAuthProvider: draft.hermesAuthProvider,
      hermesUserId: draft.hermesUserId,
      changedFields,
    };
    if (draft.type === "openclaw") {
      await bridge.UpdateOpenClawAgentBackend(
        request,
        args.openClawToken,
        args.clearOpenClawToken,
      );
    } else {
      await bridge.UpdateAgentBackend(request);
    }
    await writeCliOverlay(editing.syncId);
    // 最近使用只在 target 成功持久化后记录（spec 决策 19）；native/inherit 不进入。
    recordRecentTarget("backend", draft.deviceId, {
      providerKey: draft.llmProviderKey,
      modelKey: draft.llmModelKey,
    });
    await args.onSaved(t("agentBackends.flash.saved"));
  }
}
