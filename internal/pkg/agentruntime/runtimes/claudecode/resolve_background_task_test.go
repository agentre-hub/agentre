package claudecode

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// BackgroundTaskID 让最简替身默认「谁都不认识」;要模拟子进程报过 task_started 的用
// resolvingCCHandle。
func (f *fakeCCHandle) BackgroundTaskID(string) (string, bool) { return "", false }

// resolvingCCHandle 在最简替身之上注入一张 tool_use id → task_id 表,模拟子进程收到过
// system{subtype:"task_started"} 后的反查能力。
type resolvingCCHandle struct {
	*fakeCCHandle
	ids map[string]string
}

func (h *resolvingCCHandle) BackgroundTaskID(toolUseID string) (string, bool) {
	id, ok := h.ids[toolUseID]
	return id, ok
}

// TestRuntime_ResolveBackgroundTask 钉死 BackgroundTaskResolver:把派遣卡的 tool_use id
// 反查成这个子进程此刻真能停的 CLI task_id。这是「活着的子进程能停什么」的第一手来源,
// 比库里的 subagent_state overlay 权威;查不到时必须明确报「不认识」,不能返回空串让
// 调用方当成一个合法的任务标识。
func TestRuntime_ResolveBackgroundTask(t *testing.T) {
	Convey("Given 子进程报过该 tool_use 的 task_started,When 反查,Then 返回 CLI task_id", t, func() {
		r := New()
		h := &resolvingCCHandle{fakeCCHandle: &fakeCCHandle{}, ids: map[string]string{"tu1": "b0n82mqaj"}}
		r.cache.Put(sessionKey(42), &claudeActive{handle: h})

		taskID, err := r.ResolveBackgroundTask(context.Background(), 42, "tu1")

		So(err, ShouldBeNil)
		So(taskID, ShouldEqual, "b0n82mqaj")
	})

	Convey("Given 子进程从没报过这个 tool_use,When 反查,Then 报 ErrBackgroundTaskUnknown", t, func() {
		r := New()
		h := &resolvingCCHandle{fakeCCHandle: &fakeCCHandle{}, ids: map[string]string{"tu1": "b0n82mqaj"}}
		r.cache.Put(sessionKey(42), &claudeActive{handle: h})

		taskID, err := r.ResolveBackgroundTask(context.Background(), 42, "tu-never-seen")

		So(errors.Is(err, agentruntime.ErrBackgroundTaskUnknown), ShouldBeTrue)
		So(taskID, ShouldBeEmpty)
	})

	Convey("Given 会话不在缓存(已 evict / 未 spawn),When 反查,Then 报 ErrBackgroundTaskUnknown", t, func() {
		taskID, err := New().ResolveBackgroundTask(context.Background(), 999, "tu1")

		So(errors.Is(err, agentruntime.ErrBackgroundTaskUnknown), ShouldBeTrue)
		So(taskID, ShouldBeEmpty)
	})

	Convey("Given 空 toolUseID,When 反查,Then 报 ErrBackgroundTaskUnknown", t, func() {
		r := New()
		r.cache.Put(sessionKey(42), &claudeActive{handle: &fakeCCHandle{}})

		_, err := r.ResolveBackgroundTask(context.Background(), 42, "")

		So(errors.Is(err, agentruntime.ErrBackgroundTaskUnknown), ShouldBeTrue)
	})
}

// TestClaudeCodeRuntimeImplementsBackgroundTaskResolver 守护契约:停止派遣卡的定位
// 第一手来源就是这个端口,claudecode runtime 必须实现它,否则 chat_svc 的 type assert
// 会静默退回只认库里 overlay 的旧路径。
func TestClaudeCodeRuntimeImplementsBackgroundTaskResolver(t *testing.T) {
	Convey("claudecode runtime 实现 agentruntime.BackgroundTaskResolver", t, func() {
		var _ agentruntime.BackgroundTaskResolver = New()
	})
}
