package handlers

import (
	"context"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

type UserAskRequestHandler struct{}

func (UserAskRequestHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.UserAskRequest)
	blk := &blocks.UserAskBlock{
		RequestID:  r.RequestID,
		ToolCallID: r.ToolCallID,
		Questions:  blocks.QuestionsFromRuntime(r.Questions),
	}
	acc.AddBlock(blk, "user_ask:"+r.RequestID)

	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":             "ask_user_question",
			"requestId":        r.RequestID,
			"toolCallId":       r.ToolCallID,
			"parentToolCallId": r.ParentToolCallID,
			"askUserQuestion":  blk,
		})
	}
	tc.BeginWait(ctx, "user_ask", r.RequestID)
	return nil
}

type UserAskResolvedHandler struct{}

func (UserAskResolvedHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.UserAskResolved)
	var blkPtr *blocks.UserAskBlock
	hit := turn.Mutate[blocks.UserAskBlock](acc, "user_ask:"+r.RequestID, func(b *blocks.UserAskBlock) {
		b.Answered = !r.Skipped
		b.Skipped = r.Skipped
		b.Answers = blocks.AnswersFromRuntime(r.Answers)
		blkPtr = b
	})
	if !hit {
		return nil
	}
	if emit != nil {
		// askUserQuestion 必须带 block 指针:dispatcher_emitter.askUserQuestionFromMap
		// fallback 路径只读 requestId/answered/skipped,会把 Questions/Answers 丢成 nil,
		// 新 canonical 把前端 existing canonical 整体覆盖成 questions=null → UserAskCard 消失。
		// 跟 UserAskRequestHandler 对称传 blk 就能让 wire payload 全字段透传。
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":             "ask_user_question",
			"requestId":        r.RequestID,
			"parentToolCallId": r.ParentToolCallID,
			"askUserQuestion":  blkPtr,
		})
	}
	tc.ResolveWait(ctx, "user_ask", r.RequestID)
	return nil
}

// MarkUnansweredUserAsksExpired finalize 时把仍未答/未跳过的 AskUserQuestion block
// 标 expired —— 与 MarkRunningSubagentsCancelled / chatSvc.takeToolApprovals 同模式。
// turn 结束后该卡再提交必然失败(claudecode SubmitAnswer 走 ErrNoActiveTurn / 无 waiter),
// 标 expired 让前端锁卡并展示「已失效」,且落库后 reload 仍可见。
// 返回被本次标记的 block 指针,供调用方 emit live 锁定 patch。
func MarkUnansweredUserAsksExpired(finalBlocks []cagoblocks.ContentBlock) []*blocks.UserAskBlock {
	var marked []*blocks.UserAskBlock
	for _, b := range finalBlocks {
		ua, ok := b.(*blocks.UserAskBlock)
		if !ok || ua.Answered || ua.Skipped || ua.Expired {
			continue
		}
		ua.Expired = true
		marked = append(marked, ua)
	}
	return marked
}
