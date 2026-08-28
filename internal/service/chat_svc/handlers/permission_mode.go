package handlers

import (
	"context"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// PermissionModeWriter handler 通过这个把新 mode 落库 + 改 sess 字段。
// chat_svc 实现:走 UpdatePermissionMode(column-only) + 把 sess.PermissionMode 改成新值。
// CurrentMode 返回 sess 当前 mode 用于幂等判断。
type PermissionModeWriter interface {
	CurrentMode(sess any) string
	SetMode(ctx context.Context, sess any, mode string) error
}

type PermissionModeChangedHandler struct {
	Writer PermissionModeWriter
}

// Apply 落 PermissionModeChangeBlock + 通过 PermissionModeWriter 持久化
// sess.PermissionMode(走 context.WithoutCancel 抗 turn cancel,spec §1.4)。
// emit StreamSessionStatus patch。
//
// 幂等:r.Mode == sess.PermissionMode 时不写 / 不 emit。
func (h PermissionModeChangedHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.PermissionModeChanged)
	if r.Mode == "" {
		return nil
	}
	if tc != nil && h.Writer != nil && tc.Session != nil {
		if h.Writer.CurrentMode(tc.Session) == r.Mode {
			return nil
		}
	}

	blk := &blocks.PermissionModeChangeBlock{To: r.Mode, At: time.Now().UnixMilli()}
	acc.AddBlock(blk, "")

	if tc != nil && h.Writer != nil && tc.Session != nil {
		_ = h.Writer.SetMode(context.WithoutCancel(ctx), tc.Session, r.Mode)
	}
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind": "session_status",
			"sessionStatus": map[string]any{
				"permissionMode": r.Mode,
			},
		})
	}
	return nil
}
