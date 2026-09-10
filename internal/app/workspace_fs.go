package app

import (
	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
)

// WorkspaceFsWorkRoots 列出本会话已认领的工作根:会话 cwd 恒是第一项
// (isPrimary),AI 写进同一主仓库的另一个 worktree 时那个 worktree 也在其中。
// 下面几个方法的 root 参数只接受这里给出的路径,其余一律被拒。
func (a *App) WorkspaceFsWorkRoots(sessionID int64) ([]workspace_fs_svc.WorkRootView, error) {
	return workspace_fs_svc.Default().WorkRoots(a.ctx, sessionID)
}

// WorkspaceFsListDir 列出 root 下 relPath 这一层。
//   - root 空串表示会话 cwd;非空时必须是 WorkspaceFsWorkRoots 给出的某个根
//   - relPath 相对 root,空串表示 root 本身;越界请求由服务层拒绝
//   - includeIgnored=false 时被 git 忽略的条目不出现在结果里
//
// 本地会话与远端(agentred)会话都走这一个方法,路由由服务层按会话解析出的
// deviceID 决定,前端不感知差异。
func (a *App) WorkspaceFsListDir(sessionID int64, root, relPath string, includeIgnored bool) (*workspace_fs_svc.ListDirView, error) {
	return workspace_fs_svc.Default().ListDir(a.ctx, sessionID, root, relPath, includeIgnored)
}

// WorkspaceFsGitChanges 取 root 下的 git 变动(root 空串即会话 cwd)。
//   - scope: "uncommitted"(未提交档)或 "branch"(本分支档)
//   - baseRef: 仅 "branch" 档有意义,空串表示用推断出的默认基线;返回值的
//     BaseRef 是本次实际比较用的那个
func (a *App) WorkspaceFsGitChanges(sessionID int64, root, scope, baseRef string) (*workspace_fs_svc.GitChangesView, error) {
	return workspace_fs_svc.Default().GitChanges(a.ctx, sessionID, root, scope, baseRef)
}

// WorkspaceFsGitBranches 取会话工作目录的分支清单、当前分支与推断出的默认基线。
func (a *App) WorkspaceFsGitBranches(sessionID int64) (*workspace_fs_svc.GitBranchesView, error) {
	return workspace_fs_svc.Default().GitBranches(a.ctx, sessionID)
}

// WorkspaceFsGitState 取只读 git 状态快照:分支 / worktree 短名 / 未提交数 /
// 领先落后 / common git dir。root 为空串时用会话解析出的 cwd;非空时必须是
// WorkspaceFsWorkRoots 给出的某个根,其余一律被服务层拒。本地会话与远端
// (agentred)会话都走这一个方法,路由由服务层按会话解析出的 deviceID 决定,
// 前端不感知差异。
func (a *App) WorkspaceFsGitState(sessionID int64, root string) (*workspace_fs_svc.GitStateView, error) {
	return workspace_fs_svc.Default().GitState(a.ctx, sessionID, root)
}

// WorkspaceFsReadFile 读取 root(空串即会话 cwd)下 relPath 所指文件的内容(会话级文件
// 预览,纯读)。文本返回 UTF-8 正文;图片返回 base64 内容 + contentType;
// binary/tooLarge 是视图标志。本地与远端(agentred)会话都走这一个方法,路由
// 由服务层按会话解析出的 deviceID 决定;路径越界由服务层 / daemon 强制。
func (a *App) WorkspaceFsReadFile(sessionID int64, root, relPath string) (*workspace_fs_svc.ReadFileView, error) {
	return workspace_fs_svc.Default().ReadFile(a.ctx, sessionID, root, relPath)
}

// WorkspaceFsGitFileContent 取同一文件在 git HEAD 的版本(对比档左列)。文件未
// 跟踪 / 不在 HEAD → 空基线(hasHead=false);非 git 仓库 → notARepo。
func (a *App) WorkspaceFsGitFileContent(sessionID int64, root, relPath string) (*workspace_fs_svc.GitFileContentView, error) {
	return workspace_fs_svc.Default().GitFileContent(a.ctx, sessionID, root, relPath)
}

// WorkspaceFsSearchFiles 从 root(空串即会话 cwd)递归搜索 basename 含 query 子串(不区分
// 大小写)的文件与目录。
//   - includeIgnored 取自「显示忽略项」开关:false 时被 git 忽略的目录整棵剪枝、
//     被忽略的文件不计入;".git" 恒不进入
//   - 返回的 truncated=true 表示结果不完整(命中上限或目录数预算),前端需在列表
//     末尾出说明
//
// 本地与远端(agentred)会话都走这一个方法;旧 agentred 不认识该方法时,服务层
// 把 -32601 翻成「远端 agentred 版本过旧」而不是通用调用失败。
func (a *App) WorkspaceFsSearchFiles(sessionID int64, root, query string, includeIgnored bool) (*workspace_fs_svc.SearchFilesView, error) {
	return workspace_fs_svc.Default().SearchFiles(a.ctx, sessionID, root, query, includeIgnored)
}
