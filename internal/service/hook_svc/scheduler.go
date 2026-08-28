package hook_svc

import (
	"context"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/hook_entity"
	"github.com/agentre-hub/agentre/internal/repository/hook_repo"
)

const (
	schedulerTick    = 15 * time.Second
	maxConcurrent    = 4
	fallbackInterval = time.Hour
)

type schedulerState struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	inflight map[int64]struct{}
}

// StartScheduler 是包级入口：供 internal/app 启动。
func StartScheduler(ctx context.Context) context.CancelFunc { return Hook().StartScheduler(ctx) }

func (s *hookSvc) StartScheduler(parent context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(parent)
	s.sched.mu.Lock()
	if s.sched.cancel != nil {
		s.sched.cancel()
	}
	s.sched.cancel = cancel
	if s.sched.inflight == nil {
		s.sched.inflight = map[int64]struct{}{}
	}
	s.sched.mu.Unlock()

	go func() {
		sem := make(chan struct{}, maxConcurrent)
		ticker := time.NewTicker(schedulerTick)
		defer ticker.Stop()
		s.tick(ctx, sem)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tick(ctx, sem)
			}
		}
	}()
	return func() {
		cancel()
		s.sched.mu.Lock()
		s.sched.cancel = nil
		s.sched.mu.Unlock()
	}
}

func (s *hookSvc) tick(ctx context.Context, sem chan struct{}) {
	if hook_repo.Hook() == nil {
		return
	}
	due, err := hook_repo.Hook().ListDue(ctx, s.now())
	if err != nil {
		logger.Ctx(ctx).Warn("hook_svc.tick: list due", zap.Error(err))
		return
	}
	for _, h := range due {
		if ctx.Err() != nil {
			return
		}
		if !s.claim(h.ID) {
			continue // 仍在跑，不重叠
		}
		hook := h
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			s.release(hook.ID)
			return
		}
		go func() {
			defer func() { <-sem; s.release(hook.ID) }()
			if _, err := s.executeHook(ctx, hook, false); err != nil {
				logger.Ctx(ctx).Warn("hook_svc.tick: execute", zap.Int64("hook_id", hook.ID), zap.Error(err))
			}
		}()
	}
}

func (s *hookSvc) claim(id int64) bool {
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	if _, ok := s.sched.inflight[id]; ok {
		return false
	}
	s.sched.inflight[id] = struct{}{}
	return true
}

func (s *hookSvc) release(id int64) {
	s.sched.mu.Lock()
	delete(s.sched.inflight, id)
	s.sched.mu.Unlock()
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func (s *hookSvc) computeNextRun(h *hook_entity.Hook, now int64) int64 {
	sched, err := cronParser.Parse(h.ScheduleExpr)
	if err != nil {
		logger.Ctx(context.Background()).Warn("hook_svc.computeNextRun: bad cron", zap.String("expr", h.ScheduleExpr))
		return now + fallbackInterval.Milliseconds()
	}
	loc, lerr := time.LoadLocation(orDefault(h.Timezone, "UTC"))
	if lerr != nil {
		loc = time.UTC
	}
	return sched.Next(time.UnixMilli(now).In(loc)).UnixMilli()
}
