package claudecode

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func collectAll(t *testing.T, path string) ([]Event, string) {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- fixed test fixture path
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	d := newFrameDecoder(f)
	var events []Event
	for d.Next() {
		events = append(events, d.Event())
	}
	require.NoError(t, d.Err())
	return events, d.SessionID()
}

func TestStream_HappyEmitsTextDeltasAndDoneWithUsage(t *testing.T) {
	events, sid := collectAll(t, "testdata/stream_happy.jsonl")
	assert.Equal(t, "sess-abc", sid)

	require.GreaterOrEqual(t, len(events), 3)
	var deltas []Event
	for _, e := range events {
		if e.Kind == EventTextDelta {
			deltas = append(deltas, e)
		}
	}
	require.GreaterOrEqual(t, len(deltas), 2)
	assert.Equal(t, "Hello, ", deltas[0].Text)
	assert.Equal(t, "world.", deltas[1].Text)

	last := events[len(events)-1]
	assert.Equal(t, EventDone, last.Kind)
	assert.Equal(t, 10, last.Usage.PromptTokens)
	assert.Equal(t, 5, last.Usage.CompletionTokens)
	assert.Equal(t, 3, last.Usage.CachedTokens)
	assert.Equal(t, 1, last.Usage.CacheCreationTokens)
	// stream_happy.jsonl 的 system.init 帧带 model="claude-sonnet-4-6"，
	// 应该原样透传到 EventDone.Model，给上层用 catalog 查 context window。
	assert.Equal(t, "claude-sonnet-4-6", last.Model)
}

func TestStream_ToolUseAndToolResult(t *testing.T) {
	events, _ := collectAll(t, "testdata/stream_tool.jsonl")

	var pre, post *Event
	for i := range events {
		e := events[i]
		switch e.Kind {
		case EventPreToolUse:
			pre = &e
		case EventPostToolUse:
			post = &e
		}
	}
	require.NotNil(t, pre, "expected pre_tool_use")
	require.NotNil(t, post, "expected post_tool_use")

	require.NotNil(t, pre.Tool)
	assert.Equal(t, "tu-1", pre.Tool.ID)
	assert.Equal(t, "Read", pre.Tool.Name)
	assert.Contains(t, string(pre.Tool.Input), "/etc/hosts")

	require.NotNil(t, post.Tool)
	assert.Equal(t, "tu-1", post.Tool.ID)
	assert.Contains(t, string(post.Tool.Response), "127.0.0.1")
}

func TestStream_ThinkingDelta(t *testing.T) {
	events, _ := collectAll(t, "testdata/stream_thinking.jsonl")
	var got string
	for _, e := range events {
		if e.Kind == EventThinkingDelta {
			got = e.Text
		}
	}
	assert.Equal(t, "Let me consider...", got)
}

func TestStream_SubagentNestingAndTaskEvents(t *testing.T) {
	events, sid := collectAll(t, "testdata/stream_subagent.jsonl")
	assert.Equal(t, "sess-sa", sid)

	// 收齐三类 task_* 系统事件。
	var (
		started      *Event
		progress     *Event
		notification *Event
		preToolUses  []*Event
		postToolUses []*Event
	)
	for i := range events {
		e := events[i]
		switch e.Kind {
		case EventTaskStarted:
			started = &e
		case EventTaskProgress:
			progress = &e
		case EventTaskNotification:
			notification = &e
		case EventPreToolUse:
			preToolUses = append(preToolUses, &e)
		case EventPostToolUse:
			postToolUses = append(postToolUses, &e)
		}
	}

	require.NotNil(t, started, "expected task_started")
	require.NotNil(t, started.Tool)
	require.NotNil(t, started.Tool.Subagent)
	assert.Equal(t, "toolu-parent", started.Tool.ID)
	assert.Equal(t, "general-purpose", started.Tool.Subagent.SubagentType)
	assert.Equal(t, "probe", started.Tool.Subagent.TaskDescription)
	assert.Contains(t, started.Tool.Subagent.Prompt, "Run echo hello")

	require.NotNil(t, progress, "expected task_progress")
	require.NotNil(t, progress.Tool.Subagent)
	assert.Equal(t, "Bash", progress.Tool.Subagent.LastToolName)
	assert.Equal(t, 1, progress.Tool.Subagent.ToolUses)
	assert.Equal(t, 14096, progress.Tool.Subagent.TotalTokens)
	assert.Equal(t, 4254, progress.Tool.Subagent.DurationMs)

	require.NotNil(t, notification, "expected task_notification")
	assert.Equal(t, "completed", notification.Tool.Subagent.Status)
	assert.Equal(t, 7834, notification.Tool.Subagent.DurationMs)

	// 外层 Agent 的 PreToolUse 不带 parent；子 Bash 的 PreToolUse 带 parent==toolu-parent。
	require.GreaterOrEqual(t, len(preToolUses), 2)
	var outerAgent, innerBash *Event
	for _, p := range preToolUses {
		if p.Tool != nil && p.Tool.Name == "Agent" {
			outerAgent = p
		}
		if p.Tool != nil && p.Tool.Name == "Bash" {
			innerBash = p
		}
	}
	require.NotNil(t, outerAgent, "expected outer Agent pre tool use")
	require.NotNil(t, innerBash, "expected inner Bash pre tool use")
	assert.Equal(t, "", outerAgent.ParentToolUseID, "outer Agent tool_use should have empty parent")
	assert.Equal(t, "toolu-parent", innerBash.ParentToolUseID, "inner Bash should reference outer Agent")

	// 子 Bash 的 tool_result 也带 parent；外层 Agent 的 tool_result 不带。
	require.GreaterOrEqual(t, len(postToolUses), 2)
	var innerResult, outerResult *Event
	for _, p := range postToolUses {
		if p.Tool != nil && p.Tool.ID == "toolu-child" {
			innerResult = p
		}
		if p.Tool != nil && p.Tool.ID == "toolu-parent" {
			outerResult = p
		}
	}
	require.NotNil(t, innerResult, "expected inner Bash tool_result")
	require.NotNil(t, outerResult, "expected outer Agent tool_result")
	assert.Equal(t, "toolu-parent", innerResult.ParentToolUseID)
	assert.Equal(t, "", outerResult.ParentToolUseID)
	assert.Contains(t, outerResult.Tool.Response, "Raw output")
}

// TestStream_SubagentModelEvent 验证 R2：parent_tool_use_id 非空 + message.model 非空
// 的 subagent 内部帧产出 EventSubagentModel；主 agent 自己的帧（m1，parent 为空）即便
// 带 message.model（"claude-opus-5"）也绝不产出该事件——隔离不变量（R5）不能因为新事件
// 类型回退。fixture 里的 m2 帧对应真 CLI 2.1.220 抓帧第 12 帧的形状。
func TestStream_SubagentModelEvent(t *testing.T) {
	events, _ := collectAll(t, "testdata/stream_subagent.jsonl")

	var modelEvents []Event
	for _, e := range events {
		if e.Kind == EventSubagentModel {
			modelEvents = append(modelEvents, e)
		}
	}
	require.Len(t, modelEvents, 1, "expected exactly one EventSubagentModel from the subagent-internal frame")
	ev := modelEvents[0]
	assert.Equal(t, "toolu-parent", ev.ParentToolUseID)
	assert.Equal(t, "claude-haiku-4-5-20251001", ev.Model)
	assert.Equal(t, "sess-sa", ev.SessionID)
}

// TestStream_MainAgentFrameModelDoesNotEmitSubagentModel 验证 R5 在 frameDecoder 路径上
// 独立成立：主 agent 帧（parent_tool_use_id 为空）即便带 message.model，也绝不能产出
// EventSubagentModel。必须扫描未经 Kind 过滤的原始 events——TestStream_SubagentModelEvent
// 曾经在过滤后的 modelEvents 上做同样的断言，那个检查只在已知只有一个
// EventSubagentModel 元素时才会执行，一旦主 agent 帧真的泄漏出第二个 EventSubagentModel，
// 上面的 require.Len 会先失败并终止测试，那条断言永远执行不到，形同虚设。
// 镜像 session_test.go 的 TestSession_MainAgentFrameModelDoesNotEmitSubagentModel（那条
// 测试本来就直接扫未过滤的 events，session.parseLine 路径没有这个问题）。
func TestStream_MainAgentFrameModelDoesNotEmitSubagentModel(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s"}` + "\n" +
		`{"type":"assistant","message":{"id":"m1","model":"claude-opus-5","content":[{"type":"tool_use","id":"toolu-parent","name":"Agent","input":{"description":"probe"}}]}}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"s"}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var events []Event
	for d.Next() {
		events = append(events, d.Event())
	}
	require.NoError(t, d.Err())
	require.NotEmpty(t, events)

	for _, e := range events {
		assert.NotEqual(t, EventSubagentModel, e.Kind, "main agent frame (parent empty) must never produce EventSubagentModel")
	}
}

// TestStream_SubagentModelEvent_MissingModelNoEvent 验证老 CLI 兼容路径：subagent 内部
// 帧没有 message.model 字段（老 CLI 不发）时不产出 EventSubagentModel，不报错、不阻断。
func TestStream_SubagentModelEvent_MissingModelNoEvent(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s"}` + "\n" +
		`{"type":"assistant","parent_tool_use_id":"toolu-old","message":{"id":"m1","content":[{"type":"text","text":"legacy subagent frame"}]}}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"s"}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var events []Event
	for d.Next() {
		events = append(events, d.Event())
	}
	require.NoError(t, d.Err())

	for _, e := range events {
		assert.NotEqual(t, EventSubagentModel, e.Kind, "old frame without message.model must not produce EventSubagentModel")
	}
}

// TestStream_SubagentModelEvent_APIErrorFlagGovernsRegardlessOfSentinelString 镜像
// session_test.go 的同名测试(TestSession_SubagentModelEvent_APIErrorFlagGovernsRegardlessOfSentinelString)，
// 验证 frameDecoder.decodeLine 这条路径同样以 isApiErrorMessage 权威标志判定，而不是嗅探
// message.model 是否等于当前已知占位符 "<synthetic>"。isApiErrorMessage:true 且首个 text
// 块与顶层 error 都为空时 apiErrorEvent 放行，帧落进 parseAssistantContentWithUsage；换成
// 一个不同于 "<synthetic>" 的占位符值，若判定仍靠字符串嗅探，这个陌生值会被当成 subagent
// 的实际模型产出 EventSubagentModel。
func TestStream_SubagentModelEvent_APIErrorFlagGovernsRegardlessOfSentinelString(t *testing.T) {
	line := `{"type":"assistant","message":{"id":"m1","model":"<future-placeholder>","content":[]},"parent_tool_use_id":"toolu-parent","isApiErrorMessage":true,"session_id":"sess-xyz"}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var events []Event
	for d.Next() {
		events = append(events, d.Event())
	}
	require.NoError(t, d.Err())

	for _, e := range events {
		assert.NotEqual(t, EventSubagentModel, e.Kind, "isApiErrorMessage:true frame must never surface a model, regardless of the placeholder string value")
	}
}

// TestStream_ApiRetry 验证 system.api_retry 帧被抬成 EventRetry：CLI 在 turn 内非终态重试
// 期间会连发多条，每条都应当独立 emit 出来，字段（attempt/max_retries/retry_delay_ms/
// error_status/error）逐一透传，最后接 result.success 仍能正常拿到 EventDone。
func TestStream_ApiRetry(t *testing.T) {
	events, sid := collectAll(t, "testdata/stream_retry.jsonl")
	assert.Equal(t, "sess-retry", sid)

	var retries []Event
	var sawText, sawDone bool
	for _, e := range events {
		switch e.Kind {
		case EventRetry:
			retries = append(retries, e)
		case EventTextDelta:
			sawText = true
		case EventDone:
			sawDone = true
		}
	}
	require.Len(t, retries, 2, "expected 2 EventRetry events")

	r1 := retries[0]
	require.NotNil(t, r1.Retry)
	assert.Equal(t, 1, r1.Retry.Attempt)
	assert.Equal(t, 10, r1.Retry.MaxAttempts)
	assert.InDelta(t, 585.8761681691873, r1.Retry.DelayMs, 0.0001)
	assert.Equal(t, 529, r1.Retry.ErrorStatus)
	assert.Equal(t, "rate_limit", r1.Retry.ErrorCode)
	assert.Equal(t, "sess-retry", r1.SessionID)

	r2 := retries[1]
	require.NotNil(t, r2.Retry)
	assert.Equal(t, 2, r2.Retry.Attempt)
	assert.InDelta(t, 1229.3, r2.Retry.DelayMs, 0.0001)

	assert.True(t, sawText, "retry 之后的 text delta 仍应当到达")
	assert.True(t, sawDone, "retry 之后的 result 仍应当 emit EventDone")
}

// TestStream_ApiRetry_MinimalFields 验证只有 attempt/max_retries 时也能 emit，
// retry_delay_ms / error_status / error 缺省零值不报错。
func TestStream_ApiRetry_MinimalFields(t *testing.T) {
	d := newFrameDecoder(strings.NewReader(
		`{"type":"system","subtype":"init","session_id":"x"}` + "\n" +
			`{"type":"system","subtype":"api_retry","attempt":3,"max_retries":5}` + "\n" +
			`{"type":"result","subtype":"success","session_id":"x"}` + "\n",
	))
	var got *Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventRetry {
			got = &e
			break
		}
	}
	require.NoError(t, d.Err())
	require.NotNil(t, got, "minimal api_retry 帧仍应 emit EventRetry")
	require.NotNil(t, got.Retry)
	assert.Equal(t, 3, got.Retry.Attempt)
	assert.Equal(t, 5, got.Retry.MaxAttempts)
	assert.Zero(t, got.Retry.DelayMs)
	assert.Zero(t, got.Retry.ErrorStatus)
	assert.Empty(t, got.Retry.ErrorCode)
}

// TestStream_DoneUsesLastAssistantUsage_NotCumulativeResult 复现 bug：
// Claude Code CLI 的 result.usage 是一轮里**所有**内部 API call 用量的累加（含
// tool-use 循环里每一次 model 调用），不是"当前上下文占用"。真正反映"模型这一刻
// 看到的输入大小"的是最后一帧 assistant.message.usage（也就是最后一次 API call
// 的 input + cache_read + cache_creation）。
//
// 当前实现只采 result.usage → 前端 promptTokens + cachedTokens + cacheCreationTokens
// 进度条会随工具循环不断膨胀，工具调用多的 turn 必然超过 100%。
//
// 断言：EventDone.Usage 取**最后一帧 assistant 的 per-call usage**，不是 result 帧
// 上的累加值。
func TestStream_DoneUsesLastAssistantUsage_NotCumulativeResult(t *testing.T) {
	// 模拟一个 turn 里两次内部 API call：
	//   call#1 (tool_use)：input=200, cache_read=10000, cache_creation=0,  output=50
	//   call#2 (text)：    input=50,  cache_read=10300, cache_creation=50, output=20
	// result.usage = 累加：input=250, cache_read=20300, cache_creation=50, output=70
	// 当前上下文真正占用 = call#2 = 50 + 10300 + 50 = 10400（不是 20650）
	line := `{"type":"system","subtype":"init","session_id":"s","model":"claude-sonnet-4-6"}` + "\n" +
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"X","input":{}}],"usage":{"input_tokens":200,"output_tokens":50,"cache_read_input_tokens":10000,"cache_creation_input_tokens":0}}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n" +
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":10300,"cache_creation_input_tokens":50}}}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"s","usage":{"input_tokens":250,"output_tokens":70,"cache_read_input_tokens":20300,"cache_creation_input_tokens":50}}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var done *Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventDone {
			done = &e
		}
	}
	require.NoError(t, d.Err())
	require.NotNil(t, done, "expected EventDone")

	// 必须等于**最后一帧 assistant 的 per-call usage**，不是 result 帧上的累加值。
	assert.Equal(t, 50, done.Usage.PromptTokens, "PromptTokens 应取最后一次 API call 的 input_tokens")
	assert.Equal(t, 10300, done.Usage.CachedTokens, "CachedTokens 应取最后一次 API call 的 cache_read_input_tokens")
	assert.Equal(t, 50, done.Usage.CacheCreationTokens, "CacheCreationTokens 应取最后一次 API call 的 cache_creation_input_tokens")
	assert.Equal(t, 20, done.Usage.CompletionTokens, "CompletionTokens 应取最后一次 API call 的 output_tokens")
}

// TestStream_DoneIgnoresSubagentAssistantUsage 复现 subagent 串味 bug：
// subagent 内部 API call 的 assistant 帧（parent_tool_use_id != ""）是另一个
// 独立 Anthropic 会话——自己的 system prompt + 自己的 context window，通常比
// 主 agent 小一大截。把 subagent 的最后一帧 usage 当成"主 agent 当前上下文
// 占用"会让进度条骤降到一个小值，明显错。
//
// 关键时序：最后一帧 assistant 是 **subagent inner**（参考 stream_subagent.jsonl
// 这种 tool_use 即终止的形态，或主 agent 还没来得及 wrap-up 就被中断）。这样
// 才能区分"取最后一帧 assistant"（错）vs"取最后一帧 **主** assistant"（对）。
func TestStream_DoneIgnoresSubagentAssistantUsage(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s","model":"m"}` + "\n" +
		// 主 agent dispatch Agent 工具：cache_read=15000 ← 应该取这条
		`{"type":"assistant","message":{"id":"main1","content":[{"type":"tool_use","id":"toolu-A","name":"Agent","input":{}}],"usage":{"input_tokens":80,"output_tokens":30,"cache_read_input_tokens":15000,"cache_creation_input_tokens":0}}}` + "\n" +
		// subagent 内部两帧：小 context（2k / 3k）—— 必须忽略
		`{"type":"assistant","parent_tool_use_id":"toolu-A","message":{"id":"sub1","content":[{"type":"text","text":"sub work"}],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":2000,"cache_creation_input_tokens":0}}}` + "\n" +
		`{"type":"assistant","parent_tool_use_id":"toolu-A","message":{"id":"sub2","content":[{"type":"text","text":"sub done"}],"usage":{"input_tokens":80,"output_tokens":40,"cache_read_input_tokens":3000,"cache_creation_input_tokens":0}}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu-A","content":"summary"}]}}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"s","usage":{"input_tokens":260,"output_tokens":120,"cache_read_input_tokens":20000,"cache_creation_input_tokens":0}}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var done *Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventDone {
			done = &e
		}
	}
	require.NoError(t, d.Err())
	require.NotNil(t, done)

	assert.Equal(t, 80, done.Usage.PromptTokens, "应取 main1 的 input=80，不是 sub2 的 80（数值相同凑巧）或累加 260")
	assert.Equal(t, 15000, done.Usage.CachedTokens, "应取 main1 的 cache_read=15000，不是 sub2 的 3000 或累加 20000")
	assert.Equal(t, 30, done.Usage.CompletionTokens, "应取 main1 的 output=30，不是 sub2 的 40 或累加 120")
}

// TestStream_DoneFallsBackToResultUsage_WhenAssistantHasNoUsage 兜底：
// 若 assistant 帧整轮都没带 message.usage（理论上的老 CLI / 极简 stub），
// EventDone.Usage 仍应落到 result.usage，避免显示 0。
func TestStream_DoneFallsBackToResultUsage_WhenAssistantHasNoUsage(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s"}` + "\n" +
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"hi"}]}}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"s","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":1}}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var done *Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventDone {
			done = &e
		}
	}
	require.NoError(t, d.Err())
	require.NotNil(t, done)

	assert.Equal(t, 10, done.Usage.PromptTokens)
	assert.Equal(t, 5, done.Usage.CompletionTokens)
	assert.Equal(t, 3, done.Usage.CachedTokens)
	assert.Equal(t, 1, done.Usage.CacheCreationTokens)
}

// TestStream_EmitsUsageOnEveryMainAgentAssistantFrame —— 每个**主 agent** assistant
// 帧上都附 EventUsage，让上层（chat_svc → 前端 Composer 进度条）可以在 turn 内随
// 工具循环实时刷新「已用上下文」，不必等 EventDone 一次性跳一大截。
//
// 断言：两个主 agent assistant 帧 → 两条 EventUsage，每条都对应该帧的 per-call usage。
// EventDone 仍然按原行为吐最后一帧 usage（兜底/落库口径不变）。
func TestStream_EmitsUsageOnEveryMainAgentAssistantFrame(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s","model":"claude-sonnet-4-6"}` + "\n" +
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"X","input":{}}],"usage":{"input_tokens":200,"output_tokens":50,"cache_read_input_tokens":10000,"cache_creation_input_tokens":0}}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n" +
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":10300,"cache_creation_input_tokens":50}}}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"s","usage":{"input_tokens":250,"output_tokens":70,"cache_read_input_tokens":20300,"cache_creation_input_tokens":50}}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var usages []Event
	var done *Event
	for d.Next() {
		e := d.Event()
		switch e.Kind {
		case EventUsage:
			usages = append(usages, e)
		case EventDone:
			ev := e
			done = &ev
		}
	}
	require.NoError(t, d.Err())

	require.Len(t, usages, 2, "每个主 agent assistant 帧都应当 emit 一条 EventUsage")
	assert.Equal(t, 200, usages[0].Usage.PromptTokens)
	assert.Equal(t, 10000, usages[0].Usage.CachedTokens)
	assert.Equal(t, 0, usages[0].Usage.CacheCreationTokens)
	assert.Equal(t, 50, usages[0].Usage.CompletionTokens)
	assert.Equal(t, "s", usages[0].SessionID)

	assert.Equal(t, 50, usages[1].Usage.PromptTokens)
	assert.Equal(t, 10300, usages[1].Usage.CachedTokens)
	assert.Equal(t, 50, usages[1].Usage.CacheCreationTokens)
	assert.Equal(t, 20, usages[1].Usage.CompletionTokens)

	require.NotNil(t, done)
	// EventDone 仍取最后一帧 usage —— 落库 / 兜底口径不动。
	assert.Equal(t, 50, done.Usage.PromptTokens)
}

// TestStream_DoesNotEmitUsageForSubagentFrames —— subagent 内部 API call 的 usage
// 不能向上吐：它代表另一个独立 Anthropic 会话的输入量，混进主 agent 进度条会让
// 用户看到「进度突然倒退」。
func TestStream_DoesNotEmitUsageForSubagentFrames(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s","model":"m"}` + "\n" +
		`{"type":"assistant","message":{"id":"main1","content":[{"type":"tool_use","id":"toolu-A","name":"Agent","input":{}}],"usage":{"input_tokens":80,"output_tokens":30,"cache_read_input_tokens":15000,"cache_creation_input_tokens":0}}}` + "\n" +
		`{"type":"assistant","parent_tool_use_id":"toolu-A","message":{"id":"sub1","content":[{"type":"text","text":"sub work"}],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":2000,"cache_creation_input_tokens":0}}}` + "\n" +
		`{"type":"assistant","parent_tool_use_id":"toolu-A","message":{"id":"sub2","content":[{"type":"text","text":"sub done"}],"usage":{"input_tokens":80,"output_tokens":40,"cache_read_input_tokens":3000,"cache_creation_input_tokens":0}}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu-A","content":"summary"}]}}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"s","usage":{"input_tokens":260,"output_tokens":120,"cache_read_input_tokens":20000,"cache_creation_input_tokens":0}}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var usages []Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventUsage {
			usages = append(usages, e)
		}
	}
	require.NoError(t, d.Err())

	require.Len(t, usages, 1, "只有主 agent 帧才发 EventUsage；subagent 内部帧必须忽略")
	assert.Equal(t, 80, usages[0].Usage.PromptTokens)
	assert.Equal(t, 15000, usages[0].Usage.CachedTokens)
}

// TestStream_DoesNotEmitUsageWhenAssistantFrameHasNoUsage —— 老 CLI 不在 assistant
// 帧上挂 usage：此时不发 EventUsage（避免吐零值欺骗前端），整轮回到旧的「只在
// EventDone 兜 result.usage」路径。
func TestStream_DoesNotEmitUsageWhenAssistantFrameHasNoUsage(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s"}` + "\n" +
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"hi"}]}}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"s","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":1}}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var sawUsage bool
	for d.Next() {
		if d.Event().Kind == EventUsage {
			sawUsage = true
		}
	}
	require.NoError(t, d.Err())
	assert.False(t, sawUsage, "assistant 帧没带 usage 字段时不应当 emit EventUsage")
}

// TestStream_EmitsEventInitOnSystemInitWithModel —— system.init 帧带 model 时
// 应 emit 一条 EventInit,把 SessionID + Model 透出来。语义:turn 开始时 CLI 已经
// 告诉我们用的是哪个模型;上层(agentruntime/claudecode translator)拿这个 model
// 名查 cago llmcatalog 兜底 context window 大小,不必等 EventDone 才知道窗口,
// 避免前端"等一轮跑完上下文用量条才出现"。
func TestStream_EmitsEventInitOnSystemInitWithModel(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"sx","model":"claude-sonnet-4-6"}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"sx","usage":{"input_tokens":1,"output_tokens":1}}` + "\n"
	d := newFrameDecoder(strings.NewReader(line))
	var init *Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventInit {
			ev := e
			init = &ev
		}
	}
	require.NoError(t, d.Err())
	require.NotNil(t, init, "system.init 帧带 model 时应 emit EventInit")
	assert.Equal(t, "sx", init.SessionID)
	assert.Equal(t, "claude-sonnet-4-6", init.Model)
}

// TestStream_DoesNotEmitEventInitWhenModelMissing —— 老 CLI 不在 system.init 上
// 报 model 字段时(或字段空)不发 EventInit:下游靠 model 名查 catalog,空 model 没
// 意义,emit 反而引导上层做无效查询。前向兼容靠"没 init 事件 → 走 EventDone.Model
// 兜底"旧路径。
func TestStream_DoesNotEmitEventInitWhenModelMissing(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"sx"}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"sx"}` + "\n"
	d := newFrameDecoder(strings.NewReader(line))
	var sawInit bool
	for d.Next() {
		if d.Event().Kind == EventInit {
			sawInit = true
		}
	}
	require.NoError(t, d.Err())
	assert.False(t, sawInit, "model 缺省时不应当 emit EventInit")
}

func TestStream_UnknownSystemSubtypeFailsSchemaCheck(t *testing.T) {
	d := newFrameDecoder(strings.NewReader(
		`{"type":"system","subtype":"init","session_id":"x","schema_version":"future-9.9"}` + "\n",
	))
	// Schema-version 真正断言留到未来；本测试仅确认未知字段不会让 decoder panic。
	for d.Next() {
	}
	assert.NoError(t, d.Err())
}

// TestStream_PostToolUseCarriesToolUseResultMeta —— Claude Code CLI 在 user 帧的顶层
// (跟 message 同级,不是嵌在 message.content 里) 吐 tool_use_result 元数据:
//   - TaskCreate 的 result_meta 结构化返回系统分配的 task id (CLI 不在 tool input 里
//     回 id,只在这份 meta 里);前端历史回放靠它把 TaskCreate ↔ TaskUpdate 关联起来。
//   - TaskUpdate 的 result_meta 给 statusChange/updatedFields,前端可直接渲染。
//
// 断言:EventPostToolUse.Tool.ResultMeta 把顶层 tool_use_result 字段原样透传成
// json.RawMessage,允许上层按工具语义自行 Unmarshal。
func TestStream_PostToolUseCarriesToolUseResultMeta(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s"}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_A","type":"tool_result","content":"Task #1 created successfully: probe"}]},"tool_use_result":{"task":{"id":"1","subject":"probe"}}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_B","type":"tool_result","content":"Updated task #1 status"}]},"tool_use_result":{"success":true,"taskId":"1","updatedFields":["status"],"statusChange":{"from":"pending","to":"in_progress"}}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_C","type":"tool_result","content":"ok"}]}}` + "\n" +
		`{"type":"result","subtype":"success","session_id":"s"}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var posts []Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventPostToolUse {
			posts = append(posts, e)
		}
	}
	require.NoError(t, d.Err())
	require.Len(t, posts, 3)

	require.NotNil(t, posts[0].Tool)
	assert.Equal(t, "toolu_A", posts[0].Tool.ID)
	assert.JSONEq(t, `{"task":{"id":"1","subject":"probe"}}`, string(posts[0].Tool.ResultMeta))

	require.NotNil(t, posts[1].Tool)
	assert.Equal(t, "toolu_B", posts[1].Tool.ID)
	assert.JSONEq(t,
		`{"success":true,"taskId":"1","updatedFields":["status"],"statusChange":{"from":"pending","to":"in_progress"}}`,
		string(posts[1].Tool.ResultMeta))

	// 帧上没有 tool_use_result 字段时,ResultMeta 留空 (nil),不应该误填空 JSON。
	require.NotNil(t, posts[2].Tool)
	assert.Equal(t, "toolu_C", posts[2].Tool.ID)
	assert.Nil(t, posts[2].Tool.ResultMeta)
}

// TestStream_CompactBoundary 验证 system.compact_boundary 帧被抬成
// EventCompactBoundary,并把 compact_metadata.{pre_tokens,trigger} 解到 CompactEvent。
func TestStream_CompactBoundary(t *testing.T) {
	d := newFrameDecoder(strings.NewReader(
		`{"type":"system","subtype":"init","session_id":"sess-compact"}` + "\n" +
			`{"type":"system","subtype":"compact_boundary","compact_metadata":{"pre_tokens":12345,"trigger":"auto"}}` + "\n" +
			`{"type":"result","subtype":"success","session_id":"sess-compact"}` + "\n",
	))
	var got *Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventCompactBoundary {
			got = &e
			break
		}
	}
	require.NoError(t, d.Err())
	require.NotNil(t, got, "compact_boundary 帧应 emit EventCompactBoundary")
	assert.Equal(t, "sess-compact", got.SessionID)
	require.NotNil(t, got.Compact)
	assert.Equal(t, 12345, got.Compact.PreTokens)
	assert.Equal(t, "auto", got.Compact.Trigger)
}

// TestStream_CompactBoundary_MissingMetadata 验证 compact_metadata 缺省时仍 emit
// EventCompactBoundary,CompactEvent 字段为零值,不阻断主流程。
func TestStream_CompactBoundary_MissingMetadata(t *testing.T) {
	d := newFrameDecoder(strings.NewReader(
		`{"type":"system","subtype":"init","session_id":"x"}` + "\n" +
			`{"type":"system","subtype":"compact_boundary"}` + "\n" +
			`{"type":"result","subtype":"success","session_id":"x"}` + "\n",
	))
	var got *Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventCompactBoundary {
			got = &e
			break
		}
	}
	require.NoError(t, d.Err())
	require.NotNil(t, got, "缺 metadata 的 compact_boundary 帧仍应 emit")
	require.NotNil(t, got.Compact)
	assert.Zero(t, got.Compact.PreTokens)
	assert.Empty(t, got.Compact.Trigger)
	assert.Zero(t, got.Compact.PostTokens)
	assert.Zero(t, got.Compact.DurationMs)
}

// TestStream_CompactBoundary_FullMetadata 验证真实 CLI 的 compact_metadata
// 全字段 (pre_tokens / post_tokens / trigger / duration_ms) 都被解到 CompactEvent。
// 字段对照见 docs/architecture.md。
func TestStream_CompactBoundary_FullMetadata(t *testing.T) {
	d := newFrameDecoder(strings.NewReader(
		`{"type":"system","subtype":"init","session_id":"sess-cf"}` + "\n" +
			`{"type":"system","subtype":"compact_boundary","compact_metadata":{"pre_tokens":30117,"post_tokens":2697,"trigger":"manual","duration_ms":20696}}` + "\n" +
			`{"type":"result","subtype":"success","session_id":"sess-cf"}` + "\n",
	))
	var got *Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventCompactBoundary {
			got = &e
			break
		}
	}
	require.NoError(t, d.Err())
	require.NotNil(t, got)
	require.NotNil(t, got.Compact)
	assert.Equal(t, 30117, got.Compact.PreTokens)
	assert.Equal(t, 2697, got.Compact.PostTokens)
	assert.Equal(t, "manual", got.Compact.Trigger)
	assert.Equal(t, 20696, got.Compact.DurationMs)
}

// TestStream_StatusCompacting 验证 CLI 的 system{subtype:"status",status:"compacting"}
// 帧 (manual /compact 或 auto 阈值开始时推) 被抬成 EventStatus,Status 字段透传。
// 这是 dedicated 进度信号 —— 与 compact_boundary 结束帧成对出现:开始时一帧 status:"compacting",
// 结束前 CLI 还会再推一帧 status:null (我们故意不抬,见 TestStream_StatusCleared_NoEvent)。
func TestStream_StatusCompacting(t *testing.T) {
	d := newFrameDecoder(strings.NewReader(
		`{"type":"system","subtype":"init","session_id":"sess-st"}` + "\n" +
			`{"type":"system","subtype":"status","status":"compacting","session_id":"sess-st"}` + "\n" +
			`{"type":"result","subtype":"success","session_id":"sess-st"}` + "\n",
	))
	var got *Event
	for d.Next() {
		e := d.Event()
		if e.Kind == EventStatus {
			got = &e
			break
		}
	}
	require.NoError(t, d.Err())
	require.NotNil(t, got, "status:compacting 帧应 emit EventStatus")
	assert.Equal(t, "sess-st", got.SessionID)
	assert.Equal(t, "compacting", got.Status)
}

// TestStream_StatusCleared_NoEvent 边界:status:null + 无 permissionMode 的 status 帧
// (compact 结束 / 未知 subtype) 静默忽略,不发伪事件 —— 保持 status frame 的前向兼容
// 语义。compact 结束的信号通过 compact_boundary / done 事件传递,上层据此清 compacting 旗。
func TestStream_StatusCleared_NoEvent(t *testing.T) {
	d := newFrameDecoder(strings.NewReader(
		`{"type":"system","subtype":"init","session_id":"sess-clr"}` + "\n" +
			`{"type":"system","subtype":"status","status":null,"session_id":"sess-clr"}` + "\n" +
			`{"type":"result","subtype":"success","session_id":"sess-clr"}` + "\n",
	))
	var sawStatus, sawDone bool
	for d.Next() {
		e := d.Event()
		if e.Kind == EventStatus {
			sawStatus = true
		}
		if e.Kind == EventDone {
			sawDone = true
		}
	}
	require.NoError(t, d.Err())
	assert.False(t, sawStatus, "status:null 不应 emit EventStatus")
	assert.True(t, sawDone, "result 帧仍应触发 EventDone")
}

// TestStream_StatusAndPermissionMode_BothEmitted 边界:同一帧上 status 和 permissionMode
// 都非空时,两个事件互相独立 emit —— 不要因为其中一个非空就漏掉另一个。
// 现实里 CLI 不会一次给两个,这是契约层面的健壮性兜底。
func TestStream_StatusAndPermissionMode_BothEmitted(t *testing.T) {
	d := newFrameDecoder(strings.NewReader(
		`{"type":"system","subtype":"init","session_id":"sess-both"}` + "\n" +
			`{"type":"system","subtype":"status","status":"compacting","permissionMode":"plan","session_id":"sess-both"}` + "\n" +
			`{"type":"result","subtype":"success","session_id":"sess-both"}` + "\n",
	))
	var sawStatus, sawMode bool
	for d.Next() {
		e := d.Event()
		switch e.Kind {
		case EventStatus:
			sawStatus = true
			assert.Equal(t, "compacting", e.Status)
		case EventPermissionModeChanged:
			sawMode = true
			assert.Equal(t, "plan", e.PermissionMode)
		}
	}
	require.NoError(t, d.Err())
	assert.True(t, sawStatus, "status 字段非空应 emit EventStatus")
	assert.True(t, sawMode, "permissionMode 字段非空仍应 emit EventPermissionModeChanged")
}

// TestStream_APIErrorMessageFrameBecomesEventError 与 session.parseLine 那条平行:
// 一次性 Client.Stream 走的 frameDecoder.decodeLine 也要把 isApiErrorMessage 合成帧
// 翻成 EventError,不泄漏成正文文本增量。两个 decoder 是刻意维护的平行副本。
func TestStream_APIErrorMessageFrameBecomesEventError(t *testing.T) {
	const line = `{"type":"assistant","message":{"id":"m1","model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"API Error: Connection closed mid-response. The response above may be incomplete."}]},"error":"server_error","isApiErrorMessage":true,"session_id":"sess-xyz"}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var events []Event
	for d.Next() {
		events = append(events, d.Event())
	}
	require.NoError(t, d.Err())

	require.Len(t, events, 1)
	assert.Equal(t, EventError, events[0].Kind)
	assert.ErrorIs(t, events[0].Err, ErrAPIError)
	assert.Contains(t, events[0].Err.Error(), "Connection closed mid-response")
	for _, e := range events {
		assert.NotEqual(t, EventTextDelta, e.Kind)
	}
}

// TestStream_ApiErrorMessage_SnakeCaseFlag 钉住 CLI 实测帧形。
//
// 现场（agentred 0.1.0/82ae872 + Claude Code 2.1.224，上游网关 403）抓到的 stdout
// 里，合成 API 错误帧顶层打的是 **snake_case** 的 `is_api_error_message`，不是
// rawFrame 上写着的 `isApiErrorMessage`——后者只在 CLI 自己的 `~/.claude/projects/
// *.jsonl` 里还是驼峰（两份序列化器，各走各的）。标志解不出来，这一帧就当普通模型
// 正文走，"API Error: ..." 既不成 EventError、也不成 stopErr。
//
// 用户侧的症状：一轮跑起来又安静结束，转录里只有自己刚说的那句话，一个字的解释都
// 没有（agentred 日志：totalEvents=1 kinds=map[UserMessage:1] hasStopErr=false）。
func TestStream_ApiErrorMessage_SnakeCaseFlag(t *testing.T) {
	line := `{"type":"assistant","message":{"id":"m1","model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"Failed to authenticate. API Error: 403"}]},"parent_tool_use_id":null,"session_id":"sess-403","error":"authentication_failed","is_api_error_message":true}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var events []Event
	for d.Next() {
		events = append(events, d.Event())
	}
	require.NoError(t, d.Err())

	require.Len(t, events, 1)
	assert.Equal(t, EventError, events[0].Kind)
	require.NotNil(t, events[0].Err)
	assert.Contains(t, events[0].Err.Error(), "403")
}

// TestStream_ResultIsError 钉住 result 帧那一半。
//
// 同一次现场里，终态帧是 `{"subtype":"success","is_error":true,"result":"Failed to
// authenticate. API Error: 403 ...","terminal_reason":"api_error"}`——subtype 说
// success，is_error 说不是。`is_error` 在 rawFrame 上根本没有对应字段（同名的那个
// 在 rawContentBlock 上，是工具结果的），于是这一轮以一个干净的 EventDone 收场。
//
// 这是**第二道**没兜住的地方：就算上面那条错误帧因为别的原因没解出来，终态帧自己
// 也说了这一轮是错的，不该翻成一个成功的 Done。
func TestStream_ResultIsError(t *testing.T) {
	line := `{"type":"result","subtype":"success","is_error":true,"terminal_reason":"api_error","api_error_status":403,"result":"Failed to authenticate. API Error: 403","session_id":"sess-403"}` + "\n"

	d := newFrameDecoder(strings.NewReader(line))
	var events []Event
	for d.Next() {
		events = append(events, d.Event())
	}
	require.NoError(t, d.Err())

	var errEvent *Event
	for i := range events {
		if events[i].Kind == EventError {
			errEvent = &events[i]
		}
	}
	require.NotNil(t, errEvent, "is_error:true 的 result 帧必须交出一条 EventError")
	assert.Contains(t, errEvent.Err.Error(), "403")
}
