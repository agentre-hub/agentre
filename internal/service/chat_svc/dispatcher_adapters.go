package chat_svc

import (
	"context"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/handlers"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// dispatcher_adapters.go 给 turn dispatcher 注入持久化 + 数据写入能力。
//
// 设计意图:handlers/ 包不能依赖 chat_svc(避免循环);它声明几组小接口
// (SessionUpdater / UsageWriter / ErrorWriter / ContextWindowWriter),
// chat_svc 在这里实现并通过 TurnContext / handler 字段注入。

// sessionUpdaterAdapter 实现 turn.SessionUpdater。
// PermissionModeChangedHandler 调它时,assistantMsg 是 *chat_entity.Session。
type sessionUpdaterAdapter struct{}

func (sessionUpdaterAdapter) Update(ctx context.Context, sess any) error {
	s, ok := sess.(*chat_entity.Session)
	if !ok || s == nil {
		return nil
	}
	return chat_repo.Session().Update(ctx, s)
}

// usageWriterAdapter 实现 handlers.UsageWriter:把 agentruntime.UsageUpdate
// patch 到 *chat_entity.Message 的 token 列,再单列落库。
//
// 累加语义(CompletionTokens / ReasoningTokens 是 +=)留在内存实体上,落库只写最终值,
// 因此这里必须先 patch 再取值。不要改成 SQL 侧自增:实体是本轮的唯一真源,自增会在
// 重试/补齐路径上重复计数。
type usageWriterAdapter struct{}

func (usageWriterAdapter) WriteUsage(ctx context.Context, msg any, u *agentruntime.UsageUpdate) error {
	m, ok := msg.(*chat_entity.Message)
	if !ok || m == nil || u == nil || u.Usage == nil {
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
	if m.ID == 0 {
		return nil
	}
	return chat_repo.Message().UpdateUsage(ctx, m.ID, chat_repo.MessageUsage{
		PromptTokens:        m.PromptTokens,
		CompletionTokens:    m.CompletionTokens,
		CachedTokens:        m.CachedTokens,
		CacheCreationTokens: m.CacheCreationTokens,
		ReasoningTokens:     m.ReasoningTokens,
		TotalInputTokens:    m.TotalInputTokens,
	})
}

func (usageWriterAdapter) MessageID(msg any) int64 {
	m, ok := msg.(*chat_entity.Message)
	if !ok || m == nil {
		return 0
	}
	return m.ID
}

// errorWriterAdapter 实现 handlers.ErrorWriter:patch 内存实体 + 单列落库。
type errorWriterAdapter struct{}

func (errorWriterAdapter) WriteErrorText(ctx context.Context, msg any, errText string) error {
	m, ok := msg.(*chat_entity.Message)
	if !ok || m == nil {
		return nil
	}
	m.ErrorText = errText
	if m.ID == 0 {
		return nil
	}
	return chat_repo.Message().UpdateErrorText(ctx, m.ID, errText)
}

// contextWindowWriterAdapter 实现 handlers.ContextWindowWriter:同步内存中的
// sess.ContextWindow + 调 chat_repo.Session().UpdateContextWindow(column-only,理由见
// 该接口注释:整行回写会让带外轮的旧快照把 agent_status 拍回 idle)。
// 值与实体一致时跳过,避免每帧多一次 UPDATE。
type contextWindowWriterAdapter struct{}

func (contextWindowWriterAdapter) WriteContextWindow(ctx context.Context, sess any, tokens int) error {
	s, ok := sess.(*chat_entity.Session)
	if !ok || s == nil || s.ContextWindow == tokens {
		return nil
	}
	s.ContextWindow = tokens
	return chat_repo.Session().UpdateContextWindow(ctx, s.ID, tokens)
}

// permissionModeWriterAdapter 实现 handlers.PermissionModeWriter:
// CurrentMode 读 sess.PermissionMode(handler 幂等判断);SetMode 把
// sess.PermissionMode 同步为新值 + 调 chat_repo.Session().UpdatePermissionMode
// (column-only,避免 Update 写整行覆盖其它字段)。
type permissionModeWriterAdapter struct{}

func (permissionModeWriterAdapter) CurrentMode(sess any) string {
	s, ok := sess.(*chat_entity.Session)
	if !ok || s == nil {
		return ""
	}
	return s.PermissionMode
}

func (permissionModeWriterAdapter) SetMode(ctx context.Context, sess any, mode string) error {
	s, ok := sess.(*chat_entity.Session)
	if !ok || s == nil {
		return nil
	}
	s.PermissionMode = mode
	return chat_repo.Session().UpdatePermissionMode(ctx, s.ID, mode)
}

// planWriterAdapter 实现 handlers.PlanWriter:把 canonical PlanUpdate 投影到
// chat_svc.PlanBlock,通过 acc.AddBlock 落到 turn accumulator。
type planWriterAdapter struct{}

func (planWriterAdapter) WritePlan(acc *turn.Accumulator, plan canonical.PlanUpdate) {
	if acc == nil {
		return
	}
	text := plan.Text
	if strings.TrimSpace(text) != "" {
		acc.AddBlock(PlanBlock{Text: text, Actions: plan.Actions}, "")
		return
	}
	steps := plan.Steps
	if len(steps) == 0 {
		return
	}
	dtoSteps := make([]PlanStepDTO, 0, len(steps))
	for _, s := range steps {
		st := strings.TrimSpace(s.Step)
		if st == "" {
			continue
		}
		dtoSteps = append(dtoSteps, PlanStepDTO{Step: st, Status: string(s.Status)})
	}
	blk := PlanBlock{Steps: dtoSteps, Text: formatPlanText(dtoSteps), Actions: plan.Actions}
	if strings.TrimSpace(blk.Text) == "" {
		return
	}
	acc.AddBlock(blk, "")
}

// compactInspectorAdapter 暴露 *chat_entity.Message 的 ID/Seq 给
// handlers.CompactBoundaryHandler 用,不让 handler 包反向依赖 chat_entity。
type compactInspectorAdapter struct{}

func (compactInspectorAdapter) MessageID(msg any) int64 {
	m, ok := msg.(*chat_entity.Message)
	if !ok || m == nil {
		return 0
	}
	return m.ID
}

func (compactInspectorAdapter) MessageSeq(msg any) int {
	m, ok := msg.(*chat_entity.Message)
	if !ok || m == nil {
		return 0
	}
	return m.Seq
}

// buildHandlersWithAdapters 返回填充了 chat_svc 适配器的 handler 实例。
func buildHandlersWithAdapters(_ *chatSvc) (
	handlers.UsageUpdateHandler,
	handlers.ErrorHandler,
	handlers.ContextWindowUpdatedHandler,
	handlers.PermissionModeChangedHandler,
	handlers.PlanUpdatedHandler,
	handlers.CompactBoundaryHandler,
) {
	return handlers.UsageUpdateHandler{Writer: usageWriterAdapter{}},
		handlers.ErrorHandler{Writer: errorWriterAdapter{}},
		handlers.ContextWindowUpdatedHandler{Writer: contextWindowWriterAdapter{}},
		handlers.PermissionModeChangedHandler{Writer: permissionModeWriterAdapter{}},
		handlers.PlanUpdatedHandler{Writer: planWriterAdapter{}},
		handlers.CompactBoundaryHandler{Inspector: compactInspectorAdapter{}}
}

// sessionTransitionerAdapter 把 turn.SessionTransitioner 调到 chatSvc 的
// markSessionWaiting / markSessionRunning。需要持 *chatSvc 引用(单例方法,
// 不是 receiver-less helper)。
type sessionTransitionerAdapter struct{ svc *chatSvc }

func (a sessionTransitionerAdapter) MarkWaiting(ctx context.Context, sess any, stream string) {
	if a.svc == nil {
		return
	}
	s, ok := sess.(*chat_entity.Session)
	if !ok || s == nil {
		return
	}
	a.svc.markSessionWaiting(ctx, s, stream)
}

func (a sessionTransitionerAdapter) MarkRunning(ctx context.Context, sess any, stream string) {
	if a.svc == nil {
		return
	}
	s, ok := sess.(*chat_entity.Session)
	if !ok || s == nil {
		return
	}
	a.svc.markSessionRunning(ctx, s, stream)
}

// subagentFlipperAdapter 实现 turn.SubagentFlipper:把一个「不属于本轮」的后台任务
// 终态落到它派遣卡真正所在的那条更早的消息上。
//
// 三步一致地收尾一次后台完成,与自主续轮那条路(driveAutonomousTurn 收尾处)同款:
// 定向重写持久化态 → 会话级流镜像让已打开的界面即时翻转 → 退出 bgRunning 集合让
// 后台任务胶囊收敛。落库失败则整步放弃,不镜像也不清集合 —— 界面显示 completed 而
// 库里还是 running,重开会话又变回去,比一直显示 running 更难排查。
type subagentFlipperAdapter struct {
	svc    *chatSvc
	sess   *chat_entity.Session
	stream string
}

func (a subagentFlipperAdapter) FlipSubagentStatus(ctx context.Context, toolCallID, status, summary string) error {
	if a.svc == nil || a.sess == nil || a.sess.ID <= 0 || toolCallID == "" {
		return nil
	}
	// summary 是 CLI task_notification.summary(成功时子代理交回的报告,失败时中断
	// 原因)。空串读作「这一帧没带」,FlipSubagentStatus 对空 summary 保持原值不动。
	if err := chat_repo.Message().FlipSubagentStatus(ctx, a.sess.ID, toolCallID, status, summary); err != nil {
		return err
	}
	// 镜像到**会话级**流:派遣卡随更早那条消息早已落库,不在任何 liveBlocks 里,
	// per-turn 流那一路合并必然落空(同 subagentActivityEmitter 的理由)。
	(&dispatcherEmitter{svc: a.svc}).Emit(ctx, AutonomousStreamName(a.sess.ID), map[string]any{
		"kind":      string(StreamSubagentDone),
		"toolUseId": toolCallID,
		"info":      agentruntime.SubagentInfo{Status: status, Summary: summary},
	})
	a.svc.reconcileBgRunningOnComplete(ctx, a.sess, toolCallID, a.stream)
	return nil
}

// ResumeSubagentByTaskID 见 turn.SubagentFlipper:跨轮恢复时把更早那条消息里的派遣卡
// 推回运行态,交回它所在的 tool_call_id。仓储那一步找不到就交回空串,调用方据此退回
// 「在本轮另起一块」的既有行为。
func (a subagentFlipperAdapter) ResumeSubagentByTaskID(
	ctx context.Context, taskID, status string,
) (string, error) {
	if a.svc == nil || a.sess == nil || a.sess.ID <= 0 || taskID == "" {
		return "", nil
	}
	return chat_repo.Message().ResumeSubagentByTaskID(ctx, a.sess.ID, taskID, status)
}

// newTurnContext 构造每轮 turn 的 TurnContext。stream 由调用方填(每轮 chat
// session 不同)。
func (s *chatSvc) newTurnContext(
	assistantMsg *chat_entity.Message,
	sess *chat_entity.Session,
	stream string,
	backendType string,
) *turn.TurnContext {
	launch := ""
	if sess != nil {
		launch = sess.PermissionModeAtLaunch
	}
	tc := &turn.TurnContext{
		AssistantMsg:         assistantMsg,
		Session:              sess,
		Stream:               stream,
		BackendType:          backendType,
		LaunchPermissionMode: launch,
		SessionUpdater:       sessionUpdaterAdapter{},
		SessionTransitioner:  sessionTransitionerAdapter{svc: s},
		Waits:                turn.NewWaitTracker(),
		SubagentFlipper:      subagentFlipperAdapter{svc: s, sess: sess, stream: stream},
	}
	// 这一段 assistant 从此刻开始计时,生成表同时开走(工具空档由 tool 事件停/开)。
	tc.StartGenerationAt(time.Now())
	return tc
}
