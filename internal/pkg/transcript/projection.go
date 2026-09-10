package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
)

// ProjectMessages 把落库的消息摊成对端读得到的那份持久帧，并给每一帧配一个
// **发生时刻**（第二个返回值，与帧一一对应）。
//
// 时刻取所属消息的 createtime:一条消息摊开成的若干帧是它的展开,不是各自独立的
// 事件,没有比消息本身更细的时刻可言。它最终落到浏览器控制台转录上那个 HH:mm ——
// 那一侧现折转录,除了帧带来的东西没有别的可读。
//
// 两个宿主共用这一份：桌面端的对端补齐与 agentred 的补齐折出来的必须是同一串帧，
// 否则「同一条对话换台机器看内容不一样」就是漏同步一种块类型的直接后果。
func ProjectMessages(conversationID string, messages []*transcript_entity.Message) ([]wire.EventFrame, []int64, error) {
	sorted := append([]*transcript_entity.Message(nil), messages...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i] == nil {
			return false
		}
		if sorted[j] == nil {
			return true
		}
		return sorted[i].Seq < sorted[j].Seq
	})
	frames := make([]wire.EventFrame, 0)
	createtimes := make([]int64, 0)
	// 当前正在摊开的那条消息的时刻。appendEvent 是这一族帧的唯一出口,所以时刻在
	// 这里配给就够了 —— 新增一种块也不会漏掉它。
	var messageAt int64
	appendEvent := func(event agentruntime.Event) error {
		frames = append(frames, wire.EventFrame{ConversationID: conversationID, Event: event})
		createtimes = append(createtimes, messageAt)
		return nil
	}
	for _, message := range sorted {
		if message == nil {
			continue
		}
		messageAt = message.Createtime
		var stored []cagoblocks.StoredBlock
		if err := json.Unmarshal([]byte(message.BlocksJSON), &stored); err != nil {
			return nil, nil, fmt.Errorf("message %d blocks: %w", message.ID, err)
		}
		for _, block := range stored {
			if message.Role == "assistant" && block.Type == "user_ask" {
				var data transcriptblocks.UserAskBlock
				if err := json.Unmarshal(block.Data, &data); err != nil {
					return nil, nil, err
				}
				if err := appendEvent(agentruntime.UserAskRequest{RequestID: data.RequestID, ToolCallID: data.ToolCallID, Questions: projectQuestions(data.Questions)}); err != nil {
					return nil, nil, err
				}
				if data.Answered || data.Skipped {
					if err := appendEvent(agentruntime.UserAskResolved{RequestID: data.RequestID, Answers: projectAnswers(data.Answers), Skipped: data.Skipped}); err != nil {
						return nil, nil, err
					}
				}
				continue
			}
			if message.Role == "assistant" && block.Type == "subagent_state" {
				var data transcriptblocks.SubagentStateBlock
				if err := json.Unmarshal(block.Data, &data); err != nil {
					return nil, nil, err
				}
				if err := appendEvent(agentruntime.SubagentDone{ToolCallID: data.ParentToolCallID, Info: agentruntime.SubagentInfo{
					TaskID: data.TaskID, Kind: data.Kind, TaskDescription: data.Description, LastToolName: data.LastToolName,
					ToolUses: data.ToolUses, TotalTokens: data.TotalTokens, DurationMs: data.DurationMs, Status: data.Status,
					Mode: data.Mode, Runs: data.Runs,
				}}); err != nil {
					return nil, nil, err
				}
				if data.Model != "" {
					if err := appendEvent(agentruntime.SubagentModel{ToolCallID: data.ParentToolCallID, Model: data.Model}); err != nil {
						return nil, nil, err
					}
				}
				continue
			}
			if message.Role == "assistant" && block.Type == "tool_permission" {
				var data transcriptblocks.ToolPermissionBlock
				if err := json.Unmarshal(block.Data, &data); err != nil {
					return nil, nil, err
				}
				input, err := json.Marshal(data.ToolInput)
				if err != nil {
					return nil, nil, err
				}
				if err := appendEvent(agentruntime.ToolPermissionRequest{RequestID: data.RequestID, ToolCallID: data.ToolCallID, ToolName: data.ToolName, Input: input}); err != nil {
					return nil, nil, err
				}
				if data.Resolved {
					if err := appendEvent(agentruntime.ToolPermissionResolved{RequestID: data.RequestID, Allowed: data.Allowed, AlwaysAllow: data.AlwaysAllow, DenyReason: data.DenyReason}); err != nil {
						return nil, nil, err
					}
				}
				continue
			}
			if event, ok, err := EventForStoredBlock(message, block); err != nil {
				return nil, nil, err
			} else if ok {
				if err := appendEvent(event); err != nil {
					return nil, nil, err
				}
				// 投射不出来的块原样往下送(R8)。它是一等的密封事件,所以既过得了
				// 协议边界,又不必在这里对载荷做任何解释 —— 上面那张 switch 只覆盖
				// 它认得的几种,落库的块类型比它多。
			} else if err := appendEvent(agentruntime.UnrecognizedBlock{
				BlockType: block.Type,
				Data:      append(json.RawMessage(nil), block.Data...),
			}); err != nil {
				return nil, nil, err
			}
		}
		if message.Role == "assistant" {
			if message.PromptTokens != 0 || message.CompletionTokens != 0 || message.CachedTokens != 0 || message.CacheCreationTokens != 0 || message.ReasoningTokens != 0 || message.TotalInputTokens != 0 {
				if err := appendEvent(agentruntime.UsageUpdate{Usage: &provider.Usage{
					PromptTokens: message.PromptTokens, CompletionTokens: message.CompletionTokens,
					CachedTokens: message.CachedTokens, CacheCreationTokens: message.CacheCreationTokens,
					ReasoningTokens: message.ReasoningTokens,
				}, TotalInputTokens: message.TotalInputTokens}); err != nil {
					return nil, nil, err
				}
			}
			if message.ErrorText != "" {
				if err := appendEvent(agentruntime.ErrorEvent{Err: errors.New(message.ErrorText)}); err != nil {
					return nil, nil, err
				}
			}
			// 收口带上本轮统计:对端 Peer Tab 的 meta(模型 · 耗时 · 首字 · 速率)
			// 读的正是这几格,而它们就在手边这条消息实体上。
			if err := appendEvent(agentruntime.Done{
				Model: message.Model, DurationMs: message.DurationMs,
				FirstTokenMs: message.FirstTokenMs, TokensPerSec: message.TokensPerSec,
			}); err != nil {
				return nil, nil, err
			}
		}
	}
	return frames, createtimes, nil
}

// EventForStoredBlock 把一条落库的块折成它的持久帧。第二个返回值为 false 表示
// 「本仓折不出来」—— 调用方据此走 agentruntime.UnrecognizedBlock 兜底，块因此
// 永远不会静默消失。
func EventForStoredBlock(message *transcript_entity.Message, block cagoblocks.StoredBlock) (agentruntime.Event, bool, error) {
	if message.Role == "user" && (block.Type == "text" || block.Type == "display_text") {
		var data struct {
			Text             string `json:"text"`
			SourceDevice     string `json:"sourceDevice"`
			SourceDeviceName string `json:"sourceDeviceName"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.UserMessageEvent{
			Text: data.Text, SourceDevice: data.SourceDevice, SourceDeviceName: data.SourceDeviceName,
		}, true, nil
	}
	if message.Role != "assistant" {
		return nil, false, nil
	}
	switch block.Type {
	case "text", "display_text":
		var data struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.TextDelta{Text: data.Text}, true, nil
	case "thinking":
		var data struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.ThinkingDelta{Text: data.Text}, true, nil
	case "tool_use":
		var data struct {
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.ToolCall{ID: data.ID, Name: data.Name, Input: data.Input}, true, nil
	case "tool_result":
		var data struct {
			ToolCallID string                   `json:"tool_use_id"`
			Content    []cagoblocks.StoredBlock `json:"content"`
			IsError    bool                     `json:"is_error"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.ToolResult{ToolCallID: data.ToolCallID, Content: projectTextFromStoredBlocks(data.Content), IsError: data.IsError}, true, nil
	case "permission_mode_change":
		var data transcriptblocks.PermissionModeChangeBlock
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.PermissionModeChanged{Mode: data.To}, true, nil
	case "plan":
		// 计划卡的块类型归宿主（桌面端是 chat_svc.PlanBlock，它同时驮着视图投影）；
		// 折帧只需要它的**载荷形状**，与本函数对 text / tool_use / tool_result 的处理
		// 同一形态 —— 按 JSON 契约读，不 import 宿主的类型。
		var data struct {
			Steps []struct {
				Step   string `json:"step"`
				Status string `json:"status"`
			} `json:"steps"`
			Text    string                 `json:"text"`
			Actions []canonical.PlanAction `json:"actions"`
		}
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		steps := make([]canonical.PlanStep, 0, len(data.Steps))
		for _, step := range data.Steps {
			steps = append(steps, canonical.PlanStep{Step: step.Step, Status: canonical.PlanStepStatus(step.Status)})
		}
		return agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{Steps: steps, Text: data.Text, Actions: data.Actions}}, true, nil
	case "compact_boundary":
		var data transcriptblocks.CompactBoundaryBlock
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		return agentruntime.CompactBoundary{PreTokens: data.PreTokens, Trigger: data.Trigger}, true, nil
	case "exec_approval":
		var data transcriptblocks.ExecApprovalBlock
		if err := json.Unmarshal(block.Data, &data); err != nil {
			return nil, false, err
		}
		if data.Status == "resolved" || data.Status == "expired" {
			return agentruntime.ExecApprovalResolved{ID: data.ID, Status: data.Status, Decision: data.Decision, ResolvedBy: data.ResolvedBy, ResolvedAtMs: data.ResolvedAtMs}, true, nil
		}
		return agentruntime.ExecApprovalRequested{ID: data.ID, CommandText: data.CommandText, CommandPreview: data.CommandPreview, AllowedDecisions: data.AllowedDecisions, Host: data.Host, NodeID: data.NodeID, AgentID: data.AgentID, CreatedAtMs: data.CreatedAtMs, ExpiresAtMs: data.ExpiresAtMs}, true, nil
	default:
		return nil, false, nil
	}
}

func projectTextFromStoredBlocks(blocks []cagoblocks.StoredBlock) string {
	var out strings.Builder
	for _, block := range blocks {
		if block.Type != "text" && block.Type != "display_text" {
			continue
		}
		var data struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(block.Data, &data) == nil {
			out.WriteString(data.Text)
		}
	}
	return out.String()
}

func projectQuestions(in []transcriptblocks.AskQuestionDTO) []agentruntime.AskQuestion {
	out := make([]agentruntime.AskQuestion, 0, len(in))
	for _, question := range in {
		options := make([]agentruntime.AskOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, agentruntime.AskOption{Label: option.Label, Description: option.Description, Preview: option.Preview})
		}
		out = append(out, agentruntime.AskQuestion{ID: question.ID, Question: question.Question, Header: question.Header, MultiSelect: question.MultiSelect, IsOther: question.IsOther, IsSecret: question.IsSecret, Options: options})
	}
	return out
}

func projectAnswers(in []transcriptblocks.AskAnswerDTO) []agentruntime.AskAnswer {
	out := make([]agentruntime.AskAnswer, 0, len(in))
	for _, answer := range in {
		out = append(out, agentruntime.AskAnswer{QuestionIndex: answer.QuestionIndex, Labels: answer.Labels, OtherText: answer.OtherText})
	}
	return out
}
