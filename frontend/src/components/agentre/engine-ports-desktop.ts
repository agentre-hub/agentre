import {
  CancelTestAgentBackend,
  CreateAgentBackend,
  CreateLLMProvider,
  CreateOpenClawAgentBackend,
  DeleteAgentBackend,
  DeleteLLMModel,
  DeleteLLMProvider,
  GetGatewayStatus,
  ImportLLMModels,
  LLMModelRefCounts,
  LLMProviderRefCounts,
  ListAgentBackends,
  ListLLMModels,
  ListLLMProviders,
  LookupLLMModel,
  PreviewLLMModels,
  RemoteDeviceFingerprint,
  RemoteDeviceList,
  RemoteDeviceListProviders,
  RemoteDeviceSyncProvider,
  ResolveAgentBackendCLIPath,
  ScanAndCreateAgentBackends,
  ServerListDevices,
  SetLLMModelDefault,
  SetLLMModelEnabled,
  SetLLMProviderEnabled,
  TestAgentBackend,
  TestLLMProvider,
  TestOpenClawAgentBackend,
  UpdateAgentBackend,
  UpdateOpenClawAgentBackend,
  UpdateLLMModel,
  UpdateLLMProvider,
} from "../../../wailsjs/go/app/App";
import { agent_backend_svc, llm_provider_svc } from "../../../wailsjs/go/models";
import type {
  BackendView,
  EngineID,
  EngineSettingsPorts,
  ModelView,
  ProviderView,
} from "@agentre-ai/agentre-ui";

function required<T>(value: T | undefined, label: string): T {
  if (value === undefined) throw new Error(`${label} was missing from the response`);
  return value;
}

function emptyTestResult(ok: boolean, message: string, latencyMs?: number) {
  return { ok, message, latencyMs, openClawAgents: [], openClawModels: [], grantedScopes: [] };
}

function providerView(item: llm_provider_svc.ProviderItem): ProviderView {
  return {
    id: item.id,
    providerKey: item.providerKey,
    name: item.name,
    type: item.type,
    baseUrl: item.baseUrl,
    maskedApiKey: item.maskedApiKey,
    hasApiKey: item.hasApiKey,
    enabled: item.enabled,
    defaultModelKey: item.defaultModelKey,
    modelCount: item.modelCount,
  };
}

function modelView(item: llm_provider_svc.ModelItem): ModelView {
  return {
    id: item.id,
    providerId: item.providerId,
    providerKey: item.providerKey,
    modelKey: item.modelKey,
    modelId: item.modelId,
    name: item.name,
    contextWindow: item.contextWindow,
    maxOutput: item.maxOutput,
    enabled: item.enabled,
    isDefault: item.isDefault,
  };
}

function backendView(item: agent_backend_svc.BackendItem): BackendView {
  return {
    id: item.id,
    syncId: String(item.id),
    name: item.name,
    type: item.type,
    llmProviderKey: item.llmProviderKey ?? "",
    llmModelKey: item.llmModelKey ?? "",
    llmProviderName: item.llmProviderName,
    llmProviderType: item.llmProviderType,
    llmProviderModel: item.llmProviderModel,
    llmProviderActive: item.llmProviderActive,
    agentCount: item.agentCount,
    deviceName: item.deviceName,
    openClawGatewayUrl: item.openClawGatewayUrl,
    openClawAgentId: item.openClawAgentId,
    openClawDefaultModel: item.openClawDefaultModel,
    hasToken: item.hasToken,
    deviceId: item.deviceId,
    // The shared view never carries item.cliPath. Desktop keeps it in this
    // closure and exposes it only through the optional desktop-only port.
    cliByDevice: [{ deviceId: "desktop", status: item.cliPath ? "path" : "unchecked" }],
  };
}

/** Wails-only wiring for the shared engine UI. */
export function createDesktopEngineSettingsPorts(options: {
  onRuntimeDeviceState?: (listener: (payload: unknown) => void) => () => void;
} = {}): EngineSettingsPorts {
  const backendRows = new Map<EngineID, agent_backend_svc.BackendItem>();

  return {
    async listProviders() {
      return ((await ListLLMProviders()).items ?? []).map(providerView);
    },
    async listModels(providerID) {
      return ((await ListLLMModels(new llm_provider_svc.ListModelsRequest({ id: Number(providerID) }))).items ?? []).map(modelView);
    },
    async createProvider(input) {
      const response = await CreateLLMProvider(new llm_provider_svc.CreateProviderRequest(input));
      return providerView(required(response.item, "Created provider"));
    },
    async updateProvider(id, input) {
      const response = await UpdateLLMProvider(new llm_provider_svc.UpdateProviderRequest({ ...input, id: Number(id) }));
      return providerView(required(response.item, "Updated provider"));
    },
    async deleteProvider(id) {
      await DeleteLLMProvider(new llm_provider_svc.DeleteProviderRequest({ id: Number(id) }));
    },
    async setProviderEnabled(id, enabled) {
      const response = await SetLLMProviderEnabled(new llm_provider_svc.SetProviderEnabledRequest({ id: Number(id), enabled }));
      return providerView(required(response.item, "Updated provider"));
    },
    async setModelEnabled(id, enabled) {
      const response = await SetLLMModelEnabled(new llm_provider_svc.SetModelEnabledRequest({ id: Number(id), enabled }));
      return modelView(required(response.item, "Updated model"));
    },
    async createModels(providerID, models) {
      const response = await ImportLLMModels(new llm_provider_svc.ImportModelsRequest({
        providerId: Number(providerID),
        models: models.map((model) => new llm_provider_svc.ModelInput(model)),
      }));
      return (response.items ?? []).map(modelView);
    },
    async updateModel(id, input) {
      const response = await UpdateLLMModel(new llm_provider_svc.UpdateModelRequest({ ...input, id: Number(id) }));
      return modelView(required(response.item, "Updated model"));
    },
    async deleteModel(id) {
      await DeleteLLMModel(new llm_provider_svc.DeleteModelRequest({ id: Number(id) }));
    },
    async setDefaultModel(providerID, modelID) {
      const model = (await ListLLMModels(new llm_provider_svc.ListModelsRequest({ id: Number(providerID) }))).items
        ?.find((item) => item.id === Number(modelID));
      if (!model) throw new Error("Model not found");
      const response = await SetLLMModelDefault(new llm_provider_svc.SetModelDefaultRequest({
        providerId: Number(providerID),
        modelKey: model.modelKey,
      }));
      return providerView(required(response.item, "Updated provider"));
    },
    async providerReferenceCounts(providerKey) {
      return required((await LLMProviderRefCounts(new llm_provider_svc.ProviderRefCountsRequest({ providerKey }))).counts, "Provider reference counts");
    },
    async modelReferenceCounts(modelKey) {
      return required((await LLMModelRefCounts(new llm_provider_svc.ModelRefCountsRequest({ modelKey }))).counts, "Model reference counts");
    },
    async lookupModel(providerID, modelID) {
      const response = await LookupLLMModel(new llm_provider_svc.LookupModelRequest({ id: Number(providerID), modelId: modelID }));
      return response.known ? {
        id: modelID,
        name: response.vendor,
        vendor: response.vendor,
        contextWindow: response.contextWindow,
        maxOutput: response.maxOutput,
      } : null;
    },
    async listBackends() {
      const items = (await ListAgentBackends()).items ?? [];
      backendRows.clear();
      for (const item of items) backendRows.set(item.id, item);
      return items.map(backendView);
    },
    async createBackend(input) {
      const response = await CreateAgentBackend(new agent_backend_svc.CreateBackendRequest(input));
      return backendView(required(response.item, "Created backend"));
    },
    async updateBackend(id, input) {
      const response = await UpdateAgentBackend(new agent_backend_svc.UpdateBackendRequest({ ...input, id: Number(id) }));
      return backendView(required(response.item, "Updated backend"));
    },
    async deleteBackend(id) {
      await DeleteAgentBackend(new agent_backend_svc.DeleteBackendRequest({ id: Number(id) }));
    },
    async testProvider(providerKey, modelKey) {
      const provider = (await ListLLMProviders()).items?.find((item) => item.providerKey === providerKey);
      if (!provider) throw new Error("Provider not found");
      const response = await TestLLMProvider(new llm_provider_svc.TestConnectionRequest({
        id: provider.id,
        useDraft: false,
        type: provider.type,
        apiKey: "",
        baseUrl: "",
        modelKey: modelKey ?? "",
        modelId: "",
      }));
      return emptyTestResult(response.ok, response.message);
    },
    async discoverModels(providerKey) {
      const provider = (await ListLLMProviders()).items?.find((item) => item.providerKey === providerKey);
      if (!provider) throw new Error("Provider not found");
      const response = await PreviewLLMModels(new llm_provider_svc.PreviewModelsRequest({
        id: provider.id,
        type: provider.type,
        apiKey: "",
        baseUrl: provider.baseUrl,
      }));
      return (response.items ?? []).map((item) => ({
        id: item.id,
        name: item.vendor,
        vendor: item.vendor,
        contextWindow: item.contextWindow,
        maxOutput: item.maxOutput,
      }));
    },
    async scanBackends() {
      await ScanAndCreateAgentBackends();
      const items = (await ListAgentBackends()).items ?? [];
      backendRows.clear();
      for (const item of items) backendRows.set(item.id, item);
      return items.map(backendView);
    },
    async scanBackendResults() {
      return (await ScanAndCreateAgentBackends()).results ?? [];
    },
    async testBackend(input) {
      const response = await TestAgentBackend(new agent_backend_svc.TestBackendRequest({ ...input, id: Number(input.id) }));
      return {
        ...emptyTestResult(response.ok, response.message, response.latencyMs),
        code: response.code,
        openClawAgents: response.openClawAgents ?? [],
        openClawModels: response.openClawModels ?? [],
        grantedScopes: response.grantedScopes ?? [],
        gatewayVersion: response.gatewayVersion,
        protocol: response.protocol,
      };
    },
    async resolveBackendCLIPath(backendType, deviceId) {
      return ResolveAgentBackendCLIPath({ type: backendType, deviceId } as agent_backend_svc.ResolveCLIPathRequest);
    },
    async cancelBackendTest(requestId) {
      await CancelTestAgentBackend({ requestId } as agent_backend_svc.CancelTestBackendRequest);
    },
    async createOpenClawBackend(input, token) {
      const response = await CreateOpenClawAgentBackend(new agent_backend_svc.CreateBackendRequest(input), token);
      return backendView(required(response.item, "Created OpenClaw backend"));
    },
    async updateOpenClawBackend(id, input, token, clearToken) {
      const response = await UpdateOpenClawAgentBackend(new agent_backend_svc.UpdateBackendRequest({ ...input, id: Number(id) }), token, clearToken);
      return backendView(required(response.item, "Updated OpenClaw backend"));
    },
    async testOpenClawBackend(input, token) {
      const response = await TestOpenClawAgentBackend(new agent_backend_svc.TestBackendRequest({ ...input, id: Number(input.id) }), token);
      return {
        ...emptyTestResult(response.ok, response.message, response.latencyMs),
        code: response.code,
        openClawAgents: response.openClawAgents ?? [],
        openClawModels: response.openClawModels ?? [],
        grantedScopes: response.grantedScopes ?? [],
        gatewayVersion: response.gatewayVersion,
        protocol: response.protocol,
      };
    },
    async gatewayStatus() {
      return await GetGatewayStatus() as unknown as Record<string, unknown>;
    },
    async localDeviceFingerprint() {
      return await RemoteDeviceFingerprint();
    },
    async listAccountDevices() {
      return await ServerListDevices();
    },
    async listRuntimeDevices() {
      return await RemoteDeviceList();
    },
    async listRuntimeDeviceProviders(deviceID) {
      return await RemoteDeviceListProviders(deviceID);
    },
    async syncRuntimeDeviceProvider(deviceID, providerKey) {
      await RemoteDeviceSyncProvider(deviceID, providerKey);
    },
    onRuntimeDeviceState: options.onRuntimeDeviceState,
    cliPath: {
      async get(backendSyncID) {
        return backendRows.get(Number(backendSyncID))?.cliPath || null;
      },
      async set(backendSyncID, path) {
        const row = backendRows.get(Number(backendSyncID));
        if (!row) throw new Error("Backend must be loaded before setting its CLI path");
        await UpdateAgentBackend(new agent_backend_svc.UpdateBackendRequest({ ...row, cliPath: path }));
        backendRows.set(row.id, new agent_backend_svc.BackendItem({ ...row, cliPath: path }));
      },
    },
  };
}
