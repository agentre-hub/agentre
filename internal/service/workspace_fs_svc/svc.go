// Package workspace_fs_svc 是「文件」面板的目录 / Git 两个模式的后端入口:
// 按会话浏览工作目录、取 git 变动与分支清单。
//
// 三个方法的第一个入参一律是 sessionID 而不是 cwd 路径(设计决策 2):会话的
// 工作目录解析本来就是后端职责,且本地 / 远端规则不同;让前端传路径会把那套
// 规则复制一份到前端,并让路径成为可被伪造的入参。
//
// 解析出的 deviceID 决定走哪条路:
//   - deviceID == 0:本机会话,直接在进程内调叶子包 internal/pkg/workspacefs。
//   - deviceID != 0:远端会话,借 remote_device_svc 的租约调 workspacefs.* RPC,
//     daemon 那头调的是同一份叶子包实现(设计决策 4),两端行为不会分叉。
//
// 服务层不碰 DB:会话解析走自声明的窄接口 SessionWorkspaceResolver,由
// chat_svc 在 composition root 注入。
package workspace_fs_svc

//go:generate mockgen -source svc.go -destination mock_workspace_fs_svc/mock_svc.go

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// SessionWorkspaceResolver 把 sessionID 解析成 {deviceID, cwd}。deviceID 为 0
// 表示本机会话。
//
// 这是本服务**自己**声明的窄接口(ISP + DIP):实现方是 chat_svc —— 它已经持有
// resolveSessionCwd 与 backend 查询;本服务因此不必 import chat_repo /
// agent_repo / agent_backend_repo 去跨域读别人的表,单测也只需要注入一个闭包
// 而不必连 DB。注入模式与 chat_svc.RegisterCwdResolver 一致(设计决策 3)。
type SessionWorkspaceResolver func(ctx context.Context, sessionID int64) (deviceID int64, cwd string, err error)

// searchTimeout 是一次递归搜索(含远端一跳)的上限。用户每次输入防抖后都会打一
// 发,超过这个时长就不再是"搜索"而是挂起了:到点即失败,前端出错误态 + 重试,
// 而不是回一份看着完整的部分结果。
const searchTimeout = 10 * time.Second

var resolveWorkspaceFn SessionWorkspaceResolver

// RegisterSessionWorkspaceResolver 由 bootstrap 注入 chat_svc 的实现。
func RegisterSessionWorkspaceResolver(fn SessionWorkspaceResolver) { resolveWorkspaceFn = fn }

// WorkspaceFsSvc 给 Wails 绑定层调。
type WorkspaceFsSvc interface {
	// WorkRoots 列出本会话已认领的工作根集合(见 work_roots.go)。第一项恒是
	// 会话 cwd 对应的根(IsPrimary)。
	WorkRoots(ctx context.Context, sessionID int64) ([]WorkRootView, error)
	// ListDir 列出 root 下 relPath 这一层(relPath 为空即 root 本身)。root 为
	// 空串表示会话 cwd;非空时必须命中本会话已认领的工作根集合,否则拒绝。
	ListDir(ctx context.Context, sessionID int64, root, relPath string, includeIgnored bool) (*ListDirView, error)
	// GitChanges 取 scope 档的 git 变动。baseRef 仅 scope=="branch" 时有意义,
	// 为空或已失效时回落到远端 / 本机推断出的默认基线。
	GitChanges(ctx context.Context, sessionID int64, root, scope, baseRef string) (*GitChangesView, error)
	// GitBranches 取分支清单 + 当前分支 + 推断出的默认基线。
	GitBranches(ctx context.Context, sessionID int64) (*GitBranchesView, error)
	// GitState 取只读 git 状态快照:分支 / worktree 短名 / 未提交数 / 领先
	// 落后 / common git dir。root 为空时用会话解析出的 cwd;非空时必须命中本
	// 会话已认领的工作根集合(与 ListDir 同一道闸门),多工作根场景据此问"另
	// 一个已认领 root 的 git 状态",而不是永远只能问会话自己的 cwd。
	GitState(ctx context.Context, sessionID int64, root string) (*GitStateView, error)
	// ReadFile 读取 root(空串即会话 cwd)下 relPath 所指文件的内容(会话级
	// 文件预览,纯读)。
	// 文本为 UTF-8 正文;图片为 base64 内容 + contentType;binary/tooLarge 是
	// 视图标志,不是错误(设计决策 5)。
	ReadFile(ctx context.Context, sessionID int64, root, relPath string) (*ReadFileView, error)
	// GitFileContent 取同一文件在 git HEAD 的版本(对比档左列);未跟踪/不在
	// HEAD → 空基线(HasHead=false),非 git 仓库 → NotARepo,均不报错。
	GitFileContent(ctx context.Context, sessionID int64, root, relPath string) (*GitFileContentView, error)
	// SearchFiles 从 root(空串即会话 cwd)递归搜索 basename 含 query 子串(不区分大小写)
	// 的文件与目录。includeIgnored 取自前端「显示忽略项」开关:false 时被 git
	// 忽略的目录整棵剪枝、被忽略的文件不计入。结果不完整(命中上限 / 目录数
	// 预算)时 Truncated=true。
	SearchFiles(ctx context.Context, sessionID int64, root, query string, includeIgnored bool) (*SearchFilesView, error)
}

// ── views ───────────────────────────────────────────────────────────────────

type EntryView struct {
	Name       string `json:"name"`
	IsDir      bool   `json:"isDir"`
	Size       int64  `json:"size"`
	Mtime      int64  `json:"mtime"` // unix seconds
	Symlink    bool   `json:"symlink"`
	GitIgnored bool   `json:"gitIgnored"`
}

type ListDirView struct {
	Path      string      `json:"path"` // 解析后的绝对路径
	Entries   []EntryView `json:"entries"`
	Truncated bool        `json:"truncated"` // 超单层上限被截断
}

type ChangeView struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath"` // 仅 Status=="renamed" 时非空
	Status  string `json:"status"`  // modified | added | deleted | renamed | untracked
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary"`
}

type GitChangesView struct {
	NotARepo bool `json:"notARepo"`
	// BaseRef 是本次实际比较用的基线。scope=="uncommitted" 时恒为空;
	// scope=="branch" 时为空表示推断不出默认分支,前端据此出「选一个基线」空态。
	BaseRef   string       `json:"baseRef"`
	Changes   []ChangeView `json:"changes"`
	Truncated bool         `json:"truncated"`
}

type BranchView struct {
	Name   string `json:"name"`
	Remote bool   `json:"remote"`
}

type GitBranchesView struct {
	NotARepo        bool         `json:"notARepo"`
	CurrentBranch   string       `json:"currentBranch"`   // detached HEAD 时为空
	DefaultBaseline string       `json:"defaultBaseline"` // 推断不出时为空
	Branches        []BranchView `json:"branches"`
}

// GitStateView 是 GitState 的返回值:只读 git 状态快照(分支 / worktree 短名 /
// 未提交数 / 领先落后 / common git dir)。NotARepo 为 true 时其余字段恒为零值。
//
// CommonDir 是给下游任务(工作根认领,spec 任务 2)用的:同一主仓库的所有
// worktree 共享同一个 CommonDir,前端/调用方据此判定"两个 root 指回同一主
// 仓库",而不是去比较各 worktree 互不相同的自身路径。
type GitStateView struct {
	NotARepo    bool   `json:"notARepo"`
	Branch      string `json:"branch"`
	Worktree    string `json:"worktree"`
	Dirty       int    `json:"dirty"`
	Ahead       int    `json:"ahead"`
	Behind      int    `json:"behind"`
	HasUpstream bool   `json:"hasUpstream"`
	CommonDir   string `json:"commonDir"`
}

// ReadFileView 是 ReadFile 的返回值:文本为 UTF-8 正文(content),图片为 base64
// 内容 + contentType(如 image/png);binary/tooLarge 为 true 时 content 恒为空。
// 这三个标志是视图字段,不新增错误码(spec 决策 5)。
type ReadFileView struct {
	Content     string `json:"content"`
	ContentType string `json:"contentType,omitempty"`
	Binary      bool   `json:"binary,omitempty"`
	TooLarge    bool   `json:"tooLarge,omitempty"`
}

// GitFileContentView 是 GitFileContent 的返回值:同一文件在 git HEAD 的版本
// (对比档左列);NotARepo / !HasHead 时 content 恒为空。
type GitFileContentView struct {
	Content  string `json:"content"`
	NotARepo bool   `json:"notARepo,omitempty"`
	HasHead  bool   `json:"hasHead,omitempty"` // false 表示空基线(未跟踪/不在 HEAD)
}

// SearchHitView 是一条搜索命中:path 相对会话工作目录、"/" 分隔;isDir 让前端
// 选行的图标与菜单(目录同样参与 basename 匹配)。
type SearchHitView struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

// SearchFilesView 是 SearchFiles 的返回值。Truncated 表示结果**不完整**(命中数
// 触及上限,或遍历触及目录数预算),前端据此在列表末尾出说明——搜索不会静默
// 返回不完整结果。
type SearchFilesView struct {
	Hits      []SearchHitView `json:"hits"`
	Truncated bool            `json:"truncated"`
}

// ── impl ────────────────────────────────────────────────────────────────────

var defaultSvc WorkspaceFsSvc = &workspaceFsImpl{}

func Default() WorkspaceFsSvc { return defaultSvc }

type workspaceFsImpl struct {
	// rdSvc / resolver / writtenPaths 默认走包级单例;单测注入 mock 与闭包。
	rdSvc        remote_device_svc.RemoteDeviceSvc
	resolver     SessionWorkspaceResolver
	writtenPaths SessionWrittenPathsResolver
}

func (s *workspaceFsImpl) deviceSvc() remote_device_svc.RemoteDeviceSvc {
	if s.rdSvc != nil {
		return s.rdSvc
	}
	return remote_device_svc.Default()
}

// workspace 解析会话的 {deviceID, cwd}。cwd 为空(自由会话 / 远端设备上没配
// 项目路径)按「没有工作目录」降级,前端出对应空态。
func (s *workspaceFsImpl) workspace(ctx context.Context, sessionID int64) (int64, string, error) {
	if sessionID <= 0 {
		return 0, "", i18n.NewError(ctx, code.InvalidParameter)
	}
	fn := s.resolver
	if fn == nil {
		fn = resolveWorkspaceFn
	}
	if fn == nil {
		return 0, "", i18n.NewError(ctx, code.WorkspaceFsNoCwd)
	}
	deviceID, cwd, err := fn(ctx, sessionID)
	if err != nil {
		return 0, "", err
	}
	if cwd == "" {
		return 0, "", i18n.NewError(ctx, code.WorkspaceFsNoCwd)
	}
	return deviceID, cwd, nil
}

// call 跑一次远端往返。租约在返回前释放,调用方不持有它。
func callWorkspace[Req proto.Message, Resp proto.Message](ctx context.Context, s *workspaceFsImpl, deviceID int64, method agentrewire.RpcMethod, req Req, newResponse func() Resp) (Resp, error) {
	var zero Resp
	lease, err := s.deviceSvc().Pool().Borrow(ctx, deviceID)
	if err != nil {
		return zero, mapBorrowErr(ctx, err)
	}
	defer lease.Release()
	resp, cerr := protorpc.CallMethod(ctx, lease.Client().Conn(), uint32(method), req, newResponse)
	if cerr != nil {
		return zero, mapCallErr(ctx, cerr)
	}
	return resp, nil
}

func (s *workspaceFsImpl) ListDir(ctx context.Context, sessionID int64, root, relPath string, includeIgnored bool) (*ListDirView, error) {
	deviceID, cwd, err := s.rootFor(ctx, sessionID, root)
	if err != nil {
		return nil, err
	}

	if deviceID == 0 {
		res, lerr := workspacefs.ListDir(ctx, cwd, relPath, includeIgnored, workspacefs.DefaultMaxEntries)
		if lerr != nil {
			return nil, mapLocalErr(ctx, lerr)
		}
		view := &ListDirView{Path: res.Path, Entries: make([]EntryView, len(res.Entries)), Truncated: res.Truncated}
		for i, e := range res.Entries {
			view.Entries[i] = EntryView{
				Name: e.Name, IsDir: e.IsDir, Size: e.Size,
				Mtime: e.ModTime.Unix(), Symlink: e.Symlink, GitIgnored: e.GitIgnored,
			}
		}
		return view, nil
	}

	resp, cerr := callWorkspace(ctx, s, deviceID, agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_LIST_DIR, &agentrewire.WorkspaceFsListDirRequest{Root: cwd, RelPath: relPath, IncludeIgnored: includeIgnored}, func() *agentrewire.WorkspaceFsListDirResponse { return &agentrewire.WorkspaceFsListDirResponse{} })
	if cerr != nil {
		return nil, cerr
	}
	view := &ListDirView{Path: resp.GetPath(), Entries: make([]EntryView, len(resp.GetEntries())), Truncated: resp.GetTruncated()}
	for i, e := range resp.GetEntries() {
		view.Entries[i] = EntryView{
			Name: e.Name, IsDir: e.IsDir, Size: e.Size,
			Mtime: e.GetModTime(), Symlink: e.GetSymlink(), GitIgnored: e.GetGitIgnored(),
		}
	}
	return view, nil
}

func (s *workspaceFsImpl) GitBranches(ctx context.Context, sessionID int64) (*GitBranchesView, error) {
	deviceID, cwd, err := s.workspace(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return s.gitBranches(ctx, deviceID, cwd)
}

// ReadFile 按 deviceID 路由:本机直接调叶子包 internal/pkg/workspacefs.ReadFile;
// 远端经租约调 workspacefs.readFile RPC(daemon 侧是同一份叶子实现)。root 由
// rootFor 在服务层定夺(空串即会话 cwd,非空必须是已认领的工作根),前端因此
// 拿不到"任意路径"这个入参;root 之内的越界由叶子包 / daemon 强制(硬不变量 2)。
func (s *workspaceFsImpl) ReadFile(ctx context.Context, sessionID int64, root, relPath string) (*ReadFileView, error) {
	deviceID, cwd, err := s.rootFor(ctx, sessionID, root)
	if err != nil {
		return nil, err
	}

	if deviceID == 0 {
		res, lerr := workspacefs.ReadFile(ctx, cwd, relPath)
		if lerr != nil {
			return nil, mapLocalErr(ctx, lerr)
		}
		return &ReadFileView{
			Content: res.Content, ContentType: res.ContentType,
			Binary: res.Binary, TooLarge: res.TooLarge,
		}, nil
	}

	response, cerr := callWorkspace(ctx, s, deviceID, agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE, &agentrewire.WorkspaceFsReadFileRequest{Root: cwd, RelPath: relPath}, func() *agentrewire.WorkspaceFsReadFileResponse { return &agentrewire.WorkspaceFsReadFileResponse{} })
	if cerr != nil {
		return nil, cerr
	}
	resp := protowire.WorkspaceReadFileResponseFromProto(response)
	return &ReadFileView{
		Content: resp.Content, ContentType: resp.ContentType,
		Binary: resp.Binary, TooLarge: resp.TooLarge,
	}, nil
}

// GitFileContent 按 deviceID 路由:本机直接调叶子包 workspacefs.GitFileContent;
// 远端经租约调 workspacefs.gitFileContent RPC。notARepo / hasHead 是视图字段。
func (s *workspaceFsImpl) GitFileContent(ctx context.Context, sessionID int64, root, relPath string) (*GitFileContentView, error) {
	deviceID, cwd, err := s.rootFor(ctx, sessionID, root)
	if err != nil {
		return nil, err
	}

	if deviceID == 0 {
		res, lerr := workspacefs.GitFileContent(ctx, cwd, relPath)
		if lerr != nil {
			return nil, mapLocalErr(ctx, lerr)
		}
		return &GitFileContentView{
			Content: res.Content, NotARepo: res.NotARepo, HasHead: res.HasHead,
		}, nil
	}

	resp, cerr := callWorkspace(ctx, s, deviceID, agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_FILE_CONTENT, &agentrewire.WorkspaceFsGitFileContentRequest{Root: cwd, RelPath: relPath}, func() *agentrewire.WorkspaceFsGitFileContentResponse {
		return &agentrewire.WorkspaceFsGitFileContentResponse{}
	})
	if cerr != nil {
		return nil, cerr
	}
	return &GitFileContentView{
		Content: string(resp.GetContent()), NotARepo: resp.GetNotARepo(), HasHead: resp.GetHasHead(),
	}, nil
}

// SearchFiles 按 deviceID 路由:本机直接调叶子包 workspacefs.SearchFiles(递归
// 遍历,.git 恒不进入、被忽略目录整棵剪枝);远端经租约调 workspacefs.searchFiles
// RPC —— 那是 daemon 上的同一份叶子实现,两端的匹配与剪枝规则不会分叉。
//
// 整跳套一层 searchTimeout:递归遍历是本方法族里唯一时长随仓库规模走的调用,
// 前端每次输入防抖后都会打一发,不能让它无限期挂着占住租约。叶子包另有目录数
// 预算兜底,两者共同保证遍历必然终止。
func (s *workspaceFsImpl) SearchFiles(ctx context.Context, sessionID int64, root, query string, includeIgnored bool) (*SearchFilesView, error) {
	// 超时套在最外层:工作根闸门自己也可能要向远端问 gitState,那一跳同样
	// 不能无限期挂着占住租约。
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	deviceID, cwd, err := s.rootFor(ctx, sessionID, root)
	if err != nil {
		return nil, err
	}

	view := &SearchFilesView{Hits: []SearchHitView{}}
	if deviceID == 0 {
		res, lerr := workspacefs.SearchFiles(ctx, cwd, query, includeIgnored,
			workspacefs.DefaultMaxSearchHits, workspacefs.DefaultMaxSearchDirs)
		if lerr != nil {
			return nil, mapLocalErr(ctx, lerr)
		}
		view.Truncated = res.Truncated
		for _, hit := range res.Hits {
			view.Hits = append(view.Hits, SearchHitView{Path: hit.Path, IsDir: hit.IsDir})
		}
	} else {
		resp, cerr := callWorkspace(ctx, s, deviceID, agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_SEARCH_FILES, &agentrewire.WorkspaceFsSearchFilesRequest{Root: cwd, Query: query, IncludeIgnored: includeIgnored}, func() *agentrewire.WorkspaceFsSearchFilesResponse {
			return &agentrewire.WorkspaceFsSearchFilesResponse{}
		})
		if cerr != nil {
			return nil, cerr
		}
		view.Truncated = resp.GetTruncated()
		for _, hit := range resp.GetHits() {
			view.Hits = append(view.Hits, SearchHitView{Path: hit.GetPath(), IsDir: hit.GetIsDir()})
		}
	}

	if view.Truncated {
		// 截断是可见的降级:命中上限或目录数预算被打满。查询串本身不入日志
		// (用户输入),只记它的长度。
		logger.Ctx(ctx).Warn("workspace_fs_svc.SearchFiles: result truncated",
			zap.Int64("sessionID", sessionID), zap.Int64("deviceID", deviceID),
			zap.Int("queryLen", len(query)), zap.Int("hitCount", len(view.Hits)),
			zap.Bool("includeIgnored", includeIgnored))
	}
	return view, nil
}

// gitBranches 是 GitBranches 与「本分支」档基线解析共用的取数步骤。
func (s *workspaceFsImpl) gitBranches(ctx context.Context, deviceID int64, cwd string) (*GitBranchesView, error) {
	if deviceID == 0 {
		res, lerr := workspacefs.GitBranches(ctx, cwd)
		if lerr != nil {
			return nil, mapLocalErr(ctx, lerr)
		}
		view := &GitBranchesView{
			NotARepo:        res.NotARepo,
			CurrentBranch:   res.CurrentBranch,
			DefaultBaseline: res.DefaultBaseline,
			Branches:        make([]BranchView, len(res.Branches)),
		}
		for i, b := range res.Branches {
			view.Branches[i] = BranchView{Name: b.Name, Remote: b.Remote}
		}
		return view, nil
	}

	resp, cerr := callWorkspace(ctx, s, deviceID, agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_BRANCHES, &agentrewire.WorkspaceFsGitBranchesRequest{Root: cwd}, func() *agentrewire.WorkspaceFsGitBranchesResponse {
		return &agentrewire.WorkspaceFsGitBranchesResponse{}
	})
	if cerr != nil {
		return nil, cerr
	}
	view := &GitBranchesView{
		NotARepo:        resp.NotARepo,
		CurrentBranch:   resp.CurrentBranch,
		DefaultBaseline: resp.DefaultBaseline,
		Branches:        make([]BranchView, len(resp.GetBranches())),
	}
	for i, b := range resp.GetBranches() {
		view.Branches[i] = BranchView{Name: b.GetName(), Remote: b.GetRemote()}
	}
	return view, nil
}

// GitState 按 deviceID 路由:本机直接调叶子包 workspacefs.GitState;远端经
// 租约调 workspacefs.gitState RPC(daemon 侧是同一份叶子实现,spec 硬不变量
// 5:本地会话与远端 agentred 会话行为一致)。root 非空时覆盖会话解析出的
// cwd——多工作根场景下调用方可能要问的是"AI 写进的另一个已认领 root",而
// deviceID 仍然要靠会话解析,同一台机器上不同 root 走同一条租约。
//
// root 与本方法族其余成员走同一道 rootFor 闸门(硬不变量 2)。这里不会成环:
// 认领判定用的是私有的 gitStateAt,它不过闸门;成环的只会是"认领去调这个带
// 闸门的 GitState"。少了闸门,这个方法就等于对外开了一条"随便指一个目录,报
// 回它的分支 / 未提交数 / commonDir"的路径探测信道,远端会话下还会把那个路径
// 发进别人机器的一跳 RPC。
func (s *workspaceFsImpl) GitState(ctx context.Context, sessionID int64, root string) (*GitStateView, error) {
	deviceID, dir, err := s.rootFor(ctx, sessionID, root)
	if err != nil {
		return nil, err
	}
	return s.gitStateAt(ctx, deviceID, dir)
}

// gitStateAt 按 deviceID 路由取 dir 的只读 git 状态快照。GitState 与工作根认领
// 共用它 —— 两边因此在本地与远端走同一条判定,不会一边看本机、一边看远端。
func (s *workspaceFsImpl) gitStateAt(ctx context.Context, deviceID int64, dir string) (*GitStateView, error) {
	if deviceID == 0 {
		res := workspacefs.GitState(ctx, dir)
		return &GitStateView{
			NotARepo: res.NotARepo, Branch: res.Branch, Worktree: res.Worktree,
			Dirty: res.Dirty, Ahead: res.Ahead, Behind: res.Behind,
			HasUpstream: res.HasUpstream, CommonDir: res.CommonDir,
		}, nil
	}

	response, cerr := callWorkspace(ctx, s, deviceID, agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_STATE, &agentrewire.WorkspaceFsGitStateRequest{Root: dir}, func() *agentrewire.WorkspaceFsGitStateResponse { return &agentrewire.WorkspaceFsGitStateResponse{} })
	if cerr != nil {
		return nil, cerr
	}
	resp := protowire.WorkspaceGitStateResponseFromProto(response)
	return &GitStateView{
		NotARepo: resp.NotARepo, Branch: resp.Branch, Worktree: resp.Worktree,
		Dirty: resp.Dirty, Ahead: resp.Ahead, Behind: resp.Behind,
		HasUpstream: resp.HasUpstream, CommonDir: resp.CommonDir,
	}, nil
}

func (s *workspaceFsImpl) GitChanges(ctx context.Context, sessionID int64, root, scope, baseRef string) (*GitChangesView, error) {
	sc, err := parseScope(ctx, scope)
	if err != nil {
		return nil, err
	}
	deviceID, cwd, err := s.rootFor(ctx, sessionID, root)
	if err != nil {
		return nil, err
	}

	// 「本分支」档必须带一个确定的基线过去:叶子包对空基线返回
	// ErrBaselineRequired 而不静默兜底,基线的推断与失效回落是本层的职责。
	usedBase := ""
	if sc == workspacefs.ScopeBranch {
		branches, berr := s.gitBranches(ctx, deviceID, cwd)
		if berr != nil {
			return nil, berr
		}
		if branches.NotARepo {
			return &GitChangesView{NotARepo: true, Changes: []ChangeView{}}, nil
		}
		usedBase = pickBaseline(baseRef, branches)
		if usedBase == "" {
			// origin/HEAD / main / master 都不可得:成功返回空基线的空结果,
			// 让前端出「推断不出默认分支,请选一个基线」空态。
			return &GitChangesView{Changes: []ChangeView{}}, nil
		}
	}

	if deviceID == 0 {
		res, lerr := workspacefs.GitChanges(ctx, cwd, sc, usedBase, workspacefs.DefaultMaxEntries)
		if lerr != nil {
			return nil, mapLocalErr(ctx, lerr)
		}
		view := &GitChangesView{
			NotARepo: res.NotARepo, BaseRef: usedBase,
			Changes: make([]ChangeView, len(res.Changes)), Truncated: res.Truncated,
		}
		for i, c := range res.Changes {
			view.Changes[i] = ChangeView{
				Path: c.Path, OldPath: c.OldPath, Status: string(c.Status),
				Added: c.Added, Deleted: c.Deleted, Binary: c.Binary,
			}
		}
		return view, nil
	}

	// scope 已经过 parseScope 校验,必是 wire.Scope* 之一,直接透传原串,不靠
	// "叶子包 scope 与 wire scope 字面量恰好相等"这个隐含前提。
	response, cerr := callWorkspace(ctx, s, deviceID, agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_CHANGES, &agentrewire.WorkspaceFsGitChangesRequest{Root: cwd, Scope: scope, BaseRef: usedBase}, func() *agentrewire.WorkspaceFsGitChangesResponse { return &agentrewire.WorkspaceFsGitChangesResponse{} })
	if cerr != nil {
		return nil, cerr
	}
	resp := protowire.WorkspaceGitChangesResponseFromProto(response)
	view := &GitChangesView{
		NotARepo: resp.NotARepo, BaseRef: usedBase,
		Changes: make([]ChangeView, len(resp.Changes)), Truncated: resp.Truncated,
	}
	for i, c := range resp.Changes {
		view.Changes[i] = ChangeView{
			Path: c.Path, OldPath: c.OldPath, Status: c.Status,
			Added: c.Added, Deleted: c.Deleted, Binary: c.Binary,
		}
	}
	return view, nil
}

// pickBaseline 在「用户选过的基线」与「推断出的默认基线」之间定夺:选过的值
// 必须仍在分支清单里才作数,否则回落到默认推断(设计决策 9 的失效回落)。
// 分支清单同时就是基线选择器的选项集合,本地与远端因此用同一份判定,而不是
// 本地查 ref 存在性、远端另猜一套。
func pickBaseline(baseRef string, branches *GitBranchesView) string {
	if baseRef != "" {
		for _, b := range branches.Branches {
			if b.Name == baseRef {
				return baseRef
			}
		}
	}
	return branches.DefaultBaseline
}

func parseScope(ctx context.Context, scope string) (workspacefs.GitChangesScope, error) {
	switch scope {
	case wire.ScopeUncommitted:
		return workspacefs.ScopeUncommitted, nil
	case wire.ScopeBranch:
		return workspacefs.ScopeBranch, nil
	}
	return "", i18n.NewError(ctx, code.InvalidParameter)
}

// ── error mapping ───────────────────────────────────────────────────────────

// mapLocalErr 翻译本机分支的叶子包 sentinel。
func mapLocalErr(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, workspacefs.ErrPathRefused):
		return i18n.NewError(ctx, code.WorkspaceFsPathRefused)
	case errors.Is(err, workspacefs.ErrBaselineRequired):
		return i18n.NewError(ctx, code.WorkspaceFsBaselineRequired)
	}
	return i18n.NewError(ctx, code.WorkspaceFsReadFailed)
}

func mapBorrowErr(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, remote_device_svc.ErrDeviceNotFound):
		return i18n.NewError(ctx, code.RemoteDeviceNotFound)
	case errors.Is(err, remote_device_svc.ErrDeviceUnauthorized):
		return i18n.NewError(ctx, code.RemoteDeviceUnauthorized)
	}
	return i18n.NewError(ctx, code.WorkspaceFsDeviceOffline)
}

// mapCallErr 翻译远端调用错误。
func mapCallErr(ctx context.Context, err error) error {
	var rpcErr *protorpc.Error
	if !errors.As(err, &rpcErr) {
		return i18n.NewError(ctx, code.RemoteRunnerCallFailed)
	}
	switch int(rpcErr.Code) {
	case wire.ErrCodePathRefused:
		return i18n.NewError(ctx, code.WorkspaceFsPathRefused)
	case wire.ErrCodeBaselineRequired:
		return i18n.NewError(ctx, code.WorkspaceFsBaselineRequired)
	}
	return i18n.NewError(ctx, code.RemoteRunnerCallFailed)
}
