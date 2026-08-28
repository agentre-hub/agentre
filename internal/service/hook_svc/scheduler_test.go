package hook_svc

import (
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/hook_entity"
)

// TestComputeNextRun_Cron 固定「next_run_at 是毫秒 epoch」这条存储契约。
// now 传毫秒、返回值也必须是毫秒；把毫秒当秒解读会落到公元 58000 年，
// 而返回秒会让 hook_repo 的 `next_run_at <= ?` 立刻命中、hook 每轮都抢跑。
func TestComputeNextRun_Cron(t *testing.T) {
	s := &hookSvc{}
	h := &hook_entity.Hook{ScheduleExpr: "0 0 * * *", Timezone: "UTC"}

	now := time.Date(2026, 8, 26, 10, 30, 0, 0, time.UTC).UnixMilli()
	want := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC).UnixMilli()

	if got := s.computeNextRun(h, now); got != want {
		t.Fatalf("cron next = %d, want %d", got, want)
	}
}

// TestComputeNextRun_BadCronFallback 覆盖回退分支的同一条契约：
// 表达式解析失败时推进一个 fallbackInterval，单位同样是毫秒。
// 按秒回退只会把下次运行推后 3.6 秒,等于失去节流。
func TestComputeNextRun_BadCronFallback(t *testing.T) {
	s := &hookSvc{}
	h := &hook_entity.Hook{ScheduleExpr: "garbage", Timezone: "UTC"}

	now := time.Date(2026, 8, 26, 10, 30, 0, 0, time.UTC).UnixMilli()
	want := now + fallbackInterval.Milliseconds()

	if got := s.computeNextRun(h, now); got != want {
		t.Fatalf("bad cron fallback = %d, want %d", got, want)
	}
}
