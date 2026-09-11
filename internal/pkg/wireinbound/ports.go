package wireinbound

import (
	"context"

	runtimewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	importwire "github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
	workspacewire "github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 端口是接口而不是具体类型,这是本包能住在 internal/pkg 的**前提**。
//
// 它若挂在某一种执行端名下(比如 internal/daemon),另一种执行端就得反向 import 它;
// 它若持有宿主的具体类型,横切叶子层就反向依赖宿主。两种执行端共用的注册面因此住在
// 这里,只认接口。
//
// 依赖方向只剩一条:宿主 → 本包。两种执行端各自交出自己的实现,谁也
// 不必认识对方。接口按 ISP 只收本包真正调得到的方法 —— 例如 skills.list 由 agentred
// 自己的注册面提供,不属于共用外围,SkillsPort 因此没有 List。

// SkillsPort 交出这台机器上的技能清单与命令清单。
//
// Catalog 直接收发线上的载体:两种执行端在这一格上用的是**同一个**实现
// (handlers.SkillsHandlers),它自己就说 agentrewire,再套一层领域参数只是让同一份
// 字段清单多抄一遍。Commands 那一格两端还各有各的领域实现,因此仍收领域参数 ——
// 端口按宿主真正说的话定形,这与 PeripheralDeps 里 MCPProxy / ProjectSetPath 已经
// 直接收发 protobuf 是同一条判据。
type SkillsPort interface {
	Catalog(ctx context.Context, request *agentrewire.SkillCatalogRequest) (*agentrewire.SkillCatalogResponse, error)
	Commands(ctx context.Context, params runtimewire.SkillCommandsParams) (runtimewire.SkillCommandsResult, error)
}

// RemoteFSPort 是「挑一个目录」用的浏览面,只读加一个建目录。
type RemoteFSPort interface {
	ListDir(ctx context.Context, req remotewire.ListDirReq) (*remotewire.ListDirResp, error)
	Mkdir(ctx context.Context, req remotewire.MkdirReq) (*remotewire.MkdirResp, error)
}

// WorkspaceFSPort 是一个工作区里的文件与 git 视图。
type WorkspaceFSPort interface {
	ListDir(ctx context.Context, req workspacewire.ListDirReq) (*workspacewire.ListDirResp, error)
	GitChanges(ctx context.Context, req workspacewire.GitChangesReq) (*workspacewire.GitChangesResp, error)
	GitBranches(ctx context.Context, req workspacewire.GitBranchesReq) (*workspacewire.GitBranchesResp, error)
	ReadFile(ctx context.Context, req workspacewire.ReadFileReq) (*workspacewire.ReadFileResp, error)
	GitFileContent(ctx context.Context, req workspacewire.GitFileContentReq) (*workspacewire.GitFileContentResp, error)
	SearchFiles(ctx context.Context, req workspacewire.SearchFilesReq) (*workspacewire.SearchFilesResp, error)
	GitState(ctx context.Context, req workspacewire.GitStateReq) (*workspacewire.GitStateResp, error)
}

// TranscriptImportPort 读这台机器磁盘上的历史转录并导入。
type TranscriptImportPort interface {
	Scan(ctx context.Context, params importwire.ScanParams) (*importwire.ScanResult, error)
	Open(ctx context.Context, params importwire.OpenParams) (*importwire.OpenResult, error)
	Turns(ctx context.Context, params importwire.TurnsParams) (*importwire.TurnsResult, error)
	Execute(ctx context.Context, params importwire.ExecuteParams) (*importwire.ExecuteResult, error)
}

// PortForwardPort 是这台设备上的端口转发**声明面**:列举 / 新增 / 启停 / 删除。
//
// 直接收发线上的载体,与 SkillsPort.Catalog 同一条判据:两种执行端在这一格上用的是
// **同一个**实现(internal/daemon/portforward.Handlers,规格「设备侧的目标限制」明写
// 两类设备共用一份),它自己就说 agentrewire,再套一层领域参数只是让同一份字段清单
// 多抄一遍。
//
// 按 ISP 只收声明族四个方法:流族(open/write/close/ack)的生命周期跟着承载它的那条
// 连接走,挂在连接级注册面上,不属于这份 daemon 级的外围能力。
type PortForwardPort interface {
	List(ctx context.Context, request *agentrewire.PortForwardListRequest) (*agentrewire.PortForwardListResponse, error)
	Create(ctx context.Context, request *agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error)
	SetEnabled(ctx context.Context, request *agentrewire.PortForwardSetEnabledRequest) (*agentrewire.PortForwardSetEnabledResponse, error)
	Delete(ctx context.Context, request *agentrewire.PortForwardDeleteRequest) (*agentrewire.PortForwardDeleteResponse, error)
}

// PeripheralDeps 是宿主交出的那一份能力。
//
// **缺席就是一种回答。** 某一格为 nil 表示这台机器没有这个能力,本包据此**不注册**
// 那一族方法,调用方于是收到 method not found —— 协议里「这台机器办不到」只有这一
// 种说法,调用方据此换一台机器。这条纪律必须对每一族都成立:哪一族漏了 nil 守卫,
// 宿主就会挂上持有 nil 的 handler,进去就解空指针,对端收到的是 -32603 internal ——
// 一个只会把人引去查目标机器日志的答复。
//
// 前端那一侧早就把同一件事写成了纪律(agentre-ui 的 TranscriptPorts:可选端口缺席
// 就不渲染那个控件,而不是渲染一个按下去没反应的)。这里是它在 Go 入站侧的对应物。
type PeripheralDeps struct {
	MCPProxy         func(context.Context, *agentrewire.MCPProxyRequest) (*agentrewire.MCPProxyResponse, error)
	ProjectSetPath   func(context.Context, *agentrewire.ProjectSetLocalPathRequest) (*agentrewire.ProjectLocalPathResponse, error)
	ProjectClearPath func(context.Context, *agentrewire.ProjectClearLocalPathRequest) (*agentrewire.ProjectLocalPathResponse, error)
	Skills           SkillsPort
	RemoteFS         RemoteFSPort
	WorkspaceFS      WorkspaceFSPort
	TranscriptImport TranscriptImportPort
	PortForward      PortForwardPort
}
