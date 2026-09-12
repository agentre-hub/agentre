package wireinbound

import (
	"context"
	"errors"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	runtimewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	importwire "github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
	workspacewire "github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

func ConvertError(err error) error {
	if err == nil {
		return nil
	}
	var protobufErr *protorpc.Error
	if errors.As(err, &protobufErr) {
		return protobufErr
	}
	return &protorpc.Error{Code: protorpc.CodeInternal, Message: err.Error()}
}

func RegisterPeripheralMethods(registry *protorpc.Registry, deps PeripheralDeps) {
	registerOptionalProtobufPeripheralMethods(registry, deps)
	registerProtobufSkills(registry, deps.Skills)
	registerProtobufRemoteFS(registry, deps.RemoteFS)
	registerProtobufWorkspaceFS(registry, deps.WorkspaceFS)
	registerProtobufTranscriptImport(registry, deps.TranscriptImport)
	registerProtobufPortForward(registry, deps.PortForward)
}

// registerProtobufPortForward 挂上 portforward.* 的**声明族**四个方法。流族
// (open/write/close/ack)不在这里:一条转发流的生命周期跟着承载它的那条连接走,
// 挂在连接级注册面上。
//
// 四个方法都在 Authenticated 里:一台机器上开着哪些端口、叫什么名字,是这台机器的
// 配置,没配对的对端不该问得出来。
//
// 端口缺席就整族不挂 —— 与另外四族同一条判据。少了这道守卫,不带这个能力的宿主会
// 挂上四个持有 nil 的 handler,进去就解空指针,对端拿到的是 -32603 internal(照着
// transcriptimport 那次的原样再来一遍),而调用方要的答复是「这台机器办不到」。
func registerProtobufPortForward(registry *protorpc.Registry, declarations PortForwardPort) {
	if declarations == nil {
		return
	}
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_LIST), func() *agentrewire.PortForwardListRequest {
		return &agentrewire.PortForwardListRequest{}
	}, Authenticated(func(ctx context.Context, request *agentrewire.PortForwardListRequest) (*agentrewire.PortForwardListResponse, error) {
		response, err := declarations.List(ctx, request)
		if err != nil {
			return nil, ConvertError(err)
		}
		return response, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_CREATE), func() *agentrewire.PortForwardCreateRequest {
		return &agentrewire.PortForwardCreateRequest{}
	}, Authenticated(func(ctx context.Context, request *agentrewire.PortForwardCreateRequest) (*agentrewire.PortForwardCreateResponse, error) {
		response, err := declarations.Create(ctx, request)
		if err != nil {
			return nil, ConvertError(err)
		}
		return response, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_SET_ENABLED), func() *agentrewire.PortForwardSetEnabledRequest {
		return &agentrewire.PortForwardSetEnabledRequest{}
	}, Authenticated(func(ctx context.Context, request *agentrewire.PortForwardSetEnabledRequest) (*agentrewire.PortForwardSetEnabledResponse, error) {
		response, err := declarations.SetEnabled(ctx, request)
		if err != nil {
			return nil, ConvertError(err)
		}
		return response, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_DELETE), func() *agentrewire.PortForwardDeleteRequest {
		return &agentrewire.PortForwardDeleteRequest{}
	}, Authenticated(func(ctx context.Context, request *agentrewire.PortForwardDeleteRequest) (*agentrewire.PortForwardDeleteResponse, error) {
		response, err := declarations.Delete(ctx, request)
		if err != nil {
			return nil, ConvertError(err)
		}
		return response, nil
	}))
}

func registerOptionalProtobufPeripheralMethods(registry *protorpc.Registry, deps PeripheralDeps) {
	if deps.MCPProxy != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY), func() *agentrewire.MCPProxyRequest { return &agentrewire.MCPProxyRequest{} }, Authenticated(deps.MCPProxy))
	}
	if deps.ProjectSetPath != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PROJECT_SET_LOCAL_PATH), func() *agentrewire.ProjectSetLocalPathRequest { return &agentrewire.ProjectSetLocalPathRequest{} }, Authenticated(deps.ProjectSetPath))
	}
	if deps.ProjectClearPath != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PROJECT_CLEAR_LOCAL_PATH), func() *agentrewire.ProjectClearLocalPathRequest { return &agentrewire.ProjectClearLocalPathRequest{} }, Authenticated(deps.ProjectClearPath))
	}
}

// RequireAuthenticated 是这道闸门本身。它单独露出来,是因为会话族的闸门必须是**端口**
// (两种执行端的拒绝语在线上不是同一句:agentred 答大写 "Unauthorized",桌面端答小写
// "unauthorized"),而桌面端那一份用的正是这里这一句 —— 不该再抄一遍。
func RequireAuthenticated(ctx context.Context) error {
	conn := protorpc.ConnFromContext(ctx)
	if conn == nil || !conn.Auth().Authenticated {
		return &protorpc.Error{Code: -32001, Message: "unauthorized"}
	}
	return nil
}

func Authenticated[Req any, Resp any](handler func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, request Req) (Resp, error) {
		var zero Resp
		if err := RequireAuthenticated(ctx); err != nil {
			return zero, err
		}
		return handler(ctx, request)
	}
}

// registerProtobufSkills 把 skills.* 两个方法一起挂上。
//
// 两种执行端共用这一处注册(agentred 经 daemon、桌面端经 peer),所以「浏览器 / 另一台
// 桌面端连过来时对面认不认识这个方法」不取决于对面是哪一种 —— 调用方不必先猜。
func registerProtobufSkills(registry *protorpc.Registry, skillHandlers SkillsPort) {
	if skillHandlers == nil {
		return
	}
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SKILLS_COMMANDS), func() *agentrewire.SkillCommandsRequest { return &agentrewire.SkillCommandsRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.SkillCommandsRequest) (*agentrewire.SkillCommandsResponse, error) {
		if request.BackendType == "" {
			return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "backend type required"}
		}
		authorized := make([]runtimewire.SkillAuthorization, 0, len(request.Authorized))
		for _, item := range request.Authorized {
			authorized = append(authorized, runtimewire.SkillAuthorization{ID: item.GetId(), Enabled: item.GetEnabled()})
		}
		result, err := skillHandlers.Commands(ctx, runtimewire.SkillCommandsParams{
			BackendType: request.GetBackendType(),
			Authorized:  authorized,
			CLIPath:     request.GetCliPath(),
			Cwd:         request.GetCwd(),
		})
		if err != nil {
			return nil, ConvertError(err)
		}
		return protowire.SkillCommandsResponseToProto(result), nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SKILLS_CATALOG), func() *agentrewire.SkillCatalogRequest { return &agentrewire.SkillCatalogRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.SkillCatalogRequest) (*agentrewire.SkillCatalogResponse, error) {
		if request.GetBackendType() == "" {
			return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "backend type required"}
		}
		response, err := skillHandlers.Catalog(ctx, request)
		if err != nil {
			return nil, ConvertError(err)
		}
		return response, nil
	}))
}

func registerProtobufRemoteFS(registry *protorpc.Registry, fs RemoteFSPort) {
	if fs == nil {
		return
	}
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_LIST_DIR), func() *agentrewire.RemoteFsListDirRequest { return &agentrewire.RemoteFsListDirRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.RemoteFsListDirRequest) (*agentrewire.RemoteFsListDirResponse, error) {
		result, err := fs.ListDir(ctx, remotewire.ListDirReq{Path: request.Path})
		if err != nil {
			return nil, remoteFSError(err)
		}
		return protowire.RemoteFsListResponseToProto(*result), nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_MKDIR), func() *agentrewire.RemoteFsMkdirRequest { return &agentrewire.RemoteFsMkdirRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.RemoteFsMkdirRequest) (*agentrewire.RemoteFsMkdirResponse, error) {
		result, err := fs.Mkdir(ctx, remotewire.MkdirReq{Parent: request.Parent, Name: request.Name})
		if err != nil {
			return nil, remoteFSError(err)
		}
		return &agentrewire.RemoteFsMkdirResponse{Path: result.Path}, nil
	}))
}

func remoteFSError(err error) error {
	if mapped := remotewire.ToRPCError(err); mapped != nil {
		return ConvertError(mapped)
	}
	return ConvertError(err)
}

func workspaceFSError(err error) error {
	if mapped := workspacewire.ToRPCError(err); mapped != nil {
		return ConvertError(mapped)
	}
	return ConvertError(err)
}

func registerProtobufWorkspaceFS(registry *protorpc.Registry, fs WorkspaceFSPort) {
	if fs == nil {
		return
	}
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_LIST_DIR), func() *agentrewire.WorkspaceFsListDirRequest { return &agentrewire.WorkspaceFsListDirRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.WorkspaceFsListDirRequest) (*agentrewire.WorkspaceFsListDirResponse, error) {
		result, err := fs.ListDir(ctx, workspacewire.ListDirReq{Root: request.Root, RelPath: request.RelPath, IncludeIgnored: request.IncludeIgnored})
		if err != nil {
			return nil, workspaceFSError(err)
		}
		response := &agentrewire.WorkspaceFsListDirResponse{Path: result.Path, Truncated: result.Truncated}
		for _, entry := range result.Entries {
			response.Entries = append(response.Entries, &agentrewire.WorkspaceFsEntry{Name: entry.Name, IsDir: entry.IsDir, Size: entry.Size, ModTime: entry.ModTime, Symlink: entry.Symlink, GitIgnored: entry.GitIgnored})
		}
		return response, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_CHANGES), func() *agentrewire.WorkspaceFsGitChangesRequest { return &agentrewire.WorkspaceFsGitChangesRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.WorkspaceFsGitChangesRequest) (*agentrewire.WorkspaceFsGitChangesResponse, error) {
		result, err := fs.GitChanges(ctx, workspacewire.GitChangesReq{Root: request.Root, Scope: request.Scope, BaseRef: request.BaseRef})
		if err != nil {
			return nil, workspaceFSError(err)
		}
		return protowire.WorkspaceGitChangesResponseToProto(*result), nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_BRANCHES), func() *agentrewire.WorkspaceFsGitBranchesRequest { return &agentrewire.WorkspaceFsGitBranchesRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.WorkspaceFsGitBranchesRequest) (*agentrewire.WorkspaceFsGitBranchesResponse, error) {
		result, err := fs.GitBranches(ctx, workspacewire.GitBranchesReq{Root: request.Root})
		if err != nil {
			return nil, workspaceFSError(err)
		}
		response := &agentrewire.WorkspaceFsGitBranchesResponse{NotARepo: result.NotARepo, CurrentBranch: result.CurrentBranch, DefaultBaseline: result.DefaultBaseline}
		for _, branch := range result.Branches {
			response.Branches = append(response.Branches, &agentrewire.WorkspaceFsBranch{Name: branch.Name, Remote: branch.Remote})
		}
		return response, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE), func() *agentrewire.WorkspaceFsReadFileRequest { return &agentrewire.WorkspaceFsReadFileRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.WorkspaceFsReadFileRequest) (*agentrewire.WorkspaceFsReadFileResponse, error) {
		result, err := fs.ReadFile(ctx, workspacewire.ReadFileReq{Root: request.Root, RelPath: request.RelPath})
		if err != nil {
			return nil, workspaceFSError(err)
		}
		response, err := protowire.WorkspaceReadFileResponseToProto(*result)
		if err != nil {
			return nil, ConvertError(err)
		}
		return response, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_FILE_CONTENT), func() *agentrewire.WorkspaceFsGitFileContentRequest {
		return &agentrewire.WorkspaceFsGitFileContentRequest{}
	}, Authenticated(func(ctx context.Context, request *agentrewire.WorkspaceFsGitFileContentRequest) (*agentrewire.WorkspaceFsGitFileContentResponse, error) {
		result, err := fs.GitFileContent(ctx, workspacewire.GitFileContentReq{Root: request.Root, RelPath: request.RelPath})
		if err != nil {
			return nil, workspaceFSError(err)
		}
		return &agentrewire.WorkspaceFsGitFileContentResponse{Content: []byte(result.Content), NotARepo: result.NotARepo, HasHead: result.HasHead}, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_SEARCH_FILES), func() *agentrewire.WorkspaceFsSearchFilesRequest { return &agentrewire.WorkspaceFsSearchFilesRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.WorkspaceFsSearchFilesRequest) (*agentrewire.WorkspaceFsSearchFilesResponse, error) {
		result, err := fs.SearchFiles(ctx, workspacewire.SearchFilesReq{Root: request.Root, Query: request.Query, IncludeIgnored: request.IncludeIgnored})
		if err != nil {
			return nil, workspaceFSError(err)
		}
		response := &agentrewire.WorkspaceFsSearchFilesResponse{Truncated: result.Truncated}
		for _, hit := range result.Hits {
			response.Hits = append(response.Hits, &agentrewire.WorkspaceFsSearchHit{Path: hit.Path, IsDir: hit.IsDir})
		}
		return response, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_STATE), func() *agentrewire.WorkspaceFsGitStateRequest { return &agentrewire.WorkspaceFsGitStateRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.WorkspaceFsGitStateRequest) (*agentrewire.WorkspaceFsGitStateResponse, error) {
		result, err := fs.GitState(ctx, workspacewire.GitStateReq{Root: request.Root})
		if err != nil {
			return nil, workspaceFSError(err)
		}
		return protowire.WorkspaceGitStateResponseToProto(*result), nil
	}))
}

func transcriptImportError(err error) error {
	if mapped := importwire.ToRPCError(err); mapped != nil {
		return ConvertError(mapped)
	}
	return ConvertError(err)
}

// registerProtobufTranscriptImport 挂上 transcriptimport.* 方法族。四个方法都在
// Authenticated 里:磁盘上的转录是会话正文,没配对的对端不该问得出来。
//
// 端口缺席就整族不挂 —— 与另外三族同一条判据。少了这道闸门,不带这个端口的宿主会
// 照样注册四个方法,handler 手里是个 nil,进去就在 h.sources() 上解空指针,对端拿到
// -32603 internal(execute 那条更甚,它是这族唯一写库的)。缺席与"办不到"必须是同一句话:
// method not found。
func registerProtobufTranscriptImport(registry *protorpc.Registry, handlers TranscriptImportPort) {
	if handlers == nil {
		return
	}
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_SCAN), func() *agentrewire.TranscriptImportScanRequest {
		return &agentrewire.TranscriptImportScanRequest{}
	}, Authenticated(func(ctx context.Context, request *agentrewire.TranscriptImportScanRequest) (*agentrewire.TranscriptImportScanResponse, error) {
		result, err := handlers.Scan(ctx, protowire.TranscriptScanParamsFromProto(request))
		if err != nil {
			return nil, transcriptImportError(err)
		}
		return protowire.TranscriptScanResultToProto(*result), nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_OPEN), func() *agentrewire.TranscriptImportOpenRequest {
		return &agentrewire.TranscriptImportOpenRequest{}
	}, Authenticated(func(ctx context.Context, request *agentrewire.TranscriptImportOpenRequest) (*agentrewire.TranscriptImportOpenResponse, error) {
		result, err := handlers.Open(ctx, protowire.TranscriptOpenParamsFromProto(request))
		if err != nil {
			return nil, transcriptImportError(err)
		}
		return protowire.TranscriptOpenResultToProto(*result), nil
	}))
	// execute 与前三个只读方法同在 Authenticated 里,但它是这一族唯一写库的:
	// 归属由 handler 自己按调用连接的对端解(点名 origin 是账号级能力)。
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_EXECUTE), func() *agentrewire.TranscriptImportExecuteRequest {
		return &agentrewire.TranscriptImportExecuteRequest{}
	}, Authenticated(func(ctx context.Context, request *agentrewire.TranscriptImportExecuteRequest) (*agentrewire.TranscriptImportExecuteResponse, error) {
		result, err := handlers.Execute(ctx, protowire.TranscriptExecuteParamsFromProto(request))
		if err != nil {
			return nil, transcriptImportError(err)
		}
		return protowire.TranscriptExecuteResultToProto(*result), nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_TURNS), func() *agentrewire.TranscriptImportTurnsRequest {
		return &agentrewire.TranscriptImportTurnsRequest{}
	}, Authenticated(func(ctx context.Context, request *agentrewire.TranscriptImportTurnsRequest) (*agentrewire.TranscriptImportTurnsResponse, error) {
		result, err := handlers.Turns(ctx, protowire.TranscriptTurnsParamsFromProto(request))
		if err != nil {
			return nil, transcriptImportError(err)
		}
		response, err := protowire.TranscriptTurnsResultToProto(*result)
		if err != nil {
			return nil, ConvertError(err)
		}
		return response, nil
	}))
}
