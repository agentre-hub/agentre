package chat_svc

import (
	"github.com/cago-frame/agents/agent/blocks"

	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

// ProjectBlocks 把持久化的 cago/agents 内容块投影成前端 ChatBlock。
//
// 这是本包对「存储块 → ChatBlock」的唯一投影实现:toChatMessage(读回/重载路径)
// 与 chat_import_svc.Preview(磁盘导入预览路径)都经它产出前端渲染需要的形状,
// 所以同一份转录不论是重载出来的还是从磁盘导入预览出来的,渲染结果一致。
func ProjectBlocks(bs []blocks.ContentBlock) []ChatBlock {
	out := make([]ChatBlock, 0, len(bs))

	// 预扫一遍,把 SubagentStateBlock 按 ParentToolCallID 索引起来,
	// 后续 tool_use 命中时把元数据合入 .Subagent,实现持久化/重载路径与
	// live 路径(dispatcher_emitter mergeSubagentMeta)形态一致。
	subByParent := make(map[string]*chatblocks.SubagentStateBlock)
	for _, b := range bs {
		switch sb := b.(type) {
		case chatblocks.SubagentStateBlock:
			cp := sb
			subByParent[sb.ParentToolCallID] = &cp
		case *chatblocks.SubagentStateBlock:
			if sb != nil {
				subByParent[sb.ParentToolCallID] = sb
			}
		}
	}

	for _, b := range bs {
		switch tb := b.(type) {
		case blocks.TextBlock:
			out = append(out, ChatBlock{Type: ChatBlockTypeText, Text: tb.Text})
		case *blocks.TextBlock:
			out = append(out, ChatBlock{Type: ChatBlockTypeText, Text: tb.Text})
		case blocks.ImageBlock:
			out = append(out, imageBlockToChatBlock(tb))
		case *blocks.ImageBlock:
			if tb != nil {
				out = append(out, imageBlockToChatBlock(*tb))
			}
		case blocks.ThinkingBlock:
			out = append(out, ChatBlock{Type: ChatBlockTypeThinking, Text: tb.Text})
		case *blocks.ThinkingBlock:
			out = append(out, ChatBlock{Type: ChatBlockTypeThinking, Text: tb.Text})
		case blocks.NoticeBlock:
			out = append(out, noticeBlockToChatBlock(tb))
		case *blocks.NoticeBlock:
			if tb != nil {
				out = append(out, noticeBlockToChatBlock(*tb))
			}
		case blocks.ToolUseBlock:
			cb := toolUseToChatBlock(tb.ID, tb.Name, tb.Input)
			if sb := subByParent[tb.ID]; sb != nil {
				attachSubagentStateToChatBlock(&cb, tb.Name, sb)
			}
			out = append(out, cb)
		case *blocks.ToolUseBlock:
			cb := toolUseToChatBlock(tb.ID, tb.Name, tb.Input)
			if sb := subByParent[tb.ID]; sb != nil {
				attachSubagentStateToChatBlock(&cb, tb.Name, sb)
			}
			out = append(out, cb)
		case blocks.ToolResultBlock:
			out = append(out, toolResultToChatBlock(tb.ToolUseID, tb.Content, tb.IsError))
		case *blocks.ToolResultBlock:
			out = append(out, toolResultToChatBlock(tb.ToolUseID, tb.Content, tb.IsError))
		case *chatblocks.NestedToolUseBlock:
			out = append(out, nestedToolUseToChatBlock(tb))
		case chatblocks.NestedToolUseBlock:
			out = append(out, nestedToolUseToChatBlock(&tb))
		case *chatblocks.NestedToolResultBlock:
			out = append(out, nestedToolResultToChatBlock(tb))
		case chatblocks.NestedToolResultBlock:
			out = append(out, nestedToolResultToChatBlock(&tb))
		case *chatblocks.SubagentStateBlock, chatblocks.SubagentStateBlock,
			*chatblocks.PermissionModeChangeBlock, chatblocks.PermissionModeChangeBlock:
			// SubagentStateBlock: 元数据已在预扫阶段合入对应 tool_use 块的 .Subagent 字段,
			// 不再作为独立 block 下行前端(否则会被打成 type=unknown 让用户看到 debug 卡)。
			// PermissionModeChangeBlock: 审计 block,无 UI 元素,一并 skip。
		case *chatblocks.CompactBoundaryBlock:
			if tb != nil {
				out = append(out, ChatBlock{
					Type: ChatBlockTypeCompactBoundary,
					Compact: &ChatBlockCompactBoundary{
						PreTokens: tb.PreTokens, Trigger: tb.Trigger, At: tb.At,
					},
				})
			}
		case chatblocks.CompactBoundaryBlock:
			out = append(out, ChatBlock{
				Type: ChatBlockTypeCompactBoundary,
				Compact: &ChatBlockCompactBoundary{
					PreTokens: tb.PreTokens, Trigger: tb.Trigger, At: tb.At,
				},
			})
		case chatblocks.UserAskBlock:
			out = append(out, askUserQuestionBlockToChatBlock(tb))
		case *chatblocks.UserAskBlock:
			if tb != nil {
				out = append(out, askUserQuestionBlockToChatBlock(*tb))
			}
		case chatblocks.ToolPermissionBlock:
			out = append(out, toolPermissionBlockToChatBlock(tb))
		case *chatblocks.ToolPermissionBlock:
			if tb != nil {
				out = append(out, toolPermissionBlockToChatBlock(*tb))
			}
		case chatblocks.ExecApprovalBlock:
			out = append(out, execApprovalBlockToChatBlock(tb))
		case *chatblocks.ExecApprovalBlock:
			if tb != nil {
				out = append(out, execApprovalBlockToChatBlock(*tb))
			}
		case chatblocks.ToolApprovalBlock:
			out = append(out, toolApprovalBlockToChatBlock(tb))
		case *chatblocks.ToolApprovalBlock:
			if tb != nil {
				out = append(out, toolApprovalBlockToChatBlock(*tb))
			}
		case PlanBlock:
			out = append(out, planBlockToChatBlock(tb))
		case *PlanBlock:
			if tb != nil {
				out = append(out, planBlockToChatBlock(*tb))
			}
		default:
			out = append(out, ChatBlock{Type: ChatBlockTypeUnknown, Raw: map[string]any{"kind": b.Type()}})
		}
	}
	return out
}
