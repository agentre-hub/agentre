package chat_svc

import (
	"encoding/base64"
	"strings"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"

	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

// noticeBlockToChatBlock 把持久化的 blocks.NoticeBlock 投影成前端 ChatBlock。
// 供应商回退/切换提示(本功能产出的结构化小 JSON)解回 ProviderKey + ProviderName +
// NoticeKind、Text 置空 —— 前端走 t() 渲染;非结构化旧数据原样透传 Text。
func noticeBlockToChatBlock(tb blocks.NoticeBlock) ChatBlock {
	if p, ok := view.DecodeProviderNotice(tb.Text); ok {
		return ChatBlock{
			Type:         ChatBlockTypeNotice,
			Level:        tb.Level,
			ProviderKey:  p.ProviderKey,
			ProviderName: p.ProviderName,
			ModelKey:     p.ModelKey,
			ModelName:    p.ModelName,
			NoticeKind:   p.Kind,
		}
	}
	return ChatBlock{Type: ChatBlockTypeNotice, Level: tb.Level, Text: tb.Text}
}

func toChatMessage(m *chat_entity.Message) (ChatMessage, error) {
	bs, err := m.GetBlocks()
	if err != nil {
		return ChatMessage{}, err
	}
	source := peerMessageSourceOf(m)
	out := ChatMessage{
		ID:                  m.ID,
		SessionID:           m.SessionID,
		Role:                m.Role,
		Model:               m.Model,
		PromptTokens:        m.PromptTokens,
		CompletionTokens:    m.CompletionTokens,
		CachedTokens:        m.CachedTokens,
		CacheCreationTokens: m.CacheCreationTokens,
		ReasoningTokens:     m.ReasoningTokens,
		TotalInputTokens:    m.TotalInputTokens,
		DurationMs:          m.DurationMs,
		FirstTokenMs:        m.FirstTokenMs,
		TokensPerSec:        m.TokensPerSec,
		ErrorText:           m.ErrorText,
		Seq:                 m.Seq,
		Createtime:          m.Createtime,
		SourceDevice:        source.Device,
		SourceDeviceName:    source.Name,
		Blocks:              ProjectBlocks(bs),
		// 仓储只在补过正文的消息上写 BlocksJSON(ListMeta 留空串,FillBlocks 至少写
		// "[]"),空串因此就是「这条消息的正文还没取」。前端据此决定渲不渲染这一行。
		BlocksLoaded: m.BlocksJSON != "",
	}
	return out, nil
}

func toolUseToChatBlock(id, name string, input map[string]any) ChatBlock {
	cb := ChatBlock{Type: ChatBlockTypeToolUse, ToolCallID: id, ToolName: name}
	if len(input) > 0 {
		cb.ToolInput = input
	}
	if c, ok := canonical.FromToolUse(name, input); ok {
		cb.Canonical = view.FromCanonical(c)
	}
	return cb
}

func subagentStateToChatBlockSubagent(sb *chatblocks.SubagentStateBlock) *ChatBlockSubagent {
	if sb == nil {
		return nil
	}
	return &ChatBlockSubagent{
		TaskID:          sb.TaskID,
		Kind:            sb.Kind,
		TaskDescription: sb.Description,
		LastToolName:    sb.LastToolName,
		ToolUses:        sb.ToolUses,
		TotalTokens:     sb.TotalTokens,
		DurationMs:      sb.DurationMs,
		Status:          sb.Status,
		Summary:         sb.Summary,
		Mode:            sb.Mode,
		Runs:            cloneSubagentRunSnapshot(sb.Runs),
		Resumes:         cloneSubagentInterruptions(sb.Resumes),
		Model:           sb.Model,
	}
}

func cloneSubagentInterruptions(in []chatblocks.SubagentInterruption) []chatblocks.SubagentInterruption {
	if in == nil {
		return nil
	}
	out := make([]chatblocks.SubagentInterruption, len(in))
	copy(out, in)
	return out
}

func attachSubagentStateToChatBlock(cb *ChatBlock, toolName string, sb *chatblocks.SubagentStateBlock) {
	cb.Subagent = subagentStateToChatBlockSubagent(sb)
	if cb.Canonical != nil || !isNormalizedPiSubagentReplay(toolName, sb) {
		return
	}
	cb.Canonical = view.FromCanonical(canonical.AgentSpawn{
		TaskID:          sb.TaskID,
		TaskDescription: sb.Description,
		Mode:            sb.Mode,
		Runs:            agentSpawnRunsFromRuntime(sb.Runs),
		LastToolName:    sb.LastToolName,
		ToolUses:        sb.ToolUses,
		TotalTokens:     sb.TotalTokens,
		DurationMs:      sb.DurationMs,
		Status:          sb.Status,
	})
}

func isNormalizedPiSubagentReplay(toolName string, sb *chatblocks.SubagentStateBlock) bool {
	return sb != nil && sb.Mode != "" && len(sb.Runs) > 0 &&
		strings.Contains(strings.ToLower(toolName), "subagent")
}

func cloneSubagentRunSnapshot(runs []agentruntime.SubagentRun) []agentruntime.SubagentRun {
	if runs == nil {
		return nil
	}
	out := make([]agentruntime.SubagentRun, len(runs))
	copy(out, runs)
	return out
}

func imageBlockToChatBlock(img blocks.ImageBlock) ChatBlock {
	cb := ChatBlock{Type: ChatBlockTypeImage, Image: &ChatBlockImage{MediaType: img.MediaType}}
	if len(img.Source.Inline) > 0 {
		cb.Image.DataURL = "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Source.Inline)
	} else if img.Source.URL != "" {
		cb.Image.DataURL = img.Source.URL
	}
	return cb
}

// nestedToolUseToChatBlock 把 subagent 内层 ToolUse 投影到 wire ChatBlock。
// 与外层 toolUseToChatBlock 的差别在于带 ParentToolCallID(json: parentToolUseId) +
// 可选 SubagentRunID；前端据此先挂到外层 AgentSpawnCard，再按 normalized run 分组。
//
// canonical 与外层同一条路子(canonical.FromToolUse)。内层不会因此多出一张独立
// 卡 —— 前端 transcript-rows 见到 parentToolUseId 就把它归给父 AgentSpawnCard;
// 但组头的「改了几个文件 / ±行数」和侧栏「变更」页只认 canonical、不查工具名表,
// 内层不带就等于「会话重开之后子代理改过的文件全部消失」。live 路径
// (handlers.ToolCallHandler)一直是带着 canonical 发的,这里补齐 replay 那一半。
func nestedToolUseToChatBlock(b *chatblocks.NestedToolUseBlock) ChatBlock {
	cb := ChatBlock{
		Type:             ChatBlockTypeToolUse,
		ToolCallID:       b.ID,
		ToolName:         b.Name,
		ParentToolCallID: b.ParentToolCallID,
		SubagentRunID:    b.SubagentRunID,
	}
	if len(b.Input) > 0 {
		cb.ToolInput = b.Input
	}
	if c, ok := canonical.FromToolUse(b.Name, b.Input); ok {
		cb.Canonical = view.FromCanonical(c)
	}
	return cb
}

// nestedToolResultToChatBlock 镜像 nestedToolUseToChatBlock —— 内层 tool_result
// 保留 ParentToolCallID/SubagentRunID，Content 已经是拍平字符串。
func nestedToolResultToChatBlock(b *chatblocks.NestedToolResultBlock) ChatBlock {
	return ChatBlock{
		Type:             ChatBlockTypeToolResult,
		ToolCallID:       b.ToolCallID,
		Text:             b.Content,
		IsError:          b.IsError,
		ParentToolCallID: b.ParentToolCallID,
		SubagentRunID:    b.SubagentRunID,
	}
}

// toolResultToChatBlock 把 ToolResultBlock 拍平：拼接所有 TextBlock 内容；
// 其它子块暂时丢弃（设计稿 Sec 02/04 的特殊卡片下个迭代再做）。
func toolResultToChatBlock(toolCallID string, content []blocks.ContentBlock, isError bool) ChatBlock {
	var sb strings.Builder
	for _, c := range content {
		switch t := c.(type) {
		case blocks.TextBlock:
			sb.WriteString(t.Text)
		case *blocks.TextBlock:
			sb.WriteString(t.Text)
		}
	}
	return ChatBlock{Type: ChatBlockTypeToolResult, ToolCallID: toolCallID, Text: sb.String(), IsError: isError}
}
