package piagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 真 pi(--mode rpc,实测 2026-08-22)在 agent_end.messages 里把本轮每条 assistant
// 消息连同 usage 原样重发一遍,最后那条与刚才 message_end 的是同一条(responseId
// 相同)。下游 chat_svc 对 completion / reasoning 是 `+=` 累加(每次内部 API call
// 一条 usage),所以重发那条会把最后一跳的 output 记两遍 —— 单跳的轮直接翻倍,
// tok/s 与用量显示跟着一起虚高。
func TestStreamDoesNotDoubleCountAgentEndUsage(t *testing.T) {
	reader := newStreamingRPCReader()
	client, _ := newStreamingCaptureClient(reader)
	t.Cleanup(reader.Close)

	s, err := client.Stream(context.Background(), "run echo hi")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	const first = `{"role":"assistant","content":[{"type":"text","text":"working"}],"model":"glm-5.3","usage":{"input":1597,"output":32,"cacheRead":2496,"cacheWrite":0},"stopReason":"toolUse","responseId":"msg_a"}`
	const last = `{"role":"assistant","content":[{"type":"text","text":"done"}],"model":"glm-5.3","usage":{"input":3427,"output":17,"cacheRead":704,"cacheWrite":0},"stopReason":"stop","responseId":"msg_b"}`

	reader.Push(
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"message_end","message":`+first+`}`,
		`{"type":"message_end","message":`+last+`}`,
		`{"type":"agent_end","messages":[`+first+`,`+last+`],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
	)

	var usages []Event
	for _, ev := range collectUntilTerminal(t, s) {
		if ev.Kind == EventUsage {
			usages = append(usages, ev)
		}
	}
	require.Len(t, usages, 2, "每次内部 API call 一条 usage;agent_end 的重发不再多产一条")
	assert.Equal(t, 32, usages[0].Usage.CompletionTokens)
	assert.Equal(t, 17, usages[1].Usage.CompletionTokens)
}

// agent_end 仍然是 usage 的兜底:message_end 没带 usage 时(老 pi / 某些 provider),
// 这一轮的用量只能从 agent_end 的消息列表里捡回来。
func TestStreamStillTakesUsageOnlyCarriedByAgentEnd(t *testing.T) {
	reader := newStreamingRPCReader()
	client, _ := newStreamingCaptureClient(reader)
	t.Cleanup(reader.Close)

	s, err := client.Stream(context.Background(), "run echo hi")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	reader.Push(
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"model":"glm-5.3","stopReason":"stop","responseId":"msg_c"}}`,
		`{"type":"agent_end","messages":[{"role":"assistant","content":[{"type":"text","text":"done"}],"model":"glm-5.3","usage":{"input":100,"output":7,"cacheRead":0,"cacheWrite":0},"stopReason":"stop","responseId":"msg_c"}],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
	)

	var usages []Event
	for _, ev := range collectUntilTerminal(t, s) {
		if ev.Kind == EventUsage {
			usages = append(usages, ev)
		}
	}
	require.Len(t, usages, 1)
	assert.Equal(t, 7, usages[0].Usage.CompletionTokens)
}
