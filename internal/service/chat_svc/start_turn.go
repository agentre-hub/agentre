package chat_svc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// turnStart 承载 startTurn 一次开轮期间的全部可变状态。字段逐一对应原先散在
// startTurn 函数体里的 local;每个阶段方法内部保留原来的清理与解锁,失败时把
// 错误原样交回 startTurn 返回,分支顺序与原函数一致。
type turnStart struct {
	svc *chatSvc

	sess        *chat_entity.Session
	a           *agent_entity.Agent
	be          *agent_backend_entity.AgentBackend
	prov        *llm_provider_entity.LLMProvider
	userBlocks  []blocks.ContentBlock
	preTx       func(txCtx context.Context) error
	replacement *transcriptReplacementLifecycle
	forkAnchor  string
	extras      turnExtras

	lock         *trylockMutex
	userMsg      *chat_entity.Message
	assistantMsg *chat_entity.Message

	prepared                *preparedTurnRun
	turnCtx                 context.Context
	cancel                  context.CancelFunc
	stopRequestCancel       func() bool
	turnControl             *activeTurnControl
	startupCancelRegistered bool
}

// buildMessages 钉住本轮执行目标,并组出 user / assistant 两行(此时尚未落库)。
func (ts *turnStart) buildMessages(ctx context.Context) error {
	// 首轮实际落在这一档（R15b / 决策36）：会话已经钉住就是 no-op,没钉住就在这里
	// 钉住并写回——这是"没值涵盖首轮与全部老会话"里唯一的写点(本机档;远端档由
	// recordExecDaemon 在下面 prepareTurnRun / runTurn 实际 borrow 到 runtime 时写)。
	ts.svc.pinExecTargetIfUnset(ctx, ts.sess, ts.be)

	ts.userMsg = &chat_entity.Message{SessionID: ts.sess.ID, Role: "user", DeviceFingerprint: ts.be.DeviceFingerprint}
	_ = ts.userMsg.SetBlocks(ts.userBlocks)
	if err := persistPeerMessageSource(ts.userMsg, ts.extras.peerSource); err != nil {
		ts.lock.Unlock()
		return operationFailedWithCause(ctx, err,
			zap.Int64("sessionId", ts.sess.ID),
			zap.String("sourceDevice", ts.extras.peerSource.Device))
	}

	// 解析本轮执行侧配置（EffectiveLLMConfig v1 seam）：provider-default 在 turn 入口
	// 解析 Provider 当前默认模型，assistantMsg.Model 用解析出的 ModelID 占位（真正执行
	// 后由 result.Model 覆盖）。远端 backend 由 daemon 自家解析，desktop 不解析、不发
	// 本地结果（effectiveLLMForNonRemoteTurn 返回 nil）。
	cfg, err := ts.svc.effectiveLLMForNonRemoteTurn(ctx, ts.sess, ts.be, ts.prov)
	if err != nil {
		ts.lock.Unlock()
		return err
	}
	model := ""
	if cfg != nil {
		model = cfg.ModelID
	}
	ts.assistantMsg = &chat_entity.Message{
		SessionID:         ts.sess.ID,
		DeviceFingerprint: ts.be.DeviceFingerprint,
		Role:              "assistant",
		BlocksJSON:        "[]",
		Model:             model,
	}
	return nil
}

func (ts *turnStart) clearSynchronousTurn() {
	if ts.stopRequestCancel != nil {
		ts.stopRequestCancel()
		ts.stopRequestCancel = nil
	}
	if ts.cancel != nil {
		ts.cancel()
	}
	if ts.startupCancelRegistered {
		ts.svc.activeCancels.CompareAndDelete(ts.sess.ID, ts.turnControl)
		ts.svc.aborted.Delete(ts.sess.ID)
		ts.startupCancelRegistered = false
	}
}

// piPreflight 让 Pi 在事务之前备好 RPC 进程(必要时 fork)但扣住 prompt。
func (ts *turnStart) piPreflight(ctx context.Context) error {
	// Pi prepares/restores its RPC process and, when requested, forks before the
	// transaction, but deliberately withholds the prompt. Register cancellation
	// before preflight so Stop and request cancellation reach both phases.
	if ts.replacement != nil && ts.be.IsPiAgent() {
		runCtx := db.WithContextDB(context.Background(), db.Ctx(ctx))
		ts.turnCtx, ts.cancel = context.WithCancel(runCtx)
		ts.turnControl = &activeTurnControl{cancel: ts.cancel}
		ts.stopRequestCancel = context.AfterFunc(ctx, ts.cancel)
		ts.svc.activeCancels.Store(ts.sess.ID, ts.turnControl)
		ts.startupCancelRegistered = true
		var err error
		ts.prepared, err = ts.svc.prepareTurnRun(ts.turnCtx, ts.sess, ts.a, ts.be, ts.prov, ts.userMsg, ts.assistantMsg, ts.forkAnchor, false, true)
		if err != nil {
			ts.clearSynchronousTurn()
			ts.lock.Unlock()
			logger.Ctx(ctx).Warn("chat_svc.startTurn: pi fork startup failed",
				zap.Int64("sessionId", ts.sess.ID),
				zap.Int64("agentId", ts.a.ID),
				zap.String("backendType", ts.be.Type),
				zap.String("forkAnchor", ts.forkAnchor),
				zap.String("errorType", fmt.Sprintf("%T", err)))
			return err
		}
	}

	return nil
}

// persistTurnMessages 把本轮两行消息与 running 状态落进一个事务;Pi 转录替换
// 路径走另一条分支,先取 pre-prompt 身份再激活替换。
func (ts *turnStart) persistTurnMessages(ctx context.Context) error {
	if ts.replacement == nil {
		ts.sess.AgentStatus = "running"
		if err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
			txCtx := db.WithContextDB(ctx, tx)
			if ts.preTx != nil {
				if err := ts.preTx(txCtx); err != nil {
					return err
				}
			}
			nextSeq, err := chat_repo.Message().NextSeq(txCtx, ts.sess.ID)
			if err != nil {
				return err
			}
			ts.userMsg.Seq = nextSeq
			if err := chat_repo.Message().Create(txCtx, ts.userMsg); err != nil {
				return err
			}
			ts.assistantMsg.Seq = nextSeq + 1
			if err := chat_repo.Message().Create(txCtx, ts.assistantMsg); err != nil {
				return err
			}
			ts.sess.LastMessageAt = time.Now().UnixMilli()
			return chat_repo.Session().Update(txCtx, ts.sess)
		}); err != nil {
			ts.lock.Unlock()
			return operationFailedWithCause(ctx, err,
				zap.Int64("sessionId", ts.sess.ID),
				zap.Int64("agentId", ts.a.ID),
				zap.String("backendType", ts.be.Type))
		}
	} else {
		providerSessionID, err := ts.prepared.providerSessionIDBeforeStart()
		if err != nil {
			ts.clearSynchronousTurn()
			ts.svc.discardPreparedTurn(ts.sess.ID, ts.prepared)
			ts.lock.Unlock()
			return operationFailedWithCause(ctx, err,
				zap.Int64("sessionId", ts.sess.ID),
				zap.Int64("agentId", ts.a.ID),
				zap.String("backendType", ts.be.Type))
		}
		runningSession := *ts.sess
		runningSession.AgentStatus = "running"
		runningSession.LastMessageAt = time.Now().UnixMilli()
		runningSession.SetProviderSession(providerSessionID)
		if err := db.Ctx(ts.turnCtx).Transaction(func(tx *gorm.DB) error {
			txCtx := db.WithContextDB(ts.turnCtx, tx)
			if err := ts.replacement.activate(txCtx, ts.sess, providerSessionID, ts.userMsg, ts.assistantMsg); err != nil {
				return err
			}
			return chat_repo.Session().Update(txCtx, &runningSession)
		}); err != nil {
			ts.clearSynchronousTurn()
			ts.svc.discardPreparedTurn(ts.sess.ID, ts.prepared)
			ts.lock.Unlock()
			return operationFailedWithCause(ctx, err,
				zap.Int64("sessionId", ts.sess.ID),
				zap.Int64("agentId", ts.a.ID),
				zap.String("backendType", ts.be.Type))
		}
		*ts.sess = runningSession
	}
	return nil
}

// startPreparedRun 把扣住的 prompt 真正发出去,并收尾 Pi 转录替换。
func (ts *turnStart) startPreparedRun(ctx context.Context) error {
	if ts.prepared != nil {
		if err := ts.prepared.start(ts.turnCtx); err != nil {
			mappingSession := *ts.sess
			mappingSession.ProviderSessionID = ""
			err = ts.svc.mapTurnError(ctx, &mappingSession, ts.be, err)
			ts.clearSynchronousTurn()
			ts.svc.discardPreparedTurn(ts.sess.ID, ts.prepared)
			if restoreErr := ts.svc.restoreTranscriptReplacement(ctx, ts.replacement, ts.sess); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore Pi transcript: %w", restoreErr))
			}
			ts.lock.Unlock()
			logger.Ctx(ctx).Warn("chat_svc.startTurn: pi prompt startup failed",
				zap.Int64("sessionId", ts.sess.ID),
				zap.Int64("agentId", ts.a.ID),
				zap.String("backendType", ts.be.Type),
				zap.String("forkAnchor", ts.forkAnchor),
				zap.String("errorType", fmt.Sprintf("%T", err)))
			return err
		}
		if finalizeErr := ts.svc.finalizeTranscriptReplacement(ctx, ts.replacement); finalizeErr != nil {
			ts.clearSynchronousTurn()
			ts.svc.discardPreparedTurn(ts.sess.ID, ts.prepared)
			if ts.replacement.recovery.State == chat_repo.ReplacementRecoveryPending {
				if restoreErr := ts.svc.restoreTranscriptReplacement(ctx, ts.replacement, ts.sess); restoreErr != nil {
					finalizeErr = errors.Join(finalizeErr, fmt.Errorf("restore Pi transcript: %w", restoreErr))
				}
			} else {
				ts.sess.AgentStatus = "error"
				ts.sess.ApplyDerivedFields()
				recoveryCtx, cancelRecovery := replacementRecoveryContext(ctx)
				if statusErr := chat_repo.Session().Update(recoveryCtx, ts.sess); statusErr != nil {
					finalizeErr = errors.Join(finalizeErr, fmt.Errorf("persist failed Pi turn status: %w", statusErr))
				}
				cancelRecovery()
			}
			ts.lock.Unlock()
			return operationFailedWithCause(ctx, finalizeErr,
				zap.Int64("sessionId", ts.sess.ID),
				zap.Int64("agentId", ts.a.ID),
				zap.String("backendType", ts.be.Type),
				zap.String("recoveryState", string(ts.replacement.recovery.State)))
		}
		if ts.stopRequestCancel != nil {
			ts.stopRequestCancel()
			ts.stopRequestCancel = nil
		}
	}
	return nil
}

// emitTurnStarted 给非查看者发起的轮补一发会话级旁路事件,让已打开的面板接流。
func (ts *turnStart) emitTurnStarted(ctx context.Context, stream string) {
	// 非查看者发起的轮(群成员轮经 scheduler dispatch):per-turn 流名只有发起者能从
	// Send 响应拿到,该会话已打开(可能在后台)的 ChatPanel 拿不到 → 不接流、不翻 running。
	// 复用 autonomous 会话级旁路把流名 + 新 assistant 行推给它,让它走与自主轮相同的
	// openStream 路径实时渲染。前端 Send 默认不带此标志,避免发起者重复 openStream 双开流。
	if ts.extras.peerSource.Device != "" {
		ts.svc.publishPeerEvent(ts.sess.ID, agentruntime.UserMessageEvent{
			Text:             firstTextBlock(ts.userBlocks),
			SourceDevice:     ts.extras.peerSource.Device,
			SourceDeviceName: ts.extras.peerSource.Name,
		})
	}
	if ts.extras.emitTurnStartedBypass {
		var userMessages []ChatMessage
		if ts.extras.peerSource.Device != "" {
			if user := chatMessageForEvent(ts.sess, ts.userMsg); user != nil {
				userMessages = []ChatMessage{*user}
			}
		}
		ts.svc.emitter.Emit(ctx, AutonomousStreamName(ts.sess.ID), ChatStreamEvent{
			Kind:             StreamAutonomousStarted,
			Stream:           stream,
			UserMessages:     userMessages,
			AssistantMessage: chatMessageForEvent(ts.sess, ts.assistantMsg),
		})
	}
}

// dispatchTurn 注册取消入口,并把本轮真正交给后台 goroutine 执行。
func (ts *turnStart) dispatchTurn(ctx context.Context, stream string) {
	ts.svc.markStreamRunningForTest(ts.assistantMsg.ID)
	if ts.prepared == nil {
		runCtx := db.WithContextDB(context.Background(), db.Ctx(ctx))
		ts.turnCtx, ts.cancel = context.WithCancel(runCtx)
		ts.turnControl = &activeTurnControl{cancel: ts.cancel}
		// Non-prepared turns become cancellable immediately before async dispatch.
		ts.svc.activeCancels.Store(ts.sess.ID, ts.turnControl)
	}
	// Prepared Pi turns were registered before synchronous preflight; all other
	// turns are registered above. Either way Stop can cancel before gogo.Go runs.
	gogo.Go(func() error {
		// defer 顺序：LIFO。先注册 unlock，最后释放；中间的 cancel cleanup
		// 跑在 lock 还持有期间，新 turn 起不来 → 直接 Delete 安全。
		defer ts.lock.Unlock()
		defer ts.svc.markStreamDoneForTest(ts.assistantMsg.ID)
		defer func() {
			ts.svc.activeCancels.CompareAndDelete(ts.sess.ID, ts.turnControl)
			ts.cancel() // 兜底：runTurn 自己没 cancel（正常完成路径）也补一刀，无副作用
		}()
		ts.svc.runTurn(ts.turnCtx, ts.sess, ts.a, ts.be, ts.prov, ts.userMsg, ts.assistantMsg, stream, ts.forkAnchor, false, ts.prepared, ts.extras)
		return nil
	}, gogo.WithIgnorePanic())
}
