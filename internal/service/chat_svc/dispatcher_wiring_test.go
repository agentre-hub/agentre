package chat_svc

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// TestPackageDispatcher_AllEventTypesRegistered 验证 dispatcher 装配完整:
// 注册到 dispatcher 的所有 Event 类型都能被路由(handler 至少 Apply 不 panic)。
// SteerConsumed 不经 dispatcher(chat.go runTurn 直接拦截,见 dispatcher_wiring.go
// 顶部注释),不在这里枚举。
//
// 不验证业务正确性 —— 业务正确性由 handlers/* 单测覆盖。本测试只关心"注册形态"。
func TestPackageDispatcher_AllEventTypesRegistered(t *testing.T) {
	Convey("dispatcher 路由的 Event 类型都已注册", t, func() {
		events := []agentruntime.Event{
			agentruntime.TextDelta{},
			agentruntime.ThinkingDelta{},
			agentruntime.OutputActivity{},
			agentruntime.ToolCall{},
			agentruntime.ToolResult{},
			agentruntime.UserAskRequest{},
			agentruntime.UserAskResolved{},
			agentruntime.ToolPermissionRequest{},
			agentruntime.ToolPermissionResolved{},
			agentruntime.SubagentStarted{},
			agentruntime.SubagentProgress{},
			agentruntime.SubagentDone{},
			agentruntime.SubagentModel{},
			agentruntime.PermissionModeChanged{},
			agentruntime.UsageUpdate{},
			agentruntime.ContextWindowUpdated{},
			agentruntime.Retry{},
			agentruntime.ErrorEvent{},
			agentruntime.Done{},
			agentruntime.PlanUpdated{},
		}
		for _, ev := range events {
			acc := turn.New()
			// Apply 不应 panic;handler 可能因为 nil emit/view/turnCtx 走 no-op,但不能 crash。
			err := packageDispatcher.Apply(context.Background(), ev, acc, nil, nil, nil)
			So(err, ShouldBeNil)
		}
	})
}
