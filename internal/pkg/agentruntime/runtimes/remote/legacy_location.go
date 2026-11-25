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
// shell 命令文本。这里在事件交给消费方之前,对**没带 Cwd** 的工具事件用与执行侧同一份解析
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
}

type legacyCall struct{ name, cwd string }

// noteRun 记下一轮所在会话的后端与工作目录(执行设备上的路径)。补齐轮 / 自主续轮在本进程里
// 可能没有 Run 起过:后端未知时只做与后端无关的那几种推断(worktree 工具、Codex 命令目录)。
func (l *legacyLocations) noteRun(sid int64, backend agent_backend_entity.BackendType, cwd string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.stateLocked(sid)
	st.backend = backend
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
		st.calls[e.ID] = legacyCall{name: e.Name, cwd: e.Cwd}
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
