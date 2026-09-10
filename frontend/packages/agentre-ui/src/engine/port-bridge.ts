/* eslint-disable @typescript-eslint/no-namespace --
 * 这个文件的全部意义就是**复刻 Wails 生成模块的形状**：面板是从桌面端整体抽过来的，
 * 里面写的是 `llm_provider_svc.ProviderItem` / `agent_backend_svc.BackendItem` 这类
 * 命名空间成员。抽包时若把它们改成 ES 模块导出，就得同时改掉几千行面板里的每一个
 * 调用点 —— 而这层 bridge 存在的目的恰恰是让抽离不改面板一个字。
 * 命名空间在这里是被模仿的对象，不是随手选的写法，因此本文件豁免这条规则。
 */
import type {
  BackendInput,
  BackendView,
  EngineID,
  EngineSettingsPorts,
  ModelView,
  ProviderInput,
  ProviderView,
  TestResult,
  GatewayStatusView,
} from "./ports";
import * as React from "react";

import { useEngineSettingsPorts } from "./ports-context";

class Request {
  constructor(init: Record<string, unknown> = {}) {
    Object.assign(this, init);
  }
  static createFrom<T extends typeof Request>(
    this: T,
    init: Record<string, unknown> = {},
  ) {
    return new this(init);
  }
}

export namespace llm_provider_svc {
  export type ProviderItem = ProviderView;
  export type ModelItem = ModelView;
  export type ModelInfo = {
    id: string;
    name?: string;
    vendor: string;
    contextWindow: number;
    maxOutput: number;
  };
  export type ReferenceCounts = {
    backends: number;
    sessions: number;
    routes: number;
  };
  export type ImportModelsResponse = {
    items?: ModelView[];
    imported: number;
    updated: number;
  };
  export class ListModelsRequest extends Request {
    declare id: number;
  }
  export class ProviderRefCountsRequest extends Request {
    declare providerKey: string;
  }
  export class ModelRefCountsRequest extends Request {
    declare modelKey: string;
  }
  export class TestConnectionRequest extends Request {
    declare id?: EngineID;
    declare providerKey?: string;
    declare modelKey?: string;
    declare useDraft?: boolean;
    declare type?: string;
    declare apiKey?: string;
    declare baseUrl?: string;
    declare modelId?: string;
  }
  export class SetModelEnabledRequest extends Request {
    declare id: number;
    declare enabled: boolean;
  }
  export class SetProviderEnabledRequest extends Request {
    declare id: number;
    declare enabled: boolean;
  }
  export class SetModelDefaultRequest extends Request {
    declare id?: number;
    declare modelId?: number;
    declare providerId?: number;
    declare modelKey?: string;
  }
  export class DeleteProviderRequest extends Request {
    declare id: number;
  }
  export class DeleteModelRequest extends Request {
    declare id: number;
  }
  export class LookupModelRequest extends Request {
    declare id?: number;
    declare modelId: string;
  }
  export class UpdateModelRequest extends Request {
    declare id: number;
  }
  export class ModelInput extends Request {}
  export class ImportModelsRequest extends Request {
    declare id?: number;
    declare providerId?: number;
    declare items?: ModelInput[];
    declare models?: ModelInput[];
  }
  export class CreateProviderRequest extends Request {}
  export class UpdateProviderRequest extends Request {
    declare id: number;
  }
  export class PreviewModelsRequest extends Request {
    declare id: number;
    declare type: string;
    declare apiKey: string;
    declare baseUrl: string;
  }
}

export namespace agent_backend_svc {
  export type BackendItem = BackendView;
  export type TestBackendResponse = TestResult & { requestId?: string };
  export type ResolveCLIPathRequest = { type: string; deviceId?: string };
  export type TestBackendRequest = BackendInput & {
    id?: EngineID;
    requestId?: string;
  };
  export type CancelTestBackendRequest = { requestId: string };
  export type CreateBackendRequest = BackendInput;
  export type UpdateBackendRequest = BackendInput & { id: EngineID };
  export type DeleteBackendRequest = { id: EngineID };
}
export namespace httpgateway {
  export type GatewayStatus = GatewayStatusView;
}

/**
 * Compatibility bridge for the pre-extraction dialog tree. It translates the
 * former request-shaped calls at one boundary to the explicit shared ports.
 * Components never import host bindings; their host is selected by the panel.
 */
export function createEngineSettingsBridge(
  getPorts: () => EngineSettingsPorts,
) {
  const ports = getPorts;

  async function ListLLMProviders() {
    return { items: await ports().listProviders() };
  }
  async function ListLLMModels(request: llm_provider_svc.ListModelsRequest) {
    return { items: await ports().listModels(request.id) };
  }
  async function LLMProviderRefCounts(
    request: llm_provider_svc.ProviderRefCountsRequest,
  ) {
    return {
      counts: (await ports().providerReferenceCounts?.(
        request.providerKey,
      )) ?? {
        backends: 0,
        sessions: 0,
        routes: 0,
      },
    };
  }
  async function LLMModelRefCounts(
    request: llm_provider_svc.ModelRefCountsRequest,
  ) {
    return {
      counts: (await ports().modelReferenceCounts?.(request.modelKey)) ?? {
        backends: 0,
        sessions: 0,
        routes: 0,
      },
    };
  }
  async function TestLLMProvider(
    request: llm_provider_svc.TestConnectionRequest & {
      id?: EngineID;
      providerKey?: string;
    },
  ) {
    if (!ports().testProvider)
      throw new Error("Provider testing is unavailable");
    const provider = request.providerKey
      ? undefined
      : (await ports().listProviders()).find((item) => item.id === request.id);
    return ports().testProvider!(
      request.providerKey ?? provider?.providerKey ?? "",
      request.modelKey,
    );
  }
  async function SetLLMModelEnabled(
    request: llm_provider_svc.SetModelEnabledRequest,
  ) {
    return { item: await ports().setModelEnabled(request.id, request.enabled) };
  }
  async function SetLLMProviderEnabled(
    request: llm_provider_svc.SetProviderEnabledRequest,
  ) {
    return {
      item: await ports().setProviderEnabled(request.id, request.enabled),
    };
  }
  async function SetLLMModelDefault(
    request: llm_provider_svc.SetModelDefaultRequest & {
      providerId?: EngineID;
      modelKey?: string;
    },
  ) {
    const providerID = request.id ?? request.providerId;
    if (providerID === undefined) throw new Error("Provider ID is required");
    const model =
      request.modelId === undefined && request.modelKey
        ? (await ports().listModels(providerID)).find(
            (item) => item.modelKey === request.modelKey,
          )
        : undefined;
    return {
      item: await ports().setDefaultModel(
        providerID,
        request.modelId ?? model?.id ?? 0,
      ),
    };
  }
  async function DeleteLLMProvider(
    request: llm_provider_svc.DeleteProviderRequest,
  ) {
    await ports().deleteProvider(request.id);
  }
  async function DeleteLLMModel(request: llm_provider_svc.DeleteModelRequest) {
    await ports().deleteModel(request.id);
  }
  async function UpdateLLMModel(request: llm_provider_svc.UpdateModelRequest) {
    return {
      item: await ports().updateModel(
        request.id,
        request as Partial<ModelView>,
      ),
    };
  }
  async function LookupLLMModel(request: llm_provider_svc.LookupModelRequest) {
    const found =
      request.id === undefined
        ? undefined
        : await ports().lookupModel?.(request.id, request.modelId);
    return {
      known: Boolean(found),
      vendor: found?.name ?? "",
      contextWindow: found?.contextWindow ?? 0,
      maxOutput: found?.maxOutput ?? 0,
    };
  }
  async function ImportLLMModels(
    request: llm_provider_svc.ImportModelsRequest,
  ) {
    const input = request as llm_provider_svc.ImportModelsRequest & {
      providerId?: EngineID;
      models?: Array<Partial<ModelView>>;
    };
    const providerID = input.id ?? input.providerId;
    if (providerID === undefined) throw new Error("Provider ID is required");
    const items = await ports().createModels(
      providerID,
      (input.items ?? input.models ?? []) as Array<Partial<ModelView>>,
    );
    return { items, imported: items.length, updated: 0 };
  }
  async function CreateLLMProvider(
    request: llm_provider_svc.CreateProviderRequest,
  ) {
    return { item: await ports().createProvider(request as ProviderInput) };
  }
  async function UpdateLLMProvider(
    request: llm_provider_svc.UpdateProviderRequest,
  ) {
    return {
      item: await ports().updateProvider(
        request.id,
        request as unknown as ProviderInput,
      ),
    };
  }
  async function PreviewLLMModels(
    request: llm_provider_svc.PreviewModelsRequest,
  ) {
    if (!ports().discoverModels)
      throw new Error("Model discovery is unavailable");
    const provider = (await ports().listProviders()).find(
      (item) => item.id === request.id,
    );
    if (!provider) throw new Error("Provider not found");
    const items = await ports().discoverModels!(provider.providerKey);
    return { items };
  }

  async function ListAgentBackends() {
    return { items: await ports().listBackends() };
  }
  async function CreateAgentBackend(
    input: agent_backend_svc.CreateBackendRequest,
  ) {
    return { item: await ports().createBackend(input) };
  }
  async function UpdateAgentBackend(
    input: agent_backend_svc.UpdateBackendRequest,
  ) {
    return { item: await ports().updateBackend(input.id, input) };
  }
  async function DeleteAgentBackend(
    input: agent_backend_svc.DeleteBackendRequest,
  ) {
    await ports().deleteBackend(input.id);
  }
  async function TestAgentBackend(input: agent_backend_svc.TestBackendRequest) {
    if (!ports().testBackend) throw new Error("Backend testing is unavailable");
    return ports().testBackend!(input);
  }
  async function ScanAndCreateAgentBackends(deviceId?: string) {
    if (ports().scanBackendResults)
      return { results: await ports().scanBackendResults!(deviceId) };
    if (!ports().scanBackends)
      throw new Error("Backend scanning is unavailable");
    return { items: await ports().scanBackends!() };
  }
  async function ResolveAgentBackendCLIPath(
    input: agent_backend_svc.ResolveCLIPathRequest,
  ) {
    // 端口缺席 = 这次探测**根本没有发出**，那是关于探测的陈述，不是关于目标机的。
    // 从前的 `{found:false}` 缺省把「没问过」渲染成「没装」——一个凭空捏造的否定
    // 结论。抛出去，让调用方落到「没探到」那一格。
    const resolve = ports().resolveBackendCLIPath;
    if (!resolve) throw new Error("CLI path probing is unavailable");
    return resolve(input.type, input.deviceId);
  }
  async function CancelTestAgentBackend(
    input: agent_backend_svc.CancelTestBackendRequest,
  ) {
    await ports().cancelBackendTest?.(input.requestId);
  }
  async function CreateOpenClawAgentBackend(
    input: agent_backend_svc.CreateBackendRequest,
    token: string,
  ) {
    if (ports().createOpenClawBackend)
      return { item: await ports().createOpenClawBackend!(input, token) };
    return CreateAgentBackend(input);
  }
  async function UpdateOpenClawAgentBackend(
    input: agent_backend_svc.UpdateBackendRequest,
    token: string,
    clearToken = false,
  ) {
    if (ports().updateOpenClawBackend)
      return {
        item: await ports().updateOpenClawBackend!(
          input.id,
          input,
          token,
          clearToken,
        ),
      };
    return UpdateAgentBackend(input);
  }
  async function TestOpenClawAgentBackend(
    input: agent_backend_svc.TestBackendRequest,
    token: string,
  ) {
    if (ports().testOpenClawBackend)
      return ports().testOpenClawBackend!(input, token);
    return TestAgentBackend(input);
  }
  async function GetGatewayStatus(): Promise<httpgateway.GatewayStatus> {
    return ((await ports().gatewayStatus?.()) ??
      {}) as httpgateway.GatewayStatus;
  }
  async function RemoteDeviceFingerprint() {
    return ports().localDeviceFingerprint?.() ?? "";
  }
  async function RemoteDeviceList() {
    return ports().listRuntimeDevices?.() ?? [];
  }
  async function RemoteDeviceListProviders(deviceID: number) {
    return ports().listRuntimeDeviceProviders?.(deviceID) ?? [];
  }
  async function RemoteDeviceSyncProvider(
    deviceID: number,
    providerKey: string,
  ) {
    await ports().syncRuntimeDeviceProvider?.(deviceID, providerKey);
  }
  async function ServerListDevices() {
    return ports().listAccountDevices?.() ?? [];
  }
  function EventsOn(_event: string, listener: (payload: unknown) => void) {
    return ports().onRuntimeDeviceState?.(listener) ?? (() => {});
  }

  return {
    ListLLMProviders,
    ListLLMModels,
    LLMProviderRefCounts,
    LLMModelRefCounts,
    TestLLMProvider,
    SetLLMModelEnabled,
    SetLLMProviderEnabled,
    SetLLMModelDefault,
    DeleteLLMProvider,
    DeleteLLMModel,
    UpdateLLMModel,
    LookupLLMModel,
    ImportLLMModels,
    CreateLLMProvider,
    UpdateLLMProvider,
    PreviewLLMModels,
    ListAgentBackends,
    CreateAgentBackend,
    UpdateAgentBackend,
    DeleteAgentBackend,
    TestAgentBackend,
    ScanAndCreateAgentBackends,
    ResolveAgentBackendCLIPath,
    CancelTestAgentBackend,
    CreateOpenClawAgentBackend,
    UpdateOpenClawAgentBackend,
    TestOpenClawAgentBackend,
    GetGatewayStatus,
    RemoteDeviceFingerprint,
    RemoteDeviceList,
    RemoteDeviceListProviders,
    RemoteDeviceSyncProvider,
    ServerListDevices,
    EventsOn,
  };
}

export type EngineSettingsBridge = ReturnType<
  typeof createEngineSettingsBridge
>;

/**
 * 端口经 context 下发，因此两个面板同时挂载时各用各的那一份 —— 不再取决于谁最后
 * 渲染。
 *
 * 桥的身份必须常驻：两端的组合根都在渲染里行内构造 ports 对象（桌面是
 * `createDesktopEngineSettingsPorts()`），若桥随之换一个身份，把桥成员写进依赖数组
 * 的 effect 就会跟着宿主每一次重渲重跑一遍 —— 设置页自己的任何一次 setState 都会重
 * 拉一次后端与供应商列表。所以端口走 ref：只在提交后写入，只在 effect 与事件处理里
 * 读取，渲染期不读。
 */
export function useEngineSettingsBridge(): EngineSettingsBridge {
  const ports = useEngineSettingsPorts();
  const portsRef = React.useRef(ports);

  React.useEffect(() => {
    portsRef.current = ports;
  }, [ports]);

  const getPorts = React.useCallback(() => portsRef.current, []);

  // 桥的每个成员都是一次异步端口调用，只会从 effect 与事件处理里发起，渲染期不读
  // ref —— 规则看不出这一点，故在此处豁免。
  // eslint-disable-next-line react-hooks/refs -- 见上
  return React.useMemo(() => createEngineSettingsBridge(getPorts), [getPorts]);
}
