package claudecode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 真 CLI(2.1.x + --include-partial-messages)一次内部 API call 的帧序,实测自
// /tmp/cc_probe.jsonl:message_start → content_block_start → content_block_delta…
// → merged assistant 帧 → content_block_stop → message_delta(usage) → message_stop。
// 注意 merged assistant 帧**在** content_block_stop 之前到,且带整段正文全文。
func partialTurnFrames() []string {
	return []string{
		`{"type":"system","subtype":"init","session_id":"sx","model":"claude-opus-5"}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"message_start","message":{"type":"message","id":"m1","role":"assistant","content":[],"model":"claude-opus-5","usage":{"input_tokens":0,"output_tokens":0}}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"想"}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"一下"}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_stop","index":0}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"你"}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"好"}}}`,
		`{"type":"assistant","parent_tool_use_id":null,"message":{"type":"message","id":"m1","role":"assistant","model":"claude-opus-5","content":[{"type":"thinking","thinking":"想一下"},{"type":"text","text":"你好"},{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":0,"output_tokens":0}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"content_block_stop","index":1}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":1180,"output_tokens":61,"cache_read_input_tokens":34496}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":null,"event":{"type":"message_stop"}}`,
	}
}

type collected struct {
	text     []string
	thinking []string
	tools    []string
	usages   int
}

func collect(evs []Event, into *collected) {
	for _, ev := range evs {
		switch ev.Kind {
		case EventTextDelta:
			into.text = append(into.text, ev.Text)
		case EventThinkingDelta:
			into.thinking = append(into.thinking, ev.Text)
		case EventPreToolUse:
			into.tools = append(into.tools, ev.Tool.ID)
		case EventUsage:
			into.usages++
		}
	}
}

// TestSession_StreamsPartialTextDeltas —— stream_event 的 content_block_delta 必须
// 逐帧翻成 EventTextDelta / EventThinkingDelta(否则上层只能在整段生成完、merged
// assistant 帧到达那一刻才第一次看到文字:首 token 时延 ≈ 整轮时长,tok/s 分母塌成
// 几十毫秒 —— sess-3226 的 75331 tok/s 就是这么来的);merged assistant 帧随后不得
// 把同一段正文再吐一遍(否则转录里文字翻倍)。
func TestSession_StreamsPartialTextDeltas(t *testing.T) {
	s := &Session{}
	var got collected
	for _, line := range partialTurnFrames() {
		evs, _ := s.parseLine([]byte(line))
		collect(evs, &got)
	}

	assert.Equal(t, []string{"你", "好"}, got.text, "正文必须按 text_delta 逐帧流出,且 merged assistant 帧不重复")
	assert.Equal(t, []string{"想", "一下"}, got.thinking, "thinking 同理")
	assert.Equal(t, []string{"tu1"}, got.tools, "merged assistant 帧的 tool_use 仍要产出(input_json_delta 不流)")
}

func TestStreamDecoder_StreamsPartialTextDeltas(t *testing.T) {
	d := &frameDecoder{}
	var got collected
	for _, line := range partialTurnFrames() {
		evs, _ := d.decodeLine([]byte(line))
		collect(evs, &got)
	}

	assert.Equal(t, []string{"你", "好"}, got.text)
	assert.Equal(t, []string{"想", "一下"}, got.thinking)
	assert.Equal(t, []string{"tu1"}, got.tools)
}

// TestSession_FullTextStillEmittedWithoutPartials —— 没有 stream_event 增量的场景
// (老 CLI / 未开 --include-partial-messages / 单测 stub),merged assistant 帧仍然
// 是正文的唯一来源,必须照旧全量吐出。
func TestSession_FullTextStillEmittedWithoutPartials(t *testing.T) {
	s := &Session{}
	var got collected
	evs, _ := s.parseLine([]byte(`{"type":"assistant","parent_tool_use_id":null,"message":{"type":"message","id":"m9","role":"assistant","content":[{"type":"text","text":"整段"}]}}`))
	collect(evs, &got)
	assert.Equal(t, []string{"整段"}, got.text)
}

// TestSession_SubagentPartialsIgnored —— parent_tool_use_id 非空的 stream_event 是
// Task/Agent 子代理内部 API call。子代理增量不进主流(与 usage 同规矩),它的 merged
// assistant 帧照旧全量吐出,不能被主 agent 的去重误伤。
func TestSession_SubagentPartialsIgnored(t *testing.T) {
	s := &Session{}
	var got collected
	frames := []string{
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":"tu1","event":{"type":"message_start","message":{"type":"message","id":"m2","role":"assistant","content":[]}}}`,
		`{"type":"stream_event","session_id":"sx","parent_tool_use_id":"tu1","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"子"}}}`,
		`{"type":"assistant","parent_tool_use_id":"tu1","message":{"type":"message","id":"m2","role":"assistant","model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"子代理正文"}]}}`,
	}
	for _, line := range frames {
		evs, _ := s.parseLine([]byte(line))
		collect(evs, &got)
	}
	assert.Equal(t, []string{"子代理正文"}, got.text, "子代理增量不流,全文照旧从 merged 帧出")
}

// TestSession_PartialDedupeIsPerTurn —— 去重表按 turn 生命周期收口:result 帧后
// CLI 复用同一条 Session 跑下一轮,新一轮的 merged 帧不能被上一轮的记录误吞。
func TestSession_PartialDedupeIsPerTurn(t *testing.T) {
	s := &Session{}
	var got collected
	for _, line := range append(partialTurnFrames(), `{"type":"result","subtype":"success","session_id":"sx"}`) {
		evs, _ := s.parseLine([]byte(line))
		collect(evs, &got)
	}
	// 第二轮:CLI 复用了同一个 message id(极端情况),且这一轮没有增量帧。
	evs, _ := s.parseLine([]byte(`{"type":"assistant","parent_tool_use_id":null,"message":{"type":"message","id":"m1","role":"assistant","content":[{"type":"text","text":"下一轮"}]}}`))
	require.NotEmpty(t, evs)
	collect(evs, &got)
	assert.Equal(t, []string{"你", "好", "下一轮"}, got.text)
}
