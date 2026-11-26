package agentruntime

import (
	"encoding/json"
	"strings"
)

// Claude Code 的 worktree 工具及其结果元数据里的位置信号(spec 2026-11-24-chat-right-toolwindows
// 「识别信号」)。执行侧(本机 claudecode runtime 与 agentred)据它们给工具结果盖 Cwd;桌面端的
// 远端 runtime 在旧 agentred 没盖时用同一份解析补上 —— 两处同一口径。
const (
	ToolEnterWorktree = "EnterWorktree"
	ToolExitWorktree  = "ExitWorktree"
)

// WorktreeToolResultLocation 从 EnterWorktree / ExitWorktree 的结果元数据取「跑完之后的目录」,
// ExitWorktree 选择删除时另交回被删的 worktree 路径。其它工具、元数据缺失或解不开时为空。
//
// 形状(Claude Code 2.1.326):EnterWorktree {worktreePath, worktreeBranch?, message};
// ExitWorktree {action:"keep"|"remove", originalCwd, worktreePath, restoredCwd?, …} ——
// restoredCwd 是会话实际回到的目录(originalCwd 不在了时是回落目录),老版本不报时用
// originalCwd。
func WorktreeToolResultLocation(name string, meta json.RawMessage) (cwd, revoke string) {
	if len(meta) == 0 || (name != ToolEnterWorktree && name != ToolExitWorktree) {
		return "", ""
	}
	var m struct {
		Action       string `json:"action"`
		WorktreePath string `json:"worktreePath"`
		OriginalCwd  string `json:"originalCwd"`
		RestoredCwd  string `json:"restoredCwd"`
		Message      string `json:"message"`
	}
	if json.Unmarshal(meta, &m) != nil {
		return "", ""
	}
	if name == ToolEnterWorktree {
		return m.WorktreePath, ""
	}
	cwd = m.RestoredCwd
	if cwd == "" {
		cwd = m.OriginalCwd
	}
	if m.Action == "remove" && !strings.HasPrefix(m.Message, exitKeptDespiteRemove) {
		revoke = m.WorktreePath
	}
	return cwd, revoke
}

// exitKeptDespiteRemove 是 ExitWorktree 选了 remove、CLI 却没能删掉 worktree 时结果的开头
// (「Exited worktree but could not remove it — kept at “…”. …」):worktree 还在盘上,
// 不撤销它的根。
const exitKeptDespiteRemove = "Exited worktree but could not remove it"

// WithRevokeRoot 在结果元数据(一个 JSON 对象)里加上 RevokeRootMetaKey,其余字段原样保留。
// 没有元数据时(旧 agentred 的持久帧)从一个空对象起。解不开时原样交回:标记丢了只是
// 不撤销,不能把元数据弄坏。
func WithRevokeRoot(meta json.RawMessage, root string) json.RawMessage {
	m := map[string]json.RawMessage{}
	if len(meta) > 0 && (json.Unmarshal(meta, &m) != nil || m == nil) {
		return meta
	}
	v, err := json.Marshal(root)
	if err != nil {
		return meta
	}
	m[RevokeRootMetaKey] = v
	out, err := json.Marshal(m)
	if err != nil {
		return meta
	}
	return out
}

// WorktreeToolResultTextLocation 是 WorktreeToolResultLocation 的正文版:从 EnterWorktree /
// ExitWorktree 结果的**正文**取同样两项。
//
// 只给拿不到结果元数据的一侧用:旧 agentred 经持久帧交来的结果是从落库的 tool_result 块
// 投影的,块里只有 {tool_use_id, content, is_error},元数据在落库时就没了。正文就是 CLI 的
// message(另可能在末尾接一段沙箱说明),形状(Claude Code 2.1.326):
//
//	EnterWorktree: <Created|Resumed|Reused|Entered> worktree at “<路径>”[ on branch “<分支>”]. …
//	ExitWorktree:  Exited worktree. … Session is now back in “<目录>”. …
//	               Exited and removed worktree at “<被删的>”. … Session is now back in “<目录>”. …
//	               Exited worktree but could not remove it — kept at “<路径>”. …(不撤销)
//	               原目录不在了时恢复句是「… so the session is now in “<目录>”[, a folder …].」
//
// 路径含打印不出的字符时 CLI 写的是改过的拼写并紧跟「 (not its exact spelling)」——
// 那不是真实路径,不报;形状对不上(别的版本、截断)时同样不猜。
func WorktreeToolResultTextLocation(name, content string) (cwd, revoke string) {
	switch name {
	case ToolEnterWorktree:
		return enterWorktreeTextLocation(content), ""
	case ToolExitWorktree:
		return exitWorktreeTextLocation(content)
	}
	return "", ""
}

func enterWorktreeTextLocation(content string) string {
	verb, rest, ok := strings.Cut(content, " worktree at \u201c")
	if !ok {
		return ""
	}
	switch verb {
	case "Created", "Resumed", "Reused", "Entered":
	default:
		return ""
	}
	return quotedPath(rest, " on branch \u201c", ".")
}

func exitWorktreeTextLocation(content string) (cwd, revoke string) {
	if !strings.HasPrefix(content, "Exited ") {
		return "", ""
	}
	if rest, ok := strings.CutPrefix(content, "Exited and removed worktree at \u201c"); ok {
		revoke = quotedPath(rest, ".")
	}
	if _, rest, ok := strings.Cut(content, "Session is now back in \u201c"); ok {
		cwd = quotedPath(rest, ".")
	} else if _, rest, ok := strings.Cut(content, " so the session is now in \u201c"); ok {
		cwd = quotedPath(rest, ".", ",")
	}
	return cwd, revoke
}

// quotedPath 取 rest 开头那段被 “…” 括起的路径(左引号已被调用方切掉):路径本身可能
// 含右引号,取第一个后面接着 tails 之一的右引号。CLI 注明「不是确切拼写」时交回空串。
func quotedPath(rest string, tails ...string) string {
	for i := 0; i < len(rest); {
		j := strings.Index(rest[i:], "\u201d")
		if j < 0 {
			return ""
		}
		end := i + j
		tail := rest[end+len("\u201d"):]
		if strings.HasPrefix(tail, " (not its exact spelling)") {
			return ""
		}
		for _, t := range tails {
			if strings.HasPrefix(tail, t) {
				return rest[:end]
			}
		}
		i = end + len("\u201d")
	}
	return ""
}
