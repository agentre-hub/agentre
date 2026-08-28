package protobufadapter

import (
	"context"
	"errors"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/daemon/remotefs"
	daemonimport "github.com/agentre-hub/agentre/internal/daemon/transcriptimport"
	"github.com/agentre-hub/agentre/internal/daemon/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	runtimewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	importwire "github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
	workspacewire "github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type PeripheralDeps struct {
	MCPProxy         func(context.Context, *agentrewire.MCPProxyRequest) (*agentrewire.MCPProxyResponse, error)
	ProjectSetPath   func(context.Context, *agentrewire.ProjectSetLocalPathRequest) (*agentrewire.ProjectLocalPathResponse, error)
	ProjectClearPath func(context.Context, *agentrewire.ProjectClearLocalPathRequest) (*agentrewire.ProjectLocalPathResponse, error)
	Skills           *handlers.SkillsHandlers
	RemoteFS         *remotefs.Handlers
	WorkspaceFS      *workspacefs.Handlers
	// TranscriptImport 为 nil 时整族方法不注册,调用方拿到 -32601 ——
	// 「这个 daemon 版本不认识导入这件事」因此在 host 侧看得见(spec 硬约束 3),
	// 而不是被一个空应答伪装成「这台机器没有会话」。
	TranscriptImport *daemonimport.Handlers
}

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
	registerProtobufSkillsCatalog(registry, deps.Skills)
	registerProtobufRemoteFS(registry, deps.RemoteFS)
	registerProtobufWorkspaceFS(registry, deps.WorkspaceFS)
	registerProtobufTranscriptImport(registry, deps.TranscriptImport)
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

func Authenticated[Req any, Resp any](handler func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, request Req) (Resp, error) {
		var zero Resp
		conn := protorpc.ConnFromContext(ctx)
		if conn == nil || !conn.Auth().Authenticated {
			return zero, &protorpc.Error{Code: -32001, Message: "unauthorized"}
		}
		return handler(ctx, request)
	}
}

func registerProtobufSkillsCatalog(registry *protorpc.Registry, skillHandlers *handlers.SkillsHandlers) {
	if skillHandlers == nil {
		return
	}
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SKILLS_CATALOG), func() *agentrewire.SkillCatalogRequest { return &agentrewire.SkillCatalogRequest{} }, Authenticated(func(ctx context.Context, request *agentrewire.SkillCatalogRequest) (*agentrewire.SkillCatalogResponse, error) {
		if request.BackendType == "" {
			return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "backend type required"}
		}
		authorized := make([]runtimewire.SkillAuthorization, 0, len(request.Authorized))
		for _, item := range request.Authorized {
			authorized = append(authorized, runtimewire.SkillAuthorization{ID: item.GetId(), Enabled: item.GetEnabled()})
		}
		result, err := skillHandlers.Catalog(ctx, runtimewire.SkillCatalogParams{BackendType: request.BackendType, Authorized: authorized, CLIPath: request.CliPath})
		if err != nil {
			return nil, ConvertError(err)
		}
		return protowire.SkillCatalogResponseToProto(result), nil
	}))
}

func registerProtobufRemoteFS(registry *protorpc.Registry, fs *remotefs.Handlers) {
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

func registerProtobufWorkspaceFS(registry *protorpc.Registry, fs *workspacefs.Handlers) {
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

// registerProtobufTranscriptImport 挂上 transcriptimport.* 方法族。三个方法都在
// Authenticated 里:磁盘上的转录是会话正文,没配对的对端不该问得出来。
func registerProtobufTranscriptImport(registry *protorpc.Registry, handlers *daemonimport.Handlers) {
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
