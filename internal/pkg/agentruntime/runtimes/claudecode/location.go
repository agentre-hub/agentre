package claudecode

import (
	"encoding/json"
	"sync"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// AI 当前位置(spec 2026-11-24-chat-right-toolwindows「识别信号」,决策 15–17)。
//
// Claude Code 的位置信号都在执行侧:init 帧的目录(启动 / 恢复)、EnterWorktree 结果的
// worktreePath、ExitWorktree 结果的 restoredCwd、PostToolUse hook 输入的 cwd(覆盖
// Bash `cd`;不用 CwdChanged hook,它会误报)。drainStream 把它们盖到工具事件的 Cwd
// 上 —— 本机进程与 agentred 跑的是同一段代码,远端会话的位置因此随工具事件经 wire 到桌面:
//   - 主 agent 的工具调用带「调用时 AI 所在目录」(init 帧之后即有;init 帧单独到达、
//     还没有工具事件时,它由下一个工具调用带过去);
//   - 工具结果带「跑完之后的目录」:EnterWorktree / ExitWorktree 取结果元数据,其余工具
//     取 hook 报的 cwd;
//   - ExitWorktree 选择删除时,结果 Meta 里另加 agentruntime.RevokeRootMetaKey = 被删的
//     worktree 路径,chat_svc 据此撤销那个根。
//
// 子代理(事件带 ParentToolUseID / hook 带 agent_id)的目录只盖到它自己的结果上(消费方
// 据 ParentToolCallID 只认领),不移动主位置;子代理的调用不带目录。

const (
	toolEnterWorktree = "EnterWorktree"
	toolExitWorktree  = "ExitWorktree"
)

// SetHookLocations 由 bootstrap / agentred 在 gateway 起来后注入 hook 位置簿(与 SetSteerInbox
// 同一处)。
func (r *Runtime) SetHookLocations(h *httpgateway.HookLocations) { r.hookLocs = h }

// HookLocationsConfigured 回答「这份 runtime 拿到 hook 位置簿了没有」—— 与
// SteerInboxConfigured 同样是接线的可观测缝,两处启动路径各有一条回归测试盯着它。
func (r *Runtime) HookLocationsConfigured() bool { return r.hookLocs != nil }

// aiLocation 是一个 CLI 子进程上主 agent 的当前位置,以及在途 EnterWorktree /
// ExitWorktree 调用的工具名(tool_result 帧不带工具名)。user turn、自主续轮与后台
// subagent 活动轮可能并发 drain 同一会话,由 mu 守护。
type aiLocation struct {
	mu            sync.Mutex
	cur           string
	worktreeCalls map[string]string
}

// stampLocation 把 ev 带来的位置信号盖到它翻译出的工具事件上,并推进主位置。
func (a *claudeActive) stampLocation(ev claudecode.Event, translated []agentruntime.Event) []agentruntime.Event {
	l := &a.location
	l.mu.Lock()
	defer l.mu.Unlock()
	switch ev.Kind {
	case claudecode.EventInit:
		if ev.Cwd != "" {
			l.cur = ev.Cwd
		}
	case claudecode.EventPreToolUse:
		if ev.Tool == nil {
			return translated
		}
		if ev.Tool.Name == toolEnterWorktree || ev.Tool.Name == toolExitWorktree {
			if l.worktreeCalls == nil {
				l.worktreeCalls = make(map[string]string)
			}
			l.worktreeCalls[ev.Tool.ID] = ev.Tool.Name
		}
		if ev.ParentToolUseID != "" || l.cur == "" {
			return translated
		}
		for i, t := range translated {
			if tc, ok := t.(agentruntime.ToolCall); ok && tc.ID == ev.Tool.ID {
				tc.Cwd = l.cur
				translated[i] = tc
			}
		}
	case claudecode.EventPostToolUse:
		if ev.Tool == nil {
			return translated
		}
		// 无论这条结果是否入流都取走记录,不留残余。
		var hook httpgateway.HookLocation
		var hooked bool
		if a.hookLocs != nil {
			hook, hooked = a.hookLocs.Take(ev.Tool.ID)
		}
		name := l.worktreeCalls[ev.Tool.ID]
		delete(l.worktreeCalls, ev.Tool.ID)

		nested := ev.ParentToolUseID != ""
		if hooked && hook.AgentID != "" && !nested {
			// hook 说是子代理,事件却没有外层归属:没法把它标成「只认领」,宁可不报,
			// 也不能拖动主位置。
			return translated
		}
		var cwd, revoke string
		if ev.Tool.Err == nil {
			cwd, revoke = worktreeResultLocation(name, ev.Tool.ResultMeta)
		}
		if cwd == "" && hooked {
			cwd = hook.Cwd
		}
		if cwd == "" {
			return translated
		}
		if !nested {
			l.cur = cwd
		}
		for i, t := range translated {
			if tr, ok := t.(agentruntime.ToolResult); ok && tr.ToolCallID == ev.Tool.ID {
				tr.Cwd = cwd
				if revoke != "" {
					tr.Meta = withRevokeRoot(tr.Meta, revoke)
				}
				translated[i] = tr
			}
		}
	}
	return translated
}

// worktreeResultLocation 从 EnterWorktree / ExitWorktree 的结果元数据取「跑完之后的目录」,
// ExitWorktree 选择删除时另交回被删的 worktree 路径。其它工具、元数据缺失或解不开时为空。
//
// 形状(Claude Code 2.1.326):EnterWorktree {worktreePath, worktreeBranch?, message};
// ExitWorktree {action:"keep"|"remove", originalCwd, worktreePath, restoredCwd?, …} ——
// restoredCwd 是会话实际回到的目录(originalCwd 不在了时是回落目录),老版本不报时用
// originalCwd。
func worktreeResultLocation(name string, meta json.RawMessage) (cwd, revoke string) {
	if len(meta) == 0 || (name != toolEnterWorktree && name != toolExitWorktree) {
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
	if name == toolEnterWorktree {
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

// withRevokeRoot 在结果元数据(一个 JSON 对象)里加上 agentruntime.RevokeRootMetaKey,其余字段原样保留。
// 解不开时原样交回:标记丢了只是不撤销,不能把元数据弄坏。
func withRevokeRoot(meta json.RawMessage, root string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(meta, &m) != nil || m == nil {
		return meta
	}
	v, err := json.Marshal(root)
	if err != nil {
		return meta
	}
	m[agentruntime.RevokeRootMetaKey] = v
	out, err := json.Marshal(m)
	if err != nil {
		return meta
	}
	return out
}
