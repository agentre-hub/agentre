package agenttool

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// WriteGate 是写工具的审批闸门:登记审批 → 挂起等应答 → 终态分发。宿主提供的是
// 「怎么登记/怎么回写终态/等多久/批准后执行什么」,挂起与终态编排归 server。
// waiter 与前端应答路由由 chat_svc 统一持有,故 Begin 返回的是它给的等待 channel。
type WriteGate struct {
	// Timeout 单次审批的挂起上限(实时取:宿主可在运行期调整)。
	Timeout func() time.Duration
	// Begin 登记一张审批卡并返回等待 channel。
	Begin func(ctx context.Context, sessionID int64, requestID, tool string, input map[string]any) (<-chan bool, error)
	// Finish 回写审批终态(approved / denied / expired)。
	Finish func(ctx context.Context, sessionID int64, requestID, status, result string) error
	// Exec 执行一个已批准的写工具,返回给 agent 的文本结果。
	Exec func(ctx context.Context, ref Ref, tool string, rawArgs json.RawMessage) (string, error)
}

func (g *WriteGate) serve(w http.ResponseWriter, r *http.Request, rpcID json.RawMessage, ref Ref, tool string, rawArgs json.RawMessage) {
	var input map[string]any
	_ = json.Unmarshal(rawArgs, &input)
	requestID := uuid.NewString()

	ch, err := g.Begin(r.Context(), ref.SessionID, requestID, tool, input)
	if err != nil {
		writeRPCError(w, rpcID, -32000, "审批通道不可用: "+err.Error())
		return
	}

	select {
	case allow := <-ch:
		if !allow {
			_ = g.Finish(r.Context(), ref.SessionID, requestID, "denied", "")
			writeRPCResult(w, rpcID, textResult("用户拒绝了此操作"))
			return
		}
		result, execErr := g.Exec(r.Context(), ref, tool, rawArgs)
		if execErr != nil {
			// 业务校验失败(循环挂载/重名/cron 非法等)也算 approved 终态,错误进 Result 给 agent 纠错
			_ = g.Finish(r.Context(), ref.SessionID, requestID, "approved", "执行失败: "+execErr.Error())
			writeRPCResult(w, rpcID, textResult("已批准但执行失败: "+execErr.Error()))
			return
		}
		_ = g.Finish(r.Context(), ref.SessionID, requestID, "approved", result)
		writeRPCResult(w, rpcID, textResult(result))
	case <-time.After(g.Timeout()):
		_ = g.Finish(r.Context(), ref.SessionID, requestID, "expired", "")
		writeRPCResult(w, rpcID, textResult("审批超时，操作未执行"))
	case <-r.Context().Done():
		// 请求 ctx 已死,用 Background 调 Finish
		_ = g.Finish(context.Background(), ref.SessionID, requestID, "expired", "")
	}
}
