package chat_repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/cago-frame/cago/database/db"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
)

// ReplacementRecoveryState is persisted on the recovery marker. Pending
// means Start has not acknowledged the prompt and must restore; acknowledged
// means the active generation is committed and only hidden originals may be removed.
type ReplacementRecoveryState string

const (
	ReplacementRecoveryPending      ReplacementRecoveryState = "pending"
	ReplacementRecoveryAcknowledged ReplacementRecoveryState = "acknowledged"

	// replacementRecoveryKeyPrefix 是恢复标记在 app_settings 里的 key 前缀。
	// 标记从前借 chat_messages 的四个列表达自己(payload 借 blocks_json、查找键借
	// device_id);两根支柱在本轮都消失了,它改用通用键值表按 key 点查。
	replacementRecoveryKeyPrefix = "chat.pi_recovery:"
)

var (
	ErrReplacementNamespaceCollision = errors.New("replacement recovery namespace is already occupied")
	ErrReplacementOwnershipLost      = errors.New("replacement recovery no longer owns the active session")
)

// ReplacementRecovery owns one exact Pi replacement generation. The original
// rows retain their IDs and contents under RecoverySessionID until Start is
// acknowledged; the active IDs prevent stale restoration from deleting a retry.
type ReplacementRecovery struct {
	RecoverySessionID    int64
	SessionID            int64
	FromSeq              int
	RequestMessageID     int64
	UserMessageID        int64
	AssistantMessageID   int64
	OldProviderSessionID string
	NewProviderSessionID string
	OldAgentStatus       string
	OldLastMessageAt     int64
	State                ReplacementRecoveryState
}

type replacementRecoveryPayload struct {
	SessionID            int64                    `json:"sessionId"`
	FromSeq              int                      `json:"fromSeq"`
	RequestMessageID     int64                    `json:"requestMessageId"`
	UserMessageID        int64                    `json:"userMessageId"`
	AssistantMessageID   int64                    `json:"assistantMessageId"`
	OldProviderSessionID string                   `json:"oldProviderSessionId"`
	NewProviderSessionID string                   `json:"newProviderSessionId"`
	OldAgentStatus       string                   `json:"oldAgentStatus"`
	OldLastMessageAt     int64                    `json:"oldLastMessageAt"`
	State                ReplacementRecoveryState `json:"state"`
}

// ReplacementStageSessionID is the legacy private staging namespace. New Pi
// replacements use a generation-owned recovery namespace instead.
func ReplacementStageSessionID(sessionID int64) int64 {
	return -sessionID
}

// replacementRecoveryKey 是一条会话的恢复标记 key。一条会话同时只有一次替换生成在飞,
// 所以会话 id 就是标记的自然键。
func replacementRecoveryKey(sessionID int64) string {
	return replacementRecoveryKeyPrefix + strconv.FormatInt(sessionID, 10)
}

// ReplacementRecoverySessionID maps the owning session ID into a private
// negative namespace holding the hidden original rows. The odd mapping keeps
// the same session ID from colliding with the legacy -sessionID stage.
func ReplacementRecoverySessionID(sessionID int64) (int64, error) {
	if sessionID <= 0 || sessionID > (math.MaxInt64-1)/2 {
		return 0, fmt.Errorf("invalid replacement recovery session ID %d", sessionID)
	}
	return -(sessionID*2 + 1), nil
}

// NewReplacementRecoveryMarker 把一份恢复所有权编码成 app_settings 的一行。
func NewReplacementRecoveryMarker(recovery *ReplacementRecovery) (*app_setting_entity.AppSetting, error) {
	if recovery == nil || recovery.SessionID <= 0 || recovery.FromSeq < 0 || recovery.RequestMessageID <= 0 {
		return nil, errors.New("invalid replacement recovery")
	}
	if recovery.State != ReplacementRecoveryPending && recovery.State != ReplacementRecoveryAcknowledged {
		return nil, errors.New("invalid replacement recovery state")
	}
	payload, err := json.Marshal(replacementRecoveryPayload{
		SessionID:            recovery.SessionID,
		FromSeq:              recovery.FromSeq,
		RequestMessageID:     recovery.RequestMessageID,
		UserMessageID:        recovery.UserMessageID,
		AssistantMessageID:   recovery.AssistantMessageID,
		OldProviderSessionID: recovery.OldProviderSessionID,
		NewProviderSessionID: recovery.NewProviderSessionID,
		OldAgentStatus:       recovery.OldAgentStatus,
		OldLastMessageAt:     recovery.OldLastMessageAt,
		State:                recovery.State,
	})
	if err != nil {
		return nil, err
	}
	return &app_setting_entity.AppSetting{
		Key:        replacementRecoveryKey(recovery.SessionID),
		Value:      string(payload),
		Updatetime: time.Now().UnixMilli(),
	}, nil
}

// ParseReplacementRecoveryMarker 是 NewReplacementRecoveryMarker 的逆运算,并在还原时
// 校验这条标记确实完整拥有一次生成(缺任何一半所有权信息都算无效)。
func ParseReplacementRecoveryMarker(marker *app_setting_entity.AppSetting) (*ReplacementRecovery, error) {
	if marker == nil || marker.Key == "" {
		return nil, errors.New("invalid replacement recovery marker")
	}
	var payload replacementRecoveryPayload
	if err := json.Unmarshal([]byte(marker.Value), &payload); err != nil {
		return nil, fmt.Errorf("decode replacement recovery marker: %w", err)
	}
	if payload.State != ReplacementRecoveryPending && payload.State != ReplacementRecoveryAcknowledged {
		return nil, errors.New("invalid replacement recovery marker state")
	}
	if marker.Key != replacementRecoveryKey(payload.SessionID) {
		return nil, errors.New("replacement recovery namespace does not own marker")
	}
	if payload.SessionID <= 0 || payload.FromSeq < 0 || payload.RequestMessageID <= 0 ||
		payload.UserMessageID <= 0 || payload.AssistantMessageID <= 0 ||
		payload.UserMessageID == payload.AssistantMessageID || payload.NewProviderSessionID == "" {
		return nil, errors.New("invalid replacement recovery ownership")
	}
	recoverySessionID, err := ReplacementRecoverySessionID(payload.SessionID)
	if err != nil {
		return nil, err
	}
	return &ReplacementRecovery{
		RecoverySessionID:    recoverySessionID,
		SessionID:            payload.SessionID,
		FromSeq:              payload.FromSeq,
		RequestMessageID:     payload.RequestMessageID,
		UserMessageID:        payload.UserMessageID,
		AssistantMessageID:   payload.AssistantMessageID,
		OldProviderSessionID: payload.OldProviderSessionID,
		NewProviderSessionID: payload.NewProviderSessionID,
		OldAgentStatus:       payload.OldAgentStatus,
		OldLastMessageAt:     payload.OldLastMessageAt,
		State:                payload.State,
	}, nil
}

// SaveReplacementRecovery 落一条恢复标记(按 key upsert)。
func SaveReplacementRecovery(ctx context.Context, recovery *ReplacementRecovery) error {
	marker, err := NewReplacementRecoveryMarker(recovery)
	if err != nil {
		return err
	}
	return app_setting_repo.AppSetting().Set(ctx, marker)
}

// EnsureReplacementRecoveryNamespaceAvailable fails closed when the session
// already owns an in-flight generation: either its marker is still present, or
// its hidden namespace still holds rows.
func EnsureReplacementRecoveryNamespaceAvailable(ctx context.Context, sessionID int64) error {
	recoverySessionID, err := ReplacementRecoverySessionID(sessionID)
	if err != nil {
		return err
	}
	marker, err := app_setting_repo.AppSetting().Get(ctx, replacementRecoveryKey(sessionID))
	if err != nil {
		return err
	}
	if marker != nil {
		return ErrReplacementNamespaceCollision
	}
	var count int64
	if err := db.Ctx(ctx).
		Raw("SELECT COUNT(*) FROM `chat_messages` WHERE session_id = ?", recoverySessionID).
		Row().
		Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return ErrReplacementNamespaceCollision
	}
	return nil
}

// MoveMessagesFromSeq changes only row ownership. IDs, content, anchors,
// sequence numbers, and timestamps remain byte-for-byte intact for recovery —
// the block rows follow their message ID, so nothing needs to move with them.
func MoveMessagesFromSeq(ctx context.Context, sourceSessionID, targetSessionID int64, fromSeq int) (int64, error) {
	res := db.Ctx(ctx).Exec(
		"UPDATE `chat_messages` SET `session_id`=? WHERE session_id = ? AND seq >= ?",
		targetSessionID,
		sourceSessionID,
		fromSeq,
	)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// DeleteOwnedReplacementMessages removes only the two active rows recorded by
// one recovery generation, together with their block rows. A stale restore
// therefore cannot truncate a retry.
func DeleteOwnedReplacementMessages(ctx context.Context, sessionID, userMessageID, assistantMessageID int64) (int64, error) {
	if err := deleteBlocksOfMessages(ctx, "session_id = ? AND id IN (?,?)",
		sessionID, userMessageID, assistantMessageID); err != nil {
		return 0, err
	}
	res := db.Ctx(ctx).Exec(
		"DELETE FROM `chat_messages` WHERE session_id = ? AND id IN (?,?)",
		sessionID,
		userMessageID,
		assistantMessageID,
	)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// FindReplacementRecoveryForSession 按会话点查它的恢复标记。无标记返回 (nil, nil)。
func FindReplacementRecoveryForSession(ctx context.Context, sessionID int64) (*ReplacementRecovery, error) {
	if sessionID <= 0 {
		return nil, nil
	}
	marker, err := app_setting_repo.AppSetting().Get(ctx, replacementRecoveryKey(sessionID))
	if err != nil || marker == nil {
		return nil, err
	}
	recovery, err := ParseReplacementRecoveryMarker(marker)
	if err != nil {
		return nil, err
	}
	if recovery.SessionID != sessionID {
		return nil, errors.New("replacement recovery marker does not own session")
	}
	return recovery, nil
}

func FindReplacementRecoveryForMessage(ctx context.Context, sessionID, messageID int64) (*ReplacementRecovery, error) {
	if messageID <= 0 {
		return nil, nil
	}
	recovery, err := FindReplacementRecoveryForSession(ctx, sessionID)
	if err != nil || recovery == nil {
		return nil, err
	}
	if recovery.UserMessageID != messageID && recovery.AssistantMessageID != messageID {
		return nil, nil
	}
	return recovery, nil
}

// EnsureReplacementActiveTailOwned rejects a legacy hybrid transcript before
// restoration. FromSeq and the two active row IDs are persisted in the marker;
// no content, timestamp, or inferred ordering is used.
func EnsureReplacementActiveTailOwned(ctx context.Context, recovery *ReplacementRecovery) error {
	if recovery == nil || recovery.SessionID <= 0 || recovery.FromSeq < 0 ||
		recovery.UserMessageID <= 0 || recovery.AssistantMessageID <= 0 {
		return errors.New("invalid replacement recovery ownership")
	}
	var unexpected int64
	if err := db.Ctx(ctx).
		Raw(
			"SELECT COUNT(*) FROM `chat_messages` WHERE session_id = ? AND seq >= ? AND id NOT IN (?,?)",
			recovery.SessionID,
			recovery.FromSeq,
			recovery.UserMessageID,
			recovery.AssistantMessageID,
		).
		Row().
		Scan(&unexpected); err != nil {
		return err
	}
	if unexpected != 0 {
		return ErrReplacementOwnershipLost
	}
	return nil
}

// AcknowledgeReplacementRecovery flips only the generation that still owns the
// session and never recreates a marker deleted by a stale finalizer.
func AcknowledgeReplacementRecovery(ctx context.Context, recovery *ReplacementRecovery) error {
	if recovery == nil || recovery.SessionID <= 0 {
		return errors.New("invalid replacement recovery acknowledgement")
	}
	stored, err := FindReplacementRecoveryForSession(ctx, recovery.SessionID)
	if err != nil {
		return err
	}
	if stored == nil || stored.NewProviderSessionID != recovery.NewProviderSessionID {
		return ErrReplacementOwnershipLost
	}
	acknowledged := *stored
	acknowledged.State = ReplacementRecoveryAcknowledged
	if err := SaveReplacementRecovery(ctx, &acknowledged); err != nil {
		return err
	}
	recovery.State = ReplacementRecoveryAcknowledged
	return nil
}

// DeleteReplacementRecovery is idempotent and scoped to one session's
// generation: it drops the marker and purges the hidden namespace (originals
// plus their block rows). The returned count is the number of marker rows
// removed, so an old acknowledgement that finds nothing reports zero.
func DeleteReplacementRecovery(ctx context.Context, sessionID int64) (int64, error) {
	recoverySessionID, err := ReplacementRecoverySessionID(sessionID)
	if err != nil {
		return 0, err
	}
	if err := deleteBlocksOfMessages(ctx, "session_id = ?", recoverySessionID); err != nil {
		return 0, err
	}
	if err := db.Ctx(ctx).
		Exec("DELETE FROM `chat_messages` WHERE session_id = ?", recoverySessionID).Error; err != nil {
		return 0, err
	}
	return app_setting_repo.AppSetting().Delete(ctx, replacementRecoveryKey(sessionID))
}

// RestoreReplacementSession claims the generation by its forked provider
// session ID before any transcript restoration. A stale generation therefore
// rolls its surrounding transaction back instead of restoring over a retry.
func RestoreReplacementSession(ctx context.Context, recovery *ReplacementRecovery) error {
	if recovery == nil || recovery.SessionID <= 0 || recovery.NewProviderSessionID == "" {
		return errors.New("invalid replacement recovery session")
	}
	res := db.Ctx(ctx).Exec(
		"UPDATE `chat_sessions` SET `provider_session_id`=?,`agent_status`=?,`last_message_at`=?,`updatetime`=? "+
			"WHERE id = ? AND provider_session_id = ?",
		recovery.OldProviderSessionID,
		recovery.OldAgentStatus,
		recovery.OldLastMessageAt,
		time.Now().UnixMilli(),
		recovery.SessionID,
		recovery.NewProviderSessionID,
	)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrReplacementOwnershipLost
	}
	return nil
}
