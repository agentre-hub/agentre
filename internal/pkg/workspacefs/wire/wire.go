// Package wire 定义 agentre ↔ agentred 的 workspacefs.* RPC 协议:参数 / 结果 /
// 错误 sentinel 与 typed RPC error code 的双向翻译。daemon 端 handler 与(后续
// 切片的)host 端 svc 共享这一份类型,避免 Protobuf shape 漂移。
//
// 与 internal/pkg/remotefs/wire 是刻意分开的独立方法族(spec 设计决策 5):
// remotefs.* 面向"浏览远端机器任意绝对路径"(带 $HOME 兜底与路径黑名单);
// workspacefs.* 面向"浏览某个会话已解析出的工作目录"(root 由调用方传入,
// relPath 相对 root)。给 remotefs.* 加字段的话,旧 daemon 会静默忽略新字段
// 并按旧语义应答,版本偏斜就不可见了;新方法族在旧 daemon 上直接回
// -32601(method not found),能明确提示需要升级 daemon。
//
// 命名约定与 internal/pkg/remotefs/wire 一致:
//   - 方法在 "workspacefs.*" 命名空间下
//   - 字段名 lowerCamelCase
//   - 错误码 -32040..-32042 是稳定 wire 值,与既有方法族的 code 段不重叠
//     (remotefs.* 占 -32030..-32035,agentruntime remote wire 占
//     -32010..-32014),wrapGuarded handler 返回 *rpcerror.Error 由本包翻译,
//     客户端用 FromRPCError rehydrate。
package wire

import (
	"errors"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
)

// ── RPC method names ────────────────────────────────────────────────────────

const (
	MethodListDir        = "workspacefs.listDir"
	MethodGitChanges     = "workspacefs.gitChanges"
	MethodGitBranches    = "workspacefs.gitBranches"
	MethodReadFile       = "workspacefs.readFile"
	MethodGitFileContent = "workspacefs.gitFileContent"
	MethodSearchFiles    = "workspacefs.searchFiles"
	MethodGitState       = "workspacefs.gitState"
)

// ── Error codes ─────────────────────────────────────────────────────────────

const (
	ErrCodePathRefused      = -32040
	ErrCodeBaselineRequired = -32041
	ErrCodeNoCwd            = -32042
)

// ── Sentinel errors ─────────────────────────────────────────────────────────

var (
	// ErrPathRefused 镜像 internal/pkg/workspacefs.ErrPathRefused:relPath 越界
	// (含 ".."、绝对路径,或解析后逃出 root)。
	ErrPathRefused = errors.New("workspacefs: path refused")
	// ErrBaselineRequired 镜像 internal/pkg/workspacefs.ErrBaselineRequired:
	// GitChanges 的 scope=="branch" 时 baseRef 为空。
	ErrBaselineRequired = errors.New("workspacefs: baseline required for branch scope")
	// ErrNoCwd 镜像 internal/pkg/workspacefs.ErrNoCwd:调用方未提供工作目录
	// (root 为空)。与"越界"的 ErrPathRefused 区分开——cwd 为空是会话配置问题,
	// 不是路径问题。
	ErrNoCwd = errors.New("workspacefs: no cwd")
)

// ToRPCError 把 workspacefs sentinel 包成 *rpcerror.Error,daemon handler 返回。
// 非 sentinel 返 nil,调用方应自己包装(ErrInternal 之类)。
func ToRPCError(err error) *rpcerror.Error {
	switch {
	case errors.Is(err, ErrPathRefused):
		return &rpcerror.Error{Code: ErrCodePathRefused, Message: err.Error()}
	case errors.Is(err, ErrBaselineRequired):
		return &rpcerror.Error{Code: ErrCodeBaselineRequired, Message: err.Error()}
	case errors.Is(err, ErrNoCwd):
		return &rpcerror.Error{Code: ErrCodeNoCwd, Message: err.Error()}
	}
	return nil
}

// FromRPCError 反向把 *rpcerror.Error 翻成 sentinel。未知 code 返原 err。
// host 侧 svc 拿到后再 i18n.NewError(ctx, code.WorkspaceFsXxx) 包给前端。
func FromRPCError(err error) error {
	var rpcErr *rpcerror.Error
	if !errors.As(err, &rpcErr) {
		return err
	}
	switch rpcErr.Code {
	case ErrCodePathRefused:
		return ErrPathRefused
	case ErrCodeBaselineRequired:
		return ErrBaselineRequired
	case ErrCodeNoCwd:
		return ErrNoCwd
	}
	return err
}

// ── ListDir ─────────────────────────────────────────────────────────────────

// ListDirReq.Root 是会话已解析出的工作目录绝对路径,由调用方(host 侧 svc)
// 负责解析——协议本身不做 $HOME 兜底,这是与 remotefs.ListDirReq 的关键差异
// (spec 设计决策 2:后端方法的入参是 sessionID 而非 cwd 路径,cwd 解析在 host
// 层完成,daemon 侧收到的已经是解析结果,必填且必须是绝对路径)。
type ListDirReq struct {
	Root           string `json:"root"`
	RelPath        string `json:"relPath,omitempty"`
	IncludeIgnored bool   `json:"includeIgnored,omitempty"`
}

type Entry struct {
	Name       string `json:"name"`
	IsDir      bool   `json:"isDir"`
	Size       int64  `json:"size"`  // 字节;目录恒为 0
	ModTime    int64  `json:"mtime"` // unix seconds
	Symlink    bool   `json:"symlink,omitempty"`
	GitIgnored bool   `json:"gitIgnored,omitempty"` // 非 git 目录恒为 false
}

type ListDirResp struct {
	Path      string  `json:"path"` // = Root 解析 RelPath 后的绝对路径
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated,omitempty"` // 超 maxEntries 时为 true
}

// ── GitChanges ──────────────────────────────────────────────────────────────

// Git 变动 scope 取值,与 internal/pkg/workspacefs.GitChangesScope 的字符串值
// 一一对应(设计决策 8:"未提交 / 本分支"两档)。
const (
	ScopeUncommitted = "uncommitted"
	ScopeBranch      = "branch"
)

// GitChangesReq.BaseRef 仅 Scope==ScopeBranch 时必填;缺失时 daemon 返回
// ErrBaselineRequired。
type GitChangesReq struct {
	Root    string `json:"root"`
	Scope   string `json:"scope"` // ScopeUncommitted | ScopeBranch
	BaseRef string `json:"baseRef,omitempty"`
}

type Change struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"` // 仅 Status=="renamed" 时非空
	Status  string `json:"status"`            // modified | added | deleted | renamed | untracked
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary,omitempty"` // true 时 Added/Deleted 恒为 0
}

type GitChangesResp struct {
	NotARepo  bool     `json:"notARepo,omitempty"` // true 时其余字段恒为零值
	Changes   []Change `json:"changes"`
	Truncated bool     `json:"truncated,omitempty"`
}

// ── GitBranches ─────────────────────────────────────────────────────────────

type GitBranchesReq struct {
	Root string `json:"root"`
}

type Branch struct {
	Name   string `json:"name"` // 短名,如 "main" 或远程跟踪分支的 "origin/main"
	Remote bool   `json:"remote,omitempty"`
}

// GitBranchesResp 一次带回分支清单 + 当前分支 + 推断出的默认基线:基线推断
// (origin/HEAD → main → master)只能在仓库所在机器上做,host 侧远端分支没有
// 别的途径拿到它,所以它必须过 wire,而不是让 host 照着 Branches 再猜一遍
// (那会让本地与远端的推断规则分叉)。
type GitBranchesResp struct {
	NotARepo        bool     `json:"notARepo,omitempty"` // true 时其余字段恒为零值
	Branches        []Branch `json:"branches"`
	CurrentBranch   string   `json:"currentBranch,omitempty"`   // detached HEAD 时为空
	DefaultBaseline string   `json:"defaultBaseline,omitempty"` // 三级都不命中时为空
}

// ── ReadFile ────────────────────────────────────────────────────────────────

// ReadFileReq.Root 是会话已解析出的工作目录绝对路径(契约同 ListDirReq.Root:
// 必填且必须是绝对路径);root 为空 → ErrNoCwd(会话配置问题,与越界区分),
// RelPath 越界 → ErrPathRefused。
//
// 镜像 internal/pkg/workspacefs.ReadFile 的入参,host 侧远端分支与 daemon 共享
// 同一套路径边界语义。
type ReadFileReq struct {
	Root    string `json:"root"`
	RelPath string `json:"relPath"`
}

// ReadFileResp 镜像 internal/pkg/workspacefs.ReadFileResult:文本为 UTF-8 正文,
// 图片为 base64 内容 + ContentType(如 image/png);Binary / TooLarge 为 true 时
// Content 恒为空。
type ReadFileResp struct {
	Content     string `json:"content"`
	ContentType string `json:"contentType,omitempty"`
	Binary      bool   `json:"binary,omitempty"`
	TooLarge    bool   `json:"tooLarge,omitempty"`
}

// ── GitFileContent ──────────────────────────────────────────────────────────

// GitFileContentReq 的 Root / RelPath 契约与 ReadFileReq 相同:root 为空 →
// ErrNoCwd,relPath 越界 → ErrPathRefused。
type GitFileContentReq struct {
	Root    string `json:"root"`
	RelPath string `json:"relPath"`
}

// GitFileContentResp 镜像 internal/pkg/workspacefs.GitFileContentResult:对比档左
// 列 = 同一文件在 git HEAD 的版本;NotARepo / !HasHead 时 Content 恒为空。
type GitFileContentResp struct {
	Content  string `json:"content"`
	NotARepo bool   `json:"notARepo,omitempty"` // true 时其余字段恒为零值
	HasHead  bool   `json:"hasHead,omitempty"`  // false 表示空基线(未跟踪/不在 HEAD)
}

// ── GitState ────────────────────────────────────────────────────────────────

// GitStateReq.Root 契约同 GitBranchesReq:调用方已解析出的绝对工作目录。
type GitStateReq struct {
	Root string `json:"root"`
}

// GitStateResp 镜像 internal/pkg/workspacefs.GitStateResult:只读 git 状态快照
// (分支 / worktree 短名 / 未提交数 / 领先落后 / common git dir)。NotARepo 为
// true 时其余字段恒为零值。
//
// CommonDir 单独过 wire——它是下游任务(工作根认领)判定"两个 root 指回同一
// 主仓库"的依据,只能在仓库所在机器上算出,host 侧远端会话没有别的途径拿到它。
type GitStateResp struct {
	NotARepo    bool   `json:"notARepo,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Worktree    string `json:"worktree,omitempty"`
	Dirty       int    `json:"dirty,omitempty"`
	Ahead       int    `json:"ahead,omitempty"`
	Behind      int    `json:"behind,omitempty"`
	HasUpstream bool   `json:"hasUpstream,omitempty"`
	CommonDir   string `json:"commonDir,omitempty"`
}

// ── SearchFiles ─────────────────────────────────────────────────────────────

// SearchFilesReq 的 Root 契约同 ReadFileReq:必填绝对路径,root 为空 → ErrNoCwd。
// 没有 RelPath —— 搜索恒从会话工作目录整棵开始(spec:「遍历从会话工作目录开始」),
// 也就没有可以逃出 cwd 的路径入参。
//
// IncludeIgnored 取自前端「显示忽略项」开关:false 时被 git 忽略的目录整棵剪枝、
// 被忽略的文件不计入。
type SearchFilesReq struct {
	Root           string `json:"root"`
	Query          string `json:"query"`
	IncludeIgnored bool   `json:"includeIgnored,omitempty"`
}

// SearchHit 镜像 internal/pkg/workspacefs.SearchHit:Path 相对 Root、"/" 分隔。
type SearchHit struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir,omitempty"`
}

// SearchFilesResp 的 Truncated 表示结果不完整(命中上限或目录数预算),前端据此
// 在列表末尾出说明 —— 不静默返回不完整结果。
type SearchFilesResp struct {
	Hits      []SearchHit `json:"hits"`
	Truncated bool        `json:"truncated,omitempty"`
}
