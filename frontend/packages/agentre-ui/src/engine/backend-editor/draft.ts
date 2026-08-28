// 编辑器草稿：表单状态 → 提交给后端的 BackendDraft，以及它周边的纯函数
// （env/route 的序列化、供应商筛选与标签、OpenClaw 错误码到文案）。
// 这一层不碰 React，只做数据整形，方便单独推理与测试。
import { recordRecentTarget } from "../model-target-picker";
import { OPENCLAW_SESSION_MODE } from "../openclaw-backend-fields";
import { OPENCLAW_ERROR_KEY_BY_CODE } from "../openclaw-validation";
import type { EngineSettingsBridge } from "../port-bridge";
import type { agent_backend_svc } from "../port-bridge";
import {
  CLAUDE_TIERS,
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
};

export type PendingProviderSync = {
  draft: BackendDraft;
  providerKeys: string[];
  saveAfterSync: boolean;
};

export function normalizeForCodex(
  v: ReasoningEffortValue,
): ReasoningEffortValue {
  return v === "max" ? "high" : v;
}

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
};

export function buildBackendDraft(f: BackendDraftFields): BackendDraft {
  // 三种 backend 都保留 reasoningEffort；codex 二次兜底 normalize（防止历史脏数据 / 跨 type 残留）。
  const effort: ReasoningEffortValue =
    f.type === "openclaw"
      ? ""
      : f.type === "codex"
        ? normalizeForCodex(f.reasoningEffort)
        : f.reasoningEffort;
  return {
    type: f.type,
    name: f.name,
    // builtin 后端只能在本地运行（无 HTTP 网关路由到 daemon），强制清空以防误保存。
    deviceId: f.type === "builtin" ? "" : f.deviceId,
    llmProviderKey: f.type === "openclaw" ? "" : f.llmProviderKey,
    // openclaw 不绑定 Agentre ProviderModel（spec 决策 4/22）。
    llmModelKey: f.type === "openclaw" ? "" : f.llmModelKey.trim(),
    cliPath: isCliBackend(f.type) ? f.cliPath.trim() : "",
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
  };
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
  state: EditorState;
  editing: Backend | null;
  openClawToken: string;
  clearOpenClawToken: boolean;
  bridge: SaveBackendDraftBridge;
  onSaved: (message: string) => Promise<void> | void;
  t: Translate;
}): Promise<void> {
  const { draft, state, editing, bridge, t } = args;
  if (state.kind === "create") {
    if (draft.type === "openclaw") {
      await bridge.CreateOpenClawAgentBackend(
        { ...draft } as agent_backend_svc.CreateBackendRequest,
        args.openClawToken,
      );
    } else {
      await bridge.CreateAgentBackend({
        ...draft,
      } as agent_backend_svc.CreateBackendRequest);
    }
    // 最近使用只在 target 成功持久化后记录（spec 决策 19）；native/inherit 不进入。
    recordRecentTarget("backend", draft.deviceId, {
      providerKey: draft.llmProviderKey,
      modelKey: draft.llmModelKey,
    });
    await args.onSaved(t("agentBackends.flash.created"));
  } else if (state.kind === "edit" && editing) {
    const request = {
      id: editing.id,
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
    } as unknown as agent_backend_svc.UpdateBackendRequest;
    if (draft.type === "openclaw") {
      await bridge.UpdateOpenClawAgentBackend(
        request,
        args.openClawToken,
        args.clearOpenClawToken,
      );
    } else {
      await bridge.UpdateAgentBackend(request);
    }
    // 最近使用只在 target 成功持久化后记录（spec 决策 19）；native/inherit 不进入。
    recordRecentTarget("backend", draft.deviceId, {
      providerKey: draft.llmProviderKey,
      modelKey: draft.llmModelKey,
    });
    await args.onSaved(t("agentBackends.flash.saved"));
  }
}
