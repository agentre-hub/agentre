// Package wire 定义 agentre ↔ agentred 的 transcriptimport.* RPC 协议:方法名、
// 参数 / 结果、错误 sentinel 与 typed RPC error code 的双向翻译。daemon 端 handler
// 与 host 端服务共享这一份类型,避免 Protobuf shape 漂移。
//
// **新开方法族,不给既有方法族加字段**(spec 硬约束 3):给既有方法族加字段的话,
// 旧 agentred 会静默忽略新字段并按旧语义应答 —— 「这台机器没有会话」与「这个
// daemon 不认识这件事」在 UI 上长得一模一样,版本偏斜就不可见了。新方法族在旧
// daemon 上直接回 -32601(method not found),host 侧据此报 unsupported。
//
// 命名约定与 internal/pkg/workspacefs/wire 一致:
//   - 方法在 "transcriptimport.*" 命名空间下
//   - 字段名 lowerCamelCase
//   - 错误码 -32050..-32052 是稳定 wire 值,与既有方法族的 code 段不重叠
//     (agentruntime remote wire 占 -32010..-32014,remotefs.* 占 -32030..-32035,
//     workspacefs.* 占 -32040..-32042)
//
// 三态分工(spec「远端」):
//   - **ok / unavailable 是按后端答的**,在 BackendScan.Status 里如实往返:某台
//     机器上没装 codex,只有 codex 那一档 unavailable,claude 照常出结果。
//   - **unsupported 是设备级的**,不在本包的取值里 —— 它是 host 侧对 -32601 的
//     翻译,daemon 一旦认识这个方法族就永远答不出 unsupported。
package wire

import (
	"errors"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
)

// ── RPC method names ────────────────────────────────────────────────────────

const (
	MethodScan  = "transcriptimport.scan"
	MethodOpen  = "transcriptimport.open"
	MethodTurns = "transcriptimport.turns"
	// MethodExecute 在**这台机器上**执行一次导入:读转录、建会话身份行、把回放出的
	// 轮次落进通知日志。它与前三个只读方法分开,是因为它是这一族里唯一一个写库的 ——
	// 而写在哪台机器上,决定了这条会话此后归谁执行。
	MethodExecute = "transcriptimport.execute"
)

// ── Error codes ─────────────────────────────────────────────────────────────

const (
	ErrCodeBackendUnavailable = -32050
	ErrCodeTranscriptOpen     = -32051
	ErrCodeSessionInUse       = -32052
)

// ── Sentinel errors ─────────────────────────────────────────────────────────

var (
	// ErrBackendUnavailable 这台机器上没有这个后端的磁盘读取器(没装那个 CLI,
	// 或者这个 daemon 版本还不认识它)。与「读取失败」区分开:它是可预期的空,
	// 不是故障。
	ErrBackendUnavailable = errors.New("transcriptimport: backend unavailable on this device")
	// ErrTranscriptOpen 定位符打不开:文件已删、已损坏,或越出读取器自己的根目录。
	ErrTranscriptOpen = errors.New("transcriptimport: cannot open transcript")
	// ErrSessionInUse 调用方铸的会话 id 已经被这台对端的**另一条**会话占着。会话 id
	// 是各客户端本地自增的,复用一个还在跑的号会把那条会话的身份行改写成一份磁盘转录
	// 的元信息,而它的通知日志还在继续涨 —— 两段互不相干的转录就此长在同一个号上。
	ErrSessionInUse = errors.New("transcriptimport: session id already in use")
)

// ToRPCError 把 sentinel 包成 *rpcerror.Error,daemon handler 返回。
// 非 sentinel 返 nil,调用方自己包装。
func ToRPCError(err error) *rpcerror.Error {
	switch {
	case errors.Is(err, ErrBackendUnavailable):
		return &rpcerror.Error{Code: ErrCodeBackendUnavailable, Message: err.Error()}
	case errors.Is(err, ErrTranscriptOpen):
		return &rpcerror.Error{Code: ErrCodeTranscriptOpen, Message: err.Error()}
	case errors.Is(err, ErrSessionInUse):
		return &rpcerror.Error{Code: ErrCodeSessionInUse, Message: err.Error()}
	}
	return nil
}

// FromRPCError 反向把 *rpcerror.Error 翻成 sentinel。未知 code 返原 err ——
// 尤其是 -32601:它必须原样传上去,由 host 侧翻成 unsupported,而不是在这里被
// 抹成「后端不可用」(那正是硬约束 3 要防的静默降级)。
func FromRPCError(err error) error {
	var rpcErr *rpcerror.Error
	if !errors.As(err, &rpcErr) {
		return err
	}
	switch rpcErr.Code {
	case ErrCodeBackendUnavailable:
		return ErrBackendUnavailable
	case ErrCodeTranscriptOpen:
		return ErrTranscriptOpen
	case ErrCodeSessionInUse:
		return ErrSessionInUse
	}
	return err
}

// ── Scan ────────────────────────────────────────────────────────────────────

// 发现结果的判别值。**Status 没有 omitempty**:空候选必须自带理由 —— 「问出来
// 就是没有」与「这台机器答不出」在 UI 上是两句不同的话。
const (
	// StatusOK 这一档是问出来的,照它增删。
	StatusOK = "ok"
	// StatusUnavailable 这一档此刻答不出:目录读不动,或这台机器上没装那个 CLI。
	StatusUnavailable = "unavailable"
)

// ScanParams 是 MethodScan 的请求。Backends 为空表示「这台设备上注册的全部」。
type ScanParams struct {
	Backends []string                `json:"backends,omitempty"`
	Filter   transcriptimport.Filter `json:"filter"`
}

// BackendScan 是某个后端那一档的结果。
type BackendScan struct {
	Backend    string                       `json:"backend"`
	Status     string                       `json:"status"`
	Reason     string                       `json:"reason,omitempty"`
	Candidates []transcriptimport.Candidate `json:"candidates,omitempty"`
}

// ScanResult 是 MethodScan 的应答:按后端分档,一档失败不影响其余。
type ScanResult struct {
	Backends []BackendScan `json:"backends"`
}

// ── Open ────────────────────────────────────────────────────────────────────

// OpenParams 是 MethodOpen 的请求。
type OpenParams struct {
	Backend string `json:"backend"`
	Locator string `json:"locator"`
}

// OpenResult 只带元信息:daemon 侧取完 Meta 就把转录关掉,**不持有跨调用的句柄**
// —— 没有句柄就没有需要超时回收的会话状态,也就没有断线泄漏。
type OpenResult struct {
	Meta transcriptimport.Meta `json:"meta"`
}

// ── Turns ───────────────────────────────────────────────────────────────────

// DefaultMaxTurns / MaxTurnsPerPage 是一页的轮数预算。分页而不是一次全量:
// 42 轮 / 402 次工具调用的会话在本机是常态,整份塞进一帧会撞上 16 MiB 的帧上限。
const (
	DefaultMaxTurns = 8
	MaxTurnsPerPage = 32
)

// TurnsParams 是 MethodTurns 的请求:从 StartIndex 起取至多 MaxTurns 轮。
// MaxTurns <= 0 用 DefaultMaxTurns,超过 MaxTurnsPerPage 按上限裁。
type TurnsParams struct {
	Backend    string `json:"backend"`
	Locator    string `json:"locator"`
	StartIndex int    `json:"startIndex,omitempty"`
	MaxTurns   int    `json:"maxTurns,omitempty"`
}

// TurnsResult 是一页轮次。NextIndex 是下一页该从哪一轮起;HasMore 为假表示后面
// 没有了 —— 客户端据它收工,而不是靠「这一页不满」猜(按字节预算提前收的页同样
// 不满,但后面还有)。
type TurnsResult struct {
	Turns     []transcriptimport.Turn `json:"turns"`
	NextIndex int                     `json:"nextIndex"`
	HasMore   bool                    `json:"hasMore"`
}

// ── Execute ─────────────────────────────────────────────────────────────────

// ExecuteParams 是 MethodExecute 的请求:在这台机器上把 {后端, 定位符} 那份转录
// 落成一条归本机执行的会话。
//
// SessionID 由**调用方**铸(与 runtime.run 同一条规矩:会话 id 是各客户端本地自增
// 的主键,daemon 从不发号)。AgentID / AgentSyncID 是这条会话挂在哪个 Agent 名下,
// 与每轮起手携带的那两格同义,原样落进身份行。
//
// 标题、工作目录与 provider 会话身份**不在入参里**:它们是转录自己的事实,由这台
// 机器读出来写下去 —— 让调用方报一份等于给同一件事开第二个真相源。
type ExecuteParams struct {
	Backend     string `json:"backend"`
	Locator     string `json:"locator"`
	SessionID   int64  `json:"sessionId"`
	AgentID     int64  `json:"agentId,omitempty"`
	AgentSyncID string `json:"agentSyncId,omitempty"`
	// PeerFingerprint 把这条会话落在**点名的 origin**名下而不是调用方自己名下
	// (与 runtime.run 的同名字段同义)。省略 = 调用方自己的对端;点名是账号级能力,
	// 配对身份点名任何 origin 都会被拒。
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
}

// ExecuteResult 是 MethodExecute 的应答。
//
// AlreadyImported 为真表示这条 provider 会话在这台对端名下已经有一条会话了,
// SessionID 指的是**库里那条**(未必等于请求里铸的号),Turns 为 0 —— 日志一条都
// 没再落。重复导入是可预期的正常分支,不是错误。
type ExecuteResult struct {
	SessionID         int64  `json:"sessionId"`
	ProviderSessionID string `json:"providerSessionId,omitempty"`
	Cwd               string `json:"cwd,omitempty"`
	Title             string `json:"title,omitempty"`
	Turns             int    `json:"turns"`
	AlreadyImported   bool   `json:"alreadyImported,omitempty"`
}
