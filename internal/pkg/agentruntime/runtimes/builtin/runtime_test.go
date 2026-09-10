package builtin

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

// TestBuiltinCapabilities 钉死 builtin runtime 的能力矩阵 —— 与 Capabilities 描述
// 必须严格一致:steer / cancel_steer / abort 是 in-process 单 provider 模式天生
// 支持的;其它(drain / set_permission / answer_user_ask / tool_permission /
// fork / report_context_window)builtin 都不参与,前端 UI gating 与 chat_svc
// dispatcher 据此决定要不要走对应路径。
//
// 历史:旧 builtin.go 通过"是否实现 Steerer/Aborter/SteerCanceler"接口的方式
// 隐式声明能力;Plan A 改成结构化 Capabilities,前端不再每加一个 backend 就要
// 同步 isBuiltin/isCodex 的 if-else 列表(spec §5.4)。
func TestBuiltinCapabilities(t *testing.T) {
	Convey("builtin Capabilities 矩阵", t, func() {
		r := New()
		caps := r.Capabilities()
		So(caps.Has(capability.CapSteer), ShouldBeTrue)
		So(caps.Has(capability.CapCancelSteer), ShouldBeTrue)
		So(caps.Has(capability.CapDrainSteer), ShouldBeFalse)
		So(caps.Has(capability.CapAbort), ShouldBeTrue)
		So(caps.Has(capability.CapImageInput), ShouldBeTrue)
		So(caps.Has(capability.CapSetPermission), ShouldBeFalse)
		So(caps.Has(capability.CapAnswerUserAsk), ShouldBeFalse)
		So(caps.Has(capability.CapToolPermission), ShouldBeFalse)
		So(caps.Has(capability.CapForkSession), ShouldBeFalse)
		So(caps.Has(capability.CapReportContextWindow), ShouldBeFalse)
		// CapMCPTools=false:builtin 不支持 RunRequest.MCPServers 注入。
		So(caps.Has(capability.CapMCPTools), ShouldBeFalse)
		// CapReasoningEffort=true:builtin 起 runner 时把 Backend.ReasoningEffort
		// 交给模型(runtime.go 的 reasoning effort 分支)。
		So(caps.Has(capability.CapReasoningEffort), ShouldBeTrue)
	})
}
