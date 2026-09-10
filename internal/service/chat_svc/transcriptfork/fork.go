// Package transcriptfork 持有「重发 / 编辑一轮时把转录回退到分叉点」这一套:
// 隐藏命名空间里的原行保全与恢复(Lifecycle)、失败回滚、成功收尾,以及各 backend
// 自家会话该从哪个锚点分叉(pi 的 fork anchor / codex 的 rollback anchor)。
//
// 它从 chat_svc 拆出来的判据是无状态:整套流程只读写仓储与传进来的实体,一个
// chatSvc 字段都不碰,因此这里全是包级函数,没有需要装配的宿主端口。
package transcriptfork

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/i18n"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/svcerr"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// Lifecycle owns one Pi replacement generation. Its marker-derived hidden
// namespace preserves the exact original rows until the prepared process
// acknowledges the prompt.
type Lifecycle struct {
	sessionID        int64
	fromSeq          int
	requestMessageID int64
	recovery         *chat_repo.ReplacementRecovery
}

// RecoveryTimeout 是恢复/清理这类收尾动作自带的上限：调用方的 ctx 此时往往已经取消。
const RecoveryTimeout = 5 * time.Second

// RecoveryState 报告这次替换的恢复记录此刻处于哪一态。宿主据此区分「还没被 prepared
// 进程认走(pending)→ 回滚原行」与「已认走 → 只把会话置错」两条收尾路径。
func (r *Lifecycle) RecoveryState() chat_repo.ReplacementRecoveryState {
	return r.recovery.State
}

func ReplaceTextPreserveImages(text string, old []blocks.ContentBlock) []blocks.ContentBlock {
	out := []blocks.ContentBlock{&blocks.TextBlock{Text: text}}
	for _, b := range old {
		switch img := b.(type) {
		case blocks.ImageBlock:
			out = append(out, img)
		case *blocks.ImageBlock:
			if img != nil {
				out = append(out, img)
			}
		}
	}
	return out
}

func NewLifecycle(sessionID int64, fromSeq int, requestMessageID int64) *Lifecycle {
	return &Lifecycle{
		sessionID:        sessionID,
		fromSeq:          fromSeq,
		requestMessageID: requestMessageID,
	}
}

func (r *Lifecycle) Activate(
	txCtx context.Context,
	sess *chat_entity.Session,
	providerSessionID string,
	userMsg, assistantMsg *chat_entity.Message,
) error {
	if r == nil || sess == nil {
		return nil
	}
	recovery := &chat_repo.ReplacementRecovery{
		SessionID:            r.sessionID,
		FromSeq:              r.fromSeq,
		RequestMessageID:     r.requestMessageID,
		OldProviderSessionID: sess.ProviderSessionID,
		NewProviderSessionID: providerSessionID,
		OldAgentStatus:       sess.AgentStatus,
		OldLastMessageAt:     sess.LastMessageAt,
		State:                chat_repo.ReplacementRecoveryPending,
	}
	recoverySessionID, err := chat_repo.ReplacementRecoverySessionID(r.sessionID)
	if err != nil {
		return err
	}
	recovery.RecoverySessionID = recoverySessionID
	if err := chat_repo.EnsureReplacementRecoveryNamespaceAvailable(txCtx, r.sessionID); err != nil {
		return err
	}
	if err := chat_repo.SaveReplacementRecovery(txCtx, recovery); err != nil {
		return err
	}
	if _, err := chat_repo.MoveMessagesFromSeq(txCtx, r.sessionID, recovery.RecoverySessionID, r.fromSeq); err != nil {
		return err
	}

	userMsg.SessionID = r.sessionID
	userMsg.Seq = r.fromSeq
	if err := transcript_repo.Message().Create(txCtx, userMsg); err != nil {
		return err
	}
	assistantMsg.SessionID = r.sessionID
	assistantMsg.Seq = r.fromSeq + 1
	if err := transcript_repo.Message().Create(txCtx, assistantMsg); err != nil {
		return err
	}
	recovery.UserMessageID = userMsg.ID
	recovery.AssistantMessageID = assistantMsg.ID
	if err := chat_repo.SaveReplacementRecovery(txCtx, recovery); err != nil {
		return err
	}
	r.recovery = recovery
	return nil
}

func RecoveryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := db.WithContextDB(context.Background(), db.Ctx(ctx))
	return context.WithTimeout(base, RecoveryTimeout)
}

func Restore(
	ctx context.Context,
	replacement *Lifecycle,
	sess *chat_entity.Session,
) error {
	if replacement == nil || replacement.recovery == nil {
		return nil
	}
	recoveryCtx, cancel := RecoveryContext(ctx)
	defer cancel()
	recovery := replacement.recovery
	if err := db.Ctx(recoveryCtx).Transaction(func(tx *gorm.DB) error {
		txCtx := db.WithContextDB(recoveryCtx, tx)
		if err := chat_repo.EnsureReplacementActiveTailOwned(txCtx, recovery); err != nil {
			return err
		}
		if err := chat_repo.RestoreReplacementSession(txCtx, recovery); err != nil {
			return err
		}
		deleted, err := chat_repo.DeleteOwnedReplacementMessages(
			txCtx, recovery.SessionID, recovery.UserMessageID, recovery.AssistantMessageID,
		)
		if err != nil {
			return err
		}
		if deleted != 2 {
			return chat_repo.ErrReplacementOwnershipLost
		}
		moved, err := chat_repo.MoveMessagesFromSeq(
			txCtx, recovery.RecoverySessionID, recovery.SessionID, recovery.FromSeq,
		)
		if err != nil {
			return err
		}
		if moved == 0 {
			return chat_repo.ErrReplacementOwnershipLost
		}
		deleted, err = chat_repo.DeleteReplacementRecovery(txCtx, recovery.SessionID)
		if err != nil {
			return err
		}
		if deleted != 1 {
			return chat_repo.ErrReplacementOwnershipLost
		}
		return nil
	}); err != nil {
		return err
	}
	sess.ProviderSessionID = recovery.OldProviderSessionID
	sess.AgentStatus = recovery.OldAgentStatus
	sess.LastMessageAt = recovery.OldLastMessageAt
	sess.ApplyDerivedFields()
	return nil
}

func cleanupRecovery(
	ctx context.Context,
	recovery *chat_repo.ReplacementRecovery,
) error {
	recoveryCtx, cancel := RecoveryContext(ctx)
	defer cancel()
	return db.Ctx(recoveryCtx).Transaction(func(tx *gorm.DB) error {
		deleted, err := chat_repo.DeleteReplacementRecovery(
			db.WithContextDB(recoveryCtx, tx), recovery.SessionID,
		)
		if err != nil {
			return err
		}
		if deleted == 0 {
			return chat_repo.ErrReplacementOwnershipLost
		}
		return nil
	})
}

// reconcileTranscriptReplacement is the single session-level recovery boundary
// for a session that may still own a Pi replacement marker. Pi activation writes
// AgentStatus=running and the new provider session atomically with the marker, so
// that durable state keeps the gate active even if the configured backend changes.
// Callers hold the session turn lock before entering it.
func Reconcile(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
) (bool, error) {
	if sess == nil || be == nil ||
		(!be.IsPiAgent() && (sess.AgentStatus != "running" || !sess.HasProviderSession())) {
		return false, nil
	}
	recovery, err := chat_repo.FindReplacementRecoveryForSession(ctx, sess.ID)
	if err != nil || recovery == nil {
		return false, err
	}
	if recovery.State == chat_repo.ReplacementRecoveryAcknowledged {
		if sess.ProviderSessionID != recovery.NewProviderSessionID {
			return false, chat_repo.ErrReplacementOwnershipLost
		}
		return true, cleanupRecovery(ctx, recovery)
	}
	replacement := &Lifecycle{
		sessionID:        recovery.SessionID,
		fromSeq:          recovery.FromSeq,
		requestMessageID: recovery.RequestMessageID,
		recovery:         recovery,
	}
	if err := Restore(ctx, replacement, sess); err != nil {
		return false, err
	}
	return true, nil
}

func Finalize(
	ctx context.Context,
	replacement *Lifecycle,
) error {
	if replacement == nil || replacement.recovery == nil {
		return nil
	}
	recoveryCtx, cancel := RecoveryContext(ctx)
	defer cancel()
	recovery := replacement.recovery
	var acknowledgeErr error
	for range 2 {
		candidate := *recovery
		acknowledgeErr = db.Ctx(recoveryCtx).Transaction(func(tx *gorm.DB) error {
			return chat_repo.AcknowledgeReplacementRecovery(db.WithContextDB(recoveryCtx, tx), &candidate)
		})
		if acknowledgeErr == nil {
			recovery.State = chat_repo.ReplacementRecoveryAcknowledged
			break
		}
	}
	if acknowledgeErr != nil {
		return fmt.Errorf("acknowledge Pi transcript recovery: %w", acknowledgeErr)
	}
	if err := db.Ctx(recoveryCtx).Transaction(func(tx *gorm.DB) error {
		deleted, err := chat_repo.DeleteReplacementRecovery(
			db.WithContextDB(recoveryCtx, tx), recovery.SessionID,
		)
		if err != nil {
			return err
		}
		if deleted == 0 {
			return chat_repo.ErrReplacementOwnershipLost
		}
		return nil
	}); err != nil {
		return fmt.Errorf("cleanup Pi transcript recovery: %w", err)
	}
	return nil
}

func MessageHasImage(m *chat_entity.Message) bool {
	bs, err := m.GetBlocks()
	if err != nil {
		return false
	}
	for _, b := range bs {
		switch img := b.(type) {
		case blocks.ImageBlock:
			return true
		case *blocks.ImageBlock:
			if img != nil {
				return true
			}
		}
	}
	return false
}

// backendForkAnchor 是 Regenerate / Edit 共享的"按后端类型决定 fork 锚点"分流逻辑。
// claudecode 首轮 user msg 没有 anchor 时会清空 sess.ProviderSessionID，让上层
// startTurn → runner 当作新建会话发起；Pi 只有明确失败且从未建立原生会话的首轮
// 才能无 fork 重试，已经建立过上下文的会话丢失 provider ID 后必须 fail closed。
func BackendForkAnchor(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	userMsg *chat_entity.Message,
) (string, error) {
	if !sess.HasProviderSession() {
		if be.IsPiAgent() {
			failedFirstTurn, err := isFailedFirstPiTurn(ctx, sess, userMsg)
			if err != nil {
				return "", err
			}
			if !failedFirstTurn {
				return "", i18n.NewError(ctx, code.ChatProviderSessionGone)
			}
		}
		return "", nil
	}
	switch agent_backend_entity.BackendType(be.Type) {
	case agent_backend_entity.TypeBuiltin:
		return "", nil
	case agent_backend_entity.TypeClaudeCode:
		anchor := userMsg.ForkAnchor
		if anchor == "" {
			sess.SetProviderSession("")
		}
		return anchor, nil
	case agent_backend_entity.TypeCodex:
		return codexRollbackAnchor(ctx, sess, userMsg)
	case agent_backend_entity.TypePiAgent:
		anchor, ok := normalizedPiForkAnchor(userMsg)
		if !ok {
			return "", i18n.NewError(ctx, code.ChatRegenerateNoUserAnchor)
		}
		return anchor, nil
	default:
		runner := agentruntime.RuntimeFor(agent_backend_entity.BackendType(be.Type))
		if _, ok := runner.(agentruntime.Rewinder); !ok {
			return "", i18n.NewError(ctx, code.ChatRegenerateUnsupported)
		}
		return "", nil
	}
}

func normalizedPiForkAnchor(userMsg *chat_entity.Message) (string, bool) {
	if userMsg == nil || userMsg.ForkAnchor == "" || strings.TrimSpace(userMsg.ForkAnchor) != userMsg.ForkAnchor {
		return "", false
	}
	// Entry IDs are opaque native Pi identities. Reject malformed persisted values
	// instead of trimming them into a different provider identity.
	return userMsg.ForkAnchor, true
}

func isFailedFirstPiTurn(
	ctx context.Context,
	sess *chat_entity.Session,
	userMsg *chat_entity.Message,
) (bool, error) {
	_, hasForkAnchor := normalizedPiForkAnchor(userMsg)
	if sess == nil || userMsg == nil || hasForkAnchor {
		return false, nil
	}
	messages, err := transcript_repo.Message().List(ctx, sess.ID)
	if err != nil {
		return false, operationFailedWithCause(ctx, err)
	}
	if len(messages) != 2 {
		return false, nil
	}
	var firstUser, failedAssistant *chat_entity.Message
	for _, message := range messages {
		switch message.Role {
		case "user":
			if firstUser != nil {
				return false, nil
			}
			firstUser = message
		case "assistant":
			if failedAssistant != nil {
				return false, nil
			}
			failedAssistant = message
		default:
			return false, nil
		}
	}
	if firstUser == nil || failedAssistant == nil || firstUser.ID != userMsg.ID ||
		firstUser.Seq >= failedAssistant.Seq || strings.TrimSpace(failedAssistant.ErrorText) == "" {
		return false, nil
	}
	assistantBlocks, err := failedAssistant.GetBlocks()
	if err != nil {
		return false, i18n.NewError(ctx, code.ChatBlocksMalformed)
	}
	if len(assistantBlocks) != 0 {
		return false, nil
	}
	return true, nil
}

func codexRollbackAnchor(ctx context.Context, sess *chat_entity.Session, userMsg *chat_entity.Message) (string, error) {
	msgs, err := transcript_repo.Message().List(ctx, sess.ID)
	if err != nil {
		return "", operationFailedWithCause(ctx, err)
	}
	numTurns := 0
	for _, m := range msgs {
		if m.Seq >= userMsg.Seq && m.Role == "user" {
			numTurns++
		}
	}
	if numTurns <= 0 {
		return "", i18n.NewError(ctx, code.ChatRegenerateNoUserAnchor)
	}
	return strconv.Itoa(numTurns), nil
}

// forkErrors 是本包的错误报告口。CallerSkip=1 抵消下面那层薄 wrapper,
// 让日志的 caller 字段仍然指向真正的业务调用点。
var forkErrors = svcerr.Reporter{LogMessage: "chat_svc.transcriptfork: operation failed", CallerSkip: 1}

func operationFailedWithCause(ctx context.Context, cause error, fields ...zap.Field) error {
	return forkErrors.OperationFailedWithCause(ctx, cause, fields...)
}
