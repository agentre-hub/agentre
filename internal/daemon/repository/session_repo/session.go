// Package session_repo 提供 agentred 侧 daemon_sessions 表的持久化访问 —— 会话在这台
// daemon 上的**身份与生命周期**。它是 notification_repo(会话的通知日志)的姊妹包:一个
// 记「这条会话是谁的、在跑什么、处于哪一步」,一个记「它发出过哪些通知」。
//
// 会话身份是 (peerFingerprint, peerSessionID) 的组合(R16):会话 id 是各客户端本地自增
// 的,不同客户端必然重号,所以查询一律带上对端指纹,单独按会话 id 定位的方法本包不提供。
//
// 生命周期取值由调用方给(见 internal/daemon/handlers 的 SessionRecord 与 daemon.go 的
// 启动清扫):本包只负责存取,不解释状态机 —— 状态字符串同时是过线协议的一部分
// (wire.SessionLifecycle*),把它固化进仓储会让两处定义迟早漂移。
//
// 「某会话最新的 seq」不在本包里:唯一真相源是通知日志自己的 MAX(seq)(见
// notification_repo 与 handlers.JournalPort 的说明)。daemon_sessions 上曾经预留过一列
// latest_seq 不在会话表维护；最新游标以通知日志的 MAX(seq) 为唯一真相源。
package session_repo

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

//go:generate mockgen -source session.go -destination mock_session_repo/mock_session.go

// DaemonSession 对应 daemon_sessions 的一行。复合主键 (PeerFingerprint, PeerSessionID)。
//
// Title / AgentSyncID 是 R7 的新列:会话标题与所属 Agent 的账号级同步标识,每轮由调用
// 方携带、幂等覆盖;老会话缺字段时保持空串。ProviderSessionID 是决策 8 的新列:daemon
// 每轮从 RunAck 路径收回并落库,续话不再需要调用方提供。
type DaemonSession struct {
	PeerFingerprint string `gorm:"column:peer_fingerprint;primaryKey"`
	PeerSessionID   string `gorm:"column:peer_session_id;primaryKey"`
	AgentID         int64  `gorm:"column:agent_id"`
	Cwd             string `gorm:"column:cwd"`
	BackendType     string `gorm:"column:backend_type"`
	LifecycleState  string `gorm:"column:lifecycle_state"`
	Title           string `gorm:"column:title"`
	AgentSyncID     string `gorm:"column:agent_sync_id"`
	// ProjectSyncID 是这条会话所属项目的账号级同步标识,由发起方在起手时携带。
	//
	// 此前这一列不存在,项目归属只能由服务端按 (指纹, cwd) 反推(agent_sessions 的
	// 决策 12)。反推那条路要求 cwd 上行,而日活跃统计走的是一条不带任何路径的纯计数
	// 通道 —— 项目因此必须在发起那一刻就记下来,而不是事后从路径猜。
	//
	// 空串 = 发起方没报(老会话,或那条会话本来就不属于任何项目),不是「未知待推导」。
	ProjectSyncID     string `gorm:"column:project_sync_id"`
	ProviderSessionID string `gorm:"column:provider_session_id"`
	// ProviderKey / ModelKey 是会话级 ModelTarget 的两格(两者皆空 = 跟随 Agent
	// 绑定)。它们**不在 Upsert 的赋值列里**:每轮起手都幂等覆盖会把轮中刚选好的
	// 模型冲回旧值 —— 桌面端 chat_entity 的 ModelKey 注释写的是同一条纪律。改这
	// 两格只有 SetModelTarget 一条路。
	ProviderKey string `gorm:"column:provider_key"`
	ModelKey    string `gorm:"column:model_key"`
	Createtime  int64  `gorm:"column:createtime"`
	// LastMessageAt 是这条会话最后一次活动的时刻（毫秒 epoch）。它**不叫**
	// UpdatedAt：那个名字会被 GORM 认作行更新时刻、在每一次写入上自动改写，而这一
	// 格是会话清单的排序键与线格式 SessionSummary.last_message_at 的唯一真相源。
	LastMessageAt int64 `gorm:"column:last_message_at"`
}

func (*DaemonSession) TableName() string { return "daemon_sessions" }

// SessionRepo 存取会话的身份与生命周期。
type SessionRepo interface {
	// Upsert 建行或更新会话的元数据与生命周期。一轮执行起手时调用;同一会话跑第二轮时
	// 更新同一行而不是撞主键 —— 会话 id 在整个会话寿命内复用。
	Upsert(ctx context.Context, s *DaemonSession) error

	// UpdateLifecycle 只推进这条 (对端, 会话) 的生命周期状态。会话不存在时不报错
	// (影响 0 行):调用方是轮末回调,会话行有没有建成不该反过来影响一轮执行的收尾。
	UpdateLifecycle(ctx context.Context, peerFingerprint, peerSessionID, state string) error

	// SetModelTarget 改写这条会话钉的 ModelTarget,返回受影响行数(0 = 没有这条
	// 会话)。两格都空是**要写下去的值**(改回跟随 Agent 绑定),所以不能用「非空
	// 才写」的部分更新。刻意不走 Upsert:那是每轮起手跑的,见本文件 DaemonSession
	// 上 ProviderKey / ModelKey 的说明。
	SetModelTarget(ctx context.Context, peerFingerprint, peerSessionID, providerKey, modelKey string) (int64, error)

	// Find 取某个对端名下的一条会话;不存在返回 (nil, nil)。
	Find(ctx context.Context, peerFingerprint, peerSessionID string) (*DaemonSession, error)

	// ListByPeer 列出某个对端在本 daemon 上的全部会话,最近活动的在前。
	ListByPeer(ctx context.Context, peerFingerprint string) ([]*DaemonSession, error)

	// ListAll 列出所有对端的会话,最近活动的在前。账号可见性由上层鉴权后决定;
	// 本仓储不改变复合主键,也不解释调用方是否有资格跨对端读取。
	ListAll(ctx context.Context) ([]*DaemonSession, error)

	// CountByLifecycle 数一数此刻停在某个生命周期上的会话有几条。
	//
	// 它服务的是本机状态查询(`agentred status` 的「活跃会话数」):daemon 记着的
	// 生命周期就是「这台机器此刻在为谁干活」的真相源 —— 一轮起手置 running、轮末
	// 置 idle、进程重启把非终态一律标 interrupted。数 COUNT 而不是把行查出来在内存
	// 里数:这一列只用来印一个数字,没有理由把整张表搬出库。
	CountByLifecycle(ctx context.Context, state string) (int64, error)

	// Delete 删掉这一条 (对端, 会话) 的会话行,返回删除行数。会话不存在时删掉零行、
	// 不报错:删除必须幂等 —— 调用方(server 那条删除待办)会重放同一条指令,报错会让
	// 它永远重放下去。
	//
	// 它只删身份行;那条会话的通知日志由 notification_repo.DeleteAll 清(两个包各管
	// 各的表)。
	Delete(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error)

	// InterruptAll 把库中所有还不是 interruptedState 的会话一次改成该状态,返回受影响
	// 行数(R10 的启动清扫)。它按状态而不是按对端 / 会话枚举:daemon 刚起时内存里一条
	// 会话都没有,库里的行就是唯一的来源。
	InterruptAll(ctx context.Context, interruptedState string) (int64, error)
}

var defaultSession SessionRepo

// Session 取默认仓储单例。
func Session() SessionRepo { return defaultSession }

// RegisterSession 注入仓储实现,由 daemon 启动流程调用一次。
func RegisterSession(impl SessionRepo) { defaultSession = impl }

type sessionRepo struct{}

// NewSession 构造默认 GORM 实现。
func NewSession() SessionRepo { return &sessionRepo{} }

func (r *sessionRepo) Upsert(ctx context.Context, s *DaemonSession) error {
	now := time.Now().UnixMilli()
	if s.Createtime == 0 {
		s.Createtime = now
	}
	s.LastMessageAt = now
	// 主键冲突时更新元数据与生命周期,保留最初的 createtime。title / agent_sync_id /
	// project_sync_id / provider_session_id 一并幂等覆盖 —— 每轮起手都携带当轮的值
	// (R7 / 决策 8)。项目也在这一批里:会话可以换项目,只插不更新就再也改不回来。
	return db.Ctx(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "peer_fingerprint"}, {Name: "peer_session_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"agent_id", "cwd", "backend_type", "lifecycle_state", "title", "agent_sync_id",
			"project_sync_id", "provider_session_id", "last_message_at",
		}),
	}).Create(s).Error
}

func (r *sessionRepo) SetModelTarget(
	ctx context.Context, peerFingerprint, peerSessionID, providerKey, modelKey string,
) (int64, error) {
	// 用 map 而不是结构体:结构体的零值会被 GORM 当成「没设」跳过,而两格都空正是
	// 「改回跟随 Agent 绑定」这个有含义的取值,跳过就等于这次改动没发生。
	res := db.Ctx(ctx).Model(&DaemonSession{}).
		Where("peer_fingerprint = ? AND peer_session_id = ?", peerFingerprint, peerSessionID).
		Updates(map[string]any{
			"provider_key":    providerKey,
			"model_key":       modelKey,
			"last_message_at": time.Now().UnixMilli(),
		})
	return res.RowsAffected, res.Error
}

func (r *sessionRepo) UpdateLifecycle(ctx context.Context, peerFingerprint, peerSessionID, state string) error {
	return db.Ctx(ctx).Model(&DaemonSession{}).
		Where("peer_fingerprint = ? AND peer_session_id = ?", peerFingerprint, peerSessionID).
		Updates(map[string]any{
			"lifecycle_state": state,
			"last_message_at": time.Now().UnixMilli(),
		}).Error
}

func (r *sessionRepo) Find(ctx context.Context, peerFingerprint, peerSessionID string) (*DaemonSession, error) {
	row := &DaemonSession{}
	err := db.Ctx(ctx).
		Where("peer_fingerprint = ? AND peer_session_id = ?", peerFingerprint, peerSessionID).
		First(row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *sessionRepo) ListByPeer(ctx context.Context, peerFingerprint string) ([]*DaemonSession, error) {
	var rows []*DaemonSession
	err := db.Ctx(ctx).
		Where("peer_fingerprint = ?", peerFingerprint).
		Order("last_message_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *sessionRepo) ListAll(ctx context.Context) ([]*DaemonSession, error) {
	var rows []*DaemonSession
	err := db.Ctx(ctx).Order("last_message_at DESC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *sessionRepo) CountByLifecycle(ctx context.Context, state string) (int64, error) {
	var n int64
	err := db.Ctx(ctx).Model(&DaemonSession{}).
		Where("lifecycle_state = ?", state).
		Count(&n).Error
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (r *sessionRepo) Delete(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error) {
	tx := db.Ctx(ctx).
		Where("peer_fingerprint = ? AND peer_session_id = ?", peerFingerprint, peerSessionID).
		Delete(&DaemonSession{})
	return tx.RowsAffected, tx.Error
}

func (r *sessionRepo) InterruptAll(ctx context.Context, interruptedState string) (int64, error) {
	tx := db.Ctx(ctx).Model(&DaemonSession{}).
		Where("lifecycle_state <> ?", interruptedState).
		Updates(map[string]any{
			"lifecycle_state": interruptedState,
			"last_message_at": time.Now().UnixMilli(),
		})
	return tx.RowsAffected, tx.Error
}
