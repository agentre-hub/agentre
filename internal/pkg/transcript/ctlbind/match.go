// Package ctlbind 把 `agrctl send --wait` 建出的子会话绑到父轮里发出这次 send 的那条
// 外层 shell 调用上,并把子会话的事件转成挂在那条调用下的嵌套事件 —— 父转录里那条
// shell 行因此原地升级成子代理卡。
//
// 这里只有纯函数:调用方(父轮的宿主,本机是桌面端,远端是 agentred)负责在父轮的锁
// 里取 Accumulator 快照、调用 Match、注入 overlay 与嵌套事件。服务端那一侧的前提
// (会话级 token、带 --wait、目标会话由桌面端拥有)也由调用方先判,不在这里。
package ctlbind

import (
	"strconv"
	"strings"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

// shellToolNames 是三个后端的外层 shell 工具名:Claude `Bash`、Codex
// `command_execution`、Pi `bash`。只看外层调用 —— Pi subagent 扩展拉起的子进程继承了
// 父会话的 token,它跑的 send 对应的是嵌套块,参与匹配会和 Pi 的 tracker 抢同一张卡。
var shellToolNames = map[string]bool{
	"Bash":              true,
	"command_execution": true,
	"bash":              true,
}

// Target 描述服务端收到的这次 send。
type Target struct {
	// AgentName 是 `--agent` 的值;空表示调用方只给了 --agent-id。
	AgentName string
	// AgentID 是 `--agent-id` 的值;0 表示未指定。
	AgentID int64
	// Task 是任务文本(send 的位置参数拼成的那段),多条候选时用来消歧。
	Task string
}

// Binding 是匹配结果:被绑定的那条 shell 调用。
type Binding struct {
	ToolCallID string
	// Background 只按那条调用入参里的 run_in_background 判断,不看 send 请求与 shell
	// 结果谁先到 —— 后台 Bash 会立刻拿到结果,与 send 请求到达是竞争关系。
	Background bool
}

// Kind 是绑定出的 overlay 应当使用的 SubagentStateBlock.Kind:前台用 agrctl 专属
// kind;后台保留 local_bash,后台面板、停止按钮、轮结束保护都依赖它。
func (b Binding) Kind() string {
	if b.Background {
		return agentruntime.SubagentKindLocalBash
	}
	return agentruntime.SubagentKindAgrctl
}

// Reason 是没有绑定的原因,供调用方记 info 日志。
type Reason string

const (
	// ReasonNoCandidate 父轮里没有可绑定的外层 shell 调用:没有含 agrctl + send +
	// 目标 agent 的命令,或这样的前台调用已经拿到结果(已关闭)。
	ReasonNoCandidate Reason = "no_candidate"
	// ReasonAmbiguous 有多条候选,按任务文本筛完仍不是正好一条。
	ReasonAmbiguous Reason = "ambiguous"
	// ReasonAlreadyBound 命中的调用都已经绑过一个子会话(一条调用最多绑一个)。
	ReasonAlreadyBound Reason = "already_bound"
)

// Match 在父轮的外层工具调用里找发出这次 send 的那条 shell 调用。返回的 Reason 为空
// 表示绑定成功;非空时 Binding 为零值。
//
// 规则:
//  1. 候选 = 外层 shell 调用,命令文本同时含 `agrctl`、`send` 与目标 agent 名(或
//     --agent-id 的值);前台调用须还没有结果,后台(run_in_background)调用不论。
//  2. bound 里的调用(已绑过的)不参与竞争;候选只剩已绑过的 → ReasonAlreadyBound。
//  3. 正好一条就绑定它;多条时按任务文本筛,仍不是正好一条 → ReasonAmbiguous。
//
// calls 通常取自 turn.Accumulator.OuterToolUses(),bound 取自 BoundToolCallIDs。
func Match(calls []turn.OuterToolUse, bound map[string]bool, target Target) (Binding, Reason) {
	var free, taken []turn.OuterToolUse
	for _, c := range calls {
		if !isCandidate(c, target) {
			continue
		}
		if bound[c.ID] {
			taken = append(taken, c)
		} else {
			free = append(free, c)
		}
	}
	switch {
	case len(free) == 0 && len(taken) > 0:
		return Binding{}, ReasonAlreadyBound
	case len(free) == 0:
		return Binding{}, ReasonNoCandidate
	case len(free) > 1:
		free = filterByTask(free, target.Task)
		if len(free) != 1 {
			return Binding{}, ReasonAmbiguous
		}
	}
	return Binding{ToolCallID: free[0].ID, Background: runsInBackground(free[0])}, ""
}

// BoundToolCallIDs 从一段块序列(进行中轮的 Accumulator.Snapshot,或已落库消息解出的
// 块)里找出已被 agrctl 绑定的 shell 调用,作为 Match 的 bound 入参。
func BoundToolCallIDs(snapshot []cagoblocks.ContentBlock) map[string]bool {
	bound := make(map[string]bool)
	for _, b := range snapshot {
		var sb blocks.SubagentStateBlock
		switch v := b.(type) {
		case *blocks.SubagentStateBlock:
			sb = *v
		case blocks.SubagentStateBlock:
			sb = v
		default:
			continue
		}
		if sb.Origin == agentruntime.SubagentOriginAgrctl {
			bound[sb.ParentToolCallID] = true
		}
	}
	return bound
}

func isCandidate(c turn.OuterToolUse, target Target) bool {
	if !shellToolNames[c.Name] {
		return false
	}
	if c.HasResult && !runsInBackground(c) {
		return false // 前台调用已经拿到结果:它跑的 send 早就结束了
	}
	cmd := commandOf(c)
	if !strings.Contains(cmd, "agrctl") || !strings.Contains(cmd, "send") {
		return false
	}
	return (target.AgentName != "" && strings.Contains(cmd, target.AgentName)) ||
		(target.AgentID != 0 && strings.Contains(cmd, strconv.FormatInt(target.AgentID, 10)))
}

func filterByTask(calls []turn.OuterToolUse, task string) []turn.OuterToolUse {
	task = strings.TrimSpace(task)
	if task == "" {
		return calls
	}
	var out []turn.OuterToolUse
	for _, c := range calls {
		if strings.Contains(commandOf(c), task) {
			out = append(out, c)
		}
	}
	return out
}

// commandOf 取 shell 调用的命令文本。三个后端都是 input.command 一条字符串(Codex 带
// `/bin/zsh -lc` 包装,按子串匹配不受影响)。
func commandOf(c turn.OuterToolUse) string {
	cmd, _ := c.Input["command"].(string)
	return cmd
}

// runsInBackground 只认入参里显式的 run_in_background:true(Claude 后台 Bash);Codex
// 与 Pi 的 shell 没有后台参数。
func runsInBackground(c turn.OuterToolUse) bool {
	bg, _ := c.Input["run_in_background"].(bool)
	return bg
}
