package remote

import (
	"bytes"
	"encoding/json"
	"sync"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/shellloc"
)

// 旧 agentred 的位置回落(spec 2026-11-24-chat-right-toolwindows「识别信号 > 远端会话」)。
//
// 「AI 当时所在目录」本该由执行侧盖在工具事件的 Cwd 上(wire 字段 7)。早于它构建的 agentred
// 不发这个字段,解码出来就是空串 —— 但推出位置所需的大多数信号照样随事件到了桌面:Claude Code
// EnterWorktree / ExitWorktree 的结果元数据、Codex 命令入参里的 cwd、pi / builtin / ACP 的
// shell 命令文本。注意旧 agentred 进事件流的是持久帧(从落库块投影,实时帧只作预览):
// tool_result 块不存元数据,所以 EnterWorktree 只能从结果正文取路径;ExitWorktree 回到
// 进入之前记下的目录,是否撤销该根看落库的入参 action(见 followWorktreeTool)。这里在事件交给消费方之前,对**没带 Cwd** 的工具事件用与执行侧同一份解析
// 补上,不报错、不提示。新 agentred 已盖的 Cwd 原样保留,所以对新端是空操作。
//
// 子代理的嵌套事件(ParentToolCallID / SubagentRunID 非空)不进 shell 命令跟踪:它们只参与
// 认领,不能推动主会话的位置;worktree 工具的结果元数据对它们照样适用(消费方据嵌套归属只认领)。

// legacyLocations 是各会话的回落状态,按本地会话键,跨轮保持(cd 跨轮有效,同执行侧的 Registry)。
type legacyLocations struct {
	mu sync.Mutex
	m  map[int64]*legacyLocation
}

type legacyLocation struct {
	backend agent_backend_entity.BackendType
	// shell 只给没有结构化位置信号的后端(pi / builtin / ACP):从命令文本跟踪主 agent 的目录。
	shell *shellloc.Tracker
	// calls 记在途调用的工具名与推出的目录(工具结果帧不带工具名),结果到达时取走。
	calls map[string]legacyCall
	// runCwd 是最近一轮的工作目录;here 是 worktree 工具推出的主 agent 位置(空 = 仍在
	// runCwd);entered 是主 agent 当前所在、由 EnterWorktree 推出的 worktree。旧 agentred
	// 的 ExitWorktree 结果没有元数据,回到哪、撤销哪个根都从这里取。
	runCwd  string
	here    string
	entered *legacyWorktree
}

// legacyWorktree 是一次推出的 EnterWorktree:进入的 worktree 与进入之前所在的目录。
type legacyWorktree struct{ path, before string }

// legacyCall 是一次在途调用;remove 只对 ExitWorktree 有意义:入参 action 是 remove。
type legacyCall struct {
	name, cwd string
	remove    bool
}

// noteRun 记下一轮所在会话的后端与工作目录(执行设备上的路径)。补齐轮 / 自主续轮在本进程里
// 可能没有 Run 起过:后端未知时只做与后端无关的那几种推断(worktree 工具、Codex 命令目录)。
func (l *legacyLocations) noteRun(sid int64, backend agent_backend_entity.BackendType, cwd string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.stateLocked(sid)
	st.backend = backend
	st.runCwd = cwd
	if st.shell == nil && textLocated(backend) {
		if freshShell(backend) {
			st.shell = shellloc.NewFreshTracker(cwd)
		} else {
			st.shell = shellloc.NewTracker(cwd)
		}
	}
}

func (l *legacyLocations) stateLocked(sid int64) *legacyLocation {
	if l.m == nil {
		l.m = map[int64]*legacyLocation{}
	}
	st, ok := l.m[sid]
	if !ok {
		st = &legacyLocation{calls: map[string]legacyCall{}}
		l.m[sid] = st
	}
	return st
}

// textLocated 报告后端的位置是否只能从 shell 命令文本识别。
func textLocated(b agent_backend_entity.BackendType) bool {
	switch b {
	case agent_backend_entity.TypePiAgent, agent_backend_entity.TypeBuiltin, agent_backend_entity.TypeACP:
		return true
	}
	return false
}

// freshShell 报告后端的 shell 工具是否每次调用都在会话工作目录新起进程(pi / builtin):
// cd 只作用于它所在的那次调用。ACP 的 shell 由各 agent 自己实现,按 cd 延续处理。
func freshShell(b agent_backend_entity.BackendType) bool {
	return b == agent_backend_entity.TypePiAgent || b == agent_backend_entity.TypeBuiltin
}

// infer 给没带 Cwd 的工具事件补上位置;其它事件原样交回。
func (l *legacyLocations) infer(sid int64, ev agentruntime.Event) agentruntime.Event {
	switch e := ev.(type) {
	case agentruntime.ToolCall:
		if e.Cwd != "" {
			return ev
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		st := l.stateLocked(sid)
		nested := e.ParentToolCallID != "" || e.SubagentRunID != ""
		switch {
		case e.Name == shellloc.CodexCommandTool:
			e.Cwd = shellloc.InputCwd(e.Input)
		case !nested && st.shell != nil && isShellCall(st.backend, e):
			e.Cwd, e.Input = st.shell.Call(e.ID, e.Input)
		case !nested && st.shell != nil:
			e.Cwd = st.shell.Here(e.ID)
		}
		st.calls[e.ID] = legacyCall{name: e.Name, cwd: e.Cwd, remove: e.Name == agentruntime.ToolExitWorktree && exitRemoves(e.Input)}
		return e
	case agentruntime.ToolResult:
		l.mu.Lock()
		defer l.mu.Unlock()
		st := l.stateLocked(sid)
		call, known := st.calls[e.ToolCallID]
		delete(st.calls, e.ToolCallID)
		if e.Cwd != "" || !known {
			return ev
		}
		nested := e.ParentToolCallID != "" || e.SubagentRunID != ""
		switch {
		case call.name == agentruntime.ToolEnterWorktree || call.name == agentruntime.ToolExitWorktree:
			if e.IsError {
				return ev
			}
			cwd, revoke := agentruntime.WorktreeToolResultLocation(call.name, e.Meta)
			if !nested {
				cwd, revoke = st.followWorktreeTool(call, e.Content, cwd, revoke)
			}
			e.Cwd = cwd
			if revoke != "" && !bytes.Contains(e.Meta, []byte(agentruntime.RevokeRootMetaKey)) {
				e.Meta = agentruntime.WithRevokeRoot(e.Meta, revoke)
			}
		case call.name == shellloc.CodexCommandTool:
			e.Cwd = call.cwd
		case !nested && st.shell != nil:
			e.Cwd = st.shell.Result(e.ToolCallID)
		}
		return e
	}
	return ev
}

// followWorktreeTool 推进主 agent 的 worktree 位置,并在结果元数据没给出目录时补上(旧
// agentred 的持久帧从落库块投影,元数据没过来):EnterWorktree 从结果正文取进入的路径;
// ExitWorktree 回到进入之前所在的目录(没有记录时回到这一轮的工作目录),入参
// action=remove 时撤销进入的那个 worktree 根。cwd / revoke 是元数据给出的值,原样优先。
func (st *legacyLocation) followWorktreeTool(call legacyCall, content, cwd, revoke string) (string, string) {
	here := st.here
	if here == "" {
		here = st.runCwd
	}
	if call.name == agentruntime.ToolEnterWorktree {
		if cwd == "" {
			cwd = agentruntime.EnterWorktreeResultTextLocation(content)
		}
		if cwd != "" {
			st.entered = &legacyWorktree{path: cwd, before: here}
			st.here = cwd
		}
		return cwd, revoke
	}
	if cwd == "" {
		cwd = st.runCwd
		if st.entered != nil && st.entered.before != "" {
			cwd = st.entered.before
		}
	}
	if revoke == "" && call.remove && st.entered != nil {
		revoke = st.entered.path
	}
	st.entered = nil
	st.here = cwd
	return cwd, revoke
}

// exitRemoves 报告 ExitWorktree 的入参是否选择删除 worktree({"action":"remove",…})。
func exitRemoves(input json.RawMessage) bool {
	var in struct {
		Action string `json:"action"`
	}
	return json.Unmarshal(input, &in) == nil && in.Action == "remove"
}

// isShellCall 认出 shell 调用:pi / builtin 的 bash 工具;ACP 的工具名是标题,按入参带
// command 文本认(执行侧按 ACP 的 kind=execute 认,那份信息不过线)。
func isShellCall(b agent_backend_entity.BackendType, call agentruntime.ToolCall) bool {
	if b != agent_backend_entity.TypeACP {
		return call.Name == "bash"
	}
	var in struct {
		Command *string `json:"command"`
	}
	return json.Unmarshal(call.Input, &in) == nil && in.Command != nil
}
