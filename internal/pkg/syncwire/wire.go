// Package syncwire 定义桌面端与 server 之间工作区同步协议的线上结构、状态字面量
// 与载荷守卫（docs/specs/2026-08-07-workspace-sync.md「双向同步的行为」）。
//
// 它是一个叶子包：同步引擎（internal/service/sync_svc）与网络出入口
// （internal/service/server_svc）都依赖它，因此两者之间不需要互相 import。
//
// 载荷里不出现任何桌面端的本地自增 ID：跨机引用一律是同步标识、agentred 指纹或
// provider_key，全是字符串。GuardPayload 在上行前强制执行这条边界。
package syncwire

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	wire "github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 线上契约归共享 module github.com/agentre-hub/agentre/pkg/syncwire 所有 —— 服务端
// 与桌面端消费的是同一份定义。本包对它做别名再导出:五十多个调用点因此一行不用改,
// 而「谁拥有这份契约」这个问题只有一个答案。
//
// 留在本包的是**本端专属**的东西:载荷守卫(guard.go)、业务码的客户端表达
// (ErrResyncRequired / ErrCursorUnknown)、以及账号级实时通道的解码。
const (
	KindProject         = wire.KindProject
	KindDepartment      = wire.KindDepartment
	KindAgent           = wire.KindAgent
	KindAgentBackend    = wire.KindAgentBackend
	KindAgentExecTarget = wire.KindAgentExecTarget
	KindProjectAgent    = wire.KindProjectAgent
	KindProjectLocation = wire.KindProjectLocation
	KindLLMProvider     = wire.KindLLMProvider
	KindAgentBackendCLI = wire.KindAgentBackendCLI
	KindLabel           = wire.KindLabel
	KindIssue           = wire.KindIssue
	KindIssueLabel      = wire.KindIssueLabel
)

const (
	PushStatusAccepted = wire.PushStatusAccepted
	PushStatusConflict = wire.PushStatusConflict
	PushStatusRejected = wire.PushStatusRejected
)

// 单条拒绝的三个原因。本端从前只认得 deleted 一个,另外两个走的是兜底分支 ——
// 行为是对的,但契约里少了名字。
const (
	PushRejectReasonDeleted = wire.PushRejectReasonDeleted
	PushRejectReasonKind    = wire.PushRejectReasonKind
	PushRejectReasonPayload = wire.PushRejectReasonPayload
)

// CodeResyncRequired / CodeCursorUnknown 是 server 的两个业务码。
const (
	CodeResyncRequired = wire.CodeResyncRequired
	CodeCursorUnknown  = wire.CodeCursorUnknown
)

// PushItem / PushResult / PullItem / PullPage 是线上结构本身。
//
// Payload 从 []byte 换成了 json.RawMessage(别名指向共享定义):这正是本包从前不带
// json 标签的原因 —— []byte 会被 encoding/json 编成 base64,于是编码只能另找地方做,
// server_svc 因此又抄了一份私有结构。换成 RawMessage 之后那一份也没有存在理由了。
type (
	PushItem   = wire.PushItem
	PushResult = wire.PushResult
	PullItem   = wire.PullItem
	PullPage   = wire.PullPage
)

// ErrResyncRequired 是 CodeResyncRequired 的客户端表达:上行一律被拒,必须先拉一份
// 全量快照并以之为准。
var ErrResyncRequired = errors.New("sync: resync required")

// ErrCursorUnknown 是 CodeCursorUnknown 的客户端表达。
//
// 它与 ErrResyncRequired **不是**一回事,处置也相反:
//
//   - ErrResyncRequired(上行时)＝「你离线太久」。server 的历史是全的、本端的不全,
//     以快照为准,队列里基版本对不上的一律拦下(R6a)—— 那正是防复活的那一条。
//   - ErrCursorUnknown(下行时)＝「我不认识你说的那段历史」。server 的历史没了、
//     本端的才是全的,因此必须把 server 不认识的本地行**重新上行**,否则整个工作区
//     静默留在本机,而界面上待同步是 0、没有任何错误可循。
var ErrCursorUnknown = errors.New("sync: server does not recognize this cursor")

// LocalPathReportItem 是上报组的一条：某个项目在这台设备上的真实本机路径。
// 与同步组的那些表无关——本机路径不在桌面端之间流动，只单向上报给
// server，按设备分命名空间存放。
// LocalPathReportItem 是上报组的一条。LocalPathItem 是契约里的名字,本包这个别名
// 是历史称呼,调用点因此不用改。
type LocalPathReportItem = wire.LocalPathItem

// LocalPathItem 与 LocalPathReportItem 同物,按契约里的名字再导出一次。
type LocalPathItem = wire.LocalPathItem

// ── 账号级实时通道 ─────────────────────────────────────────────────────────

// AccountChannelSyncVersion 是账号级实时通道上目前唯一的一种信号：这个账号的
// 同步版本推进到了 AccountChannelFrame.Version。
const AccountChannelSyncVersion = "sync_version"

// AccountChannelFrame 是账号级实时通道交给同步引擎的业务信号。线上由统一
// Protobuf Codec 承载，transport 与业务结构不直接依赖线上表示。
//
// 通道**只送信号，不送对象内容**：收到之后照常走 Pull。因此漏帧、乱序、重复都
// 无害，通道断了也只退化成 30 秒轮询（规格「账号级实时通道 · 失败处理」）。
type AccountChannelFrame struct {
	// Type 是信号种类，取值见 AccountChannelSyncVersion。不认识的种类一律忽略：
	// 通道日后会承载别的通知，旧客户端不该因此断连。
	Type string `json:"type"`
	// Version 是该账号同步版本序列推进到的位置。它**只**用于「该拉了」的判断——
	// 拉哪些由本端自己的游标决定，绝不拿它当游标用，否则乱序信号就会跳过变更。
	Version int64 `json:"version"`
}

// DecodeAccountChannelFrame 解账号通知。未知通知返回 known=false，调用方应忽略
// 该帧但保持连接。Protobuf 未知字段由运行时忽略，以允许新端向旧端追加字段。
func DecodeAccountChannelFrame(payload []byte) (frame AccountChannelFrame, known bool, err error) {
	var envelope agentrewire.WireFrame
	if err := proto.Unmarshal(payload, &envelope); err != nil {
		return frame, false, fmt.Errorf("decode account channel envelope: %w", err)
	}
	notification := envelope.GetNotification()
	if notification == nil {
		return frame, false, nil
	}
	syncVersion := notification.GetAccountSyncVersion()
	if syncVersion == nil {
		return frame, false, nil
	}
	if syncVersion.Version > uint64(^uint64(0)>>1) {
		return frame, false, fmt.Errorf("decode account sync version: version overflows int64")
	}
	return AccountChannelFrame{Type: AccountChannelSyncVersion, Version: int64(syncVersion.Version)}, true, nil
}
