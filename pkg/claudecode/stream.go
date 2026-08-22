package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/cago-frame/agents/provider"
)

// frameDecoder 把 Claude Code stream-json 的 stdout 行解码为 Event。
//
// 设计点：
//   - 一行一个 JSON object，bufio.Scanner 即可；增大 buffer 容忍极长 tool_result。
//   - 每条 assistant.message.content[*] 可能是 text / thinking / tool_use 中的一种。
//     单帧多 content：依次发多个 Event。
//   - user.message.content[*] 在我们这里只承载 tool_result。
//   - system.init 提供权威 session_id；后续帧填同 id 便于消费方就近读。
//   - result frame 终结，附 Usage。
type frameDecoder struct {
	scan      *bufio.Scanner
	pending   []Event // 单行可能展开成多事件
	sessionID string
	model     string // CLI 在 system.init 报告的实际模型 id（claude-sonnet-4-6 等）
	err       error
	done      bool // result frame 已抵达，后续 Next 不再读 stdout
	// rawSink 若非 nil,每读到一行非空 stdout 就同步回调一次(未解析的原始帧)。
	// debug 级原始帧转储用;由 Client.Stream 从 Client.rawSink 注入。
	rawSink func([]byte)
	// lastAssistantUsage 跟踪本轮**最后一帧 assistant.message.usage**，即最后一次内部
	// API call 的 per-call 用量。result.usage 是整轮所有 API call 的累加，不是"当前
	// 上下文占用"。前端进度条要 input + cache_read + cache_creation = 模型这一刻看到
	// 的输入大小，所以 EventDone 优先吐 last per-call；不可得时再 fallback 到 result.usage
	// （兼容老 CLI / 极简 stub）。
	lastAssistantUsage *rawUsage
	// partials 记录哪些 message 的正文已由 stream_event 增量流出,merged assistant
	// 帧据此跳过同一段文字。见 partial.go。
	partials partialText
}

const maxFrameBytes = 16 << 20 // 16MB 单行兜底（tool_result 内联可能很大）

func newFrameDecoder(r io.Reader) *frameDecoder {
	s := bufio.NewScanner(r)
	buf := make([]byte, 0, 64<<10)
	s.Buffer(buf, maxFrameBytes)
	return &frameDecoder{scan: s}
}

func (d *frameDecoder) SessionID() string { return d.sessionID }

func (d *frameDecoder) Err() error { return d.err }

func (d *frameDecoder) Event() Event {
	if len(d.pending) == 0 {
		return Event{}
	}
	return d.pending[0]
}

// Next 推进到下一个 Event；调用方按 for d.Next() { e := d.Event() } 消费。
//
// 终止条件：result 帧到达后置 done=true，本次 Next 返回 true 把 EventDone 给消费方，
// 下一次 Next 直接返回 false，避免再去 read stdout（CLI 可能尚未关 stdout 就在等下一轮 stdin）。
func (d *frameDecoder) Next() bool {
	if d.err != nil {
		return false
	}
	if len(d.pending) > 0 {
		d.pending = d.pending[1:]
		if len(d.pending) > 0 {
			return true
		}
	}
	if d.done {
		return false
	}
	for d.scan.Scan() {
		line := d.scan.Bytes()
		if len(line) == 0 {
			continue
		}
		if d.rawSink != nil {
			d.rawSink(line)
		}
		events, ok := d.decodeLine(line)
		if !ok {
			continue
		}
		if len(events) == 0 {
			continue
		}
		d.pending = events
		return true
	}
	if err := d.scan.Err(); err != nil && !errors.Is(err, io.EOF) {
		d.err = err
	}
	return false
}

type rawFrame struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	Usage     json.RawMessage `json:"usage,omitempty"`

	// system.init 帧上的 model 字段（"claude-sonnet-4-6" 之类）。仅 init 帧有值，
	// 与内层 message.model（rawMessage.Model，parseAssistantContentWithUsage 消费，
	// 用于 subagent 内部帧的 EventSubagentModel）是两个独立字段。
	Model string `json:"model,omitempty"`

	// subagent 内部的 assistant / user 帧顶层会带 parent_tool_use_id，
	// 指向外层 Agent.tool_use_id；主 agent 自己的帧此字段为 null（→ ""）。
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`

	// ToolUseResult CLI 在 user 帧顶层（跟 message 同级,不在 message.content 里）
	// 吐的工具结构化元数据,典型如 TaskCreate 返回的 {"task":{"id":"1","subject":"..."}}。
	// 一条 user 帧通常只承载一个 tool_result block,所以 meta 与 block 一对一；
	// parseUserContent 把本字段原样塞进 ToolEvent.ResultMeta,由上层按工具语义解码。
	// 普通工具帧（Bash/Read/Edit 等）没有该字段时留 nil。
	ToolUseResult json.RawMessage `json:"tool_use_result,omitempty"`

	// system.subtype ∈ {task_started, task_progress, task_notification} 的字段。
	// task_* 帧的 usage 复用顶层 Usage（与 result.usage 同名但内层字段不同，
	// decoder 按 EventKind 分支选择解码方式）。
	TaskID       string `json:"task_id,omitempty"`
	ToolUseID    string `json:"tool_use_id,omitempty"`
	Description  string `json:"description,omitempty"`
	SubagentType string `json:"subagent_type,omitempty"`
	// TaskType 区分 task 帧来源:"local_bash"(run_in_background bash)/ "local_agent"(subagent)。
	TaskType     string `json:"task_type,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	LastToolName string `json:"last_tool_name,omitempty"`
	Status       string `json:"status,omitempty"`

	// OutputFile 仅「后台命令完成」型 task_notification 帧带（落在 tasks/<id>.output）。
	// 用于把它与 subagent(Task 工具)的 task_notification 区分 —— 后者无此字段、有
	// SubagentType。见 isBackgroundTaskNotification。
	OutputFile string `json:"output_file,omitempty"`

	// Summary 仅「后台命令完成」型 task_notification 帧带，CLI 填充完成摘要文本
	// 如 "Background command \"…\" completed (exit code 0)"。
	Summary string `json:"summary,omitempty"`

	// system.subtype == "api_retry" 的字段：CLI 把 Anthropic SDK 的可重试错误（429/5xx 等）
	// 包成 first-class 协议帧推到 stdout。字段直接放在帧顶层，不嵌在 usage / message 里。
	// ErrorField 字段名避开内置 error。
	Attempt      int     `json:"attempt,omitempty"`
	MaxRetries   int     `json:"max_retries,omitempty"`
	RetryDelayMs float64 `json:"retry_delay_ms,omitempty"`
	ErrorStatus  int     `json:"error_status,omitempty"`
	ErrorField   string  `json:"error,omitempty"`

	// system.subtype == "status" 的字段：CLI 在 permission mode 变化时（主动 set_permission_mode
	// 或被动 ExitPlanMode 通过批准后）发这一帧，带最新 mode 值。空字符串 → 当前帧不是 mode 变更
	// 通知（例如未来 CLI 用 status 帧报告其他状态），调用方应静默忽略。
	PermissionMode string `json:"permissionMode,omitempty"`

	// system.subtype == "compact_boundary" 的字段：CLI 在压缩上下文后发这一帧。
	// 内嵌对象,解析为 CompactEvent;字段缺失保持零值,不阻断主流程。
	CompactMetadata json.RawMessage `json:"compact_metadata,omitempty"`

	// type == "stream_event" 帧的内层 event(Anthropic SSE delta)。--include-partial-messages
	// 模式下 CLI 把上游 SSE 原样推出。我们只用其中的 message_delta.usage 拿
	// 「这次内部 API call 的最终 per-call 用量」—— GLM / openrouter 等 provider 经
	// gateway 走时,后续 merged "assistant" 帧的 usage 字段是 message_start 状态的
	// 0 拷贝,不可信。
	Event json.RawMessage `json:"event,omitempty"`

	// IsAPIErrorMessage 标记 CLI 合成的 API 错误帧(type:"assistant" + model:"<synthetic>")。
	// 一次 API 调用不可恢复中断时,CLI 把 "API Error: ..." 提示塞进这帧的 content 并在
	// 顶层打 isApiErrorMessage:true(+ error 分类码走 ErrorField)。它不是模型正文,
	// case "assistant" 分支据此翻成 EventError 而非 EventTextDelta。见 apiErrorEvent。
	IsAPIErrorMessage bool `json:"isApiErrorMessage,omitempty"`
}

// rawStreamEvent 是 stream_event.event 字段的解码壳。消费 type + usage + delta +
// message.id;content_block / index 等其他子结构当前 parser 用不到,不解。
type rawStreamEvent struct {
	Type  string    `json:"type"`
	Usage *rawUsage `json:"usage,omitempty"`
	// Message 只取 id:message_start 报的这条 message id,是 content_block_delta
	// 归属到哪条 assistant message 的唯一线索(delta 帧自己不带 id)。
	Message struct {
		ID string `json:"id"`
	} `json:"message,omitempty"`
	// Delta 是 content_block_delta 的增量载荷。type 为 text_delta / thinking_delta /
	// input_json_delta;后者(工具入参)不消费,工具调用整块走 merged assistant 帧。
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"delta,omitempty"`
}

// task_started / task_progress / task_notification 的 usage 字段格式与
// result.usage 不同（带 total_tokens / tool_uses / duration_ms），独立解。
type taskUsage struct {
	TotalTokens int `json:"total_tokens"`
	ToolUses    int `json:"tool_uses"`
	DurationMs  int `json:"duration_ms"`
}

type rawMessage struct {
	ID string `json:"id"`
	// Model 是 Anthropic message.model（"claude-haiku-4-5-20251001" 之类），几乎每个
	// assistant 帧都带。主 agent 自己的帧不消费这份数据（该用途已由 system.init.model
	// 满足，见 rawFrame.Model / EventInit / EventDone）；仅 subagent 内部帧
	// （parent_tool_use_id 非空）用它产出 EventSubagentModel，见
	// parseAssistantContentWithUsage。
	Model   string            `json:"model,omitempty"`
	Content []rawContentBlock `json:"content"`
	// Usage 是这一次 API call 的 per-call 用量。Anthropic 在每个 assistant
	// 帧的 inner message 上挂这个字段；pointer 区分"缺省"（老 CLI / stub）和"全 0
	// 但确实存在"（全 cache hit 等）。
	Usage *rawUsage `json:"usage,omitempty"`
}

type rawContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type rawUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (d *frameDecoder) decodeLine(line []byte) ([]Event, bool) {
	var f rawFrame
	if err := json.Unmarshal(line, &f); err != nil {
		return nil, false
	}
	switch f.Type {
	case "system":
		if f.Subtype == "init" {
			if f.SessionID != "" {
				d.sessionID = f.SessionID
			}
			if f.Model != "" {
				d.model = f.Model
				return []Event{{Kind: EventInit, SessionID: d.sessionID, Model: f.Model}}, true
			}
		}
		if ev, ok := d.decodeSystemTask(f); ok {
			return []Event{ev}, true
		}
		// session.parseLine 同款: status 帧上 status / permissionMode 互相独立。
		if f.Subtype == "status" {
			return statusEvents(d.sessionID, f), true
		}
		return nil, true
	case "assistant":
		// 与 session.parseLine 同款:isApiErrorMessage 合成帧翻成 EventError,不泄漏成
		// 正文文本增量。
		if f.IsAPIErrorMessage {
			if ev, ok := apiErrorEvent(f, d.sessionID); ok {
				return []Event{ev}, true
			}
		}
		events, usage := parseAssistantContentWithUsage(f.Message, d.sessionID, f.ParentToolUseID, f.IsAPIErrorMessage, &d.partials)
		// 仅记录主 agent 帧的 usage：parent_tool_use_id != "" 的帧来自 Task/Agent
		// subagent 内部 API call，那是独立 Anthropic 会话（自己的 system prompt /
		// context window），用它的用量覆盖主 agent 的会让进度条骤降到 subagent 的
		// 小上下文，明显错。
		//
		// zero-clobber guard:同 session.go 的 parseLine 注释。
		if usage != nil && f.ParentToolUseID == "" && !isZeroUsage(usage) &&
			!d.partials.placeholderUsage(assistantMessageID(f.Message)) {
			d.lastAssistantUsage = usage
			// 每个主 agent 帧附加一条 EventUsage，让上层在 turn 内实时刷新
			// 「已用上下文」。EventDone 仍按 resolveDoneUsage 兜底，不变。
			events = append(events, Event{
				Kind:      EventUsage,
				SessionID: d.sessionID,
				Usage: provider.Usage{
					PromptTokens:        usage.InputTokens,
					CompletionTokens:    usage.OutputTokens,
					CachedTokens:        usage.CacheReadInputTokens,
					CacheCreationTokens: usage.CacheCreationInputTokens,
				},
			})
		}
		return events, true
	case "stream_event":
		return d.parseStreamEvent(f), true
	case "user":
		return d.decodeUser(f.Message, f.ParentToolUseID, f.ToolUseResult), true
	case "result":
		if f.SessionID != "" {
			d.sessionID = f.SessionID
		}
		d.done = true
		d.partials.reset()
		ev := Event{Kind: EventDone, SessionID: d.sessionID, Model: d.model}
		ev.Usage = resolveDoneUsage(d.lastAssistantUsage, f.Usage)
		return []Event{ev}, true
	}
	return nil, true
}

// parseStreamEvent 处理 type=stream_event 帧。语义与 Session.parseStreamEvent
// 等价(详见 session.go 同名方法注释);把 frameDecoder 改造成 receiver,与既有
// d.lastAssistantUsage 状态共享同一个 lifecycle。
func (d *frameDecoder) parseStreamEvent(f rawFrame) []Event {
	return parseStreamEventFrame(f, d.sessionID, &d.partials, func(u *rawUsage) {
		d.lastAssistantUsage = u
	})
}

// resolveDoneUsage 决定 EventDone 上吐哪一份 usage：
//   - 优先用 lastAssistantUsage（本轮最后一次内部 API call 的 per-call 用量）——
//     这是反映"模型当前看到的上下文大小"的正确口径，前端进度条需要的就是它；
//   - 缺省（lastAssistantUsage == nil，例如老 CLI 不在 assistant 帧上挂 usage、
//     或单元测试的极简 stub）fallback 到 result.usage——值偏大但起码不是 0。
func resolveDoneUsage(lastAssistant *rawUsage, resultUsageRaw json.RawMessage) provider.Usage {
	if lastAssistant != nil {
		return provider.Usage{
			PromptTokens:        lastAssistant.InputTokens,
			CompletionTokens:    lastAssistant.OutputTokens,
			CachedTokens:        lastAssistant.CacheReadInputTokens,
			CacheCreationTokens: lastAssistant.CacheCreationInputTokens,
		}
	}
	if len(resultUsageRaw) > 0 {
		var u rawUsage
		if err := json.Unmarshal(resultUsageRaw, &u); err == nil {
			return provider.Usage{
				PromptTokens:        u.InputTokens,
				CompletionTokens:    u.OutputTokens,
				CachedTokens:        u.CacheReadInputTokens,
				CacheCreationTokens: u.CacheCreationInputTokens,
			}
		}
	}
	return provider.Usage{}
}

func (d *frameDecoder) decodeSystemTask(f rawFrame) (Event, bool) {
	return parseSystemTask(f, d.sessionID)
}

// decodeToolResultContent 把 Anthropic tool_result.content 原始 JSON 拍平成纯文本。
//
//   - 空 / 缺省 → ""
//   - JSON string（最常见）→ Unmarshal 还原转义序列
//   - content-block 数组 → 拼接所有 type=text 块的 text，跳过非 text 块
//   - 其它（容错）→ 原样转字符串
func decodeToolResultContent(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			return s
		}
	case '[':
		var blocks []rawContentBlock
		if err := json.Unmarshal(trimmed, &blocks); err == nil {
			var b strings.Builder
			for _, blk := range blocks {
				if blk.Type == "text" {
					b.WriteString(blk.Text)
				}
			}
			return b.String()
		}
	}
	return string(trimmed)
}

func (d *frameDecoder) decodeUser(raw json.RawMessage, parentToolUseID string, toolUseResult json.RawMessage) []Event {
	return parseUserContent(raw, d.sessionID, parentToolUseID, toolUseResult)
}
