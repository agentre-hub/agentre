// @ts-nocheck
/**
 * Host boundary for the shared engine-settings panels.
 *
 * Views deliberately contain only displayable account configuration. In
 * particular they never contain an API-key value or an absolute CLI path.
 * Hosts that can edit a local CLI override expose that capability separately
 * through the optional `cliPath` port.
 */
export type EngineID = string | number;

export type CliStatus = "recognized" | "path" | "unchecked";

export type CliByDeviceView = {
  deviceId: string;
  name?: string;
  status: CliStatus;
};

export type ModelView = {
  id: EngineID;
  providerId?: EngineID;
  providerKey: string;
  modelKey: string;
  modelId: string;
  name: string;
  contextWindow: number;
  maxOutput: number;
  enabled: boolean;
  isDefault?: boolean;
};

export type ProviderView = {
  id: EngineID;
  providerKey: string;
  name: string;
  type: string;
  baseUrl: string;
  /** A tail such as ••••1234; never a credential. */
  maskedApiKey?: string;
  hasApiKey: boolean;
  enabled: boolean;
  defaultModelKey: string;
  modelCount?: number;
};

/** A display DTO. Do not add cliPath, apiKey, envJson, or other secrets here. */
export type BackendView = {
  id: EngineID;
  syncId: string;
  name: string;
  type: string;
  llmProviderKey: string;
  llmModelKey: string;
  llmProviderName?: string;
  llmProviderType?: string;
  llmProviderModel?: string;
  llmProviderActive?: boolean;
  agentCount?: number;
  deviceName?: string;
  openClawGatewayUrl?: string;
  openClawAgentId?: string;
  openClawDefaultModel?: string;
  cliByDevice: CliByDeviceView[];
};

export type ReferenceCounts = {
  backends: number;
  sessions: number;
  routes: number;
};

export type TestResult = {
  ok: boolean;
  message: string;
  latencyMs?: number;
  code?: string;
  [key: string]: unknown;
};

export type DiscoveredModel = {
  id: string;
  name?: string;
  contextWindow?: number;
  maxOutput?: number;
};

export type ProviderInput = {
  providerKey?: string;
  type: string;
  name: string;
  /** Create/update input only; it is never exposed through ProviderView. */
  apiKey?: string;
  baseUrl: string;
  defaultModelKey?: string;
  models?: Array<Partial<ModelView>>;
  [key: string]: unknown;
};

export type BackendInput = {
  id?: EngineID;
  syncId?: string;
  type: string;
  name: string;
  llmProviderKey?: string;
  llmModelKey?: string;
  [key: string]: unknown;
};

/**
 * All mandatory CRUD operations are account-safe. Optional operations are
 * device capabilities: their absence is a feature signal, not an invitation
 * for a disabled button. Panels therefore do not render their affordance.
 */
export interface EngineSettingsPorts {
  listProviders(): Promise<ProviderView[]>;
  listModels(providerId: EngineID): Promise<ModelView[]>;
  createProvider(input: ProviderInput): Promise<ProviderView>;
  updateProvider(id: EngineID, input: ProviderInput): Promise<ProviderView>;
  deleteProvider(id: EngineID): Promise<void>;
  setProviderEnabled(id: EngineID, enabled: boolean): Promise<ProviderView>;
  setModelEnabled(id: EngineID, enabled: boolean): Promise<ModelView>;
  createModels(providerId: EngineID, models: Array<Partial<ModelView>>): Promise<ModelView[]>;
  updateModel(id: EngineID, input: Partial<ModelView>): Promise<ModelView>;
  deleteModel(id: EngineID): Promise<void>;
  setDefaultModel(providerId: EngineID, modelId: EngineID): Promise<ProviderView>;
  providerReferenceCounts?(providerKey: string): Promise<ReferenceCounts>;
  modelReferenceCounts?(modelKey: string): Promise<ReferenceCounts>;
  lookupModel?(providerId: EngineID, modelId: string): Promise<DiscoveredModel | null>;

  listBackends(): Promise<BackendView[]>;
  createBackend(input: BackendInput): Promise<BackendView>;
  updateBackend(id: EngineID, input: BackendInput): Promise<BackendView>;
  deleteBackend(id: EngineID): Promise<void>;
  testBackend?(input: BackendInput): Promise<TestResult>;

  /** Optional device-only actions. Missing means hide the action. */
  testProvider?(providerKey: string, modelKey?: string): Promise<TestResult>;
  discoverModels?(providerKey: string): Promise<DiscoveredModel[]>;
  scanBackends?(): Promise<BackendView[]>;
  cliPath?: {
    get(backendSyncId: string): Promise<string | null>;
    set(backendSyncId: string, path: string): Promise<void>;
  };
}
