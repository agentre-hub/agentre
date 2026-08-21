// @ts-nocheck
import type {
  BackendInput,
  BackendView,
  EngineID,
  EngineSettingsPorts,
  ModelView,
  ProviderInput,
  ProviderView,
} from "./ports";

let currentPorts: EngineSettingsPorts | null = null;

/**
 * Compatibility bridge for the pre-extraction dialog tree. It translates the
 * former request-shaped calls at one boundary to the explicit shared ports.
 * Components never import host bindings; their host is selected by the panel.
 */
export function bindEngineSettingsPorts(ports: EngineSettingsPorts): void {
  currentPorts = ports;
}

function ports(): EngineSettingsPorts {
  if (!currentPorts) throw new Error("Engine settings ports are not bound");
  return currentPorts;
}

class Request {
  constructor(init: Record<string, unknown> = {}) {
    Object.assign(this, init);
  }
  static createFrom<T extends typeof Request>(this: T, init: Record<string, unknown> = {}) {
    return new this(init);
  }
}

export namespace llm_provider_svc {
  export type ProviderItem = ProviderView;
  export type ModelItem = ModelView;
  export type ModelInfo = { id: string; name?: string; contextWindow?: number; maxOutput?: number };
  export type ReferenceCounts = { backends: number; sessions: number; routes: number };
  export type ImportModelsResponse = { items?: ModelView[]; imported: number; updated: number };
  export class ListModelsRequest extends Request { declare id: number; }
  export class ProviderRefCountsRequest extends Request { declare providerKey: string; }
  export class ModelRefCountsRequest extends Request { declare modelKey: string; }
  export class TestConnectionRequest extends Request { declare providerKey: string; declare modelKey?: string; }
  export class SetModelEnabledRequest extends Request { declare id: number; declare enabled: boolean; }
  export class SetProviderEnabledRequest extends Request { declare id: number; declare enabled: boolean; }
  export class SetModelDefaultRequest extends Request { declare id: number; declare modelId: number; }
  export class DeleteProviderRequest extends Request { declare id: number; }
  export class DeleteModelRequest extends Request { declare id: number; }
  export class LookupModelRequest extends Request { declare id: number; declare modelId: string; }
  export class UpdateModelRequest extends Request { declare id: number; }
  export class ModelInput extends Request {}
  export class ImportModelsRequest extends Request { declare id: number; declare items: ModelInput[]; }
  export class CreateProviderRequest extends Request {}
  export class UpdateProviderRequest extends Request { declare id: number; }
  export class PreviewModelsRequest extends Request { declare id: number; declare type: string; declare apiKey: string; declare baseUrl: string; }
}

export async function ListLLMProviders() { return { items: await ports().listProviders() }; }
export async function ListLLMModels(request: llm_provider_svc.ListModelsRequest) { return { items: await ports().listModels(request.id) }; }
export async function LLMProviderRefCounts(request: llm_provider_svc.ProviderRefCountsRequest) { return { counts: await ports().providerReferenceCounts?.(request.providerKey) ?? { backends: 0, sessions: 0, routes: 0 } }; }
export async function LLMModelRefCounts(request: llm_provider_svc.ModelRefCountsRequest) { return { counts: await ports().modelReferenceCounts?.(request.modelKey) ?? { backends: 0, sessions: 0, routes: 0 } }; }
export async function TestLLMProvider(request: llm_provider_svc.TestConnectionRequest & { id?: EngineID; providerKey?: string }) {
  if (!ports().testProvider) throw new Error("Provider testing is unavailable");
  const provider = request.providerKey
    ? undefined
    : (await ports().listProviders()).find((item) => item.id === request.id);
  return ports().testProvider(request.providerKey ?? provider?.providerKey ?? "", request.modelKey);
}
export async function SetLLMModelEnabled(request: llm_provider_svc.SetModelEnabledRequest) { return { item: await ports().setModelEnabled(request.id, request.enabled) }; }
export async function SetLLMProviderEnabled(request: llm_provider_svc.SetProviderEnabledRequest) { return { item: await ports().setProviderEnabled(request.id, request.enabled) }; }
export async function SetLLMModelDefault(request: llm_provider_svc.SetModelDefaultRequest & { providerId?: EngineID; modelKey?: string }) {
  const providerID = request.id ?? request.providerId;
  if (providerID === undefined) throw new Error("Provider ID is required");
  const model = request.modelId === undefined && request.modelKey
    ? (await ports().listModels(providerID)).find((item) => item.modelKey === request.modelKey)
    : undefined;
  return { item: await ports().setDefaultModel(providerID, request.modelId ?? model?.id ?? 0) };
}
export async function DeleteLLMProvider(request: llm_provider_svc.DeleteProviderRequest) { await ports().deleteProvider(request.id); }
export async function DeleteLLMModel(request: llm_provider_svc.DeleteModelRequest) { await ports().deleteModel(request.id); }
export async function UpdateLLMModel(request: llm_provider_svc.UpdateModelRequest) { return { item: await ports().updateModel(request.id, request as Partial<ModelView>) }; }
export async function LookupLLMModel(request: llm_provider_svc.LookupModelRequest) {
  const found = await ports().lookupModel?.(request.id, request.modelId);
  return { known: Boolean(found), vendor: found?.name ?? "", contextWindow: found?.contextWindow ?? 0, maxOutput: found?.maxOutput ?? 0 };
}
export async function ImportLLMModels(request: llm_provider_svc.ImportModelsRequest) {
  const input = request as llm_provider_svc.ImportModelsRequest & { providerId?: EngineID; models?: Array<Partial<ModelView>> };
  const providerID = input.id ?? input.providerId;
  if (providerID === undefined) throw new Error("Provider ID is required");
  const items = await ports().createModels(providerID, (input.items ?? input.models ?? []) as Array<Partial<ModelView>>);
  return { items, imported: items.length, updated: 0 };
}
export async function CreateLLMProvider(request: llm_provider_svc.CreateProviderRequest) { return { item: await ports().createProvider(request as ProviderInput) }; }
export async function UpdateLLMProvider(request: llm_provider_svc.UpdateProviderRequest) { return { item: await ports().updateProvider(request.id, request as ProviderInput) }; }
export async function PreviewLLMModels(request: llm_provider_svc.PreviewModelsRequest) {
  if (!ports().discoverModels) throw new Error("Model discovery is unavailable");
  const provider = (await ports().listProviders()).find((item) => item.id === request.id);
  if (!provider) throw new Error("Provider not found");
  const items = await ports().discoverModels(provider.providerKey);
  return { items };
}

export namespace agent_backend_svc {
  export type BackendItem = BackendView;
  export type TestBackendResponse = { ok: boolean; message: string; latencyMs?: number; requestId?: string; openClawAgents?: Array<{ id: string; name?: string }>; openClawModels?: Array<{ id: string; name?: string; available?: boolean }>; grantedScopes?: string[]; gatewayVersion?: string; protocol?: string; code?: string };
  export type ResolveCLIPathRequest = { backendType: string; deviceId?: number };
  export type TestBackendRequest = BackendInput & { id?: EngineID; requestId?: string };
  export type CancelTestBackendRequest = { requestId: string };
  export type CreateBackendRequest = BackendInput;
  export type UpdateBackendRequest = BackendInput & { id: EngineID };
  export type DeleteBackendRequest = { id: EngineID };
}
export namespace httpgateway { export type GatewayStatus = { configured?: boolean; reachable?: boolean }; }

export async function ListAgentBackends() { return { items: await ports().listBackends() }; }
export async function CreateAgentBackend(input: agent_backend_svc.CreateBackendRequest) { return { item: await ports().createBackend(input) }; }
export async function UpdateAgentBackend(input: agent_backend_svc.UpdateBackendRequest) { return { item: await ports().updateBackend(input.id, input) }; }
export async function DeleteAgentBackend(input: agent_backend_svc.DeleteBackendRequest) { await ports().deleteBackend(input.id); }
export async function TestAgentBackend(input: agent_backend_svc.TestBackendRequest) {
  if (!ports().testBackend) throw new Error("Backend testing is unavailable");
  return ports().testBackend(input);
}
export async function ScanAndCreateAgentBackends() {
  if (!ports().scanBackends) throw new Error("Backend scanning is unavailable");
  return { items: await ports().scanBackends() };
}
export async function ResolveAgentBackendCLIPath(input: agent_backend_svc.ResolveCLIPathRequest) { return { path: input.backendType ? null : null }; }
export async function CancelTestAgentBackend() {}
export async function CreateOpenClawAgentBackend(input: agent_backend_svc.CreateBackendRequest) { return CreateAgentBackend(input); }
export async function UpdateOpenClawAgentBackend(input: agent_backend_svc.UpdateBackendRequest) { return UpdateAgentBackend(input); }
export async function TestOpenClawAgentBackend(input: agent_backend_svc.TestBackendRequest) { return TestAgentBackend(input); }
export async function GetGatewayStatus(): Promise<httpgateway.GatewayStatus> { return {}; }
export async function RemoteDeviceFingerprint() { return "desktop"; }
export async function RemoteDeviceList() { return []; }
export async function RemoteDeviceListProviders() { return []; }
export async function RemoteDeviceSyncProvider() {}
export async function ServerListDevices() { return []; }
export function EventsOn() { return () => {}; }
