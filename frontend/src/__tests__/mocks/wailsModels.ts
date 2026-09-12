// Minimal mocks for wailsjs/go/models used in tests.
// Wails-generated model classes are just plain-object pass-through constructors.
//
// **凡是生产代码当值用到的 `<ns>.<Request>.createFrom(...)`，这里都得有一条。**
// 漏一条不会报「找不到模块」：那个 namespace 直接是 `undefined`，读它的属性抛
// TypeError，而调用点通常裹在 try/catch 里 —— 表现是「点了保存什么都没发生」，
// 只在测试里出现，排查成本远高于在这里多写一行。

class ModelClass {
  static createFrom(source: Record<string, unknown> = {}) {
    return new this(source);
  }

  constructor(init?: Record<string, unknown>) {
    if (init) Object.assign(this, init);
  }
}

export const llm_provider_svc = {
  CreateProviderRequest: ModelClass,
  UpdateProviderRequest: ModelClass,
  DeleteProviderRequest: ModelClass,
  DeleteModelRequest: ModelClass,
  SetProviderEnabledRequest: ModelClass,
  SetModelEnabledRequest: ModelClass,
  ProviderRefCountsRequest: ModelClass,
  ModelRefCountsRequest: ModelClass,
  TestConnectionRequest: ModelClass,
  ListModelsRequest: ModelClass,
  PreviewModelsRequest: ModelClass,
  LookupModelRequest: ModelClass,
};

export const agent_backend_svc = {
  CreateBackendRequest: ModelClass,
  UpdateBackendRequest: ModelClass,
  DeleteBackendRequest: ModelClass,
  TestBackendRequest: ModelClass,
  CancelTestBackendRequest: ModelClass,
  GetCLIOverlayRequest: ModelClass,
  SetCLIOverlayRequest: ModelClass,
  ResolveCLIPathRequest: ModelClass,
};

export const agent_svc = {
  CreateAgentRequest: ModelClass,
  UpdateAgentRequest: ModelClass,
  DeleteAgentRequest: ModelClass,
  MoveAgentRequest: ModelClass,
  UploadAvatarRequest: ModelClass,
  DeleteAvatarRequest: ModelClass,
};

export const department_svc = {
  AgentSkillDTO: ModelClass,
  AgentToolDTO: ModelClass,
  CreateDepartmentRequest: ModelClass,
  UpdateDepartmentRequest: ModelClass,
  DeleteDepartmentRequest: ModelClass,
  MoveDepartmentRequest: ModelClass,
};

export const httpgateway = {};

export const chat_svc = {
  SendRequest: ModelClass,
  AnswerToolApprovalRequest: ModelClass,
  AnswerToolApprovalResponse: ModelClass,
  ReadDroppedImagesRequest: ModelClass,
};

export const hook_svc = {
  CreateHookRequest: ModelClass,
  UpdateHookRequest: ModelClass,
};
