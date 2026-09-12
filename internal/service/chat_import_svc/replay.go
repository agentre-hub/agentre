package chat_import_svc

import (
	"context"
	"strings"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// newImportDispatcher 用**线上那张**注册表(transcript.NewTurnDispatcher),只换适配器:
// 导入路径上一轮的消息还没有主键(整轮攒齐后才 Create),所以持久化适配器一律
// **只 patch 内存实体**,不发单列 UPDATE。
//
// 从前这里另抄了一份注册表,理由是"chat_svc 那份是私有的、它的适配器直接够 chat_repo
// 包级全局"。注册表下沉到共享包之后这两条都不成立了:表只有一张,够不够得着库由传进去
// 的 Adapters 决定。
func newImportDispatcher() *turn.Dispatcher {
	return transcript.NewTurnDispatcher(transcript.Adapters{
		Usage:          memUsageWriter{},
		Error:          memErrorWriter{},
		ContextWindow:  memContextWindowWriter{},
		PermissionMode: memPermissionModeWriter{},
		Plan:           memPlanWriter{},
		Compact:        memInspector{},
	})
}

func messageOf(v any) *chat_entity.Message {
	m, _ := v.(*chat_entity.Message)
	return m
}

func sessionOf(v any) *chat_entity.Session {
	s, _ := v.(*chat_entity.Session)
	return s
}

// memUsageWriter 只 patch 内存实体(累加语义与线上同源:completion / reasoning 是 +=)。
type memUsageWriter struct{}

func (memUsageWriter) WriteUsage(_ context.Context, msg any, u *agentruntime.UsageUpdate) error {
	m := messageOf(msg)
	if m == nil || u == nil || u.Usage == nil {
		return nil
	}
	m.PromptTokens = u.Usage.PromptTokens
	m.CompletionTokens += u.Usage.CompletionTokens
	m.CachedTokens = u.Usage.CachedTokens
	m.CacheCreationTokens = u.Usage.CacheCreationTokens
	m.ReasoningTokens += u.Usage.ReasoningTokens
	if u.TotalInputTokens > 0 {
		m.TotalInputTokens = u.TotalInputTokens
	}
	return nil
}

// MessageID 恒为 0:导入路径上这条消息还没落库,拿不到主键,emit 也无人订阅。
func (memUsageWriter) MessageID(any) int64 { return 0 }

type memErrorWriter struct{}

func (memErrorWriter) WriteErrorText(_ context.Context, msg any, errText string) error {
	if m := messageOf(msg); m != nil {
		m.ErrorText = errText
	}
	return nil
}

type memContextWindowWriter struct{}

func (memContextWindowWriter) WriteContextWindow(_ context.Context, sess any, tokens int) error {
	if s := sessionOf(sess); s != nil {
		s.ContextWindow = tokens
	}
	return nil
}

type memPermissionModeWriter struct{}

func (memPermissionModeWriter) CurrentMode(sess any) string {
	if s := sessionOf(sess); s != nil {
		return s.PermissionMode
	}
	return ""
}

func (memPermissionModeWriter) SetMode(_ context.Context, sess any, mode string) error {
	if s := sessionOf(sess); s != nil {
		s.PermissionMode = mode
	}
	return nil
}

// memPlanWriter 复用 chat_svc.PlanBlock —— 计划块只能有一种持久化形态,
// 另造一个类型会让读回路径(planBlockToChatBlock)认不出导入的那一条。
type memPlanWriter struct{}

func (memPlanWriter) WritePlan(acc *turn.Accumulator, plan canonical.PlanUpdate) {
	if acc == nil {
		return
	}
	steps := make([]chat_svc.PlanStepDTO, 0, len(plan.Steps))
	for _, s := range plan.Steps {
		if strings.TrimSpace(s.Step) == "" {
			continue
		}
		steps = append(steps, chat_svc.PlanStepDTO{Step: strings.TrimSpace(s.Step), Status: string(s.Status)})
	}
	blk := chat_svc.PlanBlock{Steps: steps, Text: plan.Text, Actions: plan.Actions}
	if strings.TrimSpace(blk.Text) == "" && len(blk.Steps) == 0 {
		return
	}
	acc.AddBlock(blk, "")
}

type memInspector struct{}

func (memInspector) MessageID(msg any) int64 {
	if m := messageOf(msg); m != nil {
		return m.ID
	}
	return 0
}

func (memInspector) MessageSeq(msg any) int {
	if m := messageOf(msg); m != nil {
		return m.Seq
	}
	return 0
}

// turnPair 是一轮回放出来的一对消息(尚未落库)。
type turnPair struct {
	user      *chat_entity.Message
	assistant *chat_entity.Message
}

// replayTurn 把一轮磁盘转录变成 user + assistant 两条消息。
//
// emit 传 nil:导入不是实时流,没有订阅方;handler 一律 `if emit != nil` 守卫。
// view 传 nil:与线上路径一致(canonical 投影在读回时做)。
// 计时字段(duration_ms / first_token_ms / tokens_per_sec)一律留零 —— 磁盘上没有
// 「减去工具空档的生成耗时」这个量,按墙上时钟现算就是编一个数(spec:没有的字段留零)。
func (s *chatImportSvc) replayTurn(
	ctx context.Context,
	sess *chat_entity.Session,
	backend string,
	t transcriptimport.Turn,
	seq int,
	gaps *gapNotifier,
) (turnPair, error) {
	userMsg := &chat_entity.Message{
		SessionID:  sess.ID,
		Role:       "user",
		Seq:        seq,
		ForkAnchor: t.ForkAnchor,
		Createtime: unixMilli(t.StartedAt),
	}
	userBlocks := make([]cagoblocks.ContentBlock, 0, 1+len(t.UserImages))
	if t.UserText != "" {
		userBlocks = append(userBlocks, &cagoblocks.TextBlock{Text: t.UserText})
	}
	for i := range t.UserImages {
		img := t.UserImages[i]
		userBlocks = append(userBlocks, &img)
	}
	if err := userMsg.SetBlocks(userBlocks); err != nil {
		return turnPair{}, err
	}

	assistantMsg := &chat_entity.Message{
		SessionID:  sess.ID,
		Role:       "assistant",
		Seq:        seq + 1,
		Model:      t.Model,
		ErrorText:  t.ErrorText,
		Createtime: unixMilli(t.EndedAt),
	}
	if assistantMsg.Createtime == 0 {
		assistantMsg.Createtime = userMsg.Createtime
	}
	acc := turn.New()
	turnCtx := &turn.TurnContext{
		AssistantMsg: assistantMsg,
		Session:      sess,
		BackendType:  backend,
		Waits:        turn.NewWaitTracker(),
	}
	for _, ev := range t.Events {
		if err := ctx.Err(); err != nil {
			return turnPair{}, err
		}
		if err := s.dispatcher.Apply(ctx, ev, acc, nil, nil, turnCtx); err != nil {
			return turnPair{}, err
		}
	}
	// 逐轮用量:磁盘上给的是这一轮的最终值,直接写,不走 UsageUpdate 的累加语义
	// (那条是"每个 API call 一帧"的口径)。没有就留零,不猜。
	if t.Usage != nil {
		assistantMsg.PromptTokens = t.Usage.PromptTokens
		assistantMsg.CompletionTokens = t.Usage.CompletionTokens
		assistantMsg.CachedTokens = t.Usage.CachedTokens
		assistantMsg.CacheCreationTokens = t.Usage.CacheCreationTokens
		assistantMsg.ReasoningTokens = t.Usage.ReasoningTokens
	}
	gaps.appendTo(ctx, acc)
	if err := assistantMsg.SetBlocks(acc.Finalize()); err != nil {
		return turnPair{}, err
	}
	return turnPair{user: userMsg, assistant: assistantMsg}, nil
}
