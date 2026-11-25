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
	if m.Action == "remove" {
		revoke = m.WorktreePath
	}
	return cwd, revoke
}

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

// EnterWorktreeResultTextLocation 从 EnterWorktree 结果的**正文**取进入的 worktree 路径。
//
// 只给拿不到结果元数据的一侧用:旧 agentred 经持久帧交来的结果是从落库的 tool_result 块
// 投影的,块里只有 {tool_use_id, content, is_error},元数据在落库时就没了。正文形状
// (Claude Code 2.1.326,EnterWorktree 的 message):
//
//	<Created|Resumed|Reused|Entered> worktree at “<路径>”[ on branch “<分支>”]. …
//
// 路径含打印不出的字符时 CLI 写的是改过的拼写并紧跟「 (not its exact spelling)」——
// 那不是真实路径,交回空串;形状对不上(别的版本、截断)时同样不猜。
func EnterWorktreeResultTextLocation(content string) string {
	verb, rest, ok := strings.Cut(content, " worktree at \u201c")
	if !ok {
		return ""
	}
	switch verb {
	case "Created", "Resumed", "Reused", "Entered":
	default:
		return ""
	}
	// 路径本身可能含右引号:取第一个后面接着本句剩余形状的那一个。
	for i := 0; i < len(rest); {
		j := strings.Index(rest[i:], "\u201d")
		if j < 0 {
			return ""
		}
		end := i + j
		tail := rest[end+len("\u201d"):]
		if strings.HasPrefix(tail, " on branch \u201c") || strings.HasPrefix(tail, ".") {
			return rest[:end]
		}
		if strings.HasPrefix(tail, " (not its exact spelling)") {
			return ""
		}
		i = end + len("\u201d")
	}
	return ""
}
