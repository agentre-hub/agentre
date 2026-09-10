package chat_svc

import (
	"context"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

// TranscriptBlockWindow 是一次下发多少条消息的完整正文。
//
// 实测的重尾会话单条 26.2 MB / 8733 块,整条搬到前端既撑爆 Wails 桥也让首屏等在
// 解码上;而用户打开会话时看的永远是末尾这几轮。窗口按**消息条数**而不是块数算 ——
// 前端要的是「渲染得出来的完整消息」,半条消息渲染出来就是缺了工具结果的假转录。
const TranscriptBlockWindow = 30

// transcriptWindow 取消息表末尾的一个窗口(不足一窗时就是全部)。
func transcriptWindow(msgs []*chat_entity.Message) []*chat_entity.Message {
	if len(msgs) <= TranscriptBlockWindow {
		return msgs
	}
	return msgs[len(msgs)-TranscriptBlockWindow:]
}

// LoadMessageBlocks 取回 BeforeSeq 之前那一段消息的正文,供前端向上滚动时续接转录。
func (s *chatSvc) LoadMessageBlocks(ctx context.Context, req *LoadMessageBlocksRequest) (*LoadMessageBlocksResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	// 上界钉死在一个窗口:有界是这条接口的性质,不能靠调用方自觉。放开它,前端一次
	// limit=1e9 就把整条转录又要回去了,正是本轮要消灭的形态。
	limit := req.Limit
	if limit <= 0 || limit > TranscriptBlockWindow {
		limit = TranscriptBlockWindow
	}
	msgs, err := chat_repo.Message().ListMeta(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	// ListMeta 已按 seq 升序,BeforeSeq 之前的那一段就是一个前缀;取它的末尾 limit 条。
	end := 0
	for end < len(msgs) && msgs[end].Seq < req.BeforeSeq {
		end++
	}
	start := max(end-limit, 0)
	window := msgs[start:end]
	resp := &LoadMessageBlocksResponse{
		Messages: make([]ChatMessage, 0, len(window)),
		HasMore:  start > 0,
	}
	if len(window) == 0 {
		return resp, nil
	}
	if err := chat_repo.Message().FillBlocks(ctx, window); err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	for _, m := range window {
		cm, err := toChatMessage(m)
		if err != nil {
			return nil, i18n.NewError(ctx, code.ChatBlocksMalformed)
		}
		resp.Messages = append(resp.Messages, cm)
	}
	logger.Ctx(ctx).Debug("chat_svc: LoadMessageBlocks served",
		zap.Int64("sessionId", req.SessionID),
		zap.Int("beforeSeq", req.BeforeSeq),
		zap.Int("count", len(resp.Messages)),
		zap.Bool("hasMore", resp.HasMore))
	return resp, nil
}

// blockTypeSources 把前端点名的**投影后**块类型翻译成它在块表里的存储类型。
//
// 一张 tool_use 卡在库里可能由三种块拼出来(见 projection.go):
//   - tool_use 本身;
//   - subagent_state —— 后台任务的累计态,预扫后合入对应 tool_use 卡的 .subagent。
//     少取它,后台任务面板会把所有后台任务判成不存在;
//   - nested_tool_use —— subagent 内层的工具调用,同样投影成 tool_use 卡。少取它,
//     大纲那一轮的「编辑次数」会漏掉子代理改的文件。
//
// 两处都是决策 6 说的「静默算错」:视图看着正常,数字是错的。
var blockTypeSources = map[string][]string{
	ChatBlockTypeToolUse: {
		blocks.ToolUseBlock{}.Type(),
		chat_entity.BlockTypeSubagentState,
		chatblocks.NestedToolUseBlock{}.Type(),
	},
}

// storedBlockTypes 展开点名类型对应的存储类型,保序去重。
func storedBlockTypes(types []string) []string {
	out := make([]string, 0, len(types)+1)
	seen := make(map[string]struct{}, len(types)+1)
	for _, t := range types {
		sources, ok := blockTypeSources[t]
		if !ok {
			sources = []string{t}
		}
		for _, src := range sources {
			if _, dup := seen[src]; dup {
				continue
			}
			seen[src] = struct{}{}
			out = append(out, src)
		}
	}
	return out
}

// LoadSessionBlocksByType 给派生视图供数:整条会话的消息元数据,每条只带点名类型的块。
//
// 后台任务面板 / 大纲 / 变更这三处此前靠「前端遍历本地全量转录」得出结论,块表的
// type 列让它们改成后端按类型点查 —— 数据集合与改动前等价,但前端不再需要持有整条
// 会话的全部正文(决策 6)。
func (s *chatSvc) LoadSessionBlocksByType(ctx context.Context, req *LoadSessionBlocksByTypeRequest) (*LoadSessionBlocksByTypeResponse, error) {
	if req == nil || req.SessionID <= 0 || len(req.Types) == 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	msgs, err := chat_repo.Message().ListMeta(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if err := chat_repo.Message().FillBlocksByType(ctx, msgs, storedBlockTypes(req.Types)); err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	wanted := make(map[string]struct{}, len(req.Types))
	for _, t := range req.Types {
		wanted[t] = struct{}{}
	}
	resp := &LoadSessionBlocksByTypeResponse{Messages: make([]ChatMessage, 0, len(msgs))}
	for _, m := range msgs {
		cm, err := toChatMessage(m)
		if err != nil {
			return nil, i18n.NewError(ctx, code.ChatBlocksMalformed)
		}
		// 投影侧再筛一次:仓储按存储类型取,而存储类型与投影后的类型不是一一对应
		// (subagent_state 合进 tool_use、嵌套块折叠)。两道筛子同一口径,派生视图
		// 拿到的就正好是它点名的那些卡。
		kept := cm.Blocks[:0]
		for _, b := range cm.Blocks {
			if _, ok := wanted[b.Type]; ok {
				kept = append(kept, b)
			}
		}
		cm.Blocks = kept
		// 这是派生视图的取数,不是转录正文:即便某条消息的全部块都被点名取到了,
		// 它也不该被当成「正文已就绪」去渲染。
		cm.BlocksLoaded = false
		resp.Messages = append(resp.Messages, cm)
	}
	logger.Ctx(ctx).Debug("chat_svc: LoadSessionBlocksByType served",
		zap.Int64("sessionId", req.SessionID),
		zap.Strings("blockTypes", req.Types),
		zap.Int("messageCount", len(resp.Messages)))
	return resp, nil
}
