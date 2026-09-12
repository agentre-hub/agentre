package agentruntime

import (
	"context"
	"encoding/json"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

//go:generate mockgen -source ports.go -destination mock_agentruntime/mock_ports.go

// DaemonClientPort 抽象 internal/daemon/client.Client，仅为 RemoteRunner 单测注入用。
// 生产路径上 *client.Client 直接实现这个接口（方法签名已对齐，无需 adapter）。
// 编译期断言落在 daemon/client 包内,避免 agentruntime(抽象层)反向依赖 daemon。
type DaemonClientPort interface {
	Call(ctx context.Context, method string, params, result any) error
	Notify(method string, params any) error
	Handle(method string, fn func(ctx context.Context, params json.RawMessage) (any, error))
	Closed() <-chan struct{}
	Close() error
}

// SessionCursorPort 是「桌面端已消费到远端会话通知流的哪一条」这个游标的读写端口。
// 游标落在 chat_sessions 上，但 internal/pkg 是叶子层，不得反向 import repository /
// service，所以端口按 DIP 声明在消费方(本包)，实现由 chat_svc 用 RegisterSessionCursor
// 注入 —— 与各 runtime 用 RegisterRuntime 注册进 registry 是同一种接线方式。
type SessionCursorPort interface {
	// LoadCursor 返回 sessionID 已消费到的通知 seq。
	// daemonFingerprint 是本次连上的 daemon 实例标识("sha256:<hex>")；与会话记录的
	// 不一致(daemon 重装、换机、数据目录被清)、或该会话根本不是在远端跑的，都返回
	// ok=false：此时记录的游标指向另一条通知日志，调用方不得据此增量拉取。
	LoadCursor(ctx context.Context, sessionID int64, daemonFingerprint devicefp.Carrier) (seq int64, ok bool, err error)
	// SaveCursor 记录 sessionID 已消费到 seq。daemonFingerprint 是 seq 所属那条通知
	// 日志的 daemon 实例标识 —— 与读侧对称:seq 离开它所属的日志就是个错的数字,
	// 会话已改绑到别的 daemon 时这次写入落空(不报错),下次重连至多重复拉取,
	// 而不会从一个远超新日志长度的位置往后拉、把整段 transcript 丢掉。
	SaveCursor(ctx context.Context, sessionID int64, daemonFingerprint devicefp.Carrier, seq int64) error
}

// sessionCursor 是进程级注入点(单 desktop 进程一份)。nil = 未注入。
var sessionCursor SessionCursorPort

// RegisterSessionCursor 注入 SessionCursorPort 实现；传 nil 清空(测试用)。
func RegisterSessionCursor(p SessionCursorPort) { sessionCursor = p }

// SessionCursor 返回已注入的实现，未注入时为 nil。
func SessionCursor() SessionCursorPort { return sessionCursor }
