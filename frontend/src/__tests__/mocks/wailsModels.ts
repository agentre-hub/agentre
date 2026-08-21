// Minimal mocks for wailsjs/go/models used in tests.
// Wails-generated model classes are just plain-object pass-through constructors.

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
};
