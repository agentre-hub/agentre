import * as React from "react";
import { useUiTranslation as useTranslation } from "../i18n";
import {
  AlertCircle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  ExternalLink,
  Loader2,
  Lock,
  Pencil,
  Plus,
  Puzzle,
  Radar,
  SendHorizontal,
  Trash2,
  X,
} from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "./ui/alert";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";
import { agentreUiResources } from "../i18n";

const i18n = { t: (key: string, _options?: Record<string, unknown>) => key.split(".").reduce<unknown>((value, part) => (value as Record<string, unknown>)?.[part], agentreUiResources.en) as string ?? key };
import { cn } from "../lib/utils";

import {
  resolveModelTarget,
  truncateFlashText,
  type ResolvedModelTarget,
} from "./agent-backends-utils";
import {
  AgentBackendLogo,
  LlmModelLogo,
  LlmProviderLogo,
} from "./ai-brand-logo";
import {
  CancelTestAgentBackend,
  CreateAgentBackend,
  CreateOpenClawAgentBackend,
  DeleteAgentBackend,
  GetGatewayStatus,
  ListAgentBackends,
  ListLLMProviders,
  RemoteDeviceFingerprint,
  RemoteDeviceList,
  RemoteDeviceListProviders,
  RemoteDeviceSyncProvider,
  ResolveAgentBackendCLIPath,
  ServerListDevices,
  ScanAndCreateAgentBackends,
  TestAgentBackend,
  TestOpenClawAgentBackend,
  UpdateAgentBackend,
  UpdateOpenClawAgentBackend,
} from "./port-bridge";
import {
  agent_backend_svc,
  httpgateway,
  llm_provider_svc,
} from "./port-bridge";
import { bindEngineSettingsPorts, EventsOn } from "./port-bridge";
import type { EngineSettingsPorts } from "./ports";
import { AgentreDialog } from "./app-dialog";
import {
  ModelTargetPicker,
  recordRecentTarget,
  useModelTargetCatalog,
  type PickerProvider,
} from "./model-target-picker";
import {
  OPENCLAW_DEFAULT_GATEWAY_URL,
  OPENCLAW_SESSION_MODE,
  OpenClawBackendFields,
} from "./openclaw-backend-fields";
import {
  OPENCLAW_ERROR_KEY_BY_CODE,
  openClawDraftIssue,
} from "./openclaw-validation";
import {
  deviceSelectValue,
  pairedDeviceSelectValue,
  persistedDeviceIdForSelection,
  resolveExecutionDevice,
} from "./device-identity";

type Backend = agent_backend_svc.BackendItem;
type Provider = llm_provider_svc.ProviderItem;
type BackendType = "builtin" | "claudecode" | "codex" | "piagent" | "openclaw";
type Translate = ReturnType<typeof useTranslation>["t"];

// DeviceView — local shim matching remote_device_svc.DeviceView.
// Device DTO is defined locally to keep the package host-independent.
type DeviceView = {
  id: number;
  name: string;
  online: boolean;
  daemonFingerprint?: string;
  supportsLLMModelTarget?: boolean;
};
// remote_device_watcher_svc 的在线态推送通道（与 session-exec-target / 设备面板同一条）。
const REMOTE_DEVICE_STATE_EVENT = "remote.device.state";
type ProviderSummary = {
  key?: string;
  name?: string;
  type?: string;
  defaultModelKey?: string;
  models?: {
    key: string;
    modelId: string;
    name?: string;
    enabled: boolean;
  }[];
};

// 选择器里的展示顺序：三个 CLI 引擎在前（最常用且需要装命令行），内置与网关收尾。
// openclaw 排最后是因为它在两列网格里独占整行，避免出现空格子。
const BACKEND_TYPE_ORDER: BackendType[] = [
  "claudecode",
  "codex",
  "piagent",
  "builtin",
  "openclaw",
];

// 打开新建对话框时会对这三个类型各探一次目标机的 $PATH。
const CLI_BACKEND_TYPES: BackendType[] = ["claudecode", "codex", "piagent"];

// probing → 请求在飞；installed/missing → 目标机 $PATH 的结论；
// failed → 远端不可达（离线 / 超时 / 探测报错），此时不能谎报「未安装」。
type CLIProbeState = "probing" | "installed" | "missing" | "failed";
type CLIProbe = { state: CLIProbeState; path: string };

const backendTypeMeta: Record<
  BackendType,
  {
    disabled: boolean;
  }
> = {
  builtin: {
    disabled: false,
  },
  claudecode: {
    disabled: false,
  },
  codex: {
    disabled: false,
  },
  piagent: {
    disabled: false,
  },
  openclaw: {
    disabled: false,
  },
};

type EditorState =
  | { kind: "closed" }
  | { kind: "create" }
  | { kind: "edit"; backend: Backend; cliPath?: string; openBinding?: boolean };

type FlashState =
  | { kind: "ok"; text: string }
  | { kind: "err"; text: string }
  | null;

type SandboxValue = "" | "read-only" | "workspace-write" | "danger-full-access";
type ApprovalValue = "" | "untrusted" | "on-request" | "never";
type ReasoningEffortValue = "" | "low" | "medium" | "high" | "xhigh" | "max";
// RouteTarget 是 Claude Tier Route 的结构化目标（与后端 DTO 同形）：
// providerKey 空 = inherit-main；modelKey 空 = provider-default。
type RouteTarget = { providerKey: string; modelKey: string };
type BackendDraft = {
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
type PendingProviderSync = {
  draft: BackendDraft;
  providerKeys: string[];
  saveAfterSync: boolean;
};

// codex CLI 支持到 xhigh；UI 不暴露 max，避免「保存了 max 实际上下发 high」的迷惑。
// 类型切到 codex 时若历史值是 max，会自动降为 high（buildDraft / handleTypeChange）。
const REASONING_EFFORTS_FULL: ReasoningEffortValue[] = [
  "",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
];
const REASONING_EFFORTS_CODEX: ReasoningEffortValue[] = [
  "",
  "low",
  "medium",
  "high",
  "xhigh",
];
function normalizeForCodex(v: ReasoningEffortValue): ReasoningEffortValue {
  return v === "max" ? "high" : v;
}

type EnvEntry = { key: string; value: string };

const CLAUDE_TIERS = ["OPUS", "SONNET", "HAIKU"] as const;
type ClaudeTier = (typeof CLAUDE_TIERS)[number];

const APPROVAL_OPTIONS: {
  value: Exclude<ApprovalValue, "">;
}[] = [{ value: "untrusted" }, { value: "on-request" }, { value: "never" }];

const SANDBOX_OPTIONS: {
  value: Exclude<SandboxValue, "">;
  label: string;
}[] = [
  { value: "read-only", label: "read-only" },
  { value: "workspace-write", label: "workspace-write" },
  { value: "danger-full-access", label: "danger-full-access" },
];

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

const LOCAL_DEVICE_SELECT_VALUE = "__local_device__";

function matchingProviders(t: BackendType, providers: Provider[]) {
  if (t === "claudecode")
    return providers.filter((p) => p.type === "anthropic");
  if (t === "codex")
    return providers.filter((p) => p.type === "openai-response");
  // piagent 三类全收（anthropic / openai-chat / openai-response）：直接全列。
  return providers;
}

function isCliBackend(t: BackendType): boolean {
  return t === "claudecode" || t === "codex" || t === "piagent";
}

function cliBinaryName(t: BackendType): string {
  if (t === "claudecode") return "claude";
  if (t === "piagent") return "pi";
  return "codex";
}

// 在「目标机」的 $PATH 里找 t 对应的可执行文件。deviceId 空串 = 本机，
// 非空 = 让 agent_backend_svc 派发到那台远端 daemon 去扫它自己的 $PATH。
// 远端不可达时这里会 throw，交给调用方决定怎么呈现。
function probeCLIPath(t: BackendType, deviceId: string) {
  return ResolveAgentBackendCLIPath({
    type: t,
    deviceId,
  } as unknown as agent_backend_svc.ResolveCLIPathRequest);
}

// parseRoutes 把后端 DTO 的类型化 modelRoutes 解析成三档 Record。
function parseRoutes(
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

function safeParseEnv(s: string): EnvEntry[] {
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

function serializeEnv(entries: EnvEntry[]): string {
  const out: Record<string, string> = {};
  for (const e of entries) {
    const k = e.key.trim();
    if (!k) continue;
    out[k] = e.value;
  }
  return Object.keys(out).length === 0 ? "{}" : JSON.stringify(out);
}

function emptyRoutes(): Record<ClaudeTier, RouteTarget> {
  return {
    OPUS: { providerKey: "", modelKey: "" },
    SONNET: { providerKey: "", modelKey: "" },
    HAIKU: { providerKey: "", modelKey: "" },
  };
}

// routeTargets 把非空的 tier route 收进提交用的 map（继承主绑定的空 target 不提交）。
function routeTargetsForRequest(
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

function referencedProviderKeys(draft: BackendDraft): string[] {
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

function providerLabel(key: string, providers: Provider[]): string {
  const p = providers.find(
    (item) => item.providerKey === key || String(item.id) === key,
  );
  if (!p) return key;
  return p.name;
}

function openClawProbeErrorMessage(
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

export function AgentBackendsPanel({
  ports,
  onOpenLlmProviders,
  onOpenProxySettings,
  renderHeader,
}: {
  ports: EngineSettingsPorts;
  onOpenLlmProviders?: () => void;
  onOpenProxySettings?: () => void;
  // 页头由宿主渲染，面板把自己的页级操作（自动识别 / 新建后端）交进去：按钮要落在
  // H1 行，而它们开的创建弹窗、扫描进行态仍归面板持有。
  renderHeader?: (actions: React.ReactNode) => React.ReactNode;
}) {
  bindEngineSettingsPorts(ports);
  const { t } = useTranslation();
  const [backends, setBackends] = React.useState<Backend[]>([]);
  const [providers, setProviders] = React.useState<Provider[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [editor, setEditor] = React.useState<EditorState>({ kind: "closed" });
  const [pendingDelete, setPendingDelete] = React.useState<Backend | null>(
    null,
  );
  const [flash, setFlash] = React.useState<FlashState>(null);
  const [testingId, setTestingId] = React.useState<number | null>(null);
  const [scanning, setScanning] = React.useState(false);
  // 当前正在跑的 TestAgentBackend 的 requestId；用户点取消时拿它去后端 CancelTest。
  // 用 ref 而不是 state，避免 await TestAgentBackend 拿到的是闭包里的旧值。
  const testReqIdRef = React.useRef<string | null>(null);

  function flashFromTestResponse(res: agent_backend_svc.TestBackendResponse) {
    if (res.ok) {
      setFlash({
        kind: "ok",
        text: `✅ ${res.latencyMs}ms · ${res.message}`,
      });
    } else {
      setFlash({ kind: "err", text: `❌ ${res.message}` });
    }
  }

  async function openEditor(backend: Backend, openBinding = false) {
    const cliPath = await ports.cliPath?.get(backend.syncId) ?? "";
    setEditor({ kind: "edit", backend, cliPath, openBinding });
  }

  async function handleTestRow(backend: Backend) {
    if (testingId !== null) return;
    const requestId = newRequestId();
    testReqIdRef.current = requestId;
    setTestingId(backend.id);
    try {
      const request = {
        id: backend.id,
        useDraft: false,
        type: "",
        name: "",
        llmProviderKey: "",
        cliPath: "",
        requestId,
      } as agent_backend_svc.TestBackendRequest;
      const res =
        backend.type === "openclaw"
          ? await TestOpenClawAgentBackend(request, "")
          : await TestAgentBackend(request);
      // 用户在等待期间点了取消 → testReqIdRef 已被清掉，丢弃 stale 响应。
      if (testReqIdRef.current !== requestId) return;
      flashFromTestResponse(res);
    } catch (err) {
      if (testReqIdRef.current !== requestId) return;
      setFlash({ kind: "err", text: messageFromError(err) });
    } finally {
      if (testReqIdRef.current === requestId) {
        testReqIdRef.current = null;
        setTestingId(null);
      }
    }
  }

  async function handleCancelRow() {
    const requestId = testReqIdRef.current;
    if (!requestId) return;
    // 立刻清前端 in-flight 状态，UI 即时恢复。Backend 收到 cancel 后 prober ctx Done，
    // 那个 await TestAgentBackend 还会返回，但 stale 检测会丢弃它。
    testReqIdRef.current = null;
    setTestingId(null);
    try {
      await CancelTestAgentBackend({
        requestId,
      } as agent_backend_svc.CancelTestBackendRequest);
    } catch {
      // best effort — 后端不响应 cancel 也别刷红 flash。
    }
  }

  async function handleAutoScan() {
    if (scanning) return;
    setScanning(true);
    setFlash(null);
    try {
      const res = await ScanAndCreateAgentBackends();
      const results = res?.results ?? [];

      const created = results.filter((r) => r.created);
      const skipped = results.filter((r) => r.skipped);
      const foundAny = results.some((r) => r.found);

      if (created.length > 0) {
        const createdNames = created.map((r) => r.name).join(", ");
        if (skipped.length > 0) {
          const skippedNames = skipped.map((r) => r.name).join(", ");
          setFlash({
            kind: "ok",
            text: t("agentBackends.autoScan.partialFound", {
              createdCount: created.length,
              createdNames,
              skippedCount: skipped.length,
              skippedNames,
            }),
          });
        } else {
          setFlash({
            kind: "ok",
            text: t("agentBackends.autoScan.created", {
              count: created.length,
              names: createdNames,
            }),
          });
        }
        await reload();
      } else if (skipped.length > 0) {
        const names = skipped.map((r) => r.name).join(", ");
        setFlash({
          kind: "ok",
          text: t("agentBackends.autoScan.skipped", {
            count: skipped.length,
            names,
          }),
        });
      } else if (!foundAny) {
        setFlash({
          kind: "err",
          text: t("agentBackends.autoScan.nothingFound"),
        });
      }
    } catch (err) {
      setFlash({ kind: "err", text: messageFromError(err) });
    } finally {
      setScanning(false);
    }
  }

  const reload = React.useCallback(async () => {
    setLoading(true);
    try {
      const [b, p] = await Promise.all([
        ListAgentBackends(),
        ListLLMProviders(),
      ]);
      setBackends(b?.items ?? []);
      setProviders(p?.items ?? []);
    } catch (err) {
      setFlash({ kind: "err", text: messageFromError(err) });
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    let mounted = true;
    Promise.all([ListAgentBackends(), ListLLMProviders()])
      .then(([b, p]) => {
        if (!mounted) return;
        setBackends(b?.items ?? []);
        setProviders(p?.items ?? []);
      })
      .catch((err: unknown) => {
        if (!mounted) return;
        setFlash({ kind: "err", text: messageFromError(err) });
      })
      .finally(() => {
        if (!mounted) return;
        setLoading(false);
      });
    return () => {
      mounted = false;
    };
    // reload is for explicit refreshes only; initial load runs directly
  }, []);

  // 页级操作落在 H1 行。「新建后端」在空态下让位给空态自带的 CTA——全页始终只有一个
  // 新建入口；「自动识别」恰恰在空态最有用，所以一直留在标题行。
  const headerActions = (
    <>
      <Button
        type="button"
        size="sm"
        variant="outline"
        className="h-[30px] gap-1.5 px-3 text-xs"
        onClick={handleAutoScan}
        disabled={scanning}
        title={t("agentBackends.autoScan.buttonTitle")}
      >
        {scanning ? (
          <Loader2
            className="size-3.5 animate-spin"
            data-icon="inline-start"
            aria-hidden="true"
          />
        ) : (
          <Radar
            className="size-3.5"
            data-icon="inline-start"
            aria-hidden="true"
          />
        )}
        {scanning
          ? t("agentBackends.autoScan.scanning")
          : t("agentBackends.autoScan.button")}
      </Button>
      {loading || backends.length === 0 ? null : (
        <Button
          type="button"
          size="sm"
          data-testid="agent-backend-create"
          className="h-[30px] gap-1.5 px-3 text-xs"
          onClick={() => setEditor({ kind: "create" })}
        >
          <Plus data-icon="inline-start" aria-hidden="true" />
          {t("agentBackends.page.add")}
        </Button>
      )}
    </>
  );

  return (
    <>
      {renderHeader?.(headerActions)}
      <section className="min-w-0 overflow-hidden rounded-lg border border-border bg-card">
        {flash ? (
          <FlashBanner state={flash} onDismiss={() => setFlash(null)} />
        ) : null}
        <div className="flex min-w-0 flex-col">
          {loading ? (
            <div className="flex items-center justify-center gap-2 px-4 py-6 text-xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              {t("common.loading")}
            </div>
          ) : backends.length === 0 ? (
            <AgentBackendsEmptyState
              onCreate={() => setEditor({ kind: "create" })}
              onOpenLlmProviders={onOpenLlmProviders}
            />
          ) : (
            <div
              role="list"
              aria-label={t("agentBackends.list.ariaLabel")}
              className="flex min-w-0 flex-col"
            >
              {backends.map((b) => (
                <BackendRow
                  key={b.id}
                  backend={b}
                  testing={testingId === b.id}
                  testDisabled={testingId !== null}
                  onTest={() => handleTestRow(b)}
                  onCancelTest={handleCancelRow}
                  onEdit={() => void openEditor(b)}
                  onChangeBinding={() => void openEditor(b, true)}
                  onDelete={() => setPendingDelete(b)}
                />
              ))}
            </div>
          )}
        </div>

        {editor.kind !== "closed" ? (
          <BackendEditor
            state={editor}
            providers={providers}
            onClose={() => setEditor({ kind: "closed" })}
            onSaved={async (message) => {
              setEditor({ kind: "closed" });
              setFlash({ kind: "ok", text: message });
              await reload();
            }}
            onOpenProxySettings={onOpenProxySettings}
            onOpenLlmProviders={onOpenLlmProviders}
          />
        ) : null}

        {pendingDelete ? (
          <DeleteDialog
            backend={pendingDelete}
            onCancel={() => setPendingDelete(null)}
            onConfirmed={async () => {
              setPendingDelete(null);
              setFlash({ kind: "ok", text: t("agentBackends.flash.deleted") });
              await reload();
            }}
            onError={(text) => {
              setPendingDelete(null);
              setFlash({ kind: "err", text });
            }}
          />
        ) : null}
      </section>
    </>
  );
}

function AgentBackendsEmptyState({
  onCreate,
  onOpenLlmProviders,
}: {
  onCreate: () => void;
  onOpenLlmProviders?: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-6 py-10 text-center">
      <div
        aria-hidden="true"
        className="relative flex size-12 items-center justify-center rounded-full bg-primary-soft text-primary-text"
      >
        <Puzzle className="size-5" />
      </div>
      <div className="flex max-w-md flex-col gap-1">
        <p className="text-sm font-semibold">
          {t("agentBackends.empty.title")}
        </p>
        <p className="text-2xs leading-relaxed text-muted-foreground">
          {t("agentBackends.empty.description")}
        </p>
      </div>
      <div className="flex flex-wrap items-center justify-center gap-2">
        <Button
          type="button"
          size="sm"
          className="h-[30px] gap-1.5 px-3 text-xs"
          onClick={onCreate}
        >
          <Plus data-icon="inline-start" aria-hidden="true" />
          {t("agentBackends.empty.addFirst")}
        </Button>
        {onOpenLlmProviders ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-[30px] gap-1.5 px-3 text-xs"
            onClick={onOpenLlmProviders}
          >
            {t("agentBackends.empty.addProvider")}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

// 一行后端的绑定面包屑有四种形态：网关托管 / 走 CLI 自身登录 / 绑定失效 / 正常绑定。
type BindingVariant = "openclaw" | "cli-login" | "invalid" | "bound";

// 绑定长在元数据行上（供应商 › 模型 + 跟随默认/固定），不再独立成块 —— 一眼看清绑了什么。
function BackendRowBinding({
  backend,
  variant,
}: {
  backend: Backend;
  variant: BindingVariant;
}) {
  const { t } = useTranslation();
  const providerKey =
    (backend as unknown as { llmProviderKey?: string }).llmProviderKey ?? "";
  const modelKey =
    (backend as unknown as { llmModelKey?: string }).llmModelKey ?? "";
  const follow = modelKey === "";
  return (
    <span
      data-testid="backend-binding"
      className={cn(
        "inline-flex min-w-0 shrink items-center gap-1 rounded-md px-1.5 py-0.5",
        variant === "invalid"
          ? "bg-status-waiting-bg text-status-waiting"
          : "bg-secondary text-foreground",
      )}
    >
      {variant === "openclaw" ? (
        <>
          <LlmModelLogo
            providerType="openclaw"
            model={
              backend.openClawDefaultModel ||
              backend.openClawAgentId ||
              "openclaw"
            }
            className="size-3.5 shrink-0"
          />
          <span className="truncate">
            {backend.openClawDefaultModel ||
              backend.openClawAgentId ||
              t("agentBackends.openclaw.modelGatewayDefault")}
          </span>
        </>
      ) : variant === "cli-login" ? (
        <>
          <Lock className="size-3 shrink-0" aria-hidden="true" />
          <span className="truncate">
            {t("agentBackends.row.bindingCliLogin")}
          </span>
        </>
      ) : variant === "invalid" ? (
        <>
          <AlertCircle className="size-3 shrink-0" aria-hidden="true" />
          <span className="shrink-0 font-medium">
            {t("agentBackends.row.bindingInvalid")}
          </span>
          <span className="text-decorative-foreground" aria-hidden="true">
            ·
          </span>
          <span className="truncate">
            {t("agentBackends.row.bindingInvalidReason", {
              provider: backend.llmProviderName || providerKey,
              model: backend.llmProviderModel || modelKey,
            })}
          </span>
        </>
      ) : (
        <>
          <LlmProviderLogo
            providerType={backend.llmProviderType ?? ""}
            providerName={backend.llmProviderName ?? ""}
            className="size-3.5 shrink-0"
          />
          <span className="truncate">{backend.llmProviderName}</span>
          <span
            className="shrink-0 text-decorative-foreground"
            aria-hidden="true"
          >
            ›
          </span>
          <span className="truncate font-mono">
            {backend.llmProviderModel || t("agentBackends.provider.noModel")}
          </span>
          <Badge
            variant="secondary"
            className={cn(
              "shrink-0 rounded-sm px-1 py-0 text-2xs font-normal",
              follow && "bg-primary-soft text-primary-text",
            )}
          >
            {follow
              ? t("agentBackends.binding.modeFollow")
              : t("agentBackends.binding.modeFixed")}
          </Badge>
        </>
      )}
    </span>
  );
}

function BackendRow({
  backend,
  testing,
  testDisabled,
  onTest,
  onCancelTest,
  onEdit,
  onChangeBinding,
  onDelete,
}: {
  backend: Backend;
  testing: boolean;
  testDisabled: boolean;
  onTest: () => void;
  onCancelTest: () => void;
  onEdit: () => void;
  onChangeBinding: () => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation();
  const typ = (backend.type as BackendType) ?? "builtin";
  const cliBased = isCliBackend(typ);
  const openClaw = typ === "openclaw";
  // 未关联 provider 的 CLI 后端 = 走 CLI 自身 login，不算需处理。
  const providerKey =
    (backend as unknown as { llmProviderKey?: string }).llmProviderKey ?? "";
  const unlinkedCli = cliBased && providerKey === "";
  const warning = !openClaw && !unlinkedCli && !backend.llmProviderActive;
  const bindingVariant: BindingVariant = openClaw
    ? "openclaw"
    : unlinkedCli
      ? "cli-login"
      : warning
        ? "invalid"
        : "bound";

  // 类型回到名字旁的 chip;元数据行只留「绑定面包屑 · 运行位置 · 引用数」，一行说清
  // 「绑了谁、在哪跑、谁在用」。
  const metaTail: string[] = [
    openClaw && backend.openClawGatewayUrl
      ? backend.openClawGatewayUrl
      : // deviceName 为空 = 未关联远端设备 = 跑在本机。
        (backend.deviceName || "").trim() ||
        t("agentBackends.device.localShort"),
    backend.agentCount > 0
      ? t("agentBackends.row.agentCount", { count: backend.agentCount })
      : t("agentBackends.row.unused"),
  ];

  return (
    <div
      role="listitem"
      className={cn(
        "flex min-w-0 flex-col gap-1.5 border-b border-border px-4 py-3 transition-colors last:border-b-0 hover:bg-accent/45",
        // 需处理的行用琥珀色左缘标记，一眼可扫；用 inset 阴影避免改变行宽。
        warning && "shadow-[inset_3px_0_0_0_var(--status-waiting)]",
      )}
    >
      <div className="flex min-w-0 items-center gap-2.5">
        <AgentBackendLogo backendType={typ} className="size-7 rounded-md" />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex min-w-0 items-center gap-1.5">
            <span
              data-selectable-text="true"
              className="min-w-0 truncate text-sm font-semibold"
            >
              {backend.name}
            </span>
            <Badge
              data-testid="backend-type-chip"
              variant="secondary"
              className="shrink-0 rounded-sm px-1.5 py-0 text-2xs font-normal"
            >
              {t(`agentBackends.backendType.${typ}.label`)}
            </Badge>
            {warning ? (
              <Badge
                variant="secondary"
                className="shrink-0 rounded-sm bg-status-waiting-bg px-1.5 py-0 font-mono text-2xs text-status-waiting"
              >
                {t("agentBackends.row.needsAction")}
              </Badge>
            ) : null}
          </div>
          <div
            data-testid="backend-meta"
            className="flex min-w-0 items-center gap-1.5 text-2xs text-muted-foreground"
          >
            <BackendRowBinding backend={backend} variant={bindingVariant} />
            <span className="min-w-0 truncate">
              {metaTail.map((part, i) => (
                <React.Fragment key={i}>
                  <span
                    className="mx-1 text-decorative-foreground"
                    aria-hidden="true"
                  >
                    ·
                  </span>
                  {part}
                </React.Fragment>
              ))}
            </span>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {!openClaw ? (
            <Button
              type="button"
              variant={warning ? "default" : "outline"}
              size="xs"
              // 绑定已失效时这不是「换一个」而是「必须重选」，动词跟着状态走。
              aria-label={
                warning
                  ? t("agentBackends.actions.rebindNamed", {
                      name: backend.name,
                    })
                  : t("agentBackends.actions.changeBindingNamed", {
                      name: backend.name,
                    })
              }
              onClick={onChangeBinding}
            >
              {warning
                ? t("agentBackends.actions.rebind")
                : t("agentBackends.actions.changeBinding")}
            </Button>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            // testing 时按钮变成"取消测试"，必须保持可点击；其它行 testDisabled 仍 disable。
            aria-label={
              testing
                ? t("agentBackends.actions.cancelTestNamed", {
                    name: backend.name,
                  })
                : t("agentBackends.actions.testNamed", { name: backend.name })
            }
            title={
              testing
                ? t("agentBackends.actions.cancelTest")
                : t("agentBackends.actions.test")
            }
            className={cn(
              "size-[26px]",
              testing ? "text-status-error" : "text-muted-foreground",
            )}
            disabled={testDisabled && !testing}
            onClick={testing ? onCancelTest : onTest}
          >
            {testing ? (
              <X data-icon="only" aria-hidden="true" />
            ) : (
              <SendHorizontal data-icon="only" aria-hidden="true" />
            )}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t("agentBackends.actions.editNamed", {
              name: backend.name,
            })}
            title={t("common.edit")}
            className="size-[26px] text-muted-foreground"
            onClick={onEdit}
          >
            <Pencil data-icon="only" aria-hidden="true" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t("agentBackends.actions.deleteNamed", {
              name: backend.name,
            })}
            title={t("common.delete")}
            className="size-[26px] text-status-error"
            onClick={onDelete}
          >
            <Trash2 data-icon="only" aria-hidden="true" />
          </Button>
        </div>
      </div>
    </div>
  );
}

function BackendEditor({
  state,
  providers,
  onClose,
  onSaved,
  onOpenProxySettings,
  onOpenLlmProviders,
}: {
  state: EditorState;
  providers: Provider[];
  onClose: () => void;
  onSaved: (message: string) => Promise<void> | void;
  onOpenProxySettings?: () => void;
  onOpenLlmProviders?: () => void;
}) {
  const { t } = useTranslation();
  const editing = state.kind === "edit" ? state.backend : null;
  const initialType: BackendType = (editing?.type as BackendType) ?? "builtin";

  const [type, setType] = React.useState<BackendType>(initialType);
  const [name, setName] = React.useState(editing?.name ?? "");
  const [cliPath, setCliPath] = React.useState(
    state.kind === "edit" ? state.cliPath ?? "" : "",
  );
  const [llmProviderKey, setLlmProviderKey] = React.useState<string>(
    () =>
      (editing as unknown as { llmProviderKey?: string } | null)
        ?.llmProviderKey ?? "",
  );
  // llmModelKey 主绑定固定模型（空 = provider-default）。
  const [llmModelKey, setLlmModelKey] = React.useState<string>(
    (editing as unknown as { llmModelKey?: string } | null)?.llmModelKey ?? "",
  );
  const [routes, setRoutes] = React.useState<Record<ClaudeTier, RouteTarget>>(
    () =>
      parseRoutes(
        (
          editing as unknown as {
            modelRoutes?: Record<string, RouteTarget>;
          } | null
        )?.modelRoutes,
      ),
  );
  const [sandbox, setSandbox] = React.useState<SandboxValue>(
    (editing?.sandbox as SandboxValue) ?? "",
  );
  const [approval, setApproval] = React.useState<ApprovalValue>(
    (editing?.approval as ApprovalValue) ?? "",
  );
  const [envEntries, setEnvEntries] = React.useState<EnvEntry[]>(() =>
    safeParseEnv(editing?.envJson ?? ""),
  );
  const [reasoningEffort, setReasoningEffort] =
    React.useState<ReasoningEffortValue>(
      (editing?.reasoningEffort as ReasoningEffortValue) || "",
    );
  const [defaultPermissionMode, setDefaultPermissionMode] =
    React.useState<string>(editing?.defaultPermissionMode || "");
  const [defaultModel, setDefaultModel] = React.useState<string>(
    editing?.defaultModel || "",
  );
  const [openClawGatewayURL, setOpenClawGatewayURL] = React.useState(
    editing?.openClawGatewayUrl || OPENCLAW_DEFAULT_GATEWAY_URL,
  );
  const [openClawAgentID, setOpenClawAgentID] = React.useState(
    editing?.openClawAgentId ?? "",
  );
  const [openClawDefaultModel, setOpenClawDefaultModel] = React.useState(
    editing?.openClawDefaultModel ?? "",
  );
  const [openClawToken, setOpenClawToken] = React.useState("");
  const [clearOpenClawToken, setClearOpenClawToken] = React.useState(false);
  const [openClawProbe, setOpenClawProbe] =
    React.useState<agent_backend_svc.TestBackendResponse | null>(null);
  const [deviceId, setDeviceId] = React.useState<string>(
    editing?.deviceId ?? "",
  );
  const [devices, setDevices] = React.useState<DeviceView[]>([]);
  const [localFingerprint, setLocalFingerprint] = React.useState("");
  const [accountDeviceNames, setAccountDeviceNames] = React.useState<
    Map<string, string>
  >(new Map());
  const [advancedOpen, setAdvancedOpen] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const [pendingProviderSync, setPendingProviderSync] =
    React.useState<PendingProviderSync | null>(null);
  const [providerSyncError, setProviderSyncError] = React.useState<
    string | null
  >(null);
  const [syncingProvider, setSyncingProvider] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  const [saveResult, setSaveResult] = React.useState<FlashState>(null);
  const [testResult, setTestResult] = React.useState<FlashState>(null);
  const [gatewayStatus, setGatewayStatus] =
    React.useState<httpgateway.GatewayStatus | null>(null);
  const [cliProbing, setCliProbing] = React.useState(false);
  // 「$PATH 没挂到 binary」的提示文案；命中后清空。
  const [cliProbeMiss, setCliProbeMiss] = React.useState<string | null>(null);
  // 名称一旦被用户敲过就不再跟着类型走；编辑态本来就带着既有名字，视同已敲过。
  const nameTouchedRef = React.useRef(state.kind === "edit");
  // 类型选择器右侧徽标的数据源：三个 CLI 引擎在目标机 $PATH 里的探测结果。
  const [cliProbes, setCliProbes] = React.useState<
    Partial<Record<BackendType, CLIProbe>>
  >({});
  const cliProbeGenerationRef = React.useRef(0);

  const filteredProviders = React.useMemo(
    () => matchingProviders(type, providers),
    [type, providers],
  );

  // Picker 目录：每个 provider 的模型目录（供主目标 + Claude tier 路由使用）。
  const {
    catalog: targetCatalog,
    loading: catalogLoading,
    error: catalogError,
  } = useModelTargetCatalog(providers);

  const autoProviderKey =
    state.kind === "create" &&
    type === "builtin" &&
    llmProviderKey === "" &&
    filteredProviders[0]
      ? (filteredProviders[0].providerKey ?? String(filteredProviders[0].id))
      : "";
  const effectiveLlmProviderKey = llmProviderKey || autoProviderKey;

  // detectCLIPath 调后端 ResolveAgentBackendCLIPath；非 CLI 类型直接返回 null。
  // 选了远端 device 时把 deviceId 一起传过去，让 agent_backend_svc 按 device 派发到远端 daemon。
  // 注意：远端调用可能 throw（设备离线 / 超时 / 探测失败），调用方需要自行决定要不要兜底。
  // - handleTypeChange 的隐式自动填：用 .catch(() => undefined) 静默吞错，避免打扰新建流程
  // - handleDetectCli 的显式按钮：catch 后落到 cliProbeMiss 文案槽
  async function detectCLIPath(
    t: BackendType,
    dev: string = "",
  ): Promise<string | null> {
    if (!isCliBackend(t)) return null;
    const r = await probeCLIPath(t, dev);
    return r.found ? r.path : null;
  }

  // 新建时对三个 CLI 引擎各探一次目标机的 $PATH，让「装没装」在选类型这一步就可见，
  // 而不是选完之后才在 CLI 路径字段撞墙。换运行设备（本机 ↔ 远端）要整组重探。
  // 探测不阻塞选择：飞行中的类型照样可以点。
  React.useEffect(() => {
    if (state.kind !== "create") return;
    const generation = ++cliProbeGenerationRef.current;
    setCliProbes(
      Object.fromEntries(
        CLI_BACKEND_TYPES.map((cliType) => [
          cliType,
          { state: "probing", path: "" },
        ]),
      ) as Partial<Record<BackendType, CLIProbe>>,
    );
    for (const cliType of CLI_BACKEND_TYPES) {
      void (async () => {
        let probe: CLIProbe;
        try {
          const r = await probeCLIPath(cliType, deviceId);
          probe = {
            state: r.found ? "installed" : "missing",
            path: r.found ? r.path : "",
          };
        } catch {
          // 远端离线 / 超时 / 探测报错：只能说「没探到」，不能说「没装」。
          probe = { state: "failed", path: "" };
        }
        // 换设备后旧一轮的迟到结果直接丢弃，避免徽标显示上一台机器的结论。
        if (cliProbeGenerationRef.current !== generation) return;
        setCliProbes((prev) => ({ ...prev, [cliType]: probe }));
      })();
    }
  }, [state.kind, deviceId]);

  // 没被用户敲过的名称跟着「设备 · 类型」走，省掉一次手输。
  // 只有 CLI 引擎带设备前缀 —— 它们才能派到远端；内置 / 网关直接用类型名。
  function defaultBackendName(bt: BackendType, dev: string): string {
    if (!isCliBackend(bt)) return t(`agentBackends.backendType.${bt}.label`);
    const deviceName = deviceDisplayName(dev);
    return t("agentBackends.name.deviceDefault", {
      device: deviceName,
      name: t(`agentBackends.backendType.${bt}.shortLabel`),
    });
  }

  function handleDeviceChange(nextDeviceId: string) {
    setDeviceId(nextDeviceId);
    if (!nameTouchedRef.current) {
      setName(defaultBackendName(type, nextDeviceId));
    }
  }

  function handleTypeChange(nextType: BackendType) {
    setSaveResult(null);
    setType(nextType);
    if (!nameTouchedRef.current) {
      // openclaw 会把 deviceId 清空，名字用同一个「清空后」的设备算，避免留下上一台机器。
      setName(
        defaultBackendName(nextType, nextType === "openclaw" ? "" : deviceId),
      );
    }
    setLlmProviderKey("");
    setLlmModelKey("");
    setRoutes(emptyRoutes());
    setSandbox("");
    setApproval("");
    setTestResult(null);
    setOpenClawProbe(null);
    setOpenClawToken("");
    setClearOpenClawToken(false);
    if (nextType === "openclaw") {
      setDeviceId("");
      setOpenClawGatewayURL(OPENCLAW_DEFAULT_GATEWAY_URL);
      setOpenClawAgentID("");
      setOpenClawDefaultModel("");
    }
    // 切离 claudecode 时清空 default permission mode / default model：
    // entity.Check 仅放行 claudecode + 非空。
    if (nextType !== "claudecode") {
      setDefaultPermissionMode("");
      setDefaultModel("");
    }
    // 切到 codex 时把 max 自动降到 high，避免「保存了一个 codex 不支持的档位」。
    if (nextType === "codex") {
      setReasoningEffort((cur) => normalizeForCodex(cur));
    }
    // 切类型时清空 cliPath，避免 claude / codex 两个不同的可执行文件串台。
    setCliPath("");
    setCliProbeMiss(null);
    // create 模式下切到 CLI 类型要把识别到的路径自动填进去；用户随时可手改/清空。
    // edit 模式不渲染选择器，所以这里不会跑；编辑场景只靠 Input 旁的「自动识别」按钮。
    if (state.kind === "create" && isCliBackend(nextType)) {
      const probed = cliProbes[nextType];
      if (probed?.state === "installed") {
        // 打开对话框时那一轮探测已经给出结论，直接复用 —— 远端设备上这能省掉一次真实往返，
        // 而方向键换选项会逐个触发本函数，代价按键盘步数累加。
        setCliPath(probed.path);
      } else if (probed?.state !== "missing") {
        // 只有「还没探完 / 探测失败」才补一发；已知未安装就不用再问一次。
        void (async () => {
          // 新建流程的隐式自动填：静默吞错，远端不可达就当没识别到。
          const path = await detectCLIPath(nextType, deviceId).catch(
            () => null,
          );
          if (path) setCliPath(path);
        })();
      }
    }
  }

  // 手动「自动识别」按钮：无论命中与否都给用户视觉反馈。命中时覆盖当前值。
  async function handleDetectCli() {
    if (cliProbing) return;
    setCliProbing(true);
    setCliProbeMiss(null);
    try {
      const path = await detectCLIPath(type, deviceId);
      if (path) {
        setCliPath(path);
      } else {
        setCliProbeMiss(
          t("agentBackends.cli.notFound", { bin: cliBinaryName(type) }),
        );
      }
    } catch (e) {
      // 远端报错（设备离线 / 超时 / 探测失败）也要给用户反馈，避免 unhandled promise rejection。
      setCliProbeMiss(e instanceof Error ? e.message : String(e));
    } finally {
      setCliProbing(false);
    }
  }

  const cliBased = isCliBackend(type);
  React.useEffect(() => {
    if (!cliBased) return;
    let mounted = true;
    GetGatewayStatus()
      .then((s) => {
        if (mounted) setGatewayStatus(s);
      })
      .catch(() => {});
    return () => {
      mounted = false;
    };
  }, [cliBased]);

  // Fetch paired remote devices when the dialog opens (or re-opens).
  //
  // 顺序是有意的，不是可以并发的两个读：账号设备的收编发生在 ServerListDevices
  // **内部**（app.ServerListDevices → AdoptAccountDevices 写 paired_agentreds）。
  // 并发时 RemoteDeviceList 大概率先返回，这一次就读不到刚收编的那一行 ——
  // 用户得关掉弹窗重开才看得见那台机器。所以先让收编跑完，再读配对行。
  // 指纹与账号清单之间没有依赖，仍可并发。
  React.useEffect(() => {
    if (state.kind === "closed") return;
    let cancelled = false;
    void (async () => {
      try {
        const [fingerprint, accountDevices] = await Promise.all([
          RemoteDeviceFingerprint(),
          ServerListDevices().catch(() => []),
        ]);
        const rows = await RemoteDeviceList();
        if (cancelled) return;
        setDevices((rows ?? []) as unknown as DeviceView[]);
        setLocalFingerprint(fingerprint ?? "");
        setAccountDeviceNames(
          new Map(
            (accountDevices ?? [])
              .filter((device) => device.Fingerprint)
              .map((device) => [device.Fingerprint, device.Name] as const),
          ),
        );
      } catch {
        if (cancelled) return;
        setDevices([]);
        setLocalFingerprint("");
        setAccountDeviceNames(new Map());
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [state.kind]);

  // 运行设备下拉按 online 禁用选项，而在线态在弹窗开着的时候会翻转：刚收编的那一行
  // watcher 才开始拨号，那一刻 online=false。不订阅这条既有推送，那个灰掉的选项要
  // 等到用户关掉弹窗重开才会变可选。只改在线态，不重拉清单 —— 行集合没变。
  React.useEffect(() => {
    if (state.kind === "closed") return;
    const off = EventsOn(REMOTE_DEVICE_STATE_EVENT, (payload: unknown) => {
      const ev = payload as {
        id: number;
        name: string;
        online: boolean;
      };
      setDevices((prev) =>
        prev.map((d) =>
          d.id === ev.id
            ? { ...d, name: ev.name || d.name, online: ev.online }
            : d,
        ),
      );
    });
    return () => off?.();
  }, [state.kind]);

  // 远端执行时以目标 daemon 目录为可运行事实源（task 6 决策 12）：拉一次该设备的
  // Provider/Model 目录 + 能力位，传给 Picker 做远端门控（desktop 独有的行禁用、
  // 旧 daemon 禁用 fixed-model）。daemon 离线时目录为空 → 未验证的 fixed-model 无法保存。
  const executionDevice = React.useMemo(
    () => resolveExecutionDevice(deviceId, localFingerprint, devices),
    [deviceId, devices, localFingerprint],
  );
  const remoteDeviceID = executionDevice.pairedDeviceId;
  const remoteExecution = executionDevice.remote;
  // 只有存在已配对的 agentred 行时，本机才真能把供应商同步过去；本机自身指纹与
  // 账号内其它桌面端都没有这条通道，不提供做不到的同步入口。
  const canSyncProvider = remoteDeviceID > 0;
  const selectedDeviceValue = deviceSelectValue(
    deviceId,
    localFingerprint,
    LOCAL_DEVICE_SELECT_VALUE,
  );
  const selectedDeviceKnown =
    selectedDeviceValue === LOCAL_DEVICE_SELECT_VALUE ||
    devices.some(
      (candidate) => pairedDeviceSelectValue(candidate) === selectedDeviceValue,
    );
  const deviceDisplayName = React.useCallback(
    (value: string) => {
      if (
        value === "" ||
        (localFingerprint !== "" && value === localFingerprint)
      ) {
        return t("agentBackends.device.localShort");
      }
      return (
        devices.find(
          (candidate) => pairedDeviceSelectValue(candidate) === value,
        )?.name ||
        accountDeviceNames.get(value) ||
        value
      );
    },
    [accountDeviceNames, devices, localFingerprint, t],
  );
  const [remoteProviders, setRemoteProviders] = React.useState<
    ProviderSummary[]
  >([]);
  const remoteProviderRequestRef = React.useRef(0);
  const refreshRemoteProviders = React.useCallback(async () => {
    const request = ++remoteProviderRequestRef.current;
    if (state.kind === "closed" || remoteDeviceID <= 0) {
      setRemoteProviders([]);
      return;
    }
    try {
      const rows = await RemoteDeviceListProviders(remoteDeviceID);
      if (remoteProviderRequestRef.current === request) {
        setRemoteProviders((rows ?? []) as ProviderSummary[]);
      }
    } catch {
      if (remoteProviderRequestRef.current === request) {
        setRemoteProviders([]);
      }
    }
  }, [state.kind, remoteDeviceID]);
  React.useEffect(() => {
    void refreshRemoteProviders();
  }, [refreshRemoteProviders]);

  const remoteSupportsFixedModel = React.useMemo(() => {
    const dv = devices.find((d) => d.id === remoteDeviceID);
    return dv?.supportsLLMModelTarget ?? false;
  }, [devices, remoteDeviceID]);

  // 把 daemon 目录转成 PickerProvider[]（非敏感摘要）：供 Picker 判断哪些 desktop
  // 行在 daemon 上不存在 / 模型未同步，以及 fixed-model 是否被能力位允许。
  const remotePickerCatalog = React.useMemo<PickerProvider[]>(() => {
    if (remoteDeviceID <= 0) return [];
    return remoteProviders.map((p) => {
      const models = (p.models ?? []).map((m) => ({
        modelKey: m.key,
        modelId: m.modelId,
        name: m.name,
        enabled: m.enabled,
      }));
      const defaultModel =
        (p.defaultModelKey &&
          models.find((m) => m.modelKey === p.defaultModelKey)) ||
        null;
      return {
        providerKey: p.key ?? "",
        id: 0,
        name: p.name ?? p.key ?? "",
        type: p.type ?? "",
        enabled: true,
        defaultModel,
        models,
      };
    });
  }, [remoteDeviceID, remoteProviders]);

  const reservedOffenders = React.useMemo(
    () =>
      envEntries
        .map((e) => e.key.trim())
        .filter((k) => k && RESERVED_ENV_KEYS.has(k)),
    [envEntries],
  );

  const open = state.kind !== "closed";

  function buildDraft(): BackendDraft {
    // 三种 backend 都保留 reasoningEffort；codex 二次兜底 normalize（防止历史脏数据 / 跨 type 残留）。
    const effort: ReasoningEffortValue =
      type === "openclaw"
        ? ""
        : type === "codex"
          ? normalizeForCodex(reasoningEffort)
          : reasoningEffort;
    return {
      type,
      name,
      // builtin 后端只能在本地运行（无 HTTP 网关路由到 daemon），强制清空以防误保存。
      deviceId:
        type === "builtin"
          ? ""
          : type === "openclaw"
            ? (editing?.deviceId ?? "")
            : deviceId,
      llmProviderKey: type === "openclaw" ? "" : effectiveLlmProviderKey,
      // openclaw 不绑定 Agentre ProviderModel（spec 决策 4/22）。
      llmModelKey: type === "openclaw" ? "" : llmModelKey.trim(),
      cliPath: isCliBackend(type) ? cliPath.trim() : "",
      modelRoutes: type === "claudecode" ? routeTargetsForRequest(routes) : {},
      sandbox: type === "codex" ? sandbox : "",
      approval: type === "codex" ? approval : "",
      envJson: isCliBackend(type) ? serializeEnv(envEntries) : "{}",
      reasoningEffort: effort,
      defaultPermissionMode: type === "claudecode" ? defaultPermissionMode : "",
      defaultModel: type === "claudecode" ? defaultModel.trim() : "",
      openClawGatewayUrl: type === "openclaw" ? openClawGatewayURL.trim() : "",
      openClawAgentId: type === "openclaw" ? openClawAgentID.trim() : "",
      openClawDefaultModel:
        type === "openclaw" ? openClawDefaultModel.trim() : "",
      openClawSessionMode: type === "openclaw" ? OPENCLAW_SESSION_MODE : "",
    };
  }

  type RemoteDraftInspection = {
    missingProviderKeys: string[];
    targetIssue: "fixedUnsupported" | "syncNeeded" | null;
  };

  async function inspectRemoteDraft(
    draft: BackendDraft,
  ): Promise<RemoteDraftInspection> {
    const resolved = resolveExecutionDevice(
      draft.deviceId,
      localFingerprint,
      devices,
    );
    if (!resolved.remote) {
      return { missingProviderKeys: [], targetIssue: null };
    }

    const providerKeys = referencedProviderKeys(draft);
    if (providerKeys.length === 0) {
      return { missingProviderKeys: [], targetIssue: null };
    }
    const deviceID = resolved.pairedDeviceId;
    // 本机没有通往该设备的已配对行（典型是账号内另一台桌面端）：这台机器读不到它的
    // 目录，也同步不过去。此时既不能断言"供应商缺失"，也不能拿一个做不到的同步挡住
    // 保存 —— 未验证就是未验证，按无结论放行（门控仍在 Picker 侧禁用未验证目标）。
    if (deviceID <= 0) {
      return { missingProviderKeys: [], targetIssue: null };
    }

    const remoteRaw = (await RemoteDeviceListProviders(deviceID)) as
      | ProviderSummary[]
      | null
      | undefined;
    const remoteByKey = new Map(
      (remoteRaw ?? [])
        .filter((provider) => provider.key)
        .map((provider) => [provider.key ?? "", provider] as const),
    );
    const missingProviderKeys = providerKeys.filter(
      (key) => !remoteByKey.has(key),
    );
    if (missingProviderKeys.length > 0) {
      return { missingProviderKeys, targetIssue: null };
    }

    const targets: RouteTarget[] = [];
    if (draft.llmProviderKey) {
      targets.push({
        providerKey: draft.llmProviderKey,
        modelKey: draft.llmModelKey,
      });
    }
    if (draft.type === "claudecode") {
      targets.push(...Object.values(draft.modelRoutes));
    }
    const supportsFixedModel =
      devices.find((device) => device.id === deviceID)
        ?.supportsLLMModelTarget ?? false;
    for (const target of targets) {
      const providerKey = target.providerKey.trim();
      if (!providerKey) continue;
      const remoteProvider = remoteByKey.get(providerKey);
      if (!remoteProvider) continue;
      const modelKey = target.modelKey.trim();
      if (modelKey) {
        if (!supportsFixedModel) {
          return {
            missingProviderKeys: [],
            targetIssue: "fixedUnsupported",
          };
        }
        const remoteModel = (remoteProvider.models ?? []).find(
          (model) => model.key === modelKey && model.enabled,
        );
        if (!remoteModel) {
          return { missingProviderKeys: [], targetIssue: "syncNeeded" };
        }
        continue;
      }
      const localDefaultModelKey = targetCatalog.find(
        (provider) => provider.providerKey === providerKey,
      )?.defaultModel?.modelKey;
      if (localDefaultModelKey) {
        const remoteDefaultModel = (remoteProvider.models ?? []).find(
          (model) =>
            model.key === remoteProvider.defaultModelKey && model.enabled,
        );
        if (
          remoteProvider.defaultModelKey !== localDefaultModelKey ||
          !remoteDefaultModel
        ) {
          return { missingProviderKeys: [], targetIssue: "syncNeeded" };
        }
      }
    }
    return { missingProviderKeys: [], targetIssue: null };
  }

  function remoteTargetIssueMessage(
    issue: RemoteDraftInspection["targetIssue"],
  ): string {
    return issue === "fixedUnsupported"
      ? t("modelTargetPicker.fixedModelUnsupported")
      : t("modelTargetPicker.remoteSyncNeeded");
  }

  async function saveDraft(draft: BackendDraft) {
    if (state.kind === "create") {
      if (draft.type === "openclaw") {
        await CreateOpenClawAgentBackend(
          { ...draft } as agent_backend_svc.CreateBackendRequest,
          openClawToken,
        );
      } else {
        await CreateAgentBackend({
          ...draft,
        } as agent_backend_svc.CreateBackendRequest);
      }
      // 最近使用只在 target 成功持久化后记录（spec 决策 19）；native/inherit 不进入。
      recordRecentTarget("backend", draft.deviceId, {
        providerKey: draft.llmProviderKey,
        modelKey: draft.llmModelKey,
      });
      await onSaved(t("agentBackends.flash.created"));
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
        await UpdateOpenClawAgentBackend(
          request,
          openClawToken,
          clearOpenClawToken,
        );
      } else {
        await UpdateAgentBackend(request);
      }
      // 最近使用只在 target 成功持久化后记录（spec 决策 19）；native/inherit 不进入。
      recordRecentTarget("backend", draft.deviceId, {
        providerKey: draft.llmProviderKey,
        modelKey: draft.llmModelKey,
      });
      await onSaved(t("agentBackends.flash.saved"));
    }
  }

  // 同 handleTestRow：用 ref 跟踪 in-flight requestId，方便点取消时丢弃 stale 响应。
  const testReqIdRef = React.useRef<string | null>(null);

  async function handleTest() {
    if (testing || submitting) return;
    if (isCliBackend(type) && reservedOffenders.length > 0) {
      setTestResult({
        kind: "err",
        text: t("agentBackends.env.reservedDisabled", {
          keys: reservedOffenders.join(", "),
        }),
      });
      setAdvancedOpen(true);
      return;
    }
    const requestId = newRequestId();
    testReqIdRef.current = requestId;
    setTesting(true);
    setTestResult(null);
    setOpenClawProbe(null);
    try {
      const draft = buildDraft();
      const request = {
        id: state.kind === "edit" && editing ? editing.id : 0,
        useDraft: true,
        ...draft,
        requestId,
      } as unknown as agent_backend_svc.TestBackendRequest;
      const res =
        type === "openclaw"
          ? await TestOpenClawAgentBackend(request, openClawToken)
          : await TestAgentBackend(request);
      if (testReqIdRef.current !== requestId) return;
      if (res.ok) {
        if (type === "openclaw") {
          setOpenClawProbe(res);
          if (openClawAgentID === "" && (res.openClawAgents ?? []).length > 0) {
            const selected =
              res.openClawAgents.find((agent) => agent.default) ??
              res.openClawAgents[0];
            setOpenClawAgentID(selected.id);
          }
          if (
            openClawDefaultModel === "" &&
            (res.openClawModels ?? []).length > 0
          ) {
            const available =
              res.openClawModels.find((model) => model.available) ??
              res.openClawModels[0];
            setOpenClawDefaultModel(available.id);
          }
        }
        setTestResult({
          kind: "ok",
          text:
            type === "openclaw"
              ? t("agentBackends.openclaw.probePassed", {
                  latency: res.latencyMs,
                })
              : t("agentBackends.test.passed", {
                  latency: res.latencyMs,
                  message: res.message,
                }),
        });
      } else {
        setTestResult({
          kind: "err",
          text:
            type === "openclaw"
              ? openClawProbeErrorMessage(res.code ?? "", res.message ?? "", t)
              : res.message,
        });
      }
    } catch (err) {
      if (testReqIdRef.current !== requestId) return;
      setTestResult({ kind: "err", text: messageFromError(err) });
    } finally {
      if (testReqIdRef.current === requestId) {
        testReqIdRef.current = null;
        setTesting(false);
      }
    }
  }

  async function handleCancelTest() {
    const requestId = testReqIdRef.current;
    if (!requestId) return;
    testReqIdRef.current = null;
    setTesting(false);
    setTestResult(null);
    try {
      await CancelTestAgentBackend({
        requestId,
      } as agent_backend_svc.CancelTestBackendRequest);
    } catch {
      // best effort
    }
  }

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (submitting) return;
    setSaveResult(null);
    if (isCliBackend(type) && reservedOffenders.length > 0) {
      setSaveResult({
        kind: "err",
        text: t("agentBackends.env.reservedDisabled", {
          keys: reservedOffenders.join(", "),
        }),
      });
      setAdvancedOpen(true);
      return;
    }
    // OpenClaw 的保存路径只能拿到后端错误字符串(Wails 边界无结构化通道),不预校验
    // 的话用户会看到后端 i18n 的中文「参数错误」。规则与服务端一致,服务端仍是权威。
    if (type === "openclaw") {
      const issue = openClawDraftIssue({
        name,
        gatewayURL: openClawGatewayURL,
        sessionMode: OPENCLAW_SESSION_MODE,
      });
      if (issue) {
        setSaveResult({
          kind: "err",
          text: openClawProbeErrorMessage(issue, "", t),
        });
        return;
      }
    }
    setSubmitting(true);
    try {
      const draft = buildDraft();
      const inspection = await inspectRemoteDraft(draft);
      if (inspection.missingProviderKeys.length > 0) {
        const missingKeys = inspection.missingProviderKeys;
        setProviderSyncError(null);
        setPendingProviderSync({
          draft,
          providerKeys: missingKeys,
          saveAfterSync: true,
        });
        return;
      }
      if (inspection.targetIssue) {
        setSaveResult({
          kind: "err",
          text: remoteTargetIssueMessage(inspection.targetIssue),
        });
        return;
      }
      await saveDraft(draft);
    } catch (err) {
      setSaveResult({ kind: "err", text: messageFromError(err) });
    } finally {
      setSubmitting(false);
    }
  }

  async function handleConfirmProviderSync() {
    if (!pendingProviderSync || syncingProvider) return;
    const deviceID = resolveExecutionDevice(
      pendingProviderSync.draft.deviceId,
      localFingerprint,
      devices,
    ).pairedDeviceId;
    if (deviceID <= 0) return;
    const saveAfterSync = pendingProviderSync.saveAfterSync;
    setSyncingProvider(true);
    setSubmitting(saveAfterSync);
    setProviderSyncError(null);
    try {
      for (const key of pendingProviderSync.providerKeys) {
        await RemoteDeviceSyncProvider(deviceID, key);
      }
      const draft = pendingProviderSync.draft;
      if (saveAfterSync) {
        const inspection = await inspectRemoteDraft(draft);
        if (
          inspection.missingProviderKeys.length > 0 ||
          inspection.targetIssue
        ) {
          setProviderSyncError(
            inspection.targetIssue
              ? remoteTargetIssueMessage(inspection.targetIssue)
              : t("modelTargetPicker.remoteSyncNeeded"),
          );
          return;
        }
        await saveDraft(draft);
      } else {
        setPendingProviderSync(null);
        await refreshRemoteProviders();
        setSaveResult({
          kind: "ok",
          text: t("agentBackends.flash.providerSynced"),
        });
      }
    } catch (err) {
      setProviderSyncError(providerSyncMessageFromError(err));
    } finally {
      setSyncingProvider(false);
      setSubmitting(false);
    }
  }

  // 同步入口只做同步（复制凭证到目标设备），不改动草稿里的模型绑定：用户点它是为了
  // 让某个供应商在目标设备上可用，不是为了改选它（改选走 Picker 的选项行）。
  function handlePickerProviderSync(provider: PickerProvider) {
    setProviderSyncError(null);
    setPendingProviderSync({
      draft: buildDraft(),
      providerKeys: [provider.providerKey],
      saveAfterSync: false,
    });
  }

  function handleManualProviderSync() {
    if (!canSyncProvider) return;
    const draft = buildDraft();
    const keys = referencedProviderKeys(draft);
    if (keys.length === 0) return;
    setProviderSyncError(null);
    setPendingProviderSync({
      draft,
      providerKeys: keys,
      saveAfterSync: false,
    });
  }

  function closeProviderSyncDialog() {
    setPendingProviderSync(null);
    setProviderSyncError(null);
  }

  // builtin 必须有 provider；CLI 自身登录、OpenClaw 走 Gateway 认证，都允许未关联。
  const providerOptional = isCliBackend(type) || type === "openclaw";
  // piagent 绑定时 provider-default / fixed-model 都必须最终解析到可用模型
  // （spec「ModelTarget contract」）。目录加载完成且能确定目标解析不到模型时才前置拦截。
  const selectedTargetProvider = targetCatalog.find(
    (p) => p.providerKey === effectiveLlmProviderKey,
  );
  const piAgentModelMissing =
    type === "piagent" &&
    effectiveLlmProviderKey !== "" &&
    !!selectedTargetProvider &&
    (llmModelKey !== ""
      ? !selectedTargetProvider.models.some(
          (m) => m.modelKey === llmModelKey && m.enabled,
        )
      : !selectedTargetProvider.defaultModel);
  // 主目标是否已失效：绑定了 provider/model 但目录里解析不出来（Provider/Model 缺失/停用）。
  const mainTargetInvalid =
    effectiveLlmProviderKey !== "" &&
    (!selectedTargetProvider ||
      !selectedTargetProvider.enabled ||
      (llmModelKey !== ""
        ? !selectedTargetProvider.models.some(
            (m) => m.modelKey === llmModelKey && m.enabled,
          )
        : !selectedTargetProvider.defaultModel?.enabled));
  const resolvedMainTarget = resolveModelTarget(
    effectiveLlmProviderKey,
    llmModelKey,
    targetCatalog,
  );
  const openClawIssue =
    type === "openclaw"
      ? openClawDraftIssue({
          name,
          gatewayURL: openClawGatewayURL,
          sessionMode: OPENCLAW_SESSION_MODE,
        })
      : null;
  const saveBlockedReason =
    name.trim() === ""
      ? t("agentBackends.summary.reasons.nameRequired")
      : mainTargetInvalid
        ? t("agentBackends.summary.reasons.invalidTarget")
        : piAgentModelMissing
          ? t("agentBackends.provider.modelRequiredTitle")
          : !providerOptional &&
              (filteredProviders.length === 0 || effectiveLlmProviderKey === "")
            ? t("agentBackends.summary.reasons.bindingRequired")
            : isCliBackend(type) && reservedOffenders.length > 0
              ? t("agentBackends.env.reservedDisabled", {
                  keys: reservedOffenders.join(", "),
                })
              : openClawIssue
                ? openClawProbeErrorMessage(openClawIssue, "", t)
                : null;
  const effectiveSaveBlockedReason =
    type !== "openclaw" && resolvedMainTarget.mode === "invalid"
      ? t("agentBackends.summary.reasons.invalidTarget")
      : saveBlockedReason;
  const submitDisabled = submitting || effectiveSaveBlockedReason !== null;
  const manualProviderSyncKeys = canSyncProvider
    ? referencedProviderKeys(buildDraft())
    : [];
  const showManualProviderSync =
    canSyncProvider && manualProviderSyncKeys.length > 0;

  return (
    <>
      <AgentreDialog
        open={open}
        onOpenChange={(o) => (!o ? onClose() : undefined)}
        title={
          state.kind === "edit"
            ? t("agentBackends.editor.editTitle")
            : t("agentBackends.editor.createTitle")
        }
        description={t("agentBackends.editor.description")}
        contentClassName="max-w-xl"
        bodyClassName="flex flex-col gap-4"
        onSubmit={handleSubmit}
        footerClassName="flex-col items-stretch gap-2"
        footer={
          <>
            {saveResult ? <TestResultPill state={saveResult} /> : null}
            {testResult ? <TestResultPill state={testResult} /> : null}
            <div className="flex w-full items-center gap-2">
              {testing ? (
                <Button
                  type="button"
                  variant="outline"
                  onClick={handleCancelTest}
                  className="gap-1.5 text-status-error"
                >
                  <X className="size-3.5" aria-hidden="true" />
                  {t("agentBackends.actions.cancelTest")}
                </Button>
              ) : (
                <Button
                  type="button"
                  variant="outline"
                  disabled={
                    submitting || syncingProvider || piAgentModelMissing
                  }
                  onClick={handleTest}
                  className="gap-1.5"
                >
                  <SendHorizontal className="size-3.5" aria-hidden="true" />
                  {t("agentBackends.actions.test")}
                </Button>
              )}
              <div className="ml-auto flex items-center gap-2">
                <Button
                  type="button"
                  variant="outline"
                  onClick={onClose}
                  disabled={submitting || syncingProvider}
                >
                  {t("common.cancel")}
                </Button>
                <Button
                  type="submit"
                  disabled={submitDisabled || syncingProvider}
                >
                  {submitting ? t("common.saving") : t("common.save")}
                </Button>
              </div>
            </div>
          </>
        }
      >
        {/* 类型排在名称之前：它决定了下面出现哪些字段，也决定名称的默认值。 */}
        {state.kind === "edit" ? (
          <BackendTypeReadonly type={type} />
        ) : (
          <div className="flex flex-col gap-1.5 text-xs">
            <span className="font-medium">
              {t("agentBackends.fields.type")}
            </span>
            <BackendTypePicker
              value={type}
              onChange={handleTypeChange}
              probes={cliProbes}
            />
          </div>
        )}

        <label className="flex flex-col gap-1.5 text-xs">
          <span className="font-medium">{t("agentBackends.fields.name")}</span>
          <Input
            value={name}
            onChange={(e) => {
              nameTouchedRef.current = true;
              setName(e.target.value);
            }}
            placeholder={t("agentBackends.fields.namePlaceholder")}
            required
            autoFocus
          />
        </label>

        <div className="flex flex-col gap-1.5 text-xs">
          <span className="font-medium">
            {t("agentBackends.fields.device")}
          </span>
          <Select
            value={selectedDeviceValue}
            onValueChange={(v) =>
              handleDeviceChange(
                persistedDeviceIdForSelection(
                  v,
                  LOCAL_DEVICE_SELECT_VALUE,
                  localFingerprint,
                ),
              )
            }
            disabled={type === "builtin" || type === "openclaw"}
          >
            <SelectTrigger aria-label={t("agentBackends.fields.device")}>
              <SelectValue
                placeholder={t("agentBackends.device.placeholder")}
              />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={LOCAL_DEVICE_SELECT_VALUE}>
                {t("agentBackends.device.local")}
              </SelectItem>
              {devices.map((d) => (
                <SelectItem
                  key={d.id}
                  value={pairedDeviceSelectValue(d)}
                  disabled={!d.online}
                >
                  📡 {d.name}
                  {d.online ? "" : t("agentBackends.device.offlineSuffix")}
                </SelectItem>
              ))}
              {!selectedDeviceKnown && deviceId ? (
                <SelectItem value={deviceId} disabled>
                  📡 {editing?.deviceName || deviceDisplayName(deviceId)}
                </SelectItem>
              ) : null}
            </SelectContent>
          </Select>
          {type === "builtin" ? (
            <span className="text-2xs text-muted-foreground">
              {t("agentBackends.device.builtinLocalOnly")}
            </span>
          ) : null}
        </div>

        {type !== "openclaw" ? (
          <ModelBindingSection
            type={type}
            providers={filteredProviders}
            value={effectiveLlmProviderKey}
            modelKey={llmModelKey}
            onTargetChange={({ providerKey, modelKey }) => {
              setLlmProviderKey(providerKey);
              setLlmModelKey(modelKey);
            }}
            onSyncProvider={
              canSyncProvider ? handlePickerProviderSync : undefined
            }
            invalid={mainTargetInvalid}
            piAgentModelMissing={piAgentModelMissing}
            editing={!!editing}
            onOpenLlmProviders={onOpenLlmProviders}
            catalog={targetCatalog}
            catalogLoading={catalogLoading}
            catalogError={catalogError}
            executionLocation={remoteExecution ? deviceId : ""}
            supportsFixedModel={
              remoteExecution ? remoteSupportsFixedModel : true
            }
            remoteCatalog={remoteExecution ? remotePickerCatalog : undefined}
            routes={routes}
            onRoutesChange={setRoutes}
            customModel={defaultModel}
            onCustomModelChange={setDefaultModel}
            resolvedMainTarget={resolvedMainTarget}
            openPickerOnMount={state.kind === "edit" && !!state.openBinding}
          />
        ) : (
          <OpenClawBackendFields
            gatewayURL={openClawGatewayURL}
            onGatewayURLChange={(value) => {
              setOpenClawGatewayURL(value);
              setOpenClawProbe(null);
            }}
            token={openClawToken}
            onTokenChange={(value) => {
              setOpenClawToken(value);
              if (value !== "") setClearOpenClawToken(false);
              setOpenClawProbe(null);
            }}
            hasToken={editing?.hasToken ?? false}
            clearToken={clearOpenClawToken}
            onClearTokenChange={(value) => {
              setClearOpenClawToken(value);
              if (value) setOpenClawToken("");
            }}
            agentID={openClawAgentID}
            onAgentIDChange={(value) => {
              setOpenClawAgentID(value);
              setOpenClawProbe(null);
            }}
            defaultModel={openClawDefaultModel}
            onDefaultModelChange={(value) => {
              setOpenClawDefaultModel(value);
              setOpenClawProbe(null);
            }}
            probe={openClawProbe}
          />
        )}

        {showManualProviderSync ? (
          <Alert className="border-border bg-secondary text-xs">
            <Radar className="size-4" aria-hidden="true" />
            <AlertTitle className="text-xs">
              {t("agentBackends.providerSync.inlineTitle")}
            </AlertTitle>
            <AlertDescription className="flex flex-col gap-2 text-2xs">
              <span>{t("agentBackends.providerSync.inlineDescription")}</span>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="self-start"
                disabled={syncingProvider}
                onClick={handleManualProviderSync}
              >
                {t("agentBackends.providerSync.syncRemote")}
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}

        {cliBased ? (
          <CliPathField
            type={type}
            value={cliPath}
            onChange={(v) => {
              setCliPath(v);
              if (cliProbeMiss) setCliProbeMiss(null);
            }}
            onDetect={handleDetectCli}
            detecting={cliProbing}
            missMessage={cliProbeMiss}
          />
        ) : null}

        {type === "claudecode" ? (
          <DefaultPermissionModeField
            value={defaultPermissionMode}
            onChange={setDefaultPermissionMode}
            isRemote={remoteExecution}
            hasIsSandbox={envEntries.some(
              (e) => e.key.trim() === "IS_SANDBOX" && e.value.trim() !== "",
            )}
            onAddIsSandbox={() => {
              setEnvEntries((prev) => {
                const idx = prev.findIndex(
                  (e) => e.key.trim() === "IS_SANDBOX",
                );
                if (idx >= 0) {
                  const next = prev.slice();
                  next[idx] = { key: "IS_SANDBOX", value: "1" };
                  return next;
                }
                return [...prev, { key: "IS_SANDBOX", value: "1" }];
              });
              // env_json 默认折叠;一键填后展开让用户能看见结果
              setAdvancedOpen(true);
            }}
          />
        ) : null}

        {type === "codex" ? (
          <>
            <SandboxField value={sandbox} onChange={setSandbox} />
            <ApprovalField value={approval} onChange={setApproval} />
          </>
        ) : null}

        <EffectiveConfigSummary
          type={type}
          deviceName={deviceDisplayName(deviceId)}
          cliPath={cliBased ? cliPath : ""}
          resolvedMainTarget={resolvedMainTarget}
          customModel={defaultModel}
          routes={routes}
          catalog={targetCatalog}
          referenceCount={editing?.agentCount ?? 0}
          saveBlockedReason={effectiveSaveBlockedReason}
          openClawModel={openClawDefaultModel || openClawAgentID}
        />

        {type !== "openclaw" ? (
          <ReasoningEffortField
            type={type}
            value={reasoningEffort}
            onChange={setReasoningEffort}
          />
        ) : null}

        {cliBased ? (
          <EnvJsonField
            entries={envEntries}
            onChange={setEnvEntries}
            open={advancedOpen}
            onToggle={() => setAdvancedOpen((o) => !o)}
            reservedOffenders={reservedOffenders}
          />
        ) : null}

        {cliBased ? (
          <ProxyNote
            status={gatewayStatus}
            providerLinked={llmProviderKey !== ""}
            onOpenProxySettings={onOpenProxySettings}
          />
        ) : null}
      </AgentreDialog>
      {pendingProviderSync ? (
        <AgentreDialog
          open
          onOpenChange={(o) =>
            !o && !syncingProvider ? closeProviderSyncDialog() : undefined
          }
          title={t("agentBackends.providerSync.title")}
          description={
            pendingProviderSync.saveAfterSync
              ? t("agentBackends.providerSync.descriptionSave")
              : t("agentBackends.providerSync.descriptionOnly")
          }
          bodyClassName="flex flex-col gap-3"
          footer={
            <div className="flex w-full items-center justify-end gap-2">
              <Button
                type="button"
                variant="outline"
                disabled={syncingProvider}
                onClick={closeProviderSyncDialog}
              >
                {t("common.cancel")}
              </Button>
              <Button
                type="button"
                disabled={syncingProvider}
                onClick={handleConfirmProviderSync}
              >
                {syncingProvider ? (
                  <Loader2
                    className="size-3.5 animate-spin"
                    aria-hidden="true"
                  />
                ) : null}
                {syncingProvider
                  ? t("agentBackends.providerSync.syncing")
                  : pendingProviderSync.saveAfterSync
                    ? t("agentBackends.providerSync.syncAndSave")
                    : t("agentBackends.providerSync.syncRemote")}
              </Button>
            </div>
          }
        >
          <Alert className="border-status-waiting/40 bg-status-waiting-bg text-xs">
            <AlertCircle className="size-4" aria-hidden="true" />
            <AlertTitle className="text-xs">
              {t("agentBackends.providerSync.requiredTitle")}
            </AlertTitle>
            <AlertDescription className="text-2xs">
              {t("agentBackends.providerSync.requiredDescription")}
            </AlertDescription>
          </Alert>
          {providerSyncError ? (
            <Alert className="border-status-error/40 bg-status-error-bg text-xs">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertTitle className="text-xs">
                {t("agentBackends.providerSync.failedTitle")}
              </AlertTitle>
              <AlertDescription className="whitespace-pre-line text-2xs">
                {providerSyncError}
              </AlertDescription>
            </Alert>
          ) : null}
          <div className="flex flex-col gap-1.5 text-xs">
            {pendingProviderSync.providerKeys.map((key) => (
              <div
                key={key}
                className="flex items-center justify-between rounded-md border border-border bg-secondary px-2 py-1.5"
              >
                <span className="min-w-0 truncate">
                  {providerLabel(key, providers)}
                </span>
                <span className="ml-2 shrink-0 font-mono text-2xs text-muted-foreground">
                  {key}
                </span>
              </div>
            ))}
          </div>
        </AgentreDialog>
      ) : null}
    </>
  );
}

// 类型选择器：两列卡片，每张只放「logo + 名称 + 一个徽标」。
// 不放整句说明 —— 徽标只承载「选之前才有用、选之后就看不到」的事实
// （CLI 装没装 / 内置只能跑本机），其余前置条件选中后表单自己会呈现。
function BackendTypePicker({
  value,
  onChange,
  probes,
}: {
  value: BackendType;
  onChange: (v: BackendType) => void;
  probes: Partial<Record<BackendType, CLIProbe>>;
}) {
  const { t } = useTranslation();
  const groupRef = React.useRef<HTMLDivElement>(null);

  // radiogroup 的键盘契约：方向键换选项并把焦点带过去（Tab 只进出整组）。
  function moveSelection(delta: number) {
    const from = BACKEND_TYPE_ORDER.indexOf(value);
    const total = BACKEND_TYPE_ORDER.length;
    const next = BACKEND_TYPE_ORDER[(from + delta + total) % total];
    onChange(next);
    groupRef.current
      ?.querySelector<HTMLButtonElement>(`[data-backend-type="${next}"]`)
      ?.focus();
  }

  return (
    <div
      ref={groupRef}
      role="radiogroup"
      aria-label={t("agentBackends.fields.type")}
      // 对话框是 w-full max-w-xl —— 窗口窄于 sm 时它跟着缩，两列会把
      // 「Claude Code CLI + 未安装」挤到截断，所以窄窗退回单列。
      className="grid grid-cols-1 gap-1.5 sm:grid-cols-2"
      onKeyDown={(e) => {
        if (e.key === "ArrowDown" || e.key === "ArrowRight") {
          e.preventDefault();
          moveSelection(1);
        } else if (e.key === "ArrowUp" || e.key === "ArrowLeft") {
          e.preventDefault();
          moveSelection(-1);
        }
      }}
    >
      {BACKEND_TYPE_ORDER.map((backendType) => {
        const checked = value === backendType;
        return (
          <button
            key={backendType}
            type="button"
            role="radio"
            aria-checked={checked}
            data-backend-type={backendType}
            disabled={backendTypeMeta[backendType].disabled}
            // roving tabindex：整组只有选中项可 Tab 聚焦。
            tabIndex={checked ? 0 : -1}
            onClick={() => onChange(backendType)}
            className={cn(
              "flex min-w-0 items-center gap-2 rounded-md border px-2.5 py-2 text-left outline-none transition-colors",
              "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50",
              "disabled:pointer-events-none disabled:opacity-50",
              checked
                ? "border-primary bg-primary-soft"
                : "border-border bg-card hover:border-border-strong hover:bg-accent/60",
              // 5 个选项排两列会空一格,让网关独占末行收尾。单列时不能加 span-2:
              // 那会在 1 列的网格里撑出一条隐式列,把整行顶出容器。
              backendType === "openclaw" && "sm:col-span-2",
            )}
          >
            <span aria-hidden="true">
              <AgentBackendLogo backendType={backendType} className="size-4" />
            </span>
            <span className="min-w-0 flex-1 truncate text-xs font-semibold">
              {t(`agentBackends.backendType.${backendType}.label`)}
            </span>
            <BackendTypeBadge type={backendType} probe={probes[backendType]} />
          </button>
        );
      })}
    </div>
  );
}

function BackendTypeBadge({
  type,
  probe,
}: {
  type: BackendType;
  probe?: CLIProbe;
}) {
  const { t } = useTranslation();
  // 内置引擎的意外之处是「运行设备」会被禁用 —— 提前说，省得用户选完才发现派不出去。
  if (type === "builtin") {
    return (
      <span className="shrink-0 rounded-full border border-border bg-secondary px-1.5 text-2xs text-muted-foreground">
        {t("agentBackends.backendType.builtin.badge")}
      </span>
    );
  }
  // 网关不给徽标：它要填什么，选中之后表单自己会说。
  if (!isCliBackend(type) || !probe) return null;

  const tone =
    probe.state === "installed"
      ? "border-status-running/30 bg-status-running-bg text-status-running"
      : probe.state === "probing"
        ? "border-border bg-secondary text-muted-foreground"
        : "border-status-waiting/30 bg-status-waiting-bg text-status-waiting";
  return (
    <span
      // e2e 用它断言探测结论，避免拿 i18n 文案当定位符。
      data-probe-state={probe.state}
      className={cn(
        "flex shrink-0 items-center gap-1 rounded-full border px-1.5 text-2xs",
        tone,
      )}
      title={probe.state === "installed" ? probe.path : undefined}
    >
      {probe.state === "probing" ? (
        <Loader2 className="size-2.5 animate-spin" aria-hidden="true" />
      ) : null}
      {t(`agentBackends.backendType.probe.${probe.state}`)}
    </span>
  );
}

// 编辑态：类型创建后不可改。渲染整组再 disabled 只是白占地方，换成一行只读摘要。
function BackendTypeReadonly({ type }: { type: BackendType }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-baseline gap-2">
        <span className="font-medium">{t("agentBackends.fields.type")}</span>
        <span className="text-2xs text-muted-foreground">
          {t("agentBackends.fields.typeLocked")}
        </span>
      </div>
      <div className="flex items-center gap-2 rounded-md border border-border bg-secondary px-2.5 py-2">
        <span aria-hidden="true">
          <AgentBackendLogo backendType={type} className="size-4" />
        </span>
        <span className="min-w-0 flex-1 truncate text-xs font-semibold">
          {t(`agentBackends.backendType.${type}.label`)}
        </span>
      </div>
    </div>
  );
}

function ProviderConfigureCta({ onConfigure }: { onConfigure?: () => void }) {
  const { t } = useTranslation();
  return (
    <Button
      type="button"
      size="sm"
      className="mt-1.5 h-7 px-2.5 text-2xs"
      onClick={onConfigure}
    >
      {t("agentBackends.provider.configureCta")}
    </Button>
  );
}

// 触发按钮主行：品牌标识 + 供应商名 + 跟随/固定徽标 —— 和列表行的绑定面包屑
// （BackendRowBinding）同一套处理，让「绑了谁、是不是跟随」在收起状态下也一眼可读。
// 按钮的无障碍名由 ModelTargetPicker 的 aria-label 决定，这里只管视觉主行。
function BindingTriggerLabel({ target }: { target: ResolvedModelTarget }) {
  const { t } = useTranslation();
  if (target.mode === "native") {
    return (
      <span className="min-w-0 truncate">
        {t("agentBackends.binding.cliLogin")}
      </span>
    );
  }
  if (target.mode === "invalid") {
    return (
      <span className="min-w-0 truncate">
        {t("agentBackends.binding.invalidTarget", {
          target: target.providerName || target.modelId,
        })}
      </span>
    );
  }
  const follow = target.mode === "provider-default";
  return (
    <>
      <LlmProviderLogo
        providerType={target.providerType}
        providerName={target.providerName}
        className="size-3.5 shrink-0"
      />
      <span className="min-w-0 truncate font-medium">
        {target.providerName}
      </span>
      <Badge
        data-testid="binding-mode-chip"
        variant="secondary"
        className={cn(
          "shrink-0 rounded-sm px-1 py-0 text-2xs font-normal",
          follow && "bg-primary-soft text-primary-text",
        )}
      >
        {follow
          ? t("agentBackends.binding.modeFollow")
          : t("agentBackends.binding.modeFixed")}
      </Badge>
    </>
  );
}

function bindingTriggerSub(t: Translate, target: ResolvedModelTarget): string {
  if (target.mode === "native") return t("agentBackends.binding.cliResolution");
  if (target.mode === "invalid")
    return t("agentBackends.binding.invalidResolution");
  return target.mode === "provider-default"
    ? t("agentBackends.binding.followResolution", { model: target.modelId })
    : t("agentBackends.binding.fixedResolution", { model: target.modelId });
}

// 分级路由 Picker 顶部「继承主绑定」项的解析副行 —— 与会话场景同一形态：箭头点出
// 「解析到」，品牌标识让主绑定的供应商一眼可认，模型 ID 单独走等宽。CLI 登录态 / 失效
// 目标没有可认的供应商，回落既有纯文字说明（宁可少画一个标识，也不画半个空标识）。
function bindingSpecialSublabel(
  t: Translate,
  target: ResolvedModelTarget,
): React.ReactNode {
  if (
    target.mode === "native" ||
    target.mode === "invalid" ||
    !target.providerType
  ) {
    return bindingTriggerSub(t, target);
  }
  return (
    <span
      data-testid="special-resolution"
      className="flex min-w-0 items-center gap-1"
    >
      <span aria-hidden="true">→</span>
      <LlmProviderLogo
        providerType={target.providerType}
        providerName={target.providerName}
        className="size-3.5"
      />
      <span className="min-w-0 truncate">
        {target.providerName}
        {target.modelId ? (
          <>
            {" · "}
            <span className="font-mono">{target.modelId}</span>
          </>
        ) : null}
      </span>
    </span>
  );
}

function routeConclusion(
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

function ModelBindingSection({
  type,
  providers,
  value,
  modelKey,
  onTargetChange,
  onSyncProvider,
  invalid,
  piAgentModelMissing,
  editing,
  onOpenLlmProviders,
  catalog,
  catalogLoading,
  catalogError,
  executionLocation,
  supportsFixedModel,
  remoteCatalog,
  routes,
  onRoutesChange,
  customModel,
  onCustomModelChange,
  resolvedMainTarget,
  openPickerOnMount,
}: {
  type: BackendType;
  providers: Provider[];
  value: string;
  modelKey: string;
  onTargetChange: (target: RouteTarget) => void;
  onSyncProvider?: (provider: PickerProvider) => void;
  invalid: boolean;
  piAgentModelMissing: boolean;
  editing: boolean;
  onOpenLlmProviders?: () => void;
  catalog: PickerProvider[];
  catalogLoading: boolean;
  catalogError: boolean;
  executionLocation: string;
  supportsFixedModel: boolean;
  remoteCatalog?: PickerProvider[];
  routes: Record<ClaudeTier, RouteTarget>;
  onRoutesChange: (routes: Record<ClaudeTier, RouteTarget>) => void;
  customModel: string;
  onCustomModelChange: (value: string) => void;
  resolvedMainTarget: ResolvedModelTarget;
  openPickerOnMount: boolean;
}) {
  const { t } = useTranslation();
  const cliLogin = resolvedMainTarget.mode === "native";
  return (
    <section
      data-testid="model-binding-block"
      className="flex flex-col gap-3 rounded-lg border border-border bg-secondary/30 p-3"
    >
      {/* 区块标题就是这项配置的唯一标题，下面的 Picker 直接是它的第一项，不再重复一遍
          字段名 —— 「分级路由 / 自定义模型从属于主绑定」也由区块嵌套自己讲清楚，不另起
          一段说明（mockup ?view=backend 的区块头就只有标题）。 */}
      <div className="flex min-w-0 items-center gap-1.5">
        <h3 className="text-xs font-semibold">
          {t("agentBackends.binding.label")}
        </h3>
        {isCliBackend(type) ? (
          <span className="font-mono text-2xs text-muted-foreground">
            {t("agentBackends.provider.optionalSuffix")}
          </span>
        ) : null}
      </div>
      <ModelTargetField
        type={type}
        providers={providers}
        value={value}
        modelKey={modelKey}
        onTargetChange={onTargetChange}
        onSyncProvider={onSyncProvider}
        invalid={invalid}
        piAgentModelMissing={piAgentModelMissing}
        editing={editing}
        onOpenLlmProviders={onOpenLlmProviders}
        catalog={catalog}
        catalogLoading={catalogLoading}
        catalogError={catalogError}
        executionLocation={executionLocation}
        supportsFixedModel={supportsFixedModel}
        remoteCatalog={remoteCatalog}
        resolvedTarget={resolvedMainTarget}
        openPickerOnMount={openPickerOnMount}
      />
      {type === "claudecode" ? (
        <>
          {cliLogin ? (
            <DefaultModelField
              value={customModel}
              onChange={onCustomModelChange}
              description={t("agentBackends.defaultModel.cliOnlyDescription")}
            />
          ) : (
            <p className="text-2xs text-muted-foreground">
              {t("agentBackends.defaultModel.ignoredDescription")}
            </p>
          )}
          <ModelRoutesField
            catalog={catalog}
            catalogLoading={catalogLoading}
            catalogError={catalogError}
            routes={routes}
            onChange={onRoutesChange}
            executionLocation={executionLocation}
            supportsFixedModel={supportsFixedModel}
            remoteCatalog={remoteCatalog}
            mainTarget={resolvedMainTarget}
          />
        </>
      ) : null}
    </section>
  );
}

function ModelTargetField({
  type,
  providers,
  value,
  modelKey,
  onTargetChange,
  onSyncProvider,
  invalid,
  piAgentModelMissing,
  editing,
  onOpenLlmProviders,
  catalog,
  catalogLoading,
  catalogError,
  executionLocation,
  supportsFixedModel = true,
  remoteCatalog,
  resolvedTarget,
  openPickerOnMount = false,
}: {
  type: BackendType;
  providers: Provider[];
  value: string;
  modelKey: string;
  onTargetChange: (t: { providerKey: string; modelKey: string }) => void;
  onSyncProvider?: (provider: PickerProvider) => void;
  invalid: boolean;
  piAgentModelMissing: boolean;
  editing: boolean;
  onOpenLlmProviders?: () => void;
  catalog: PickerProvider[];
  catalogLoading: boolean;
  catalogError: boolean;
  executionLocation: string;
  supportsFixedModel?: boolean;
  remoteCatalog?: PickerProvider[];
  resolvedTarget: ResolvedModelTarget;
  openPickerOnMount?: boolean;
}) {
  const { t } = useTranslation();
  // claudecode / codex / piagent 允许「不关联」走 CLI 自身登录；builtin 必填。
  const optional = isCliBackend(type);
  // Match by providerKey (preferred) or fall back to string id for legacy data.
  const matchesProvider = (p: Provider) =>
    (p.providerKey && p.providerKey === value) || String(p.id) === value;
  const stale = editing && value !== "" && !providers.some(matchesProvider);
  const empty = providers.length === 0;
  const selected = { providerKey: value, modelKey };
  const noCompatibleDescription = t("modelTargetPicker.noCompatibleProviders");

  if (empty && !optional) {
    return (
      <div className="flex flex-col gap-1.5 text-xs">
        <Alert className="border-status-waiting/40 bg-status-waiting-bg text-xs">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle className="text-xs">
            {t("agentBackends.provider.emptyTitle")}
          </AlertTitle>
          <AlertDescription className="text-2xs">
            {t("agentBackends.provider.noneDescription")}
            {onOpenLlmProviders ? (
              <ProviderConfigureCta onConfigure={onOpenLlmProviders} />
            ) : null}
          </AlertDescription>
        </Alert>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-1.5 text-xs">
      {stale ? (
        <Alert className="border-status-waiting/40 bg-status-waiting-bg text-xs">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle className="text-xs">
            {t("agentBackends.provider.staleTitle")}
          </AlertTitle>
          <AlertDescription className="text-2xs">
            {t("agentBackends.provider.staleDescription", {
              optionalClause: optional
                ? t("agentBackends.provider.staleOptionalClause")
                : "",
            })}
          </AlertDescription>
        </Alert>
      ) : null}
      {empty && optional ? (
        <Alert className="border-border bg-secondary text-xs">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle className="text-xs">
            {t("agentBackends.provider.noMatchTitle")}
          </AlertTitle>
          <AlertDescription className="text-2xs">
            {noCompatibleDescription}
            {onOpenLlmProviders ? (
              <ProviderConfigureCta onConfigure={onOpenLlmProviders} />
            ) : null}
          </AlertDescription>
        </Alert>
      ) : (
        <ModelTargetPicker
          scenario="backend"
          backendType={type}
          executionLocation={executionLocation}
          selected={selected}
          onChange={(target) =>
            onTargetChange({
              providerKey: target.providerKey,
              modelKey: target.modelKey,
            })
          }
          onSyncProvider={onSyncProvider}
          catalog={catalog}
          loading={catalogLoading}
          error={catalogError}
          invalid={invalid}
          openOnMount={openPickerOnMount}
          supportsFixedModel={supportsFixedModel}
          remoteCatalog={remoteCatalog}
          triggerLabel={<BindingTriggerLabel target={resolvedTarget} />}
          triggerSub={bindingTriggerSub(t, resolvedTarget)}
          aria-label={t("agentBackends.binding.label")}
        />
      )}
      {piAgentModelMissing ? (
        <Alert className="border-status-waiting/40 bg-status-waiting-bg text-xs">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle className="text-xs">
            {t("agentBackends.provider.modelRequiredTitle")}
          </AlertTitle>
          <AlertDescription className="text-2xs">
            {t("agentBackends.provider.modelRequiredDescription")}
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}

function CliPathField({
  type,
  value,
  onChange,
  onDetect,
  detecting,
  missMessage,
}: {
  type: BackendType;
  value: string;
  onChange: (v: string) => void;
  onDetect: () => void;
  detecting: boolean;
  missMessage: string | null;
}) {
  const { t } = useTranslation();
  const bin = cliBinaryName(type);
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium">{t("agentBackends.cli.label")}</span>
        <span className="font-mono text-2xs text-muted-foreground">
          {value.trim() === ""
            ? t("agentBackends.cli.emptyHint", { bin })
            : t("agentBackends.cli.explicitHint")}
        </span>
      </div>
      <div className="flex items-center gap-1.5">
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={`/usr/local/bin/${bin}`}
          className="font-mono"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-9 shrink-0 gap-1 px-2 text-2xs"
          onClick={onDetect}
          disabled={detecting}
          aria-label={t("agentBackends.cli.detect")}
          title={t("agentBackends.cli.detectTitle", { bin })}
        >
          {detecting ? (
            <Loader2 className="size-3 animate-spin" aria-hidden="true" />
          ) : (
            <Radar className="size-3" aria-hidden="true" />
          )}
          {t("agentBackends.cli.detect")}
        </Button>
      </div>
      {missMessage ? (
        <span className="font-mono text-2xs text-status-waiting">
          {missMessage}
        </span>
      ) : null}
    </div>
  );
}

function ModelRoutesField({
  catalog,
  catalogLoading,
  catalogError,
  routes,
  onChange,
  executionLocation,
  supportsFixedModel = true,
  remoteCatalog,
  mainTarget,
}: {
  catalog: PickerProvider[];
  catalogLoading: boolean;
  catalogError: boolean;
  routes: Record<ClaudeTier, RouteTarget>;
  onChange: (r: Record<ClaudeTier, RouteTarget>) => void;
  executionLocation: string;
  supportsFixedModel?: boolean;
  remoteCatalog?: PickerProvider[];
  mainTarget: ResolvedModelTarget;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const summary = CLAUDE_TIERS.map(
    (tier) =>
      `${tier}: ${routeConclusion(t, routes[tier], mainTarget, catalog)}`,
  ).join(" · ");
  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-border bg-background px-2.5 py-2 text-xs">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className="flex min-w-0 cursor-pointer items-center gap-1.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        {open ? (
          <ChevronDown className="size-3.5 shrink-0" aria-hidden="true" />
        ) : (
          <ChevronRight className="size-3.5 shrink-0" aria-hidden="true" />
        )}
        {/* 标签不换行、摘要吃掉剩余宽度：默认状态下摘要才是这一行真正要读的内容。 */}
        <span className="shrink-0 font-medium">
          {t("agentBackends.modelRoutes.label")}
        </span>
        <span className="min-w-0 flex-1 truncate text-2xs text-muted-foreground">
          {summary}
        </span>
        <Badge
          variant="secondary"
          className="shrink-0 rounded-sm px-1.5 py-0 text-2xs font-normal"
        >
          {t("agentBackends.modelRoutes.defaultChip")}
        </Badge>
      </button>
      {open ? (
        <div className="flex flex-col gap-1.5 pt-1.5">
          <p className="text-2xs text-muted-foreground">
            {t("agentBackends.modelRoutes.hint")}
          </p>
          {CLAUDE_TIERS.map((tier) => {
            const route = routes[tier] ?? { providerKey: "", modelKey: "" };
            const tierInvalid =
              route.providerKey !== "" &&
              !catalog.some(
                (p) =>
                  p.providerKey === route.providerKey &&
                  (route.modelKey === ""
                    ? !!p.defaultModel
                    : p.models.some(
                        (m) => m.modelKey === route.modelKey && m.enabled,
                      )),
              );
            return (
              <div
                key={tier}
                className="grid grid-cols-[64px_1fr] items-center gap-2"
              >
                <Badge
                  variant="secondary"
                  className="justify-self-start rounded-sm px-1.5 py-0.5 font-mono text-2xs"
                >
                  {tier}
                </Badge>
                <ModelTargetPicker
                  scenario="route"
                  backendType="claudecode"
                  executionLocation={executionLocation}
                  selected={route}
                  onChange={(target) =>
                    onChange({
                      ...routes,
                      [tier]: {
                        providerKey: target.providerKey,
                        modelKey: target.modelKey,
                      },
                    })
                  }
                  catalog={catalog}
                  loading={catalogLoading}
                  error={catalogError}
                  invalid={tierInvalid}
                  supportsFixedModel={supportsFixedModel}
                  remoteCatalog={remoteCatalog}
                  specialSublabel={bindingSpecialSublabel(t, mainTarget)}
                  triggerSub={routeConclusion(t, route, mainTarget, catalog)}
                  compact
                  aria-label={t("agentBackends.modelRoutes.tierAria", { tier })}
                />
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

function SandboxField({
  value,
  onChange,
}: {
  value: SandboxValue;
  onChange: (v: SandboxValue) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium">{t("agentBackends.sandbox.label")}</span>
        <span className="font-mono text-2xs text-muted-foreground">
          {t("agentBackends.sandbox.hint")}
        </span>
      </div>
      <div className="grid grid-cols-3 gap-1 rounded-md border border-border bg-secondary p-0.5">
        {SANDBOX_OPTIONS.map((opt) => {
          const active = value === opt.value;
          return (
            <button
              key={opt.value}
              type="button"
              onClick={() => onChange(active ? "" : opt.value)}
              aria-pressed={active}
              className={cn(
                "rounded-[5px] px-2 py-1.5 font-mono text-2xs transition-colors",
                active
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {opt.label}
            </button>
          );
        })}
      </div>
      {value === "" ? (
        <span className="font-mono text-2xs text-muted-foreground">
          {t("agentBackends.sandbox.defaultHint")}
        </span>
      ) : null}
    </div>
  );
}

function ApprovalField({
  value,
  onChange,
}: {
  value: ApprovalValue;
  onChange: (v: ApprovalValue) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium">{t("agentBackends.approval.label")}</span>
        <span className="font-mono text-2xs text-muted-foreground">
          {t("agentBackends.approval.hint")}
        </span>
      </div>
      <Select
        value={value === "" ? "never" : value}
        onValueChange={(v) => onChange(v as ApprovalValue)}
      >
        <SelectTrigger aria-label={t("agentBackends.approval.label")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {APPROVAL_OPTIONS.map((opt) => (
            <SelectItem key={opt.value} value={opt.value}>
              <span className="inline-flex items-center gap-2">
                <span className="font-mono text-2xs">{opt.value}</span>
                <span className="text-muted-foreground">
                  {t(`agentBackends.approval.options.${opt.value}`)}
                </span>
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

// ReasoningEffortField shadcn Select 把"思考力度"以六档（默认 + low/medium/high/xhigh/max）
// 暴露给用户。codex 类型下展示到 xhigh，隐藏 max——max 在底层会 clamp 到 high，
// UI 直接隐藏避免「保存了 max 实际上等于 high」的迷惑。
//
// Select 不接受空字符串作为 SelectItem value，所以把 "" 映射为字面量 "default"，
// 在 onValueChange 回传时再翻译回 ""，与后端枚举对齐。
function EffectiveConfigSummary({
  type,
  deviceName,
  cliPath,
  resolvedMainTarget,
  customModel,
  routes,
  catalog,
  referenceCount,
  saveBlockedReason,
  openClawModel,
}: {
  type: BackendType;
  deviceName: string;
  cliPath: string;
  resolvedMainTarget: ResolvedModelTarget;
  customModel: string;
  routes: Record<ClaudeTier, RouteTarget>;
  catalog: PickerProvider[];
  referenceCount: number;
  saveBlockedReason: string | null;
  openClawModel: string;
}) {
  const { t } = useTranslation();
  const source =
    type === "openclaw"
      ? t("agentBackends.summary.sources.openclaw")
      : resolvedMainTarget.mode === "native"
        ? t("agentBackends.summary.sources.cli")
        : t("agentBackends.summary.sources.agentre", {
            provider: resolvedMainTarget.providerName,
          });
  const effectiveModel =
    type === "openclaw"
      ? openClawModel || t("agentBackends.openclaw.modelGatewayDefault")
      : resolvedMainTarget.mode === "native"
        ? customModel || t("agentBackends.summary.cliAccountModel")
        : resolvedMainTarget.modelId ||
          t("agentBackends.summary.unresolvedModel");
  const mode =
    type === "openclaw"
      ? t("agentBackends.summary.modeGateway")
      : resolvedMainTarget.mode === "provider-default"
        ? t("agentBackends.binding.modeFollow")
        : resolvedMainTarget.mode === "fixed"
          ? t("agentBackends.binding.modeFixed")
          : resolvedMainTarget.mode === "invalid"
            ? t("agentBackends.summary.modeInvalid")
            : t("agentBackends.summary.modeCli");
  return (
    <section
      data-testid="effective-config-summary"
      className={cn(
        "flex flex-col gap-2 rounded-lg border px-3 py-3 text-xs",
        saveBlockedReason
          ? "border-status-waiting/40 bg-status-waiting-bg"
          : "border-border bg-secondary/30",
      )}
    >
      <h3 className="font-semibold">{t("agentBackends.summary.title")}</h3>
      <dl className="grid grid-cols-[110px_minmax(0,1fr)] gap-x-2 gap-y-1 text-2xs">
        <dt className="text-muted-foreground">
          {t("agentBackends.summary.runtime")}
        </dt>
        {/* 运行位置要能回答「到底跑哪个可执行文件」，自定义 CLI 路径必须一起显示。 */}
        <dd data-testid="summary-runtime" className="truncate">
          {deviceName}
          {cliPath.trim() !== "" ? (
            <>
              <span
                className="mx-1 text-decorative-foreground"
                aria-hidden="true"
              >
                ·
              </span>
              <span className="font-mono">{cliPath.trim()}</span>
            </>
          ) : null}
        </dd>
        <dt className="text-muted-foreground">
          {t("agentBackends.summary.source")}
        </dt>
        <dd className="truncate">{source}</dd>
        <dt className="text-muted-foreground">
          {t("agentBackends.summary.model")}
        </dt>
        <dd className="truncate">
          {effectiveModel} · {mode}
        </dd>
        {type === "claudecode" ? (
          <>
            <dt className="text-muted-foreground">
              {t("agentBackends.summary.routes")}
            </dt>
            <dd className="min-w-0">
              {CLAUDE_TIERS.map((tier) => (
                <span key={tier} className="mr-2 inline-block">
                  {tier}:{" "}
                  {routeConclusion(
                    t,
                    routes[tier],
                    resolvedMainTarget,
                    catalog,
                  )}
                </span>
              ))}
            </dd>
          </>
        ) : null}
        <dt className="text-muted-foreground">
          {t("agentBackends.summary.references")}
        </dt>
        <dd>
          {t("agentBackends.summary.referenceCount", { count: referenceCount })}
        </dd>
      </dl>
      {/* 校验结论独立成一整行:通过时给正向确认，不通过时把原因说完整。 */}
      {saveBlockedReason ? (
        <p className="flex items-center gap-1.5 rounded-md bg-status-waiting-bg px-2 py-1.5 text-2xs text-status-waiting">
          <AlertCircle className="size-3 shrink-0" aria-hidden="true" />
          {t("agentBackends.summary.cannotSaveReason", {
            reason: saveBlockedReason,
          })}
        </p>
      ) : (
        <p className="flex items-center gap-1.5 rounded-md bg-status-running-bg px-2 py-1.5 text-2xs font-medium text-status-running">
          <CheckCircle2 className="size-3 shrink-0" aria-hidden="true" />
          {t("agentBackends.summary.saveReady")}
        </p>
      )}
    </section>
  );
}

function ReasoningEffortField({
  type,
  value,
  onChange,
}: {
  type: BackendType;
  value: ReasoningEffortValue;
  onChange: (v: ReasoningEffortValue) => void;
}) {
  const { t } = useTranslation();
  const options =
    type === "codex" ? REASONING_EFFORTS_CODEX : REASONING_EFFORTS_FULL;
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium">
          {t("agentBackends.reasoning.label")}
        </span>
        <span className="font-mono text-2xs text-muted-foreground">
          reasoning_effort
        </span>
      </div>
      <Select
        value={value === "" ? "default" : value}
        onValueChange={(v) =>
          onChange((v === "default" ? "" : v) as ReasoningEffortValue)
        }
      >
        <SelectTrigger aria-label={t("agentBackends.reasoning.label")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((opt) => (
            <SelectItem
              key={opt || "default"}
              value={opt === "" ? "default" : opt}
            >
              <span className="inline-flex items-center gap-2">
                <span className="font-mono text-2xs">{opt || "default"}</span>
                <span className="text-muted-foreground">
                  {t(`agentBackends.reasoning.options.${opt || "default"}`)}
                </span>
              </span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {type === "codex" ? (
        <span className="text-2xs text-muted-foreground">
          {t("agentBackends.reasoning.codexHint")}
        </span>
      ) : null}
    </div>
  );
}

// DefaultModelField 是 claudecode 的「自定义模型」配置：spawn claude 子进程时
// 下发的 --model 值。主要用于走 CLI 自身登录态（未绑 provider）时指定模型
// （如 claude-fable-5）；绑了 provider 时 provider.Model 优先，本字段仅兜底。
// 纯自由文本，CLI 既收别名（fable/opus/sonnet）也收完整模型名。
function DefaultModelField({
  value,
  onChange,
  description,
}: {
  value: string;
  onChange: (v: string) => void;
  description: string;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center justify-between">
        <span className="font-medium">
          {t("agentBackends.defaultModel.label")}
        </span>
        <span className="font-mono text-2xs text-muted-foreground">
          {t("agentBackends.defaultModel.hint")}
        </span>
      </div>
      <Input
        aria-label={t("agentBackends.defaultModel.label")}
        value={value}
        autoCapitalize="off"
        autoComplete="off"
        autoCorrect="off"
        spellCheck={false}
        placeholder={t("agentBackends.defaultModel.placeholder")}
        onChange={(e) => onChange(e.currentTarget.value)}
        className="h-9 font-mono text-xs"
      />
      <span className="text-2xs text-muted-foreground">{description}</span>
    </div>
  );
}

// DefaultPermissionModeField 是 claudecode 的「新会话默认起手 mode」配置。
// 取值：
//   - "" → 走 pkg/claudecode 兜底（acceptEdits）。
//   - default / acceptEdits / plan → 普通模式。
//   - bypassPermissions → 起手即跳审批；CLI spawn 时下发 --permission-mode bypassPermissions，
//     runtime 仍可在 4 档之间自由切换（实测 claude v2.1.x 行为）。
//
// 用 shadcn Select 而不是 Switch：4 档枚举 + 危险等级递进的视觉提示。
//
// 远端 + bypass 的额外坑：claude CLI 内部把 --permission-mode bypassPermissions
// 当作 --dangerously-skip-permissions 走 root 检查，若 agentred 以 root/sudo
// 运行会被 CLI 直接拒掉。设 IS_SANDBOX=1 可让 CLI 跳过该检查（CLI 自带的
// 沙箱逃生口）。此处展示提示 + 一键填到 env_json。
function DefaultPermissionModeField({
  value,
  onChange,
  isRemote,
  hasIsSandbox,
  onAddIsSandbox,
}: {
  value: string;
  onChange: (v: string) => void;
  isRemote: boolean;
  hasIsSandbox: boolean;
  onAddIsSandbox: () => void;
}) {
  const { t } = useTranslation();
  const isBypass = value === "bypassPermissions";
  const showRootHint = isBypass && isRemote;
  return (
    <div
      className={cn(
        "flex flex-col gap-1.5 rounded-md border px-3 py-2 text-xs",
        isBypass
          ? "border-destructive/40 bg-destructive-soft"
          : "border-border bg-secondary/40",
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-0.5">
          <span
            className={cn(
              "inline-flex items-center gap-1.5 font-medium",
              isBypass ? "text-destructive" : "",
            )}
          >
            {isBypass ? (
              <AlertCircle className="size-3.5 shrink-0" aria-hidden="true" />
            ) : null}
            {t("agentBackends.permission.label")}
          </span>
          <span className="font-mono text-2xs text-muted-foreground">
            {t("agentBackends.permission.hint")}
          </span>
        </div>
        <Select
          value={value === "" ? "__inherit__" : value}
          onValueChange={(v) => onChange(v === "__inherit__" ? "" : v)}
        >
          <SelectTrigger
            aria-label={t("agentBackends.permission.label")}
            className="h-7 w-[170px] text-2xs"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__inherit__">
              <span className="text-muted-foreground">
                {t("agentBackends.permission.options.inherit")}
              </span>
            </SelectItem>
            <SelectItem value="default">
              {t("agentBackends.permission.options.default")}
            </SelectItem>
            <SelectItem value="acceptEdits">
              {t("agentBackends.permission.options.acceptEdits")}
            </SelectItem>
            <SelectItem value="plan">
              {t("agentBackends.permission.options.plan")}
            </SelectItem>
            <SelectItem value="bypassPermissions">
              {t("agentBackends.permission.options.bypassPermissions")}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>
      {isBypass ? (
        <span className="text-2xs text-destructive">
          {t("agentBackends.permission.bypassWarning")}
        </span>
      ) : null}
      {showRootHint ? (
        <div className="flex flex-wrap items-center gap-2 rounded border border-status-waiting/40 bg-status-waiting-bg px-2 py-1.5 text-2xs text-status-waiting">
          <span className="min-w-0 flex-1">
            {t("agentBackends.permission.remoteRootHintPrefix")}{" "}
            <span className="font-mono">IS_SANDBOX=1</span>{" "}
            {t("agentBackends.permission.remoteRootHintSuffix")}
          </span>
          {hasIsSandbox ? (
            <span className="inline-flex items-center gap-1 font-mono text-2xs text-muted-foreground">
              <CheckCircle2 className="size-3" aria-hidden="true" />
              {t("agentBackends.permission.isSandboxConfigured")}
            </span>
          ) : (
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-6 gap-1 px-2 text-2xs"
              onClick={onAddIsSandbox}
            >
              <Plus className="size-3" aria-hidden="true" />
              {t("agentBackends.permission.addIsSandbox")}
            </Button>
          )}
        </div>
      ) : null}
    </div>
  );
}

function EnvJsonField({
  entries,
  onChange,
  open,
  onToggle,
  reservedOffenders,
}: {
  entries: EnvEntry[];
  onChange: (next: EnvEntry[]) => void;
  open: boolean;
  onToggle: () => void;
  reservedOffenders: string[];
}) {
  const { t } = useTranslation();
  const filledCount = entries.filter((e) => e.key.trim() !== "").length;
  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-border bg-secondary/40 px-3 py-2 text-xs">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        className="flex items-center justify-between gap-2 text-left"
      >
        <span className="inline-flex items-center gap-1.5 font-medium">
          {open ? (
            <ChevronDown className="size-3.5" aria-hidden="true" />
          ) : (
            <ChevronRight className="size-3.5" aria-hidden="true" />
          )}
          {t("agentBackends.env.title")}
        </span>
        <span className="font-mono text-2xs text-muted-foreground">
          {t("agentBackends.env.count", { count: filledCount })}
        </span>
      </button>
      {open ? (
        <div className="flex flex-col gap-1.5 pt-1.5">
          {reservedOffenders.length > 0 ? (
            <div className="rounded-sm bg-destructive-soft px-2 py-1 text-2xs text-destructive">
              {t("agentBackends.env.reservedDisabled", {
                keys: reservedOffenders.join(", "),
              })}
            </div>
          ) : null}
          {entries.length === 0 ? (
            <span className="text-2xs text-muted-foreground">
              {t("agentBackends.env.empty")}
            </span>
          ) : null}
          {entries.map((entry, i) => {
            const trimmed = entry.key.trim();
            const isReserved = trimmed !== "" && RESERVED_ENV_KEYS.has(trimmed);
            return (
              <div
                key={i}
                className="grid grid-cols-[1fr_1fr_28px] items-center gap-1.5"
              >
                <Input
                  value={entry.key}
                  onChange={(ev) =>
                    onChange(
                      entries.map((x, j) =>
                        j === i ? { ...x, key: ev.target.value } : x,
                      ),
                    )
                  }
                  placeholder="KEY"
                  className={cn(
                    "h-7 font-mono text-2xs",
                    isReserved && "border-destructive",
                  )}
                />
                <Input
                  value={entry.value}
                  onChange={(ev) =>
                    onChange(
                      entries.map((x, j) =>
                        j === i ? { ...x, value: ev.target.value } : x,
                      ),
                    )
                  }
                  placeholder="VALUE"
                  className="h-7 font-mono text-2xs"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  aria-label={t("agentBackends.env.deleteKey")}
                  onClick={() => onChange(entries.filter((_, j) => j !== i))}
                >
                  <X data-icon="only" aria-hidden="true" />
                </Button>
              </div>
            );
          })}
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 self-start gap-1 px-2 text-2xs"
            onClick={() => onChange([...entries, { key: "", value: "" }])}
          >
            <Plus className="size-3" aria-hidden="true" />
            {t("common.add")}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function ProxyNote({
  status,
  providerLinked,
  onOpenProxySettings,
}: {
  status: httpgateway.GatewayStatus | null;
  providerLinked: boolean;
  onOpenProxySettings?: () => void;
}) {
  const { t } = useTranslation();
  // 未关联 provider 时 CLI 走自身登录，本地代理不参与，无需提示其状态。
  if (!providerLinked) {
    return (
      <div className="flex items-center gap-2 rounded-md border border-border bg-secondary px-3 py-2 text-2xs text-muted-foreground">
        <span
          className="size-1.5 shrink-0 rounded-full bg-muted-foreground"
          aria-hidden="true"
        />
        <span className="min-w-0 flex-1 truncate">
          {t("agentBackends.proxy.unlinked")}
        </span>
      </div>
    );
  }

  const running = status?.status === "running";
  const label = running
    ? status?.listenURL || "127.0.0.1"
    : t("agentBackends.proxy.notRunning");
  return (
    <div
      className={cn(
        "flex items-center gap-2 rounded-md border px-3 py-2 text-2xs",
        running
          ? "border-primary-text/30 bg-primary-soft text-primary-text"
          : "border-border bg-secondary text-muted-foreground",
      )}
    >
      <span
        className={cn(
          "size-1.5 shrink-0 rounded-full",
          running ? "bg-status-running" : "bg-muted-foreground",
        )}
        aria-hidden="true"
      />
      <span className="min-w-0 flex-1 truncate">
        {running
          ? t("agentBackends.proxy.running", { url: label })
          : t("agentBackends.proxy.stopped", {
              label,
              reasonSuffix: status?.reason ? ` · ${status.reason}` : "",
            })}
      </span>
      {onOpenProxySettings ? (
        <button
          type="button"
          onClick={onOpenProxySettings}
          className="inline-flex shrink-0 items-center gap-1 font-medium underline-offset-2 hover:underline"
        >
          {t("agentBackends.proxy.openSettings")}
          <ExternalLink className="size-3" aria-hidden="true" />
        </button>
      ) : null}
    </div>
  );
}

function TestResultPill({ state }: { state: FlashState }) {
  if (!state) return null;
  const ok = state.kind === "ok";
  const { display, full, truncated } = truncateFlashText(state.text);
  return (
    <div
      className={cn(
        "flex items-start gap-2 rounded-md border px-3 py-2 text-xs",
        ok
          ? "border-status-running/40 bg-status-running-bg text-status-running"
          : "border-destructive/40 bg-destructive-soft text-destructive",
      )}
      role="status"
    >
      {ok ? (
        <CheckCircle2 className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
      ) : (
        <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
      )}
      <span
        className="min-w-0 flex-1 break-words"
        title={truncated ? full : undefined}
      >
        {display}
      </span>
    </div>
  );
}

function DeleteDialog({
  backend,
  onCancel,
  onConfirmed,
  onError,
}: {
  backend: Backend;
  onCancel: () => void;
  onConfirmed: () => Promise<void> | void;
  onError: (text: string) => void;
}) {
  const { t } = useTranslation();
  const [submitting, setSubmitting] = React.useState(false);
  return (
    <AgentreDialog
      open
      onOpenChange={(o) => (!o ? onCancel() : undefined)}
      title={t("agentBackends.deleteDialog.title")}
      description={t("agentBackends.deleteDialog.description", {
        name: backend.name,
      })}
      footer={
        <>
          <Button
            type="button"
            variant="outline"
            onClick={onCancel}
            disabled={submitting}
          >
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={submitting}
            onClick={async () => {
              setSubmitting(true);
              try {
                await DeleteAgentBackend({
                  id: backend.id,
                } as agent_backend_svc.DeleteBackendRequest);
                await onConfirmed();
              } catch (err) {
                onError(messageFromError(err));
              } finally {
                setSubmitting(false);
              }
            }}
          >
            {t("common.delete")}
          </Button>
        </>
      }
    />
  );
}

function FlashBanner({
  state,
  onDismiss,
}: {
  state: FlashState;
  onDismiss: () => void;
}) {
  const { t } = useTranslation();
  if (!state) return null;
  const ok = state.kind === "ok";
  const { display, full, truncated } = truncateFlashText(state.text);
  return (
    <div
      className={cn(
        "flex items-center gap-2 px-4 py-2 text-xs",
        ok
          ? "bg-status-running-bg text-status-running"
          : "bg-destructive-soft text-destructive",
      )}
      role="status"
    >
      {ok ? (
        <CheckCircle2 className="size-3.5" />
      ) : (
        <AlertCircle className="size-3.5" />
      )}
      <span
        className="min-w-0 flex-1 truncate"
        title={truncated ? full : undefined}
      >
        {display}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        onClick={onDismiss}
        aria-label={t("agentBackends.flash.close")}
      >
        <ChevronDown className="size-3.5 rotate-45" aria-hidden="true" />
      </Button>
    </div>
  );
}

function messageFromError(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  try {
    return JSON.stringify(err);
  } catch {
    return i18n.t("common.unknownError");
  }
}

function providerSyncMessageFromError(err: unknown): string {
  const message = messageFromError(err);
  if (
    message.includes("org.freedesktop.secrets") ||
    message.includes("Secret Service")
  ) {
    return [
      i18n.t("agentBackends.providerSync.secretServiceMissing"),
      i18n.t("agentBackends.providerSync.secretServiceAction"),
      i18n.t("agentBackends.providerSync.originalError", { message }),
    ].join("\n");
  }
  return message;
}

// newRequestId 为一次 Test 调用分配 uuid；优先用 crypto.randomUUID，
// 老环境（理论上不会在 wails webview 出现）回落到 Math.random 拼接。
function newRequestId(): string {
  if (
    typeof crypto !== "undefined" &&
    typeof crypto.randomUUID === "function"
  ) {
    return crypto.randomUUID.call(crypto);
  }
  return `req-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}
