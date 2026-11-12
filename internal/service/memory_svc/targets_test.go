package memory_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/service/memory_svc"
	"github.com/agentre-hub/agentre/internal/service/memory_svc/mock_memory_svc"
)

// 测试里的判档与 chat_svc.MemoryTier 同一规则：有 CapMemory 为完整，内置只读摘要，其余不带。
func stubTier(caps capability.Capabilities, backendType string) string {
	switch {
	case caps.Has(capability.CapMemory):
		return memory_svc.TierFull
	case backendType == "builtin":
		return memory_svc.TierSummaries
	default:
		return memory_svc.TierNone
	}
}

func memCaps() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{capability.CapMemory: true}}
}

func TestTargetAdvisor_Restricted(t *testing.T) {
	convey.Convey("agent 头部的受限执行目标", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		src := mock_memory_svc.NewMockTargetSource(ctrl)
		ctx := context.Background()
		advisor := memory_svc.NewTargetAdvisor(src, stubTier)
		given := func(ts ...memory_svc.ExecTarget) {
			src.EXPECT().ExecTargets(ctx, int64(5)).Return(ts, nil)
		}

		convey.Convey("全部完整支持：什么都不列", func() {
			given(
				memory_svc.ExecTarget{BackendID: 1, BackendType: "claudecode", Caps: memCaps()},
				memory_svc.ExecTarget{BackendID: 2, BackendType: "codex", DeviceName: "机房 2 号机", Remote: true, Caps: memCaps()},
			)
			got, err := advisor.Restricted(ctx, 5)
			convey.So(err, convey.ShouldBeNil)
			convey.So(got, convey.ShouldBeEmpty)
			convey.So(got, convey.ShouldNotBeNil)
		})

		convey.Convey("没有执行目标：什么都不列", func() {
			given()
			got, err := advisor.Restricted(ctx, 5)
			convey.So(err, convey.ShouldBeNil)
			convey.So(got, convey.ShouldBeEmpty)
		})

		convey.Convey("内置后端：只能读摘要", func() {
			given(memory_svc.ExecTarget{BackendID: 3, BackendName: "内置", BackendType: "builtin"})
			got, _ := advisor.Restricted(ctx, 5)
			convey.So(got, convey.ShouldResemble, []memory_svc.RestrictedTarget{
				{BackendID: 3, BackendName: "内置", Level: memory_svc.TierSummaries, Reason: memory_svc.ReasonBuiltinSummaries},
			})
		})

		convey.Convey("acp / openclaw / hermes：这个后端不支持，本机与远端都一样", func() {
			for _, bt := range []string{"acp", "openclaw", "hermes"} {
				src.EXPECT().ExecTargets(ctx, int64(5)).Return([]memory_svc.ExecTarget{
					{BackendID: 4, BackendName: bt, BackendType: bt},
					{BackendID: 5, BackendName: bt, BackendType: bt, DeviceName: "机房", Remote: true},
				}, nil)
				got, _ := advisor.Restricted(ctx, 5)
				convey.So(len(got), convey.ShouldEqual, 2)
				for _, r := range got {
					convey.So(r.Level, convey.ShouldEqual, memory_svc.TierNone)
					convey.So(r.Reason, convey.ShouldEqual, memory_svc.ReasonUnsupportedBackend)
				}
			}
		})

		convey.Convey("远端设备跑着能带记忆的后端却没报能力：需要升级", func() {
			given(memory_svc.ExecTarget{
				BackendID: 6, BackendName: "Claude Code", BackendType: "claudecode",
				DeviceName: "机房 2 号机", Remote: true, TypeSupportsMemory: true,
			})
			got, _ := advisor.Restricted(ctx, 5)
			convey.So(got, convey.ShouldResemble, []memory_svc.RestrictedTarget{
				{BackendID: 6, BackendName: "Claude Code", DeviceName: "机房 2 号机", Level: memory_svc.TierNone, Reason: memory_svc.ReasonNeedsUpgrade},
			})
		})

		convey.Convey("本机的能带记忆的后端不会被判成需要升级", func() {
			given(memory_svc.ExecTarget{BackendID: 7, BackendType: "claudecode", TypeSupportsMemory: true})
			got, _ := advisor.Restricted(ctx, 5)
			convey.So(got[0].Reason, convey.ShouldEqual, memory_svc.ReasonUnsupportedBackend)
		})

		convey.Convey("目标来源出错：原样上报", func() {
			boom := errors.New("boom")
			src.EXPECT().ExecTargets(ctx, int64(5)).Return(nil, boom)
			_, err := advisor.Restricted(ctx, 5)
			convey.So(err, convey.ShouldEqual, boom)
		})
	})
}
