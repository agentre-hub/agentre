import * as React from "react";
import { useUiTranslation as useTranslation } from "../i18n";
import { ExternalLink, Loader2, Plus, Radar } from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { agentreUiResources } from "../i18n";

const i18n = {
  t: (key: string, _options?: Record<string, unknown>) =>
    (key
      .split(".")
      .reduce<unknown>(
        (value, part) => (value as Record<string, unknown>)?.[part],
        agentreUiResources.en,
      ) as string) ?? key,
};
import { cn } from "../lib/utils";

import {
  accountDeviceOptions,
  type DeviceOption,
} from "./agent-backends-utils";
import { agent_backend_svc, httpgateway } from "./port-bridge";
import { useEngineSettingsBridge } from "./port-bridge";
import type { AccountDeviceView, EngineSettingsPorts } from "./ports";
import {
  EngineSettingsPortsProvider,
  useEngineSettingsPorts,
} from "./ports-context";
import { AgentreDialog } from "./app-dialog";
import {
  useModelTargetCatalog,
  type PickerProvider,
} from "./model-target-picker";
import { OPENCLAW_SESSION_MODE } from "./openclaw-backend-fields";
import { openClawDraftIssue } from "./openclaw-validation";
import { AgentBackendsEmptyState, BackendRow } from "./agent-backends-list";
import {
  BackendTypePicker,
  BackendTypeReadonly,
  EffectiveConfigSummary,
  FlashBanner,
} from "./agent-backends-badges";
import {
  ApprovalField,
  CliPathField,
  DefaultPermissionModeField,
  EnvJsonField,
  ModelBindingSection,
  ReasoningEffortField,
  SandboxField,
} from "./agent-backends-fields";
import {
  RESERVED_ENV_KEYS,
  isCliBackend,
  type ApprovalValue,
  type Backend,
  type BackendType,
  type ClaudeTier,
  type EnvEntry,
  type FlashState,
  type Provider,
  type ReasoningEffortValue,
  type RouteTarget,
  type SandboxValue,
} from "./agent-backends-shared";
import {
  persistedDeviceIdForSelection,
  resolveExecutionDevice,
} from "./device-identity";

// BackendEditor 的实现分在 ./backend-editor/ 下：草稿整形与远端体检是纯函数，
// 三族状态（CLI 探测 / 运行设备 / OpenClaw 字段）各自成 hook，字段组与两个弹窗是
// 只吃 props 的组件。这里只留装配。
import { DeviceField } from "./backend-editor/device-field";
import {
  buildBackendDraft,
  emptyRoutes,
  matchingProviders,
  normalizeForCodex,
  openClawProbeErrorMessage,
  parseRoutes,
  referencedProviderKeys,
  safeParseEnv,
  saveBackendDraft,
  type BackendDraft,
  type PendingProviderSync,
} from "./backend-editor/draft";
import { BackendEditorFooter } from "./backend-editor/editor-footer";
import type { EditorState } from "./backend-editor/editor-types";
import { OpenClawSection } from "./backend-editor/openclaw-section";
import {
  ManualProviderSyncAlert,
  ProviderSyncDialog,
} from "./backend-editor/provider-sync";
import {
  inspectRemoteDraft,
  remoteTargetIssueMessage,
} from "./backend-editor/remote-draft-inspection";
import { computeSaveValidation } from "./backend-editor/save-validation";
import { useBackendDevices } from "./backend-editor/use-backend-devices";
import { useCliProbes } from "./backend-editor/use-cli-probes";
import { useOpenClawFields } from "./backend-editor/use-openclaw-fields";
import { useRemoteProviderCatalog } from "./backend-editor/use-remote-provider-catalog";

type AgentBackendsPanelProps = {
  onOpenLlmProviders?: () => void;
  onOpenProxySettings?: () => void;
  // 页头由宿主渲染，面板把自己的页级操作（自动识别 / 新建后端）交进去：按钮要落在
  // H1 行，而它们开的创建弹窗、扫描进行态仍归面板持有。
  renderHeader?: (actions: React.ReactNode) => React.ReactNode;
};

// 宿主传进来的那一份端口只覆盖本面板的子树：两个面板同时挂载时各用各的，
// 与谁最后渲染无关。
export function AgentBackendsPanel({
  ports,
  ...props
}: AgentBackendsPanelProps & { ports: EngineSettingsPorts }) {
  return (
    <EngineSettingsPortsProvider ports={ports}>
      <AgentBackendsPanelBody {...props} />
    </EngineSettingsPortsProvider>
  );
}

function AgentBackendsPanelBody({
  onOpenLlmProviders,
  onOpenProxySettings,
  renderHeader,
}: AgentBackendsPanelProps) {
  const ports = useEngineSettingsPorts();
  const {
    CancelTestAgentBackend,
    ListAgentBackends,
    ListLLMProviders,
    ScanAndCreateAgentBackends,
    ServerListDevices,
    TestAgentBackend,
    TestOpenClawAgentBackend,
  } = useEngineSettingsBridge();
  const { t } = useTranslation();
  // 宿主有没有「本机」这个指代对象，全靠 localDeviceFingerprint 端口在不在席
  // （规格决策 4）。桌面端接了它：本地是一个可选项，页级扫描默认就扫本机；浏览器
  // 没接：既没有「本地」项，扫描也必须先点名一台机器。
  const hasLocalDevice = Boolean(ports.localDeviceFingerprint);
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
  // 页级扫描的候选机器；只有没有本机可扫的宿主才需要它。
  const [scanTargets, setScanTargets] = React.useState<DeviceOption[]>([]);
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
    const cliPath = (await ports.cliPath?.get(backend.syncId)) ?? "";
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

  // 扫的是哪台机器不能由巧合决定：没有本机可扫时，device 是用户当场点名的那台，
  // 结论也就按 (设备, 类型) 来读——同一类型在别的机器上已有后端，不构成在这台上
  // 跳过的理由（规格决策 13，跳过的判定在宿主侧）。
  async function handleAutoScan(device?: DeviceOption) {
    if (scanning) return;
    setScanning(true);
    setFlash(null);
    const onDevice = (text: string) =>
      device
        ? t("agentBackends.autoScan.onDevice", {
            device: device.name,
            message: text,
          })
        : text;
    try {
      const res = await ScanAndCreateAgentBackends(device?.value);
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
            text: onDevice(
              t("agentBackends.autoScan.partialFound", {
                createdCount: created.length,
                createdNames,
                skippedCount: skipped.length,
                skippedNames,
              }),
            ),
          });
        } else {
          setFlash({
            kind: "ok",
            text: onDevice(
              t("agentBackends.autoScan.created", {
                count: created.length,
                names: createdNames,
              }),
            ),
          });
        }
        await reload();
      } else if (skipped.length > 0) {
        const names = skipped.map((r) => r.name).join(", ");
        setFlash({
          kind: "ok",
          text: onDevice(
            t("agentBackends.autoScan.skipped", {
              count: skipped.length,
              names,
            }),
          ),
        });
      } else if (!foundAny) {
        setFlash({
          kind: "err",
          text: onDevice(t("agentBackends.autoScan.nothingFound")),
        });
      }
    } catch (err) {
      setFlash({ kind: "err", text: onDevice(messageFromError(err)) });
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
  }, [ListAgentBackends, ListLLMProviders]);

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
  }, [ListAgentBackends, ListLLMProviders]);

  // 没有本机可扫的宿主要先点名一台机器，候选就是账号里的执行端设备。
  // 有本机的宿主不拉这份清单：桌面端的 ServerListDevices 内部会写库收编设备，
  // 为了一个用不上的菜单在页面挂载时触发那次写入是没有道理的。
  React.useEffect(() => {
    if (hasLocalDevice) return;
    let cancelled = false;
    void (async () => {
      const rows = await ServerListDevices().catch(
        () => [] as AccountDeviceView[],
      );
      if (!cancelled) setScanTargets(accountDeviceOptions(rows ?? []));
    })();
    return () => {
      cancelled = true;
    };
  }, [ServerListDevices, hasLocalDevice]);

  const scanButton = (
    <Button
      type="button"
      size="sm"
      variant="outline"
      className="h-[30px] gap-1.5 px-3 text-xs"
      onClick={hasLocalDevice ? () => void handleAutoScan() : undefined}
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
  );

  // 页级操作落在 H1 行。「新建后端」在空态下让位给空态自带的 CTA——全页始终只有一个
  // 新建入口；「自动识别」恰恰在空态最有用，所以一直留在标题行。
  const headerActions = (
    <>
      {hasLocalDevice ? (
        scanButton
      ) : (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>{scanButton}</DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuLabel>
              {t("agentBackends.autoScan.pickDevice")}
            </DropdownMenuLabel>
            {scanTargets.length === 0 ? (
              <DropdownMenuItem disabled>
                {t("agentBackends.device.noneAvailable")}
              </DropdownMenuItem>
            ) : (
              scanTargets.map((device) => (
                <DropdownMenuItem
                  key={device.value}
                  onSelect={() => void handleAutoScan(device)}
                >
                  {device.name}
                  {device.online ? "" : t("agentBackends.device.offlineSuffix")}
                </DropdownMenuItem>
              ))
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
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
                  hasLocalDevice={hasLocalDevice}
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
            canEditEnvJSON={ports.canEditEnvJSON === true}
            canCreateBuiltin={ports.canCreateBuiltin === true}
            // CLI 路径与 Gateway token 都是「只存在于那台机器上」的东西：前者是
            // 按设备的可执行文件覆盖，后者进的是本机安全存储。宿主没有对应端口就
            // 意味着它既写不进也读不回 —— 那就别把框摆出来让人白填一次。
            canEditCliPath={Boolean(ports.cliPath)}
            canEditOpenClawToken={Boolean(
              editor.kind === "edit"
                ? ports.updateOpenClawBackend
                : ports.createOpenClawBackend,
            )}
            hasLocalDevice={hasLocalDevice}
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

function BackendEditor({
  state,
  providers,
  onClose,
  onSaved,
  onOpenProxySettings,
  onOpenLlmProviders,
  canEditEnvJSON,
  canCreateBuiltin,
  canEditCliPath,
  canEditOpenClawToken,
  hasLocalDevice,
}: {
  state: EditorState;
  providers: Provider[];
  onClose: () => void;
  onSaved: (message: string) => Promise<void> | void;
  onOpenProxySettings?: () => void;
  onOpenLlmProviders?: () => void;
  canEditEnvJSON: boolean;
  canCreateBuiltin: boolean;
  canEditCliPath: boolean;
  canEditOpenClawToken: boolean;
  hasLocalDevice: boolean;
}) {
  const bridge = useEngineSettingsBridge();
  const {
    CancelTestAgentBackend,
    GetGatewayStatus,
    RemoteDeviceListProviders,
    RemoteDeviceSyncProvider,
    ResolveAgentBackendCLIPath,
    TestAgentBackend,
    TestOpenClawAgentBackend,
  } = bridge;
  const { t } = useTranslation();
  const editing = state.kind === "edit" ? state.backend : null;
  const initialType: BackendType =
    (editing?.type as BackendType) ??
    (canCreateBuiltin ? "builtin" : "claudecode");

  const [type, setType] = React.useState<BackendType>(initialType);
  const [name, setName] = React.useState(editing?.name ?? "");
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
  const openClaw = useOpenClawFields(editing);
  const deviceState = useBackendDevices({
    stateKind: state.kind,
    hasLocalDevice,
    initialDeviceId: editing?.deviceId ?? "",
    bridge,
    t,
  });
  const deviceId = deviceState.deviceId;
  const remoteExecution = deviceState.remoteExecution;
  const canSyncProvider = deviceState.canSyncProvider;
  // CLI 探测吃的是「当前选中的运行设备」，所以必须排在设备那一族之后。
  const cli = useCliProbes({
    stateKind: state.kind,
    initialCliPath: state.kind === "edit" ? (state.cliPath ?? "") : "",
    type,
    deviceId,
    resolveCliPath: ResolveAgentBackendCLIPath,
    t,
  });
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
  // 名称一旦被用户敲过就不再跟着类型走；编辑态本来就带着既有名字，视同已敲过。
  const nameTouchedRef = React.useRef(state.kind === "edit");

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

  // 没被用户敲过的名称跟着「设备 · 类型」走，省掉一次手输。
  // 只有 CLI 引擎带设备前缀 —— 它们才能派到远端；内置 / 网关直接用类型名。
  function defaultBackendName(bt: BackendType, dev: string): string {
    if (!isCliBackend(bt)) return t(`agentBackends.backendType.${bt}.label`);
    // 还没点名机器、宿主又没有本机可指代：名字里不塞一台不存在的机器。
    if (dev === "" && !hasLocalDevice)
      return t(`agentBackends.backendType.${bt}.label`);
    const deviceName = deviceState.deviceDisplayName(dev);
    return t("agentBackends.name.deviceDefault", {
      device: deviceName,
      name: t(`agentBackends.backendType.${bt}.shortLabel`),
    });
  }

  function handleDeviceChange(nextDeviceId: string) {
    deviceState.setDeviceId(nextDeviceId);
    if (!nameTouchedRef.current) {
      setName(defaultBackendName(type, nextDeviceId));
    }
  }

  function handleTypeChange(nextType: BackendType) {
    setSaveResult(null);
    setType(nextType);
    if (!nameTouchedRef.current) {
      setName(defaultBackendName(nextType, deviceId));
    }
    setLlmProviderKey("");
    setLlmModelKey("");
    setRoutes(emptyRoutes());
    setSandbox("");
    setApproval("");
    setTestResult(null);
    openClaw.resetForType(nextType);
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
    cli.setCliPath("");
    cli.setCliProbeMiss(null);
    // create 模式下切到 CLI 类型要把识别到的路径自动填进去；用户随时可手改/清空。
    // edit 模式不渲染选择器，所以这里不会跑；编辑场景只靠 Input 旁的「自动识别」按钮。
    if (state.kind === "create" && isCliBackend(nextType)) {
      const probed = cli.cliProbes[nextType];
      if (probed?.state === "installed") {
        // 打开对话框时那一轮探测已经给出结论，直接复用 —— 远端设备上这能省掉一次真实往返，
        // 而方向键换选项会逐个触发本函数，代价按键盘步数累加。
        cli.setCliPath(probed.path);
      } else if (probed?.state !== "missing") {
        // 只有「还没探完 / 探测失败」才补一发；已知未安装就不用再问一次。
        void (async () => {
          // 新建流程的隐式自动填：静默吞错，远端不可达就当没识别到。
          const path = await cli
            .detectCLIPath(nextType, deviceId)
            .catch(() => null);
          if (path) cli.setCliPath(path);
        })();
      }
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
  }, [GetGatewayStatus, cliBased]);

  const {
    refreshRemoteProviders,
    remoteSupportsFixedModel,
    remotePickerCatalog,
  } = useRemoteProviderCatalog({
    stateKind: state.kind,
    remoteDeviceID: deviceState.remoteDeviceID,
    devices: deviceState.devices,
    listRemoteProviders: RemoteDeviceListProviders,
  });

  const reservedOffenders = React.useMemo(
    () =>
      envEntries
        .map((e) => e.key.trim())
        .filter((k) => k && RESERVED_ENV_KEYS.has(k)),
    [envEntries],
  );

  const open = state.kind !== "closed";

  function buildDraft(): BackendDraft {
    return buildBackendDraft({
      type,
      name,
      deviceId,
      llmProviderKey: effectiveLlmProviderKey,
      llmModelKey,
      cliPath: cli.cliPath,
      routes,
      sandbox,
      approval,
      envEntries,
      reasoningEffort,
      defaultPermissionMode,
      defaultModel,
      openClawGatewayURL: openClaw.gatewayURL,
      openClawAgentID: openClaw.agentID,
      openClawDefaultModel: openClaw.defaultModel,
    });
  }

  function inspectDraft(draft: BackendDraft) {
    return inspectRemoteDraft({
      draft,
      localFingerprint: deviceState.localFingerprint,
      devices: deviceState.devices,
      targetCatalog,
      listRemoteProviders: RemoteDeviceListProviders,
    });
  }

  function saveDraft(draft: BackendDraft) {
    return saveBackendDraft({
      draft,
      state,
      editing,
      openClawToken: openClaw.token,
      clearOpenClawToken: openClaw.clearToken,
      bridge,
      onSaved,
      t,
    });
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
    openClaw.setProbe(null);
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
          ? await TestOpenClawAgentBackend(request, openClaw.token)
          : await TestAgentBackend(request);
      if (testReqIdRef.current !== requestId) return;
      if (res.ok) {
        if (type === "openclaw") {
          openClaw.applyProbe(res);
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
        gatewayURL: openClaw.gatewayURL,
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
      const inspection = await inspectDraft(draft);
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
          text: remoteTargetIssueMessage(inspection.targetIssue, t),
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
      deviceState.localFingerprint,
      deviceState.devices,
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
        const inspection = await inspectDraft(draft);
        if (
          inspection.missingProviderKeys.length > 0 ||
          inspection.targetIssue
        ) {
          setProviderSyncError(
            inspection.targetIssue
              ? remoteTargetIssueMessage(inspection.targetIssue, t)
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

  const {
    piAgentModelMissing,
    mainTargetInvalid,
    resolvedMainTarget,
    effectiveSaveBlockedReason,
  } = computeSaveValidation({
    type,
    name,
    llmProviderKey: effectiveLlmProviderKey,
    llmModelKey,
    openClawGatewayURL: openClaw.gatewayURL,
    targetCatalog,
    filteredProviders,
    reservedOffenders,
    t,
  });
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
          <BackendEditorFooter
            saveResult={saveResult}
            testResult={testResult}
            testing={testing}
            submitting={submitting}
            syncingProvider={syncingProvider}
            piAgentModelMissing={piAgentModelMissing}
            submitDisabled={submitDisabled}
            onTest={handleTest}
            onCancelTest={handleCancelTest}
            onClose={onClose}
          />
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
              probes={cli.cliProbes}
              canCreateBuiltin={canCreateBuiltin}
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

        <DeviceField
          type={type}
          value={deviceState.selectedDeviceValue}
          onSelect={(v) =>
            handleDeviceChange(
              persistedDeviceIdForSelection(
                v,
                deviceState.localSelectValue,
                deviceState.localFingerprint,
              ),
            )
          }
          hasLocalDevice={hasLocalDevice}
          deviceOptions={deviceState.deviceOptions}
          selectedDeviceKnown={deviceState.selectedDeviceKnown}
          deviceId={deviceId}
          revokedFallbackName={
            editing?.deviceName ||
            deviceState.accountDeviceNames.get(deviceId) ||
            t("agentBackends.device.revoked")
          }
        />

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
          <OpenClawSection
            fields={openClaw}
            canEditToken={canEditOpenClawToken}
            hasToken={editing?.hasToken ?? false}
          />
        )}

        {showManualProviderSync ? (
          <ManualProviderSyncAlert
            disabled={syncingProvider}
            onSync={handleManualProviderSync}
          />
        ) : null}

        {cliBased && canEditCliPath ? (
          <CliPathField
            type={type}
            value={cli.cliPath}
            onChange={(v) => {
              cli.setCliPath(v);
              if (cli.cliProbeMiss) cli.setCliProbeMiss(null);
            }}
            onDetect={cli.handleDetectCli}
            detecting={cli.cliProbing}
            missMessage={cli.cliProbeMiss}
          />
        ) : null}

        {type === "claudecode" ? (
          <DefaultPermissionModeField
            value={defaultPermissionMode}
            onChange={setDefaultPermissionMode}
            isRemote={remoteExecution}
            canEditEnvJSON={canEditEnvJSON}
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
          deviceName={deviceState.deviceDisplayName(deviceId)}
          cliPath={cliBased ? cli.cliPath : ""}
          resolvedMainTarget={resolvedMainTarget}
          customModel={defaultModel}
          routes={routes}
          catalog={targetCatalog}
          referenceCount={editing?.agentCount ?? 0}
          saveBlockedReason={effectiveSaveBlockedReason}
          openClawModel={openClaw.defaultModel || openClaw.agentID}
        />

        {type !== "openclaw" ? (
          <ReasoningEffortField
            type={type}
            value={reasoningEffort}
            onChange={setReasoningEffort}
          />
        ) : null}

        {cliBased && canEditEnvJSON ? (
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
        <ProviderSyncDialog
          pending={pendingProviderSync}
          providers={providers}
          syncing={syncingProvider}
          error={providerSyncError}
          onClose={closeProviderSyncDialog}
          onConfirm={handleConfirmProviderSync}
        />
      ) : null}
    </>
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
  const { DeleteAgentBackend } = useEngineSettingsBridge();
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
