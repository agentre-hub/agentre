package agentruntime

import "encoding/json"

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
// 解不开时原样交回:标记丢了只是不撤销,不能把元数据弄坏。
func WithRevokeRoot(meta json.RawMessage, root string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(meta, &m) != nil || m == nil {
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
