// Package syncmeta_entity 提供账号级表共用的同步元数据（R1、docs/specs/2026-08-07-workspace-sync.md
// 366 行）。七张账号级表——projects、departments、agents、agent_backends、
// project_agents、project_locations，加 task 1 新建的执行目标表
// agent_exec_targets——各自匿名内嵌 SyncMeta，GORM 按约定把它的字段提升进宿主表的
// schema，六列在这七张表里同名同型。
//
// llm_providers also embeds SyncMeta, using provider_key as its stable sync ID;
// its models travel inside the provider payload rather than becoming a second kind.
package syncmeta_entity

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// SyncMeta 是账号级对象共用的同步元数据（决策 3/4/19/27）。
type SyncMeta struct {
	// SyncID 账号内全局唯一、终身不变的同步标识（决策 3，ULID；R1）。同一个对象在
	// 任一端被创建后，跨机的一切引用都以它表达，本地自增主键的差异对用户不可见。
	// 未登录时创建的行也照常生成（R12a）；本迁移执行时已存在的历史行留空——本仓
	// DDL 一律 not null default ''，因此宿主表的唯一索引必须是 WHERE sync_id != ''
	// 的部分索引，否则多行空标识会互撞。
	SyncID string `gorm:"column:sync_id;type:text;not null;default:''"`
	// SyncAccountID 这一行所属账号。存的是**本机**为 (server 地址, 远端用户主键)
	// 分配的账号键（sync_accounts.id，见 sync_account_entity），不是 server 那边的
	// user_id —— 后者是各自库里的自增主键，两套自建部署的第一个用户都是 1，直接拿
	// 它当归属，换一套 server 就会把上一个账号的行认成本账号的（迁移 202608080013）。
	//
	// 0 = 尚未认领：未登录期间创建的行（R12）与迁移前已存在的历史行都落在这里，
	// 直到 R12a 的首次登录把它们收进当前账号。
	SyncAccountID int64 `gorm:"column:sync_account_id;type:bigint;not null;default:0"`
	// SyncVersion server 分配的账号级单调序列值（决策 27），本地只存不改；出站
	// 队列另存每条改动的基版本（R4a），不复用这一列。
	SyncVersion int64 `gorm:"column:sync_version;type:bigint;not null;default:0"`
	// SyncUpdatedAt 最后一次修改的本地时间（毫秒 epoch），只用于展示与 30 天窗口
	// 计算，不参与胜负比较（决策 19/27）。
	SyncUpdatedAt int64 `gorm:"column:sync_updated_at;type:bigint;not null;default:0"`
	// SyncOriginFingerprint 最后一次修改来自哪台设备——存的是那台设备的规范指纹
	// （与本工作区其余跨机引用同一种表示）；空串表示这一行还没有被同步层标记过来源。
	SyncOriginFingerprint string `gorm:"column:sync_origin_fingerprint;type:text;not null;default:''"`
	// SyncDeletedAt 墓碑时间（毫秒 epoch，R6）；0 = 未删除。与各表既有的 status 软
	// 删语义正交——status 是本地可见性，这一列是跨机墓碑标记，由后续任务的删除
	// 路径一并写入。
	SyncDeletedAt int64 `gorm:"column:sync_deleted_at;type:bigint;not null;default:0"`
}

var (
	ulidMu      sync.Mutex
	ulidEntropy = ulid.Monotonic(rand.Reader, 0)
)

// NewSyncID 生成一个新的账号内同步标识（ULID，决策 3）：单调时钟源 + 加密随机熵，
// 同一毫秒内多次调用也不重复。ulid.Monotonic 本身不是并发安全的，这里用一把互斥
// 锁串行化。
func NewSyncID() string {
	ulidMu.Lock()
	defer ulidMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), ulidEntropy).String()
}

// EnsureSyncID 是「行创建 / 下一次落库时就地生成标识」（R1）的落点：已有标识时
// 原样保留（标识终身不变，绝不覆盖），仓储层的 Create/Update 在落库前调用它。
func (m *SyncMeta) EnsureSyncID() {
	if m.SyncID == "" {
		m.SyncID = NewSyncID()
	}
}

// IsClaimed 报告这一行是否已经归属某个账号。
func (m SyncMeta) IsClaimed() bool { return m.SyncAccountID != 0 }

// EligibleForSync 判断这一行在 currentAccountID 下是否可以携带 SyncID 参与同步。
//
//   - currentAccountID == 0（未登录）→ false：未登录时本规格引入的一切都不存在（R12）。
//   - SyncAccountID == 0（尚未被任何账号认领）→ true：R12a 首次登录时可将它收进
//     当前账号，沿用它已经生成的 SyncID。
//   - SyncAccountID == currentAccountID → true：本账号自己的行。
//   - 其余（属于另一个非零账号）→ false：R13a 换账号，这些行不上行、也不携带
//     原同步标识。
func (m SyncMeta) EligibleForSync(currentAccountID int64) bool {
	if currentAccountID == 0 {
		return false
	}
	return m.SyncAccountID == 0 || m.SyncAccountID == currentAccountID
}
