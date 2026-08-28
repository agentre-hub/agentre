package chat_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// CancelQueued 撤回 Enqueue 投递但尚未被 AI 消费的排队消息。QueuedID 为空
// 表示清空整条队列。codex 后端 runner 不实现 SteerCanceler，会返
// ChatCancelUnsupported 让前端把 chip 的 X 渲染为锁图标。
//
// 返回错误码：
//   - ChatSessionNotFound / InvalidParameter
//   - ChatCancelUnsupported: 后端不实现 SteerCanceler
//   - ChatSteerNoActive:   turn 已结束或 runner 不再持有该 session
//   - ChatCancelNotFound:  非空 queuedID 但已被 AI 消费 / 不存在
//
// Stop 中断当前 turn。三件事按顺序做：
//
//  1. LoadAndDelete activeCancels —— 原子拿到 turnCtx 的 cancel；拿不到说明 turn
//     已自然完成 / 还没起 / 已被另一个 Stop 拉走，返 ChatStopNoActive。
//  2. Store aborted flag —— runTurn 收尾 LoadAndDelete 看到就走 StreamAborted 路径
//     并跳过 DrainPending 自动接续。
//  3. 先 cancel turnCtx，让已接受的本地 Pi 流立即进入自己的 bounded settlement
//     window；再以同一 generation 的内存 aborter 尝试写 abort。写端最多等待同一
//     500ms 边界，不能把 Stop 卡在满管道前。其它后端仍先 cancel，再尽力通过仓储
//     解析 runner.Abort。启动期还没绑定 aborter 时也保持 cancel-first，不给未确认
//     prompt settlement grace，也不让 Stop 的仓储查询延迟同步 preflight / SQL 取消。
func (s *chatSvc) Stop(ctx context.Context, req *StopRequest) (*StopResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	raw, ok := s.activeCancels.LoadAndDelete(req.SessionID)
	if !ok {
		// 内存里没有活跃 turn,两种情况:
		//   (a) turn 自然刚跑完、Stop 与收尾 race —— DB 已是 idle/error,无害。
		//   (b)「重启遗孤」:app crash / wails dev 热重载 / 第二实例 让 turn goroutine
		//      死了,但 DB agent_status 还停在 running/waiting(ResetActiveSessions
		//      只在主实例 Startup 后才扫,这些路径漏网),而 activeCancels(内存)已空。
		//      此时前端按 DB 状态把「停止」按钮亮着可点,旧逻辑直接返 ChatStopNoActive
		//      被前端静默吞掉 → 会话永远停不掉。这里 reconcile 回 idle 让停止生效。
		return s.reconcileOrphanStop(ctx, req.SessionID)
	}
	control, _ := raw.(*activeTurnControl)
	s.aborted.Store(req.SessionID, struct{}{})
	logger.Ctx(ctx).Info("chat_svc.Stop: aborting turn",
		zap.Int64("sessionId", req.SessionID))

	gracefulAborter, gracefulAbort := control.gracefulAborter()
	// Activation/staging SQL, prepared Start, and accepted stream drain all carry
	// this generation-specific context. Cancel first so a blocked abort write can
	// never delay SQL/pre-prompt cancellation or the accepted stream's settlement timer.
	if control != nil && control.cancel != nil {
		control.cancel()
	}
	if gracefulAbort {
		abortCtx, cancelAbort := context.WithTimeout(ctx, piStopAbortWriteBound)
		_, abortErr := gracefulAborter.Abort(abortCtx, req.SessionID, 0)
		cancelAbort()
		if abortErr != nil && !errors.Is(abortErr, agentruntime.ErrNoActiveTurn) {
			logger.Ctx(ctx).Warn("chat_svc.Stop: local Pi abort failed",
				zap.Int64("sessionId", req.SessionID),
				zap.String("backendType", string(agent_backend_entity.TypePiAgent)),
				zap.Error(abortErr))
		}
	}

	// Other backends keep the prior best-effort runner.Abort lookup after cancel.
	// A bound local Pi aborter is generation-specific and must not be redispatched
	// through the global runtime after its active control has been removed.
	if !gracefulAbort {
		if sess, err := chat_repo.Session().Find(ctx, req.SessionID); err == nil && sess != nil {
			if _, be, _, berr := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID); berr == nil && be != nil {
				// 中断没下发下去不致命(前面已 cancel turnCtx 兜底),布尔判据这里
				// 无人可报;失败的留底由 requestRuntimeAbort 自己记。
				_, _ = s.requestRuntimeAbort(ctx, be, req.SessionID, 0)
			}
		}
	}
	return &StopResponse{Stopped: true}, nil
}

// reconcileOrphanStop 处理「Stop 时内存里没有活跃**用户**轮」的情况:
//   - 会话查不到 / 已是终态(idle/error)→ turn 早就收尾,返 ChatStopNoActive(无害的
//     「太晚了」,前端静默)。
//   - 会话还停在 running/waiting,先问 runtime 有没有带外轮在飞(自主续轮 / 后台
//     subagent 活动轮不进 activeCancels,却是此刻真正活跃的那一轮)→ 有就中断它,再
//     按被中断轮的类型决定谁 reconcile 会话状态(决策 3):
//   - 中断的是自主轮 → 状态留给 driveAutonomousTurn 收尾(idle/error),Stop 不落库;
//   - 中断的是 subagent 活动轮 → driveSubagentActivity 不写会话状态,由这里自己把
//     running/waiting 翻回 idle 并持久化(复用遗孤路径的翻写逻辑),一次点停止即收干净;
//   - runtime 报 ErrNoActiveTurn / 解析不出 runner → 才是真「重启遗孤」→ 翻回 idle 并
//     落库(等同 abort 收尾),让前端那颗按 DB 状态一直亮着的「停止」按钮真能把会话停下来。
//
// 不去 emit StreamSessionStatus:遗孤会话没有活跃 stream 订阅(stream 名按
// sessionID+assistantMsgID 双键),推了也送不到;前端 doStop 成功后会主动 reload
// 把按钮收回去。
func (s *chatSvc) reconcileOrphanStop(ctx context.Context, sessionID int64) (*StopResponse, error) {
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil || sess == nil {
		return nil, i18n.NewError(ctx, code.ChatStopNoActive)
	}
	if sess.AgentStatus != "running" && sess.AgentStatus != "waiting" {
		return nil, i18n.NewError(ctx, code.ChatStopNoActive)
	}
	outcome, interrupted := s.abortOutOfBandTurn(ctx, sess)
	if interrupted {
		// 确有一轮被中断,会话仍在跑。中断的是 subagent 活动轮时,会话的
		// running/waiting 已无合法依据且那一轮不写状态 —— 由这里接管翻 idle 落库;
		// 中断的是自主轮时状态留给它自己收尾,不落库。
		if outcome.TurnKind == agentruntime.TurnKindSubagentActivity {
			if perr := s.reconcileSessionToIdle(ctx, sess); perr != nil {
				return nil, perr
			}
		}
		return &StopResponse{Stopped: true}, nil
	}
	if perr := s.reconcileSessionToIdle(ctx, sess); perr != nil {
		return nil, perr
	}
	return &StopResponse{Stopped: true}, nil
}

// reconcileSessionToIdle 把会话 running/waiting 翻回 idle 并清 attention,复用
// persistSessionStatus(重试一次 + 失败上抛不静默)。遗孤路径与「被中断的是 subagent
// 活动轮」的接管路径共用这一份翻写逻辑。
func (s *chatSvc) reconcileSessionToIdle(ctx context.Context, sess *chat_entity.Session) error {
	logger.Ctx(ctx).Info("chat_svc.Stop: reconciling session to idle",
		zap.Int64("sessionId", sess.ID),
		zap.String("prevStatus", sess.AgentStatus))
	sess.AgentStatus = "idle"
	sess.NeedsAttention = false
	return s.persistSessionStatus(ctx, sess)
}

// abortOutOfBandTurn 在「没有活跃用户轮」的前提下,把中断请求交给 runtime 试一次,
// 报告是否真有一轮被中断,以及被中断的那一轮的类型(AbortOutcome.TurnKind)。
//
// 带外轮(自主续轮 / 后台 subagent 活动轮)独占帧流期间不进 activeCancels —— 它就是
// 该会话此刻活跃的那一轮,而 Abort 的契约正是「中断该会话当前活跃的那一轮」,两者
// 都不活跃时才返回 ErrNoActiveTurn。因此这里可以直接拿 Abort 的返回值当判据:
// 中断成功 = 确有一轮被中断,会话仍在跑,调用方不能再把它当遗孤 reconcile 成 idle(那会
// 在 CLI 还在产帧时谎报 idle);ErrNoActiveTurn / 解析不出 runner = 内存里真的什么
// 都没有,交回遗孤路径。被中断轮的类型由 reconcileOrphanStop 拿来分流:自主轮留给它
// 自己收尾,subagent 活动轮则由 Stop 接管翻 idle(决策 3)。turnToken 固定传 0(中断
// 当前活跃的带外轮,等价旧行为)。
func (s *chatSvc) abortOutOfBandTurn(ctx context.Context, sess *chat_entity.Session) (agentruntime.AbortOutcome, bool) {
	_, be, _, err := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if err != nil || be == nil {
		logger.Ctx(ctx).Warn("chat_svc.Stop: cannot resolve backend to interrupt out-of-band turn",
			zap.Int64("sessionId", sess.ID), zap.Error(err))
		return agentruntime.AbortOutcome{}, false
	}
	outcome, ok := s.requestRuntimeAbort(ctx, be, sess.ID, 0)
	if !ok {
		return agentruntime.AbortOutcome{}, false
	}
	logger.Ctx(ctx).Info("chat_svc.Stop: interrupted out-of-band turn",
		zap.Int64("sessionId", sess.ID),
		zap.String("backendType", be.Type),
		zap.String("turnKind", string(outcome.TurnKind)),
		zap.String("prevStatus", sess.AgentStatus))
	return outcome, true
}

// requestRuntimeAbort 把「中断该会话当前活跃的那一轮」尽力下发给 runtime,报告是否
// 真有一轮被中断以及被中断轮的类型:Abort 返 nil = 确有一轮被中断(AbortOutcome 携带
// 轮类型);ErrNoActiveTurn / 解析不出 runner / runner 不支持中断 = 内存里没有可中断
// 的轮(返回零值 outcome + false)。任何一步失败都只记日志、不返回错误 —— 三个调用方
// (Stop 里活跃用户轮的 best-effort 中断、Stop 的遗孤路径 abortOutOfBandTurn、自主续轮
// 落库失败处置 failAutonomousTurnPersist)要么只要这个布尔判据 + 轮类型、要么连它都
// 不要,失败各自另有兜底(已 cancel turnCtx / 交回遗孤 reconcile / 前两步的可观察结果
// 已经产生)。
//
// **会阻塞**:claudecode 的 Abort 写完 control_request 后要等 CLI 的 control_response,
// 而那条回执要常驻 readLoop 前进才派发得了。调用方若同时还担着「让帧流继续被消费」的
// 责任(failAutonomousTurnPersist 的抽干),必须以非阻塞方式调用,否则两者互相等着。
func (s *chatSvc) requestRuntimeAbort(ctx context.Context, be *agent_backend_entity.AgentBackend, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, bool) {
	backendType := ""
	if be != nil {
		backendType = be.Type
	}
	runner, err := s.selectRunner(ctx, be, sessionID)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.requestRuntimeAbort: selectRunner failed, cannot interrupt turn",
			zap.Int64("sessionId", sessionID), zap.String("backendType", backendType), zap.Error(err))
		return agentruntime.AbortOutcome{}, false
	}
	aborter, ok := runner.(agentruntime.Aborter)
	if !ok {
		return agentruntime.AbortOutcome{}, false
	}
	outcome, aerr := aborter.Abort(ctx, sessionID, turnToken)
	if aerr != nil {
		if !errors.Is(aerr, agentruntime.ErrNoActiveTurn) {
			logger.Ctx(ctx).Warn("chat_svc.requestRuntimeAbort: runner.Abort failed",
				zap.Int64("sessionId", sessionID), zap.String("backendType", backendType), zap.Error(aerr))
		}
		return agentruntime.AbortOutcome{}, false
	}
	return outcome, true
}

// StopBackgroundTask 停掉某个后台任务 / 子 agent(run_in_background),而不是中断整个 turn。
// 流程:
//  1. 按 toolCallID 从持久化 subagent_state 读出 CLI task_id + 当前状态;
//  2. 已终态 / 找不到 overlay → 幂等成功(任务已不在跑,前端 reload 自然对齐);
//  3. 缺 task_id(老会话的块没记)→ ChatStopBgTaskUnknown,让前端提示;
//  4. resolve runner,断言 BackgroundTaskStopper(否则 ChatStopBgUnsupported,正常已被
//     capability 位挡在前端),下发 stop_task;
//  5. 成功后主动把块 flip 成 "canceled" —— 前端 reload 立即显示「已停止」;CLI 停任务后
//     另发的 task_notification(canceled/failed)经既有自主轮再幂等收敛一次。
func (s *chatSvc) StopBackgroundTask(ctx context.Context, req *StopBackgroundTaskRequest) (*StopBackgroundTaskResponse, error) {
	if req == nil || req.SessionID <= 0 || req.ToolCallID == "" {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}

	taskID, status, found, err := chat_repo.Message().FindSubagentState(ctx, req.SessionID, req.ToolCallID)
	if err != nil {
		return nil, err
	}
	if !found || (status != "" && status != "running") {
		// 任务已终态 / 无 overlay:当已停处理,幂等。
		return &StopBackgroundTaskResponse{Stopped: true}, nil
	}
	if taskID == "" {
		return nil, i18n.NewError(ctx, code.ChatStopBgTaskUnknown)
	}

	_, be, _, berr := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if berr != nil {
		return nil, berr
	}
	runner, rerr := s.selectRunner(ctx, be, sess.ID)
	if rerr != nil {
		return nil, rerr
	}
	stopper, ok := runner.(agentruntime.BackgroundTaskStopper)
	if !ok {
		return nil, i18n.NewError(ctx, code.ChatStopBgUnsupported)
	}

	logger.Ctx(ctx).Info("chat_svc.StopBackgroundTask: stopping background task",
		zap.Int64("sessionId", req.SessionID),
		zap.String("toolUseId", req.ToolCallID),
		zap.String("taskId", taskID))

	if serr := stopper.StopBackgroundTask(ctx, req.SessionID, taskID); serr != nil {
		if errors.Is(serr, agentruntime.ErrNoActiveTurn) {
			// 子进程已 evict → 任务随之消失,当已停处理(幂等)。
			return &StopBackgroundTaskResponse{Stopped: true}, nil
		}
		logger.Ctx(ctx).Warn("chat_svc.StopBackgroundTask: runner stop failed",
			zap.Int64("sessionId", req.SessionID),
			zap.String("taskId", taskID),
			zap.Error(serr))
		return nil, i18n.NewError(ctx, code.ChatStopInternal)
	}

	// 主动翻 canceled(summary 留空:不写后端硬编码文案,「已停止」由前端 StatusPill 出 i18n)。
	if ferr := chat_repo.Message().FlipSubagentStatus(ctx, req.SessionID, req.ToolCallID, "canceled", ""); ferr != nil {
		logger.Ctx(ctx).Warn("chat_svc.StopBackgroundTask: flip subagent_state failed",
			zap.Int64("sessionId", req.SessionID),
			zap.String("toolUseId", req.ToolCallID),
			zap.Error(ferr))
	}
	return &StopBackgroundTaskResponse{Stopped: true}, nil
}
