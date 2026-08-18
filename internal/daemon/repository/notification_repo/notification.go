// Package notification_repo 提供 agentred 侧 daemon_notification_logs 表的持久化
// 访问——「日志的一行 = 一条本该发出的通知」:method/payload 是原样的
// JSON-RPC (method, params),补齐就是按 seq 升序把它们重新投递给客户端。
//
// 会话身份是 (peerFingerprint, peerSessionID) 的组合,不是对端会话 id 单独——会话 id
// 是各客户端本地自增的,不同客户端必然重号(R16)。本包不含会话元数据(agent id / cwd /
// backend 类型 / 生命周期状态)的读写,那是后续任务的会话生命周期仓储的职责;本任务只
// 覆盖「storage 层」本身:给定 (peerFingerprint, peerSessionID),一条通知能以下一个
// seq 落库、并按游标按序读回。
package notification_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"
)

//go:generate mockgen -source notification.go -destination mock_notification_repo/mock_notification.go

// NotificationLog 对应 daemon_notification_logs 的一行。复合主键
// (PeerFingerprint, PeerSessionID, Seq) 见规格「持久化数据变化 / agentred 侧」。
type NotificationLog struct {
	PeerFingerprint string `gorm:"column:peer_fingerprint;primaryKey"`
	PeerSessionID   string `gorm:"column:peer_session_id;primaryKey"`
	Seq             int64  `gorm:"column:seq;primaryKey"`
	Method          string `gorm:"column:method"`
	Payload         string `gorm:"column:payload"`
	CreatedAt       int64  `gorm:"column:created_at"`
}

func (*NotificationLog) TableName() string { return "daemon_notification_logs" }

// NotificationRepo 持久化并按序回放某个 (peerFingerprint, peerSessionID) 的通知日志。
type NotificationRepo interface {
	// Append 以该会话的下一个 seq(已记录的最大 seq + 1,该会话还没有通知时为 1)
	// 落库一条通知,并把库分配到的 seq 回填进 n.Seq(入参里的 Seq 被忽略)。分配与
	// 写入是同一条语句,因此同一会话的并发写者不会拿到同一个 seq:seq 在单个会话内
	// 单调无洞,每条通知都真的落了库。
	Append(ctx context.Context, n *NotificationLog) error

	// ListSince 返回 (peerFingerprint, peerSessionID) 下 seq > cursor 的通知,按 seq
	// 升序,最多 limit 条,并告知这一页之后是否还有更多。
	ListSince(ctx context.Context, peerFingerprint, peerSessionID string, cursor int64, limit int) (rows []*NotificationLog, hasMore bool, err error)

	// LatestSeq 返回该会话已记录的最大 seq,一条通知都没有时为 0。它是「最新 seq」的
	// 唯一真相源(见包注释与 handlers.JournalPort):会话表上不存第二份冗余游标。
	LatestSeq(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error)

	// LatestSeqByPeer 一次取回该对端全部会话的最大 seq(会话 id → seq)。会话清单要为
	// 每条会话报最新 seq,按会话数发 N 条 LatestSeq 会让清单随会话数线性变慢;没有任何
	// 通知的会话不出现在结果里,调用方按 0 处理。
	LatestSeqByPeer(ctx context.Context, peerFingerprint string) (map[string]int64, error)

	// OldestSeq 返回该会话此刻**现存最老**的 seq,一条通知都没有时为 0。
	//
	// 它是留存回收(DeleteBelow)在读侧的对应物:回收之后 seq 1..N 不再从 1 开始,而
	// 补齐的客户端游标可能正落在被回收掉的那一段里。少了这个下界,客户端拉到的每一页
	// 第一条都比 游标+1 大,只能当成跳号丢弃并再拉一次同一页 —— 游标永远推不动,会话
	// 没有错误地冻住。有了它,客户端知道那截尾巴是真的没有了,复位游标接着补。
	OldestSeq(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error)

	// DeleteBelow 删除这条 (对端, 会话) 下 seq **严格小于** seq 的日志行,返回删除行数。
	//
	// 「严格小于」是硬的:高水位那一行必须活着。它一旦被删,该会话的 MAX(seq) 归零,
	// Append 会从 1 重新分配 seq,而客户端游标还停在旧高水位上 —— 此后每条实时通知都
	// 被它当成重复丢弃,会话没有跳号、没有错误地冻住。
	DeleteBelow(ctx context.Context, peerFingerprint, peerSessionID string, seq int64) (int64, error)
}

var defaultNotification NotificationRepo

// Notification 取默认仓储单例。
func Notification() NotificationRepo { return defaultNotification }

// RegisterNotification 注入仓储实现,由 daemon 启动流程调用一次。
func RegisterNotification(impl NotificationRepo) { defaultNotification = impl }

type notificationRepo struct{}

// NewNotification 构造默认 GORM 实现。
func NewNotification() NotificationRepo { return &notificationRepo{} }

// appendSQL 在一条语句里完成「取该会话的下一个 seq」与「写入」,由 RETURNING 交回
// 实际分配到的 seq。写成一条语句是必须的,不是优化:SQLite 对单条写语句整条持写锁,
// 所以并发写者会被串行化,各自读到的 MAX(seq) 必然包含前一个写者刚写的行。拆成
// 「先 SELECT MAX(seq)+1、再 INSERT」两步则两个写者会拿到同一个 seq,后写的那条撞主键
// 冲突——要么整条通知永久丢失,要么调用方拿到一个裸的唯一约束错误。同一会话上
// 并发的通知生产者是现实存在的(handlers/runtime.go 的 fanout 与
// startAutonomousFanout 是同一 sid 上两个独立 goroutine)。
//
// 入参里的 n.Seq 一律被忽略:seq 只由这条语句分配,本包不提供「按指定 seq 写入」的
// 第二条写路径。
const appendSQL = "INSERT INTO daemon_notification_logs " +
	"(peer_fingerprint, peer_session_id, seq, method, payload, created_at) " +
	"SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ? " +
	"FROM daemon_notification_logs WHERE peer_fingerprint = ? AND peer_session_id = ? " +
	"RETURNING seq"

func (r *notificationRepo) Append(ctx context.Context, n *NotificationLog) error {
	if n.CreatedAt == 0 {
		n.CreatedAt = time.Now().UnixMilli()
	}
	var seq int64
	if err := db.Ctx(ctx).Raw(appendSQL,
		n.PeerFingerprint, n.PeerSessionID, n.Method, n.Payload, n.CreatedAt,
		n.PeerFingerprint, n.PeerSessionID,
	).Row().Scan(&seq); err != nil {
		return err
	}
	n.Seq = seq
	return nil
}

func (r *notificationRepo) ListSince(ctx context.Context, peerFingerprint, peerSessionID string, cursor int64, limit int) ([]*NotificationLog, bool, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []*NotificationLog
	// Fetch one extra row to learn hasMore without a second COUNT query.
	err := db.Ctx(ctx).
		Where("peer_fingerprint = ? AND peer_session_id = ? AND seq > ?", peerFingerprint, peerSessionID, cursor).
		Order("seq ASC").
		Limit(limit + 1).
		Find(&rows).Error
	if err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}

func (r *notificationRepo) LatestSeq(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error) {
	var seq int64
	err := db.Ctx(ctx).
		Raw("SELECT COALESCE(MAX(seq), 0) FROM daemon_notification_logs WHERE peer_fingerprint = ? AND peer_session_id = ?",
			peerFingerprint, peerSessionID).
		Row().Scan(&seq)
	if err != nil {
		return 0, err
	}
	return seq, nil
}

func (r *notificationRepo) OldestSeq(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error) {
	var seq int64
	err := db.Ctx(ctx).
		Raw("SELECT COALESCE(MIN(seq), 0) FROM daemon_notification_logs WHERE peer_fingerprint = ? AND peer_session_id = ?",
			peerFingerprint, peerSessionID).
		Row().Scan(&seq)
	if err != nil {
		return 0, err
	}
	return seq, nil
}

func (r *notificationRepo) DeleteBelow(ctx context.Context, peerFingerprint, peerSessionID string, seq int64) (int64, error) {
	tx := db.Ctx(ctx).
		Where("peer_fingerprint = ? AND peer_session_id = ? AND seq < ?", peerFingerprint, peerSessionID, seq).
		Delete(&NotificationLog{})
	return tx.RowsAffected, tx.Error
}

func (r *notificationRepo) LatestSeqByPeer(ctx context.Context, peerFingerprint string) (map[string]int64, error) {
	var rows []struct {
		PeerSessionID string `gorm:"column:peer_session_id"`
		Seq           int64  `gorm:"column:seq"`
	}
	err := db.Ctx(ctx).
		Raw("SELECT peer_session_id, MAX(seq) AS seq FROM daemon_notification_logs WHERE peer_fingerprint = ? GROUP BY peer_session_id",
			peerFingerprint).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.PeerSessionID] = row.Seq
	}
	return out, nil
}
