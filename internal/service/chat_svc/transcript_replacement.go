package chat_svc

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/i18n"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

func replaceTextPreserveImages(text string, old []blocks.ContentBlock) []blocks.ContentBlock {
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

func newTranscriptReplacementLifecycle(sessionID int64, fromSeq int, requestMessageID int64) *transcriptReplacementLifecycle {
	return &transcriptReplacementLifecycle{
		sessionID:        sessionID,
		fromSeq:          fromSeq,
		requestMessageID: requestMessageID,
	}
}

func (r *transcriptReplacementLifecycle) activate(
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
	if err := chat_repo.Message().Create(txCtx, userMsg); err != nil {
		return err
	}
	assistantMsg.SessionID = r.sessionID
	assistantMsg.Seq = r.fromSeq + 1
	if err := chat_repo.Message().Create(txCtx, assistantMsg); err != nil {
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

func replacementRecoveryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := db.WithContextDB(context.Background(), db.Ctx(ctx))
	return context.WithTimeout(base, transcriptRecoveryTimeout)
}

func (s *chatSvc) restoreTranscriptReplacement(
	ctx context.Context,
	replacement *transcriptReplacementLifecycle,
	sess *chat_entity.Session,
) error {
	if replacement == nil || replacement.recovery == nil {
		return nil
	}
	recoveryCtx, cancel := replacementRecoveryContext(ctx)
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

func (s *chatSvc) cleanupTranscriptReplacementRecovery(
	ctx context.Context,
	recovery *chat_repo.ReplacementRecovery,
) error {
	recoveryCtx, cancel := replacementRecoveryContext(ctx)
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
func (s *chatSvc) reconcileTranscriptReplacement(
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
		return true, s.cleanupTranscriptReplacementRecovery(ctx, recovery)
	}
	replacement := &transcriptReplacementLifecycle{
		sessionID:        recovery.SessionID,
		fromSeq:          recovery.FromSeq,
		requestMessageID: recovery.RequestMessageID,
		recovery:         recovery,
	}
	if err := s.restoreTranscriptReplacement(ctx, replacement, sess); err != nil {
		return false, err
	}
	return true, nil
}

func (s *chatSvc) finalizeTranscriptReplacement(
	ctx context.Context,
	replacement *transcriptReplacementLifecycle,
) error {
	if replacement == nil || replacement.recovery == nil {
		return nil
	}
	recoveryCtx, cancel := replacementRecoveryContext(ctx)
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

func messageHasImage(m *chat_entity.Message) bool {
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
func (s *chatSvc) backendForkAnchor(
	ctx context.Context,
	sess *chat_entity.Session,
	be *agent_backend_entity.AgentBackend,
	userMsg *chat_entity.Message,
) (string, error) {
	if !sess.HasProviderSession() {
		if be.IsPiAgent() {
			failedFirstTurn, err := s.isFailedFirstPiTurn(ctx, sess, userMsg)
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
		return s.codexRollbackAnchor(ctx, sess, userMsg)
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

func (s *chatSvc) isFailedFirstPiTurn(
	ctx context.Context,
	sess *chat_entity.Session,
	userMsg *chat_entity.Message,
) (bool, error) {
	_, hasForkAnchor := normalizedPiForkAnchor(userMsg)
	if sess == nil || userMsg == nil || hasForkAnchor {
		return false, nil
	}
	messages, err := chat_repo.Message().List(ctx, sess.ID)
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

func (s *chatSvc) codexRollbackAnchor(ctx context.Context, sess *chat_entity.Session, userMsg *chat_entity.Message) (string, error) {
	msgs, err := chat_repo.Message().List(ctx, sess.ID)
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
