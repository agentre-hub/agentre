package ctlbind

import (
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

// shell 造一条外层 shell 调用;bg 为 nil 表示入参里没有 run_in_background。
func shell(id, name, command string, bg *bool, hasResult bool) turn.OuterToolUse {
	input := map[string]any{"command": command}
	if bg != nil {
		input["run_in_background"] = *bg
	}
	return turn.OuterToolUse{ID: id, Name: name, Input: input, HasResult: hasResult}
}

func boolPtr(v bool) *bool { return &v }

func TestMatch(t *testing.T) {
	reviewer := Target{AgentName: "reviewer", Task: "审一下 PR 42"}
	cases := []struct {
		name   string
		calls  []turn.OuterToolUse
		bound  map[string]bool
		target Target
		want   Binding
		reason Reason
	}{
		{
			name:   "唯一候选(Claude Bash)→ 绑定前台",
			calls:  []turn.OuterToolUse{shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"`, nil, false)},
			target: reviewer,
			want:   Binding{ToolCallID: "sh-1"},
		},
		{
			name: "Codex command_execution 的 zsh -lc 包装照样认",
			calls: []turn.OuterToolUse{shell("item-3", "command_execution",
				`/bin/zsh -lc 'agrctl send --wait --agent reviewer "审一下 PR 42"'`, nil, false)},
			target: reviewer,
			want:   Binding{ToolCallID: "item-3"},
		},
		{
			name:   "Pi bash",
			calls:  []turn.OuterToolUse{shell("tc-1", "bash", `agrctl send --wait --agent reviewer 审一下 PR 42`, nil, false)},
			target: reviewer,
			want:   Binding{ToolCallID: "tc-1"},
		},
		{
			name:   "--agent-id 指定目标时按 id 值认",
			calls:  []turn.OuterToolUse{shell("sh-1", "Bash", `agrctl send --wait --agent-id 42 "跑测试"`, nil, false)},
			target: Target{AgentID: 42, Task: "跑测试"},
			want:   Binding{ToolCallID: "sh-1"},
		},
		{
			name: "多条候选时按任务文本消歧",
			calls: []turn.OuterToolUse{
				shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "看看文档"`, nil, false),
				shell("sh-2", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"`, nil, false),
			},
			target: reviewer,
			want:   Binding{ToolCallID: "sh-2"},
		},
		{
			name: "按任务文本筛完仍不止一条 → 歧义不绑",
			calls: []turn.OuterToolUse{
				shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"`, nil, false),
				shell("sh-2", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"`, nil, false),
			},
			target: reviewer,
			reason: ReasonAmbiguous,
		},
		{
			name: "多条候选且任务文本一条都对不上 → 歧义不绑",
			calls: []turn.OuterToolUse{
				shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "甲"`, nil, false),
				shell("sh-2", "Bash", `agrctl send --wait --agent reviewer "乙"`, nil, false),
			},
			target: reviewer,
			reason: ReasonAmbiguous,
		},
		{
			name:   "没有任何调用 → 无候选",
			target: reviewer,
			reason: ReasonNoCandidate,
		},
		{
			name: "命令缺 agrctl / send / 目标 agent 任一 → 无候选",
			calls: []turn.OuterToolUse{
				shell("sh-1", "Bash", `send --wait --agent reviewer "审一下 PR 42"`, nil, false),
				shell("sh-2", "Bash", `agrctl get --agent reviewer`, nil, false),
				shell("sh-3", "Bash", `agrctl send --wait --agent writer "审一下 PR 42"`, nil, false),
			},
			target: reviewer,
			reason: ReasonNoCandidate,
		},
		{
			name: "不是 shell 工具 → 无候选",
			calls: []turn.OuterToolUse{
				{ID: "w-1", Name: "Write", Input: map[string]any{"command": `agrctl send --wait --agent reviewer x`}},
			},
			target: reviewer,
			reason: ReasonNoCandidate,
		},
		{
			name:   "前台调用已拿到结果(已关闭)→ 无候选",
			calls:  []turn.OuterToolUse{shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"`, nil, true)},
			target: reviewer,
			reason: ReasonNoCandidate,
		},
		{
			name: "run_in_background 的调用即便已拿到结果也照样绑定,且判为后台",
			calls: []turn.OuterToolUse{
				shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"`, boolPtr(true), true),
			},
			target: reviewer,
			want:   Binding{ToolCallID: "sh-1", Background: true},
		},
		{
			name: "run_in_background 的调用还没结果 → 后台",
			calls: []turn.OuterToolUse{
				shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"`, boolPtr(true), false),
			},
			target: reviewer,
			want:   Binding{ToolCallID: "sh-1", Background: true},
		},
		{
			name: "显式 run_in_background:false → 前台",
			calls: []turn.OuterToolUse{
				shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"`, boolPtr(false), false),
			},
			target: reviewer,
			want:   Binding{ToolCallID: "sh-1"},
		},
		{
			name:   "唯一命中的调用已绑过 → 不再绑定",
			calls:  []turn.OuterToolUse{shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"; agrctl send --wait --agent reviewer "审一下 PR 42"`, nil, false)},
			bound:  map[string]bool{"sh-1": true},
			target: reviewer,
			reason: ReasonAlreadyBound,
		},
		{
			// 规则 5 + 不挂错卡:已绑过的 sh-1 仍开着,它命令里的第二个 send 与 sh-2 的 send
			// 认不出来是谁发的 —— 已绑过的调用照样算候选,歧义就不绑。
			name: "已绑过的调用仍算候选:与另一条开着的调用分不清 → 歧义不绑",
			calls: []turn.OuterToolUse{
				shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"; agrctl send --wait --agent reviewer "审一下 PR 42"`, boolPtr(true), true),
				shell("sh-2", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"`, boolPtr(true), true),
			},
			bound:  map[string]bool{"sh-1": true},
			target: reviewer,
			reason: ReasonAmbiguous,
		},
		{
			name: "已绑过的调用被任务文本筛掉后,剩下的唯一一条照常绑定",
			calls: []turn.OuterToolUse{
				shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "看看文档"`, nil, false),
				shell("sh-2", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"`, nil, false),
			},
			bound:  map[string]bool{"sh-1": true},
			target: reviewer,
			want:   Binding{ToolCallID: "sh-2"},
		},
		{
			name: "按任务文本筛出的唯一一条已绑过 → 不再绑定",
			calls: []turn.OuterToolUse{
				shell("sh-1", "Bash", `agrctl send --wait --agent reviewer "审一下 PR 42"; agrctl send --wait --agent reviewer "审一下 PR 42"`, nil, false),
				shell("sh-2", "Bash", `agrctl send --wait --agent reviewer "看看文档"`, nil, false),
			},
			bound:  map[string]bool{"sh-1": true},
			target: reviewer,
			reason: ReasonAlreadyBound,
		},
	}

	Convey("Match 在父轮外层 shell 调用里挑出 send --wait 对应的那一条", t, func() {
		for _, tc := range cases {
			Convey(tc.name, func() {
				got, reason := Match(tc.calls, tc.bound, tc.target)
				So(reason, ShouldEqual, tc.reason)
				So(got, ShouldResemble, tc.want)
			})
		}

		Convey("Given 只有子代理内层的 shell 调用(嵌套块), Then 不参与匹配", func() {
			acc := turn.New()
			acc.AddToolUse(&cagoblocks.ToolUseBlock{ID: "agent-1", Name: "Agent"}, "")
			acc.AddToolUse(&blocks.NestedToolUseBlock{
				ID: "n-1", Name: "bash", ParentToolCallID: "agent-1",
				Input: map[string]any{"command": `agrctl send --wait --agent reviewer "审一下 PR 42"`},
			}, "")
			_, reason := Match(acc.OuterToolUses(), nil, reviewer)
			So(reason, ShouldEqual, ReasonNoCandidate)
		})
	})
}

func TestBinding_Kind(t *testing.T) {
	Convey("Binding.Kind 决定 overlay 的 kind", t, func() {
		Convey("前台用 agrctl 专属 kind", func() {
			So(Binding{ToolCallID: "a"}.Kind(), ShouldEqual, agentruntime.SubagentKindAgrctl)
		})
		Convey("后台保留 local_bash,沿用后台面板/停止/轮结束保护", func() {
			So(Binding{ToolCallID: "a", Background: true}.Kind(), ShouldEqual, agentruntime.SubagentKindLocalBash)
		})
	})
}

func TestBoundToolCallIDs(t *testing.T) {
	Convey("BoundToolCallIDs 从块序列里找出已被 agrctl 绑定的 shell 调用", t, func() {
		snapshot := []cagoblocks.ContentBlock{
			&cagoblocks.ToolUseBlock{ID: "sh-1", Name: "Bash"},
			&blocks.SubagentStateBlock{ParentToolCallID: "sh-1", Origin: agentruntime.SubagentOriginAgrctl},
			blocks.SubagentStateBlock{ParentToolCallID: "sh-2", Origin: agentruntime.SubagentOriginAgrctl},
			&blocks.SubagentStateBlock{ParentToolCallID: "agent-1", Kind: "local_agent"},
		}
		So(BoundToolCallIDs(snapshot), ShouldResemble, map[string]bool{"sh-1": true, "sh-2": true})
		So(BoundToolCallIDs(nil), ShouldBeEmpty)
	})
}
