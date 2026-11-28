package httpgateway

import "sync"

// HookLocation 是一次 PostToolUse hook 报告的「工具跑完之后 AI 所在的目录」。
// AgentID 非空 = 这次工具调用来自子代理(hook 输入带 agent_id):它的目录只参与认领,
// 不移动主会话的 AI 当前位置。
type HookLocation struct {
	Cwd     string
	AgentID string
}

// HookLocations 记下 hook 子进程经 GET /hook/v1/inbox 报来的每个工具调用之后的目录,
// 按 tool_use_id 取。claudecode runtime 在处理同一 tool_use_id 的 tool_result 帧时
// Take 它,盖到 ToolResult.Cwd 上。
//
// 顺序有保证:CLI 等 hook 进程退出才往 stdout 写这条 tool_result,而 hook 进程要等
// 收件箱应答才退出,应答之前 Record 已经落下 —— tool_result 到达时记录一定可见。
//
// 取用即删;一条会话的残留(例如 hook 报了、tool_result 被过滤)在会话关闭时 Forget。
type HookLocations struct {
	mu   sync.Mutex
	recs map[string]hookLocationRec
}

type hookLocationRec struct {
	sid string
	loc HookLocation
}

// NewHookLocations returns an empty book.
func NewHookLocations() *HookLocations {
	return &HookLocations{recs: make(map[string]hookLocationRec)}
}

// Record 记下 sid 会话里 toolUseID 这次调用之后的目录。toolUseID 或 cwd 为空(老 CLI
// 不报)时什么都不记。
func (h *HookLocations) Record(sid, toolUseID, cwd, agentID string) {
	if toolUseID == "" || cwd == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recs[toolUseID] = hookLocationRec{sid: sid, loc: HookLocation{Cwd: cwd, AgentID: agentID}}
}

// Take 取走 toolUseID 的记录;没有时 ok=false。
func (h *HookLocations) Take(toolUseID string) (HookLocation, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec, ok := h.recs[toolUseID]
	if ok {
		delete(h.recs, toolUseID)
	}
	return rec.loc, ok
}

// Forget 删掉 sid 会话的全部残留记录(会话关闭时调用)。
func (h *HookLocations) Forget(sid string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, rec := range h.recs {
		if rec.sid == sid {
			delete(h.recs, id)
		}
	}
}
