package piagent

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

var _ agentruntime.SteerDrainer = (*Runtime)(nil)

// heldSteers 是一条会话扣在 agentre 这边、留给下一轮的排队消息。
//
// pi 的 steer 一送进 RPC 就由 pi 自己在这一轮里消费（轮末也会先吃完 steering 队列
// 再停），于是「轮中切了模型、再排一条消息」那条消息永远跑在旧模型上，开轮前的
// applyPendingCLIModel 根本没机会介入。留给下一轮的消息因此不进 pi，轮末由
// DrainPending 交回 chat_svc 作为下一轮的 prompt，走与手动发送同一条开轮路径。
type heldSteers struct {
	steers []agentruntime.ConsumedSteer
	// handedOff 表示刚交回过一批、调用方紧接着要开下一轮：这段空档里到达的 Steer
	// 同样接住（SteerDrainer 契约），下一轮 PrepareRun 登记时清掉。
	handedOff bool
}

// SteerNextTurn 把一条排队消息扣下留给下一轮（不送进 pi 当前这一轮）。没有在飞的
// 一轮时返回 ErrNoActiveTurn，调用方照常发起一轮。
func (r *Runtime) SteerNextTurn(ctx context.Context, sessionID int64, queuedID, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	held := r.held[sessionID]
	if r.prepared[sessionID] == nil && r.active[sessionID] == nil && (held == nil || !held.handedOff) {
		return agentruntime.ErrNoActiveTurn
	}
	r.holdSteerLocked(sessionID, queuedID, text)
	logger.Ctx(ctx).Debug("piagent.Runtime.SteerNextTurn: steer held for next turn",
		zap.Int64("sessionId", sessionID), zap.String("queuedId", queuedID))
	return nil
}

// DrainPending 交回扣下的排队消息并清空；非空时进入交接态，直到下一轮登记。
func (r *Runtime) DrainPending(ctx context.Context, sessionID int64) []agentruntime.ConsumedSteer {
	r.mu.Lock()
	defer r.mu.Unlock()
	held := r.held[sessionID]
	if held == nil || len(held.steers) == 0 {
		return nil
	}
	out := held.steers
	held.steers, held.handedOff = nil, true
	logger.Ctx(ctx).Debug("piagent.Runtime.DrainPending: held steers handed to next turn",
		zap.Int64("sessionId", sessionID), zap.Int("count", len(out)))
	return out
}

// holdSteerLocked 调用方持 r.mu。
func (r *Runtime) holdSteerLocked(sessionID int64, queuedID, text string) {
	held := r.held[sessionID]
	if held == nil {
		held = &heldSteers{}
		r.held[sessionID] = held
	}
	held.steers = append(held.steers, agentruntime.ConsumedSteer{QueuedID: queuedID, Text: text})
}

// steerDuringHandoffLocked 交接空档里（已交回一批、下一轮还没登记）到达的 Steer 没有
// 流可送，扣下留到下一轮之后。调用方持 r.mu。
func (r *Runtime) steerDuringHandoffLocked(sessionID int64, queuedID, text string) bool {
	held := r.held[sessionID]
	if held == nil || !held.handedOff {
		return false
	}
	r.holdSteerLocked(sessionID, queuedID, text)
	return true
}

// endHandoffLocked 下一轮已登记：交接态结束，空档里扣下的消息留到这一轮轮末。调用方持 r.mu。
func (r *Runtime) endHandoffLocked(sessionID int64) {
	held := r.held[sessionID]
	if held == nil {
		return
	}
	held.handedOff = false
	if len(held.steers) == 0 {
		delete(r.held, sessionID)
	}
}
