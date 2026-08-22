package claudecode

import (
	"encoding/json"

	"github.com/cago-frame/agents/provider"
)

// partialText 是 --include-partial-messages 模式下「正文已经流出去过」的账本。
//
// CLI 对同一段正文说两次话:先按 Anthropic SSE 逐帧推 stream_event
// content_block_delta,一段结束后再推一条 merged assistant 帧,里面是这条 message
// 的**全文**。两边都翻成 EventTextDelta 会让转录里的文字翻倍,所以谁流过就由谁负责:
// 某条 message 的 text / thinking 一旦有增量流出去,后到的 merged 帧里对应类型的块
// 就跳过;没有增量(老 CLI、未开 flag、子代理帧、单测 stub)时 merged 帧照旧是唯一来源。
//
// 账本按 turn 收口(result 帧 reset),不跨轮留记录。
type partialText struct {
	// curMsgID 是最近一条 message_start 报的 message id。content_block_delta 自己
	// 不带 message id,只能靠它归属。
	curMsgID string
	text     map[string]bool
	thinking map[string]bool
}

func (p *partialText) noteMessageStart(id string) {
	p.curMsgID = id
}

// markText 记下 curMsgID 的正文已流出;curMsgID 为空(没见过 message_start)时返回
// false —— 无法归属就不敢流,退回让 merged 帧全量吐,宁可不流也不能吐两遍。
func (p *partialText) markText() bool {
	if p.curMsgID == "" {
		return false
	}
	if p.text == nil {
		p.text = map[string]bool{}
	}
	p.text[p.curMsgID] = true
	return true
}

func (p *partialText) markThinking() bool {
	if p.curMsgID == "" {
		return false
	}
	if p.thinking == nil {
		p.thinking = map[string]bool{}
	}
	p.thinking[p.curMsgID] = true
	return true
}

func (p *partialText) streamedText(msgID string) bool {
	return msgID != "" && p.text[msgID]
}

func (p *partialText) streamedThinking(msgID string) bool {
	return msgID != "" && p.thinking[msgID]
}

func (p *partialText) reset() {
	p.curMsgID = ""
	p.text = nil
	p.thinking = nil
}

// parseStreamEventFrame 把一帧 type=stream_event 翻成 Event 列表。
//
//   - message_start:记住 message id(后面的 delta 靠它归属)。
//   - content_block_delta 的 text_delta / thinking_delta:逐帧流出正文。这是上层
//     拿到「第一个可见 token」和真实生成时长的唯一来源 —— merged assistant 帧要等
//     整段生成完才到,拿它当首 token 会让 tok/s 的分母塌成几十毫秒。
//     input_json_delta(工具入参)不流:工具调用由 merged 帧的 tool_use 块整块产出。
//   - message_delta 的 usage:这次内部 API call 的真实 per-call 用量(见
//     resolveDoneUsage / zero-clobber guard)。
//
// parent_tool_use_id 非空的帧来自 Task/Agent 子代理内部 API call,整帧忽略(与 usage
// 同规矩),它的正文仍由子代理自己的 merged assistant 帧承载。
func parseStreamEventFrame(f rawFrame, sid string, p *partialText, remember func(*rawUsage)) []Event {
	if f.ParentToolUseID != "" || len(f.Event) == 0 {
		return nil
	}
	var ev rawStreamEvent
	if err := json.Unmarshal(f.Event, &ev); err != nil {
		return nil
	}
	switch ev.Type {
	case "message_start":
		p.noteMessageStart(ev.Message.ID)
		return nil
	case "content_block_delta":
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text == "" || !p.markText() {
				return nil
			}
			return []Event{{Kind: EventTextDelta, SessionID: sid, Text: ev.Delta.Text}}
		case "thinking_delta":
			if ev.Delta.Thinking == "" || !p.markThinking() {
				return nil
			}
			return []Event{{Kind: EventThinkingDelta, SessionID: sid, Text: ev.Delta.Thinking}}
		}
		return nil
	case "message_delta":
		if ev.Usage == nil || isZeroUsage(ev.Usage) {
			return nil
		}
		if remember != nil {
			remember(ev.Usage)
		}
		return []Event{{
			Kind:      EventUsage,
			SessionID: sid,
			Usage: provider.Usage{
				PromptTokens:        ev.Usage.InputTokens,
				CompletionTokens:    ev.Usage.OutputTokens,
				CachedTokens:        ev.Usage.CacheReadInputTokens,
				CacheCreationTokens: ev.Usage.CacheCreationInputTokens,
			},
		}}
	}
	return nil
}
