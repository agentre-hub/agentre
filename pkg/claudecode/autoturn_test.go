package claudecode

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNormalizeTaskStatus 钉死 CLI 英式 "cancelled" 归一到内部美式 "canceled",
// 其它状态原样透传(用户停止后 CLI 发的 task_notification 走这条边界)。
func TestNormalizeTaskStatus(t *testing.T) {
	assert.Equal(t, "canceled", normalizeTaskStatus("cancelled"))
	assert.Equal(t, "canceled", normalizeTaskStatus("canceled"))
	assert.Equal(t, "completed", normalizeTaskStatus("completed"))
	assert.Equal(t, "failed", normalizeTaskStatus("failed"))
	assert.Equal(t, "", normalizeTaskStatus(""))
}

// TestNormalizeTaskStatus_StoppedAndKilled 是 sess-4198 的回归:CLI 用 "stopped"
// (会话结束时没跑完 / 被 TaskStop 停掉)和 "killed" 报告被中止的任务。它们原样透传进
// SubagentStateBlock 后,前端不认识这两个值,一律当 running,后台任务胶囊就一直显示
// 「运行中」。它们和 cancelled 是同一个终态,在同一条边界归一。
func TestNormalizeTaskStatus_StoppedAndKilled(t *testing.T) {
	assert.Equal(t, "canceled", normalizeTaskStatus("stopped"))
	assert.Equal(t, "canceled", normalizeTaskStatus("killed"))
}

// TestIsBackgroundTaskNotification 钉死后台型 task_notification 的辨析 —— 它是自主续轮
// 的起始标记(有 output_file、无 subagent_type)。真实 CLI 2.1.185 抓帧显示:后台 bash
// 与 run_in_background 子 agent 的「完成」通知都是后台型(都带 output_file、都不带
// subagent_type),都该起自主续轮;只有 subagent 内层的进度/非完成通知(无 output_file)
// 才返 false。
func TestIsBackgroundTaskNotification(t *testing.T) {
	parse := func(s string) rawFrame {
		var f rawFrame
		if err := json.Unmarshal([]byte(s), &f); err != nil {
			t.Fatalf("bad fixture: %v", err)
		}
		return f
	}

	cases := []struct {
		name string
		line string
		want bool
	}{
		{
			name: "后台型(有 output_file,无 subagent_type)",
			line: `{"type":"system","subtype":"task_notification","task_id":"bg1","tool_use_id":"tu1","status":"completed","output_file":"/tmp/tasks/bg1.output","summary":"Background command completed"}`,
			want: true,
		},
		{
			name: "subagent 内层非完成通知(有 subagent_type,无 output_file)",
			line: `{"type":"system","subtype":"task_notification","task_id":"t9","subagent_type":"general","description":"explore","status":"completed","summary":"Subagent finished"}`,
			want: false,
		},
		{
			// 真实 CLI 2.1.185:run_in_background 子 agent 完成的通知形态与后台 bash 一致
			// —— 带 output_file(子 agent 的 JSONL transcript)、不带 subagent_type → 后台型。
			name: "后台 subagent 完成(有 output_file,无 subagent_type)",
			line: `{"type":"system","subtype":"task_notification","task_id":"a827","tool_use_id":"toolu_agent","status":"completed","output_file":"/tmp/tasks/a827.output","summary":"Agent came to rest"}`,
			want: true,
		},
		{
			name: "task_started 非 notification",
			line: `{"type":"system","subtype":"task_started","task_id":"t9","subagent_type":"general"}`,
			want: false,
		},
		{
			name: "init 帧",
			line: `{"type":"system","subtype":"init","session_id":"s","model":"m"}`,
			want: false,
		},
		{
			name: "assistant 帧",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, isBackgroundTaskNotification(parse(c.line)))
		})
	}
}
