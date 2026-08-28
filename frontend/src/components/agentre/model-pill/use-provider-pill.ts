import * as React from "react";

import {
  ListLLMProviders,
  RemoteDeviceFingerprint,
  RemoteDeviceList,
  RemoteDeviceListProviders,
  RemoteDeviceSyncProvider,
  SetChatSessionModelTarget,
} from "../../../../wailsjs/go/app/App";
import { llm_provider_svc } from "../../../../wailsjs/go/models";
import i18n from "@/i18n";
import { resolveExecutionDevice } from "@agentre-hub/agentre-ui";

import {
  recordRecentTarget,
  useModelTargetCatalog,
  type ModelTarget,
  type PickerProvider,
} from "../model-target-picker";

// DeviceView / ProviderSummary — local shims matching remote_device_svc DTOs
//（wailsjs/go/models 是构建期生成，此处按 agent-backends 同款做法本地声明）。
type DeviceView = {
  id: number;
  name: string;
  online: boolean;
  daemonFingerprint?: string;
  supportsLLMModelTarget?: boolean;
};
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

/**
 * 供应商选择器可渲染的后端集合（规格决策 4/5）：openclaw 不消费 agentre provider，
 * 不渲染供应商选择器；其余四类后端均可选。
 */
const PROVIDER_SELECTABLE_BACKENDS = new Set([
  "builtin",
  "claudecode",
  "codex",
  "piagent",
]);

/** 后端是否渲染供应商选择器（openclaw / 未知后端 → false）。 */
export function isProviderSelectableBackend(backendType: string): boolean {
  return PROVIDER_SELECTABLE_BACKENDS.has(backendType);
}

/**
 * 供应商类型与后端是否兼容 —— 与后端 agent_backend_entity.ProviderTypeMatch
 * 对齐（builtin→全部；claudecode→anthropic；codex→openai-response；
 * piagent→anthropic/openai-chat/openai-response；其余后端→false）。
 * 列不兼容项必然在后端 Send 校验失败，前端只列兼容供应商。
 */
export function isProviderCompatible(
  backendType: string,
  providerType: string,
): boolean {
  switch (backendType) {
    case "builtin":
      return true;
    case "claudecode":
      return providerType === "anthropic";
    case "codex":
      return providerType === "openai-response";
    case "piagent":
      return (
        providerType === "anthropic" ||
        providerType === "openai-chat" ||
        providerType === "openai-response"
      );
    default:
      return false;
  }
}

export interface UseProviderPillOptions {
  /** agent 后端类型；非可选择的 backend（openclaw 等）不拉取供应商，pill 常显但
   *   disabled（决策 10：禁用而非隐藏）。 */
  backendType: string;
  /** agent 已绑定的 provider key；空串 = 未绑（CLI 登录态）。 */
  boundProviderKey?: string | null;
  /**
   * agent 已绑定的 model key；空串/null = 跟随该供应商默认，非空 = 固定模型。
   * undefined 表示新建会话的 ChatAgentItem 没有 model key：此时 UI 只承诺跟随绑定
   * 供应商，不猜测模型（spec 2026-08-13 决策 18）。
   */
  boundModelKey?: string | null;
  /**
   * >0 = 已有会话：选择立即持久化（调用 SetChatSessionModelTarget）；0 / undefined =
   * 新建会话：纯瞬态，首发 Send 时随 SendRequest.ProviderKey/ModelKey 透传
   * （spec 2026-08-11「新建与已有会话流程」）。
   */
  sessionId?: number;
  /**
   * 会话当前已持久化的 provider key（ChatSessionDetail.providerKey）。仅 sessionId>0
   * 时用于水合初始选择；会话切换 / 切换成功回填后随之更新本地显示。
   */
  persistedProviderKey?: string | null;
  /**
   * 会话当前已持久化的 model key（ChatSessionDetail.modelKey）。与 providerKey 组合成
   * 会话 ModelTarget 水合初始选择（spec 2026-08-11 决策 1）。
   */
  persistedModelKey?: string | null;
  /**
   * 持久化切换成功后的回调（典型 reloadSession()，把新追加的切换 notice 拉进
   * transcript；pill 标签本身已由 SetChatSessionModelTarget 的响应直接更新，不依赖它）。
   */
  onSwitched?: () => void;
  /**
   * 目标执行位置：空串 = 本机；非空数字串 = 远端设备 id（spec「Remote execution」）。
   * 非空时以目标 daemon 目录为可运行事实源（task 6 决策 12）：daemon 上不存在的
   * Provider/Model 禁用并标注需同步，旧 daemon（无 llm-model-target-v1 能力位）禁用
   * 全部 fixed-model，绝不静默降级；daemon 离线/目录为空 → 未验证的 fixed-model 无法选。
   */
  executionLocation?: string;
}

// ProviderPillState 的定义住在共享包里：它是触发器那两格的入参，两端同一份。
// 这里只再导出，宿主里的既有 import 路径不用动。
export type { ProviderPillState } from "@agentre-hub/agentre-ui";
import type { ProviderPillState } from "@agentre-hub/agentre-ui";
import { resolveProviderPillState } from "@agentre-hub/agentre-ui";

export interface UseProviderPillReturn {
  /** 当前选择的 provider key；空串 = 跟随 agent 绑定。新建会话是纯瞬态本地值，
   *  已有会话镜像 chat_sessions.provider_key（乐观更新，失败回滚）。 */
  providerKey: string;
  /** 当前选择的 model key；与 providerKey 组合成会话 ModelTarget。空 = provider-default
   *  （providerKey 非空时）或 inherit-agent（providerKey 空时）。 */
  modelKey: string;
  /** 由共享 ModelTargetPicker 发射的完整目标（providerKey + modelKey）。 */
  setTarget: (target: ModelTarget) => void;
  /** 与 backendType 兼容的供应商列表。 */
  providers: llm_provider_svc.ProviderItem[];
  /** effective backend 类型，供共享 Picker 的 provider.type 兼容过滤。 */
  backendType: string;
  /** Picker 目录（provider-default + fixed-model），由共享 useModelTargetCatalog 组装。 */
  catalog: PickerProvider[];
  /** 目标执行设备的 daemon 目录（非敏感摘要）；undefined = 本机（不启用远端门控）。 */
  remoteCatalog?: PickerProvider[];
  /** 目标执行设备是否公布 llm-model-target-v1 能力位；本机恒 true（不限制）。 */
  supportsFixedModel: boolean;
  /** 目标执行设备上是否缺少当前选中的 Provider（远端场景提示）。 */
  remoteMissing: boolean;
  /** 把本机 Provider（含 API Key）显式同步到目标设备，并刷新远端目录。 */
  syncProvider: (providerKey: string) => Promise<void>;
  /** 本机是否真有一条通往目标设备的同步通道（已配对的 agentred 行）。为 false 时
   *  目标设备只能被门控、不能被同步——此时不得提供同步入口（否则点了什么都不会发生）。 */
  canSyncProvider: boolean;
  /** 目标执行位置（透传自选项，供 ProviderPill 传给共享 Picker）。 */
  executionLocation: string;
  /** 模型目录加载中 → pill 禁用。 */
  catalogLoading: boolean;
  /** 模型目录拉取失败（全部失败）→ 弹层底部错误行。 */
  catalogError: boolean;
  /** 列表加载中 → pill 禁用。 */
  loading: boolean;
  /** 拉取失败 或 持久化切换失败 → 弹层底部错误行。 */
  error: string | null;
  /** 未绑 agent（CLI 登录态）。 */
  unbound: boolean;
  /** 生效 key：providerKey || boundProviderKey（用于 pill 标签 / 高亮）。 */
  effectiveKey: string;
  /** Composer 常驻 pill 的四态与解析结果。 */
  pillState: ProviderPillState;
  /**
   * 目录里真正解析出来的模型 ID；解析不出（目录还没到 / Provider 或 Model 被删停用）
   * 时是空串，**绝不回落 model key**。给「需要一个人读模型名」的槽用（转录脚注在
   * 消息自己的 model 为空时回退到它）——model key 是 uuid.NewString() 生成的引用键,
   * 写到脸上就是一串 UUID。pillState.modelLabel 不承担这件事：失效态的 pill 要把
   * 指向的那个键显示出来（「<key> · 目标已失效」），两者语义不同。
   */
  resolvedModelLabel: string;
  /** Picker 顶部「跟随 Agent 绑定」项的解析副行。 */
  boundResolutionLabel: string;
  /** 绑定供应商的类型（品牌标识判定用）；空串 = 目录里解析不出供应商。 */
  boundProviderType: string;
  /** 绑定供应商的人读名（品牌标识回落 + 副行正文）。 */
  boundProviderLabel: string;
  /** 绑定解析出的模型 ID；空串 = 只承诺供应商（新建会话没有 agent model key）。 */
  boundModelLabel: string;
  /** 确知该 Agent 后端没绑供应商（走 CLI 自身登录账号）；「还不知道」时为 false。 */
  boundCliLogin: boolean;
  /** 当前选中 target 在目录里解析不出来（Provider/Model 缺失/停用/被删）→「目标已失效」。 */
  invalid: boolean;
  /** pill 是否禁用（规格「UI 与禁用状态」状态表：加载中 / 后端不可选 / 无兼容供应商）。 */
  disabled: boolean;
  /** disabled 的原因，供 tooltip 说明；null = 未禁用或禁用原因是既有的「加载中」
   *   （沿用既有行为，不需要新增说明）。 */
  disabledReason: "unsupportedBackend" | "noCompatibleProviders" | null;
}

/**
 * useProviderPill: composer 里 LLM ModelTarget 选择器的状态（新建会话瞬态选择 + 已有
 * 会话立即持久化切换，spec 2026-08-10 决策 1/9/10 + 2026-08-11「新建与已有会话流程」）。
 *
 * 数据流:
 *  - 新建会话（sessionId<=0）还没有 session 行，瞬态选择只存本地 state，首发 Send
 *    时随 SendRequest.ProviderKey/ModelKey 透传给后端随 Session 一起落库。
 *  - 已有会话（sessionId>0）：初始选择水合自 persistedProviderKey/persistedModelKey；
 *    选中一项立即调 SetChatSessionModelTarget(sessionId, providerKey, modelKey) 持久化
 *    （乐观更新，失败回滚并把错误显示在弹层底部）；成功后按响应更新显示并调
 *    onSwitched()（典型是 reloadSession()，把后端追加的切换 notice 拉进 transcript）。
 *  - 供应商列表只随 backendType 变化拉取一次；模型目录经共享 useModelTargetCatalog 按
 *    Provider 拉取。Picker 的兼容过滤与后端 ProviderTypeMatch 对齐。
 *  - pill 在任何会话/后端状态下都渲染（决策 10：禁用而非隐藏）。
 */
export function useProviderPill({
  backendType,
  boundProviderKey,
  boundModelKey,
  sessionId,
  persistedProviderKey,
  persistedModelKey,
  onSwitched,
  executionLocation = "",
}: UseProviderPillOptions): UseProviderPillReturn {
  const selectable = isProviderSelectableBackend(backendType);
  const [providerKey, setProviderKeyState] = React.useState(
    persistedProviderKey ?? "",
  );
  const [modelKey, setModelKeyState] = React.useState(persistedModelKey ?? "");
  const [providers, setProviders] = React.useState<
    llm_provider_svc.ProviderItem[]
  >([]);
  const [loading, setLoading] = React.useState(selectable);
  const [error, setError] = React.useState<string | null>(null);

  // backendTypeRef 让在途的 ListLLMProviders 响应按「解析时刻的当前 backend」过滤，
  // 避免 agent 快速切换时旧请求晚到、把上一后端的兼容集合短暂画进弹层。
  const backendTypeRef = React.useRef(backendType);

  const fetchProviders = React.useCallback(async () => {
    if (!isProviderSelectableBackend(backendTypeRef.current)) {
      setProviders([]);
      setLoading(false);
      setError(null);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const resp = await ListLLMProviders();
      const all = resp.items ?? [];
      setProviders(
        all.filter((p) => isProviderCompatible(backendTypeRef.current, p.type)),
      );
    } catch {
      setProviders([]);
      setError(i18n.t("providerPill.errorFetch"));
    } finally {
      setLoading(false);
    }
  }, []);

  // 供应商列表只随 backendType 变化重新拉取：与后端类型强绑定，两条同类型的已有
  // 会话之间切换不重复请求。
  React.useEffect(() => {
    backendTypeRef.current = backendType;
    setError(null);
    void fetchProviders();
  }, [backendType, fetchProviders]);

  // 模型目录：只对兼容供应商拉取。
  const {
    catalog,
    loading: catalogLoading,
    error: catalogError,
  } = useModelTargetCatalog(providers);

  // 会话切换（sessionId 变化）、agent 切换（backendType 变化，新建会话场景）、或
  // 持久化选择被外部改写（切换成功回填 / 另一处 reload）时，把显示值同步回 DB 当前
  // 值：新建会话固定是空串；已有会话取 persistedProviderKey/persistedModelKey。
  React.useEffect(() => {
    setProviderKeyState(persistedProviderKey ?? "");
    setModelKeyState(persistedModelKey ?? "");
  }, [sessionId, backendType, persistedProviderKey, persistedModelKey]);

  const setTarget = React.useCallback(
    (target: ModelTarget) => {
      const nextProvider = target.providerKey ?? "";
      const nextModel = target.modelKey ?? "";
      // 选中的就是当前已生效的同一完整组合：什么都不做，与后端
      // SetChatSessionModelTarget 的同一处幂等 no-op 语义对齐（避免每点一次已选中项
      // 就打一次 IPC，也避免同一 provider 的 provider-default / fixed-model 误判）。
      if (nextProvider === providerKey && nextModel === modelKey) {
        return;
      }
      const previousProvider = providerKey;
      const previousModel = modelKey;
      setProviderKeyState(nextProvider);
      setModelKeyState(nextModel);
      setError(null);

      if (!sessionId || sessionId <= 0) {
        // 新建会话：纯瞬态，首发 Send 时随 SendRequest.ProviderKey/ModelKey 透传。
        return;
      }
      void SetChatSessionModelTarget({
        sessionId,
        providerKey: nextProvider,
        modelKey: nextModel,
      })
        .then(() => {
          // 只有 target 成功持久化后才记录最近使用（spec「UI, accessibility and
          // recent targets」决策 19）。按执行位置指纹隔离：本机会话落 local，远端
          // 会话落 daemon:<deviceID>，远端选择绝不进入本机最近。
          recordRecentTarget("chat", executionLocation, {
            providerKey: nextProvider,
            modelKey: nextModel,
          });
          onSwitched?.();
        })
        .catch((e: unknown) => {
          const msg = e instanceof Error ? e.message : String(e);
          console.error("[provider-pill] switch failed", e);
          setProviderKeyState(previousProvider);
          setModelKeyState(previousModel);
          setError(msg);
        });
    },
    [providerKey, modelKey, sessionId, executionLocation, onSwitched],
  );

  // disabledReason 优先级：后端不可选 > 加载中（无专属原因，沿用既有行为）>
  // 加载成功但无兼容供应商。拉取失败时 providers 也是空数组，但不算「无兼容供应商」
  // ——失败态本身保持可用，靠弹层底部错误行说明（既有行为），不能被这条规则误判禁用。
  const disabledReason: "unsupportedBackend" | "noCompatibleProviders" | null =
    !selectable
      ? "unsupportedBackend"
      : !loading && !error && providers.length === 0
        ? "noCompatibleProviders"
        : null;

  // 绑定值那一态：pill 在「跟随 Agent 绑定」时的脸，也是 Picker 顶部那一项的副行。
  // 推导本身住在共享包里 —— 它是 ProviderPillTrigger 那两格的入参算法，与被它喂的
  // 呈现件分居两地时会漂（agentre-server 那份就漂成了另一套失效判定与模型脸）。
  const boundState = React.useMemo(
    () =>
      resolveProviderPillState({
        boundProviderKey,
        boundModelKey,
        target: { providerKey: "", modelKey: "" },
        catalog,
      }),
    [boundModelKey, boundProviderKey, catalog],
  );

  const pillState = React.useMemo(
    () =>
      resolveProviderPillState({
        boundProviderKey,
        boundModelKey,
        target: { providerKey, modelKey },
        catalog,
      }),
    [boundModelKey, boundProviderKey, catalog, modelKey, providerKey],
  );

  // invalid：选中了目标，但它在目录里解析不出来（Provider/Model 缺失/停用/被删）。
  // 未选（inherit-agent / 空 target）恒不 invalid —— 推导在那一路直接给跟随态。
  const invalid = pillState.mode === "invalid";

  // ── 远端门控（gap 1）：目标执行设备是远端时，以 daemon 目录为可运行事实源。──────
  // executionLocation 可以是 canonical fingerprint，也兼容遗留 paired-row 数字 ID；
  // 只有调用本地 RemoteDevice* RPC 时才翻成本安装的 paired row。
  const [localFingerprint, setLocalFingerprint] = React.useState("");
  const [remoteDevices, setRemoteDevices] = React.useState<DeviceView[]>([]);
  const [remoteProviders, setRemoteProviders] = React.useState<
    ProviderSummary[]
  >([]);
  const executionDevice = React.useMemo(
    () =>
      resolveExecutionDevice(
        executionLocation,
        localFingerprint,
        remoteDevices,
      ),
    [executionLocation, localFingerprint, remoteDevices],
  );
  const remoteDeviceID = executionDevice.pairedDeviceId;
  // 换目标后旧一轮的迟到结果直接丢弃，否则上一台机器的目录会盖住当前目标的门控。
  const remoteRequestRef = React.useRef(0);
  const fetchRemote = React.useCallback(async () => {
    const request = ++remoteRequestRef.current;
    const stale = () => remoteRequestRef.current !== request;
    if (executionLocation === "") {
      setLocalFingerprint("");
      setRemoteDevices([]);
      setRemoteProviders([]);
      return;
    }
    // 两个 RPC 各自结算：配对目录拉取失败不该连带丢掉已经读到的本机指纹，否则本机
    // 自身的 canonical fingerprint 会被当成另一台机器（resolveExecutionDevice 的
    // 「未定」分支只在指纹真的未知时才该出现）。
    const [fingerprint, dvRows] = await Promise.all([
      RemoteDeviceFingerprint().catch(() => ""),
      RemoteDeviceList().catch(() => null),
    ]);
    if (stale()) return;
    const devices = (dvRows ?? []) as unknown as DeviceView[];
    setLocalFingerprint(fingerprint ?? "");
    setRemoteDevices(devices);
    const resolved = resolveExecutionDevice(
      executionLocation,
      fingerprint ?? "",
      devices,
    );
    if (!resolved.remote || resolved.pairedDeviceId <= 0) {
      setRemoteProviders([]);
      return;
    }
    try {
      const provRows = await RemoteDeviceListProviders(resolved.pairedDeviceId);
      if (stale()) return;
      setRemoteProviders((provRows ?? []) as unknown as ProviderSummary[]);
    } catch {
      // daemon 离线 / 拉取失败 → 目录视为空：未验证的 fixed-model 无法选（严格）。
      if (stale()) return;
      setRemoteProviders([]);
    }
  }, [executionLocation]);
  React.useEffect(() => {
    void fetchRemote();
  }, [fetchRemote]);

  const supportsFixedModel = React.useMemo(() => {
    if (!executionDevice.remote) return true; // 本机：不设能力位限制。
    if (remoteDeviceID <= 0) return false; // 未配对 fingerprint：远端能力未知，严格禁用。
    const dv = remoteDevices.find((d) => d.id === remoteDeviceID);
    // 设备离线 / 能力位未知 → false：远端 fixed-model 一律禁用（绝不静默降级）。
    return dv?.supportsLLMModelTarget ?? false;
  }, [executionDevice.remote, remoteDevices, remoteDeviceID]);

  // 把 daemon 目录转成 PickerProvider[]（非敏感摘要），供 Picker 远端门控。
  const remoteCatalog = React.useMemo<PickerProvider[] | undefined>(() => {
    if (!executionDevice.remote) return undefined;
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
  }, [executionDevice.remote, remoteProviders]);

  // 当前选中的 provider 在 daemon 目录里缺失 → 弹层底部 remoteMissing 提示。
  const remoteMissing = React.useMemo(() => {
    if (!executionDevice.remote || !remoteCatalog) return false;
    const key = providerKey || boundProviderKey || "";
    if (key === "") return false;
    return !remoteCatalog.some((p) => p.providerKey === key);
  }, [executionDevice.remote, remoteCatalog, providerKey, boundProviderKey]);

  const syncProvider = React.useCallback(
    async (key: string) => {
      if (remoteDeviceID <= 0) return;
      await RemoteDeviceSyncProvider(remoteDeviceID, key);
      await fetchRemote();
    },
    [fetchRemote, remoteDeviceID],
  );

  return {
    providerKey,
    modelKey,
    setTarget,
    providers,
    backendType,
    catalog,
    catalogLoading,
    catalogError,
    loading,
    error,
    unbound: !boundProviderKey,
    effectiveKey: providerKey || boundProviderKey || "",
    pillState,
    resolvedModelLabel: invalid ? "" : pillState.modelLabel,
    boundResolutionLabel: boundState.resolutionLabel,
    boundProviderType: boundState.providerType,
    boundProviderLabel: boundState.providerLabel,
    boundModelLabel: boundState.modelLabel,
    boundCliLogin: boundState.cliLogin,
    invalid,
    disabled: loading || catalogLoading || disabledReason !== null,
    disabledReason,
    executionLocation,
    remoteCatalog,
    supportsFixedModel,
    remoteMissing,
    syncProvider,
    canSyncProvider: remoteDeviceID > 0,
  };
}
