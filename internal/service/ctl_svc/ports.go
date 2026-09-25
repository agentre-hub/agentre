package ctl_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

//go:generate mockgen -source ports.go -destination mock_ctl_svc/mock_ports.go

// 生产写网关依赖的服务层窄投影（ISP）：每个方法都是现有服务的公开方法，写网关只做
// 「ctl 文档 → 服务请求」的翻译，校验、同步通知与 config:changed 都留在服务层。

// DepartmentService 部门读写（department_svc）。
type DepartmentService interface {
	Load(ctx context.Context, req *department_svc.LoadOrgRequest) (*department_svc.LoadOrgResponse, error)
	Create(ctx context.Context, req *department_svc.CreateDepartmentRequest) (*department_svc.CreateDepartmentResponse, error)
	Update(ctx context.Context, req *department_svc.UpdateDepartmentRequest) (*department_svc.UpdateDepartmentResponse, error)
	Move(ctx context.Context, req *department_svc.MoveDepartmentRequest) (*department_svc.MoveDepartmentResponse, error)
	Delete(ctx context.Context, req *department_svc.DeleteDepartmentRequest) (*department_svc.DeleteDepartmentResponse, error)
	CascadeImpact(ctx context.Context, departmentID int64) (departments, agents int, err error)
}

// AgentService Agent 写入（agent_svc）。
type AgentService interface {
	Create(ctx context.Context, req *agent_svc.CreateAgentRequest) (*agent_svc.CreateAgentResponse, error)
	Update(ctx context.Context, req *agent_svc.UpdateAgentRequest) (*agent_svc.UpdateAgentResponse, error)
	Move(ctx context.Context, req *agent_svc.MoveAgentRequest) (*agent_svc.MoveAgentResponse, error)
	Delete(ctx context.Context, req *agent_svc.DeleteAgentRequest) (*agent_svc.DeleteAgentResponse, error)
	SetPinned(ctx context.Context, req *agent_svc.SetPinnedRequest) (*agent_svc.SetPinnedResponse, error)
}

// ProjectService 项目写入（project_svc）。
type ProjectService interface {
	Create(ctx context.Context, req *project_svc.CreateProjectRequest) (*project_entity.Project, error)
	Update(ctx context.Context, req *project_svc.UpdateProjectRequest) (*project_entity.Project, error)
	Move(ctx context.Context, req *project_svc.MoveProjectRequest) (*project_entity.Project, error)
	Delete(ctx context.Context, id int64) error
	SetLocalPath(ctx context.Context, id int64, path string) (*project_entity.Project, error)
	ClearLocalPath(ctx context.Context, id int64) (*project_entity.Project, error)
	AddMember(ctx context.Context, projectID, agentID int64) error
	RemoveMember(ctx context.Context, projectID, agentID int64) error
}

// ProviderService 提供方与模型读写（llm_provider_svc）。
type ProviderService interface {
	List(ctx context.Context, req *llm_provider_svc.ListProvidersRequest) (*llm_provider_svc.ListProvidersResponse, error)
	Create(ctx context.Context, req *llm_provider_svc.CreateProviderRequest) (*llm_provider_svc.CreateProviderResponse, error)
	Update(ctx context.Context, req *llm_provider_svc.UpdateProviderRequest) (*llm_provider_svc.UpdateProviderResponse, error)
	Delete(ctx context.Context, req *llm_provider_svc.DeleteProviderRequest) (*llm_provider_svc.DeleteProviderResponse, error)
	SetProviderEnabled(ctx context.Context, req *llm_provider_svc.SetProviderEnabledRequest) (*llm_provider_svc.SetProviderEnabledResponse, error)
	ListModels(ctx context.Context, req *llm_provider_svc.ListModelsRequest) (*llm_provider_svc.ListModelsResponse, error)
	ImportModels(ctx context.Context, req *llm_provider_svc.ImportModelsRequest) (*llm_provider_svc.ImportModelsResponse, error)
	UpdateModel(ctx context.Context, req *llm_provider_svc.UpdateModelRequest) (*llm_provider_svc.UpdateModelResponse, error)
	SetModelDefault(ctx context.Context, req *llm_provider_svc.SetModelDefaultRequest) (*llm_provider_svc.SetModelDefaultResponse, error)
	SetModelEnabled(ctx context.Context, req *llm_provider_svc.SetModelEnabledRequest) (*llm_provider_svc.SetModelEnabledResponse, error)
	DeleteModel(ctx context.Context, req *llm_provider_svc.DeleteModelRequest) (*llm_provider_svc.DeleteModelResponse, error)
}

// BackendService Agent 后端读写（agent_backend_svc）。OpenClaw 的 token 由它路由到后端
// 绑定设备的钥匙串。
type BackendService interface {
	List(ctx context.Context, req *agent_backend_svc.ListBackendsRequest) (*agent_backend_svc.ListBackendsResponse, error)
	Create(ctx context.Context, req *agent_backend_svc.CreateBackendRequest) (*agent_backend_svc.CreateBackendResponse, error)
	CreateOpenClaw(ctx context.Context, req *agent_backend_svc.CreateBackendRequest, token string) (*agent_backend_svc.CreateBackendResponse, error)
	Update(ctx context.Context, req *agent_backend_svc.UpdateBackendRequest) (*agent_backend_svc.UpdateBackendResponse, error)
	UpdateOpenClaw(ctx context.Context, req *agent_backend_svc.UpdateBackendRequest, token string, clearToken bool) (*agent_backend_svc.UpdateBackendResponse, error)
	Delete(ctx context.Context, req *agent_backend_svc.DeleteBackendRequest) (*agent_backend_svc.DeleteBackendResponse, error)
}

// DeviceDirectory 已配对设备目录（remote_device_svc），把后端的 --device 名字解析成指纹。
type DeviceDirectory interface {
	List(ctx context.Context) ([]*remote_device_svc.DeviceView, error)
	// EnsureFromAccount 向 server 拉一次账号设备并收养（remote_device_svc.
	// EnsureFromAccount，与 App.ServerListDevices 同一实现），带回账号里「本机
	// 自己」那一行的展示名。deviceID 按名字在本地目录里找不到时调用它再重试一次
	// List，不必等前端先打开过设备面板才认得出账号里已有、本机还没记录的
	// agentred；selfName 命中时 --device 直接解析成本机（空指纹）。
	EnsureFromAccount(ctx context.Context) (selfName string, ok bool, err error)
}

// servicePorts 按调用现取服务单例（bootstrap 之后才注册），测试注入 mock。
type servicePorts struct {
	departments func() DepartmentService
	agents      func() AgentService
	projects    func() ProjectService
	providers   func() ProviderService
	backends    func() BackendService
	devices     func() DeviceDirectory
}

func productionPorts() servicePorts {
	return servicePorts{
		departments: func() DepartmentService { return department_svc.Department() },
		agents:      func() AgentService { return agent_svc.Agent() },
		projects:    func() ProjectService { return project_svc.Default() },
		providers:   func() ProviderService { return llm_provider_svc.LLMProvider() },
		backends:    func() BackendService { return agent_backend_svc.AgentBackend() },
		devices:     func() DeviceDirectory { return remote_device_svc.Default() },
	}
}
