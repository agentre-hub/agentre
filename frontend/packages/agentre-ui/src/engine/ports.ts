import type { BackendType } from "./agent-backends-shared";

/**
 * Host boundary for the shared engine-settings panels.
 *
 * Views deliberately contain only displayable account configuration. In
 * particular they never contain an API-key value or an absolute CLI path.
 * Hosts that can edit a local CLI override expose that capability separately
 * through the optional `cliPath` port.
 */
export type EngineID = number;

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
  agentCount: number;
  deviceName?: string;
  openClawGatewayUrl?: string;
  openClawAgentId?: string;
  openClawDefaultModel?: string;
  // hermes 独占：一个已在运行的 `hermes serve` 的 Server URL（规范形式 http(s)://host:port）。
  // 这里漏一个字段不会报错，只会让编辑弹窗把已保存的值显示成空。
  hermesUrl?: string;
  // hermes gated serve 的非敏感展示字段（write-read 回归的同一族）。
  hermesAuthProvider?: string;
  hermesUserId?: string;
  hasToken?: boolean;
  deviceId?: string;
  modelRoutes?: Record<string, { providerKey: string; modelKey: string }>;
  sandbox?: string;
  approval?: string;
  envJson?: string;
  reasoningEffort?: string;
  defaultPermissionMode?: string;
  defaultModel?: string;
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
  openClawAgents: Array<{ id: string; name?: string; default?: boolean }>;
  openClawModels: Array<{ id: string; name?: string; available?: boolean }>;
  grantedScopes: string[];
  gatewayVersion?: string;
  protocol?: string | number;
};

export type DiscoveredModel = {
  id: string;
  name?: string;
  vendor: string;
  contextWindow: number;
  maxOutput: number;
};

/** Host-neutral paired-runtime view used only by optional desktop capabilities. */
export type RuntimeDeviceView = {
  id: number;
  name: string;
  online: boolean;
  daemonFingerprint?: string;
  supportsLLMModelTarget?: boolean;
};

export type CliProbeResult = { path: string; found: boolean };

/** One GET /api/auth/providers entry. */
export type HermesAuthProviderView = {
  name: string;
  displayName: string;
  supportsPassword: boolean;
};

export type HermesLoginInput = {
  url: string;
  provider: string;
  username: string;
  password: string;
};

export type HermesLoginResult = { provider: string; userId: string };

export type HermesLogoutInput = { id?: EngineID; url?: string };

export type BackendScanResult = {
  name: string;
  found: boolean;
  created: boolean;
  skipped: boolean;
};

/**
 * An account-scoped device row. `kind` and `online` are what a host without a
 * local machine of its own has to go on: the panel keeps only the executable
 * kinds (`desktop` / `agentred`), and marks an offline device in words rather
 * than disabling it.
 */
export type AccountDeviceView = {
  fingerprint: string;
  name: string;
  kind?: string;
  online?: boolean;
};
export type GatewayStatusView = {
  status?: string;
  listenURL?: string;
  reason?: string;
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
  createModels(
    providerId: EngineID,
    models: Array<Partial<ModelView>>,
  ): Promise<ModelView[]>;
  updateModel(id: EngineID, input: Partial<ModelView>): Promise<ModelView>;
  deleteModel(id: EngineID): Promise<void>;
  setDefaultModel(
    providerId: EngineID,
    modelId: EngineID,
  ): Promise<ProviderView>;
  providerReferenceCounts?(providerKey: string): Promise<ReferenceCounts>;
  modelReferenceCounts?(modelKey: string): Promise<ReferenceCounts>;
  lookupModel?(
    providerId: EngineID,
    modelId: string,
  ): Promise<DiscoveredModel | null>;

  listBackends(): Promise<BackendView[]>;
  /**
   * Backend types whose full read/write/test contract this host implements.
   * Missing means every shared-package type is supported.
   */
  supportedBackendTypes?: readonly BackendType[];
  createBackend(input: BackendInput): Promise<BackendView>;
  updateBackend(id: EngineID, input: BackendInput): Promise<BackendView>;
  deleteBackend(id: EngineID): Promise<void>;
  testBackend?(input: BackendInput): Promise<TestResult>;

  /** Optional device-only actions. Missing means hide the action. */
  testProvider?(providerKey: string, modelKey?: string): Promise<TestResult>;
  discoverModels?(providerKey: string): Promise<DiscoveredModel[]>;
  scanBackends?(): Promise<BackendView[]>;
  /** `deviceId` names the machine to scan; hosts with a local machine omit it. */
  scanBackendResults?(deviceId?: string): Promise<BackendScanResult[]>;
  /** Host may expose the desktop-only EnvJSON editor. */
  canEditEnvJSON?: boolean;
  /**
   * Add `IS_SANDBOX=1` to an existing backend's env table, server-side.
   *
   * Only for hosts that cannot edit the env table themselves. The desktop owns
   * env_json locally and mutates its own entries instead; a browser host never
   * receives env_json, so the merge has to happen behind this call. Missing
   * means the host offers no way to set the key and the hint stays read-only.
   */
  addIsSandbox?(backendSyncId: string): Promise<void>;
  /** Browser hosts disable local-only built-in backend creation. */
  canCreateBuiltin?: boolean;
  /**
   * Per-(backend, device) CLI executable override. deviceId is the same value
   * as BackendView.deviceId / the editor's selected runtime device ("" = the
   * host's own local machine); switching device reads/writes a different row.
   */
  cliPath?: {
    get(backendSyncId: string, deviceId: string): Promise<string | null>;
    set(backendSyncId: string, deviceId: string, path: string): Promise<void>;
  };

  /** Desktop-only runtime capabilities. Browser hosts omit these methods. */
  resolveBackendCLIPath?(
    backendType: string,
    deviceId?: string,
  ): Promise<CliProbeResult>;
  cancelBackendTest?(requestId: string): Promise<void>;
  createOpenClawBackend?(
    input: BackendInput,
    token: string,
  ): Promise<BackendView>;
  updateOpenClawBackend?(
    id: EngineID,
    input: BackendInput,
    token: string,
    clearToken: boolean,
  ): Promise<BackendView>;
  testOpenClawBackend?(input: BackendInput, token: string): Promise<TestResult>;
  /** Hermes gated-serve login. Missing means the host cannot sign in. */
  listHermesAuthProviders?(url: string): Promise<HermesAuthProviderView[]>;
  loginHermesBackend?(input: HermesLoginInput): Promise<HermesLoginResult>;
  logoutHermesBackend?(input: HermesLogoutInput): Promise<void>;
  gatewayStatus?(): Promise<GatewayStatusView>;
  localDeviceFingerprint?(): Promise<string>;
  listAccountDevices?(): Promise<AccountDeviceView[]>;
  listRuntimeDevices?(): Promise<RuntimeDeviceView[]>;
  listRuntimeDeviceProviders?(deviceID: number): Promise<unknown[]>;
  syncRuntimeDeviceProvider?(
    deviceID: number,
    providerKey: string,
  ): Promise<void>;
  onRuntimeDeviceState?(listener: (payload: unknown) => void): () => void;
}
