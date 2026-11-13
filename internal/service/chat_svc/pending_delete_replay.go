package chat_svc

import (
	"context"
	"errors"
	"sync"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// pending_delete_replay.go 把 chat_pending_deletes 里记下的待删除补发出去（spec
// 2026-11-12 决策 12）：账号登录 / 登录刷新、配对 agentred 上线、启动时各触发一次。
// 发送本身复用 delete_propagation.go 的 deleteSavedSession / deleteOnAgentred。
// 日志只记对话身份、指纹与计数，不记标题与内容。

// rpcCodeNotFound 是对端答「没有这条会话」的 RPC 码（桌面端入站删除的 -32002）。
const rpcCodeNotFound = -32002

// replayScope 是一次补发要处理哪些待删除；多次触发在补发进行中会合并。
type replayScope struct {
	all     bool
	server  bool
	devices map[int64]bool
}

func (sc replayScope) empty() bool { return !sc.all && !sc.server && len(sc.devices) == 0 }

func (sc replayScope) merge(o replayScope) replayScope {
	sc.all = sc.all || o.all
	sc.server = sc.server || o.server
	if len(o.devices) > 0 {
		devices := make(map[int64]bool, len(sc.devices)+len(o.devices))
		for id := range sc.devices {
			devices[id] = true
		}
		for id := range o.devices {
			devices[id] = true
		}
		sc.devices = devices
	}
	return sc
}

func (sc replayScope) covers(row *chat_entity.PendingDelete) bool {
	if sc.all {
		return true
	}
	switch row.Target {
	case chat_entity.PendingDeleteTargetServer:
		return sc.server
	case chat_entity.PendingDeleteTargetAgentred:
		return sc.devices[row.DeviceID]
	}
	return false
}

// replayScheduler 保证补发不重叠，并且触发方永不被阻塞：补发在后台 goroutine 里跑，
// 它运行期间到来的触发合并成「跑完再来一轮」，不会丢。
type replayScheduler struct {
	run     func(ctx context.Context, sc replayScope)
	mu      sync.Mutex
	running bool
	queued  replayScope
}

func (r *replayScheduler) schedule(sc replayScope) {
	r.mu.Lock()
	r.queued = r.queued.merge(sc)
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()

	gogo.Go(func() error {
		for {
			r.mu.Lock()
			cur := r.queued
			r.queued = replayScope{}
			if cur.empty() {
				r.running = false
				r.mu.Unlock()
				return nil
			}
			r.mu.Unlock()
			r.runGuarded(cur)
		}
	})
}

// runGuarded 让一轮补发的 panic 不至于把 running 永久卡在 true。
func (r *replayScheduler) runGuarded(sc replayScope) {
	defer func() {
		if p := recover(); p != nil {
			logger.Default().Error("chat_svc.replayScheduler: replay panicked", zap.Any("panic", p))
		}
	}()
	r.run(context.Background(), sc)
}

var pendingDeleteReplayer = &replayScheduler{run: func(ctx context.Context, sc replayScope) {
	if s, ok := defaultChat.(*chatSvc); ok && s != nil {
		s.replayPendingDeletes(ctx, sc)
	}
}}

// ReplayAllPendingDeletes 补发全部待删除（启动时）。立即返回。
func ReplayAllPendingDeletes() { pendingDeleteReplayer.schedule(replayScope{all: true}) }

// ReplayServerPendingDeletes 补发 target=server 的待删除（账号登录 / 登录刷新后）。立即返回。
func ReplayServerPendingDeletes() { pendingDeleteReplayer.schedule(replayScope{server: true}) }

// ReplayDevicePendingDeletes 补发 target=agentred 且属于这台配对设备的待删除（它上线后）。立即返回。
func ReplayDevicePendingDeletes(deviceID int64) {
	pendingDeleteReplayer.schedule(replayScope{devices: map[int64]bool{deviceID: true}})
}

// replayPendingDeletes 把范围内的待删除逐条重发：成功、或对方答「已经没有了」就清掉这一行；
// 其它失败、以及没登录，都留着等下一次。
func (s *chatSvc) replayPendingDeletes(ctx context.Context, sc replayScope) {
	repo := chat_repo.PendingDelete()
	if repo == nil {
		return
	}
	rows, err := repo.List(ctx)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.replayPendingDeletes: list pending deletes failed", zap.Error(err))
		return
	}
	var sent, cleared, kept, skipped int
	serverLoggedOut := false
	for _, row := range rows {
		if !sc.covers(row) {
			continue
		}
		log := logger.Ctx(ctx).With(
			zap.String("conversationId", row.ConversationID),
			zap.String("target", string(row.Target)),
			zap.String("machineFingerprint", row.MachineFingerprint))
		var sendErr error
		switch row.Target {
		case chat_entity.PendingDeleteTargetServer:
			if serverLoggedOut {
				skipped++
				continue
			}
			_, sendErr = deleteSavedSession(ctx, row.ConversationID, row.MachineFingerprint)
			if sendErr != nil && isLoggedOut(sendErr) {
				// 没登录：不是失败，行原样留着，等登录后再补发。
				serverLoggedOut = true
				skipped++
				continue
			}
		case chat_entity.PendingDeleteTargetAgentred:
			sendErr = s.deleteOnAgentred(ctx, row.DeviceID, row.ConversationID)
			var rpcErr *protorpc.Error
			if errors.As(sendErr, &rpcErr) && rpcErr.Code == rpcCodeNotFound {
				sendErr = nil // 对端本就没有这条会话：目的已达成
			}
		default:
			skipped++
			continue
		}
		sent++
		if sendErr != nil {
			kept++
			log.Warn("chat_svc.replayPendingDeletes: replay failed, keeping pending delete",
				zap.Int64("deviceId", row.DeviceID), zap.Error(sendErr))
			continue
		}
		if err := repo.Remove(ctx, row.ConversationID, row.Target); err != nil {
			kept++
			log.Warn("chat_svc.replayPendingDeletes: replay succeeded but clearing the row failed", zap.Error(err))
			continue
		}
		cleared++
	}
	if sent+skipped > 0 {
		logger.Ctx(ctx).Info("chat_svc.replayPendingDeletes: done",
			zap.Int("sent", sent), zap.Int("cleared", cleared),
			zap.Int("kept", kept), zap.Int("skippedLoggedOut", skipped))
	}
}
