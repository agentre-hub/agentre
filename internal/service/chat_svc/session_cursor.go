package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// sessionCursorPort 用 chat_sessions 实现 agentruntime.SessionCursorPort：
// 远端 runtime 住在 internal/pkg 叶子层，够不着 repository，游标读写由本层代劳。
type sessionCursorPort struct{}

// init 把实现注入 agentruntime。与 runtimes/* 在 init 里 RegisterRuntime 同款：
// 谁 import 了 chat_svc(桌面端 composition root 必然 import)，端口就已装配好。
func init() { agentruntime.RegisterSessionCursor(sessionCursorPort{}) }

// LoadCursor 读出会话已消费到的通知 seq。实例标识对不上(daemon 重装、换机、数据目录
// 被清)时判为失效，交出 ok=false 而不是一个指向别的日志的 seq。
func (sessionCursorPort) LoadCursor(ctx context.Context, sessionID int64, daemonFingerprint string) (int64, bool, error) {
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return 0, false, err
	}
	if sess == nil {
		return 0, false, nil
	}
	if !sess.CursorValidFor(daemonFingerprint) {
		if sess.RanOnDaemon() {
			logger.Ctx(ctx).Warn("chat_svc.LoadCursor: daemon instance changed, cursor invalidated",
				zap.Int64("sessionId", sessionID),
				zap.String("recordedDaemon", sess.ExecDeviceFingerprint),
				zap.String("currentDaemon", daemonFingerprint))
		}
		return 0, false, nil
	}
	return sess.EventCursor, true, nil
}

// SaveCursor 推进会话的通知消费游标。身份一路带到仓储的 WHERE 守卫:会话已改绑到
// 别的 daemon 时,老连接上迟到的这次写入落空,不会把老日志的 seq 记到新 daemon 名下。
func (sessionCursorPort) SaveCursor(ctx context.Context, sessionID int64, daemonFingerprint string, seq int64) error {
	return chat_repo.Session().UpdateEventCursor(ctx, sessionID, daemonFingerprint, seq)
}
