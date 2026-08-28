package claudecode

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRecordDecoder_APIErrorLine ~/.claude/projects/*.jsonl 里的 system/api_error 行:
// 顶层 error 是对象,而帧壳一度把它声明成 string —— 整行 unmarshal 失败被当坏行丢掉
// (实测 200 个真实文件里 267 个失败行全部由此而来)。
func TestRecordDecoder_APIErrorLine(t *testing.T) {
	const line = `{"parentUuid":"p1","isSidechain":false,"type":"system","subtype":"api_error","level":"error",` +
		`"error":{"message":"529 overloaded","status":529,"formatted":"529 overloaded_error","connection":null,` +
		`"isNetworkDown":false,"rateLimits":null},"retryInMs":601,"retryAttempt":1,"maxRetries":10,` +
		`"source":"request_retry","uuid":"u1"}`

	d := NewRecordDecoder()
	events, ok := d.Decode([]byte(line))
	require.True(t, ok, "整行不该再被判为坏行")
	require.Len(t, events, 1)
	require.Equal(t, EventRetry, events[0].Kind)
	require.NotNil(t, events[0].Retry)
	require.Equal(t, 1, events[0].Retry.Attempt)
	require.Equal(t, 10, events[0].Retry.MaxAttempts)
	require.Equal(t, float64(601), events[0].Retry.DelayMs)
	require.Equal(t, 529, events[0].Retry.ErrorStatus)
	// formatted 里已经带了状态码,前缀去掉才不会渲染成 "HTTP 529 529 overloaded_error"。
	require.Equal(t, "overloaded_error", events[0].Retry.ErrorCode)
}

// TestRecordDecoder_CamelToolUseResult 磁盘那份序列化器写的是驼峰 toolUseResult,
// stdout 上是 snake_case tool_use_result;两份拼法都要收,否则子代理关联字段整块丢失。
func TestRecordDecoder_CamelToolUseResult(t *testing.T) {
	const line = `{"type":"user","uuid":"u2","parentUuid":"a1",` +
		`"toolUseResult":{"agentId":"a1b2","agentType":"general-purpose"},` +
		`"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}`

	d := NewRecordDecoder()
	events, ok := d.Decode([]byte(line))
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, EventPostToolUse, events[0].Kind)
	require.NotNil(t, events[0].Tool)
	require.JSONEq(t, `{"agentId":"a1b2","agentType":"general-purpose"}`, string(events[0].Tool.ResultMeta))
}

// TestRecordDecoder_CamelCompactMetadata 同上:磁盘上是 compactMetadata,
// 只认 snake_case 会让压缩边界块的前后 token 数变成 0。
func TestRecordDecoder_CamelCompactMetadata(t *testing.T) {
	const line = `{"type":"system","subtype":"compact_boundary","uuid":"u3","parentUuid":"a1",` +
		`"compactMetadata":{"pre_tokens":120000,"post_tokens":8000,"trigger":"auto","duration_ms":3000}}`

	d := NewRecordDecoder()
	events, ok := d.Decode([]byte(line))
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, EventCompactBoundary, events[0].Kind)
	require.NotNil(t, events[0].Compact)
	require.Equal(t, 120000, events[0].Compact.PreTokens)
	require.Equal(t, 8000, events[0].Compact.PostTokens)
	require.Equal(t, "auto", events[0].Compact.Trigger)
}

// TestRecordDecoder_BadLine 单行坏数据只丢那一行,调用方据此计缺口。
func TestRecordDecoder_BadLine(t *testing.T) {
	d := NewRecordDecoder()
	_, ok := d.Decode([]byte(`{"type":"assistant","message":{`))
	require.False(t, ok)

	events, ok := d.Decode([]byte(`{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"hi"}]}}`))
	require.True(t, ok, "坏行不该毒化后续解码")
	require.Len(t, events, 1)
	require.Equal(t, EventTextDelta, events[0].Kind)
}
