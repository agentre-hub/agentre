// Package session_repo 提供 agentred 侧 daemon_sessions 表的持久化访问 —— 会话在这台
// daemon 上的**身份与生命周期**。它是 notification_repo(会话的通知日志)的姊妹包:一个
// 记「这条会话是谁的、在跑什么、处于哪一步」,一个记「它发出过哪些通知」。
//
// 会话身份是 conversation_id 一列:它是这条对话在桌面端、agentred 与 server 三套库以及
// 线格式上共用的全局唯一标识(规格 2026-08-31「身份键收缩为一列」),因此不再需要拿对端
// 指纹去消歧。peer_fingerprint 退出主键、保留为普通列,继续承担来源标注与授权 —— 读路径
// 仍按它收窄,一个对端看不见另一个对端名下的会话。
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
	"strings"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

//go:generate mockgen -source session.go -destination mock_session_repo/mock_session.go

// DaemonSession 对应 daemon_sessions 的一行。主键 (ConversationID)。
//
// Title / AgentSyncID 是 R7 的新列:会话标题与所属 Agent 的账号级同步标识,每轮由调用
// 方携带、幂等覆盖;老会话缺字段时保持空串。ProviderSessionID 是决策 8 的新列:daemon
// 每轮从 RunAck 路径收回并落库,续话不再需要调用方提供。
type DaemonSession struct {
	// ConversationID 是这条对话的全局唯一标识(uuid),也是本表的主键。
	ConversationID string `gorm:"column:conversation_id;primaryKey"`
	// PeerFingerprint 是把这条对话交到本机执行的对端 —— 来源标注与授权,不再是身份的
	// 一部分。它在建行那一次落下,此后的幂等覆盖不再改写(见 Upsert)。
	PeerFingerprint string `gorm:"column:peer_fingerprint"`
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
	// ReasoningEffort 是会话级思考力度这一格(空 = 跟随后端配置)。它与上面两格同一
	// 条纪律:**不在 Upsert 的赋值列里**,改它只有 SetReasoningEffort 一条路 —— 每轮
	// 起手幂等覆盖会把用户轮中刚选好的档位冲回旧值。
	ReasoningEffort string `gorm:"column:reasoning_effort"`
	Createtime      int64  `gorm:"column:createtime"`
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

	// UpdateLifecycle 只推进这条对话的生命周期状态。会话不存在时不报错
	// (影响 0 行):调用方是轮末回调,会话行有没有建成不该反过来影响一轮执行的收尾。
	UpdateLifecycle(ctx context.Context, peerFingerprint, conversationID, state string) error

	// SetModelTarget 改写这条会话钉的 ModelTarget,返回受影响行数(0 = 没有这条
	// 会话)。两格都空是**要写下去的值**(改回跟随 Agent 绑定),所以不能用「非空
	// 才写」的部分更新。刻意不走 Upsert:那是每轮起手跑的,见本文件 DaemonSession
	// 上 ProviderKey / ModelKey 的说明。
	SetModelTarget(ctx context.Context, peerFingerprint, conversationID, providerKey, modelKey string) (int64, error)

	// SetReasoningEffort 改写这条会话钉的思考力度,返回受影响行数(0 = 没有这条
	// 会话)。空串是**要写下去的值**(改回跟随后端配置),所以同样不能用「非空才写」
	// 的部分更新;刻意不走 Upsert,理由同 SetModelTarget。
	SetReasoningEffort(ctx context.Context, peerFingerprint, conversationID, reasoningEffort string) (int64, error)

	// Find 取某个对端名下的一条会话;不存在返回 (nil, nil)。
	Find(ctx context.Context, peerFingerprint, conversationID string) (*DaemonSession, error)

	// ListByPeer 列出某个对端在本 daemon 上的会话,最近活动的在前。
	//
	// keyword 非空时按标题的大小写不敏感子串再收窄一层(空串 / 全空白 = 不收窄)。
	// 收窄放在这一层而不是调用方: 对端要的往往只是其中几条,整份回传既费带宽也把
	// 无关会话的标题送了出去。匹配面只有 title —— daemon 存的是 agent_sync_id /
	// project_sync_id,手上根本没有 agent 名与项目名。
	ListByPeer(ctx context.Context, peerFingerprint, keyword string) ([]*DaemonSession, error)

	// ListAll 列出所有对端的会话,最近活动的在前,keyword 语义同 ListByPeer。
	// 账号可见性由上层鉴权后决定; 本仓储不解释调用方是否有资格跨对端读取。
	ListAll(ctx context.Context, keyword string) ([]*DaemonSession, error)

	// CountByLifecycle 数一数此刻停在某个生命周期上的会话有几条。
	//
	// 它服务的是本机状态查询(`agentred status` 的「活跃会话数」):daemon 记着的
	// 生命周期就是「这台机器此刻在为谁干活」的真相源 —— 一轮起手置 running、轮末
	// 置 idle、进程重启把非终态一律标 interrupted。数 COUNT 而不是把行查出来在内存
	// 里数:这一列只用来印一个数字,没有理由把整张表搬出库。
	CountByLifecycle(ctx context.Context, state string) (int64, error)

	// Delete 删掉这一条 (对端, 对话) 的会话行,返回删除行数。会话不存在时删掉零行、
	// 不报错:删除必须幂等 —— 调用方(server 那条删除待办)会重放同一条指令,报错会让
	// 它永远重放下去。
	//
	// 它只删身份行;那条会话的通知日志由 notification_repo.DeleteAll 清(两个包各管
	// 各的表)。
	Delete(ctx context.Context, peerFingerprint, conversationID string) (int64, error)

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
	//
	// 冲突目标必须逐字落在库上那个 PK / UNIQUE 约束上,否则 SQLite 在**运行期**报
	// 「ON CONFLICT clause does not match any PRIMARY KEY or UNIQUE constraint」——
	// 本包的 sqlmock 单测按 SQL 文本匹配,对面没有 schema,这一格由
	// daemon_test.go 的真库用例守着。peer_fingerprint 不在赋值列里:它标的是这条对话
	// 最初交进来的那个对端,不随后来的每一轮改写。
	return db.Ctx(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "conversation_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"agent_id", "cwd", "backend_type", "lifecycle_state", "title", "agent_sync_id",
			"project_sync_id", "provider_session_id", "last_message_at",
		}),
	}).Create(s).Error
}

func (r *sessionRepo) SetModelTarget(
	ctx context.Context, peerFingerprint, conversationID, providerKey, modelKey string,
) (int64, error) {
	// 用 map 而不是结构体:结构体的零值会被 GORM 当成「没设」跳过,而两格都空正是
	// 「改回跟随 Agent 绑定」这个有含义的取值,跳过就等于这次改动没发生。
	res := db.Ctx(ctx).Model(&DaemonSession{}).
		Where("peer_fingerprint = ? AND conversation_id = ?", peerFingerprint, conversationID).
		Updates(map[string]any{
			"provider_key":    providerKey,
			"model_key":       modelKey,
			"last_message_at": time.Now().UnixMilli(),
		})
	return res.RowsAffected, res.Error
}

func (r *sessionRepo) SetReasoningEffort(
	ctx context.Context, peerFingerprint, conversationID, reasoningEffort string,
) (int64, error) {
	// 同 SetModelTarget 用 map:结构体的零值会被 GORM 当成「没设」跳过,而空串正是
	// 「改回跟随后端配置」这个有含义的取值,跳过就等于这次改动没发生。
	res := db.Ctx(ctx).Model(&DaemonSession{}).
		Where("peer_fingerprint = ? AND conversation_id = ?", peerFingerprint, conversationID).
		Updates(map[string]any{
			"reasoning_effort": reasoningEffort,
			"last_message_at":  time.Now().UnixMilli(),
		})
	return res.RowsAffected, res.Error
}

func (r *sessionRepo) UpdateLifecycle(ctx context.Context, peerFingerprint, conversationID, state string) error {
	return db.Ctx(ctx).Model(&DaemonSession{}).
		Where("peer_fingerprint = ? AND conversation_id = ?", peerFingerprint, conversationID).
		Updates(map[string]any{
			"lifecycle_state": state,
			"last_message_at": time.Now().UnixMilli(),
		}).Error
}

func (r *sessionRepo) Find(ctx context.Context, peerFingerprint, conversationID string) (*DaemonSession, error) {
	row := &DaemonSession{}
	err := db.Ctx(ctx).
		Where("peer_fingerprint = ? AND conversation_id = ?", peerFingerprint, conversationID).
		First(row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// titleKeywordScope 把关键词变成标题上的 LIKE。空串 / 全空白不发这一段 ——
// 一个只按了空格的搜索框不该把整台机器筛空。
//
// LIKE 元字符一律转成字面量: 不转的话「100%」会退化成「1、0、0 加任意后缀」,
// 搜得越具体命中反而越宽。
func titleKeywordScope(keyword string) func(*gorm.DB) *gorm.DB {
	return func(d *gorm.DB) *gorm.DB {
		kw := strings.TrimSpace(keyword)
		if kw == "" {
			return d
		}
		escaper := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
		return d.Where(`title LIKE ? ESCAPE '\'`, "%"+escaper.Replace(kw)+"%")
	}
}

func (r *sessionRepo) ListByPeer(ctx context.Context, peerFingerprint, keyword string) ([]*DaemonSession, error) {
	var rows []*DaemonSession
	err := db.Ctx(ctx).
		Where("peer_fingerprint = ?", peerFingerprint).
		Scopes(titleKeywordScope(keyword)).
		Order("last_message_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *sessionRepo) ListAll(ctx context.Context, keyword string) ([]*DaemonSession, error) {
	var rows []*DaemonSession
	err := db.Ctx(ctx).
		Scopes(titleKeywordScope(keyword)).
		Order("last_message_at DESC").
		Find(&rows).Error
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

func (r *sessionRepo) Delete(ctx context.Context, peerFingerprint, conversationID string) (int64, error) {
	tx := db.Ctx(ctx).
		Where("peer_fingerprint = ? AND conversation_id = ?", peerFingerprint, conversationID).
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
