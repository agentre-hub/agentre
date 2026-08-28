package sync_svc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

// 出站队列的操作类型，与 syncqueue_entity 的常量同名同义。
const (
	OpCreate = "create"
	OpUpdate = "update"
	OpDelete = "delete"
)

// PollInterval 是下行轮询周期（R3：「最终」的上界是一个轮询周期加一次网络往返）。
const PollInterval = 30 * time.Second

// TombstoneWindow 是墓碑 / 暂缓行 / 「没能同步的改动」共用的 30 天保留窗口
// （R2a、R5、R6）。
const TombstoneWindow = 30 * 24 * time.Hour

// pushBatch 单次上行的最大条数（server 的 binding 上限是 500）。
const pushBatch = 200

// pullLimit 单页下行条数。
const pullLimit = 200

// maxPullPages 一次同步最多连翻多少页，避免全量重同步时无限循环。
const maxPullPages = 200

// Transport 是同步的网络出入口（消费方声明的窄接口，由 server_svc 实现、bootstrap
// 注入）。未注入 = 单机模式：一行也不会发出去。
type Transport interface {
	SyncPush(ctx context.Context, items []syncwire.PushItem) ([]syncwire.PushResult, error)
	SyncPull(ctx context.Context, cursor int64, limit int) (*syncwire.PullPage, error)
	// ReportLocalPaths 上报本机路径整份快照（R16）：与同步组无关的单向投影。
	ReportLocalPaths(ctx context.Context, items []syncwire.LocalPathReportItem) error
	// PutAvatar / GetAvatar 头像按内容哈希单独传（R16a），正文不进同步载荷。
	avatarTransport
}

// AccountChannelDialer 是账号级实时通道的**可选**出入口：Transport 的实现带上它，
// 引擎就在 30 秒轮询之外多一个下行触发源；不带（单机构建、旧版 server、单元测试里
// 的替身）就只剩轮询，而那本身是一个完整可用的形态。
//
// 刻意不并进 Transport：通道是优化，不是关键路径。把它写进 Transport 等于要求每一个
// 实现都提供它，也就等于宣称「没有通道就不算能同步」——恰恰是规格否掉的那个方向
// （「把通道整个关掉，所有功能仍然正确，只是变慢到 30 秒」）。
type AccountChannelDialer interface {
	// DialAccountChannel 建立一条账号级实时通道，返回信号流。
	//
	// 连接断开时实现方**关闭**返回的 channel——调用方据此重连，并在重连成功后
	// 主动 Pull 一次补齐断线期间的变更。ctx 结束时同样关闭。
	// 建不起来时返回错误，调用方退回轮询，不重试到底、不阻塞任何操作。
	DialAccountChannel(ctx context.Context) (<-chan syncwire.AccountChannelFrame, error)
}

// LocalChange 是一次本地增删改的同步侧描述。域服务在改动**落库成功之后**交出它，
// 由同步层决定入队与上行；同步层的任何失败都不回传给域服务（R8）。
type LocalChange struct {
	// Kind 是 syncwire 的对象类型常量之一（syncKinds）。
	Kind string
	// LocalID 本地自增主键，只用于诊断与去重；成员关系没有自增主键，填 0。
	LocalID int64
	// Op 三选一：OpCreate / OpUpdate / OpDelete。
	Op string
	// Meta 是改动落库后这一行的同步元数据：同步标识必须已经生成（仓储层的
	// Create/Update 会 EnsureSyncID），基版本取 SyncVersion（R4a）。
	Meta syncmeta_entity.SyncMeta
}

// Status 是设置里同步区要展示的状态（R5 的列表另有读口）。
type Status struct {
	// Enabled 为 false 时同步区整个不存在（R12：未登录）。
	Enabled   bool  `json:"enabled"`
	AccountID int64 `json:"accountID"`
	// Cursor 本端已经消费到的同步版本号。
	Cursor int64 `json:"cursor"`
	// LastSuccessAt 上次成功同步的时间（毫秒 epoch）。
	LastSuccessAt int64 `json:"lastSuccessAt"`
	// PendingCount 待同步（出站队列 + 暂缓落地）的条数。
	PendingCount int `json:"pendingCount"`
	// DeferredCount 因引用目标未到达而暂缓落地的条数（R2a）。
	DeferredCount int `json:"deferredCount"`
	// LostChangeCount 「没能同步的改动」条数（R5）。
	LostChangeCount int `json:"lostChangeCount"`
	// LastError 上次同步失败的原因，只放错误文本，绝不放载荷 / 路径。
	LastError string `json:"lastError"`
	// BoardJoinNoticePending 看板首次并入同步组的那条一次性说明还欠着没给：界面
	// 据此弹一次说明，随后调 AcknowledgeBoardJoinNotice 把它销掉。
	BoardJoinNoticePending bool `json:"boardJoinNoticePending"`
}

// LostChangeView 是「没能同步的改动」列表的一行（R5）。
type LostChangeView struct {
	ID           int64  `json:"id"`
	EntityType   string `json:"entityType"`
	EntitySyncID string `json:"entitySyncID"`
	Reason       string `json:"reason"`
	// PayloadJSON 是那一版的内容正文，供展开查看与恢复。
	PayloadJSON  string `json:"payloadJSON"`
	OriginDevice string `json:"originDevice"`
	BaseVersion  int64  `json:"baseVersion"`
	OccurredAt   int64  `json:"occurredAt"`
}

// RestoreOutcome 是一次恢复的结果（R5a）。
type RestoreOutcome struct {
	// Restored 为 true 表示已按这份内容改回本地并正常上行。
	Restored bool
	// TargetDeleted 为 true 表示目标已在别处被删除：恢复不会让它回来，界面据此
	// 提供「按这份内容新建」。
	TargetDeleted bool
}

// outbound 是本地一行翻成的上行内容。
//
// **没有基版本**：R4a 的基版本是「本端**编辑时**见到的那一版」，由出站队列在入队时
// 记下（决策 27）。从行上现读一遍会拿到「此刻」的版本——编辑与出队之间落了一次他端
// 下行时那正好等于当前版本，冲突就判不出来了。
type outbound struct {
	SyncID              string
	UpdatedAt           int64
	ProjectSyncID       string
	AgentredFingerprint string
	Payload             json.RawMessage
}

// inbound 是一条下行项在落地前的样子；暂缓落地时整个被存进入站队列（R2a）。
type inbound struct {
	Kind                string          `json:"kind"`
	SyncID              string          `json:"sync_id"`
	ProjectSyncID       string          `json:"project_sync_id,omitempty"`
	AgentredFingerprint string          `json:"agentred_fingerprint,omitempty"`
	Payload             json.RawMessage `json:"payload,omitempty"`
	Version             int64           `json:"version"`
	UpdatedAt           int64           `json:"updated_at"`
	// OriginFingerprint 是最后一次修改来自哪台机器（决策 14）；空串 = server 直写。
	OriginFingerprint string `json:"origin_fingerprint"`
	// DeletedAt 非零 = 墓碑，值是删除时刻（决策 20）。
	DeletedAt int64 `json:"deleted_at"`
}

// IsTombstone 说这一行是不是墓碑。判据是删除时刻非零（决策 20）。
func (in *inbound) IsTombstone() bool { return in != nil && in.DeletedAt > 0 }

func inboundOf(it syncwire.PullItem) *inbound {
	return &inbound{
		Kind:                it.Kind,
		SyncID:              it.SyncID,
		ProjectSyncID:       it.ProjectSyncID,
		AgentredFingerprint: it.AgentredFingerprint,
		Payload:             json.RawMessage(it.Payload),
		Version:             it.Version,
		UpdatedAt:           it.UpdatedAt,
		OriginFingerprint:   it.OriginFingerprint,
		DeletedAt:           it.DeletedAt,
	}
}

// relatedRow 是随某一行一并入队的从属行：Agent 的执行目标随 Agent 一起上行，
// 删除时子行一并落墓碑（R6）。
type relatedRow struct {
	Kind    string
	LocalID int64
	SyncID  string
	Version int64
}

// 同步侧的语义错误。Wails 绑定层只把它们的文本交给前端。
var (
	// ErrNotLoggedIn 未登录：本规格引入的一切都不存在（R12）。
	ErrNotLoggedIn = errors.New("sync: not logged in")
	// ErrLostChangeNotFound 「没能同步的改动」里没有这一条（可能刚被别处清掉）。
	ErrLostChangeNotFound = errors.New("sync: lost change not found")
)
