package agentruntime_test

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"

	// 触发 NEW runtime 子包 init() 注册到 RuntimeFor 表
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/builtin"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/claudecode"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/codex"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
)

// TestRuntimeFor_AllSubpackagesRegistered 钉死 Plan C Session 3k 的注册契约:
// 本地 runtime 子包 init() 都把自己登记到了 agentruntime.RuntimeFor 表。
// 这是后续 chat_svc.selectRunner 切换到新表的前置条件 —— 任意子包漏 init,
// 切换后 selectRunner 会拿 nil 然后 502。
//
// remote runtime 不参与全局注册(它是 session-instanced,由 chat_svc 按
// device 现起);因此不在断言里。
func TestRuntimeFor_AllSubpackagesRegistered(t *testing.T) {
	Convey("NEW runtime 注册表覆盖本地 backend type", t, func() {
		Convey("claudecode 已注册", func() {
			r := agentruntime.RuntimeFor(agent_backend_entity.TypeClaudeCode)
			So(r, ShouldNotBeNil)
			caps := r.Capabilities()
			So(caps.Has("steer"), ShouldBeTrue)
		})
		Convey("codex 已注册", func() {
			r := agentruntime.RuntimeFor(agent_backend_entity.TypeCodex)
			So(r, ShouldNotBeNil)
			caps := r.Capabilities()
			So(caps.Has("steer"), ShouldBeTrue)
		})
		Convey("builtin 已注册", func() {
			r := agentruntime.RuntimeFor(agent_backend_entity.TypeBuiltin)
			So(r, ShouldNotBeNil)
			caps := r.Capabilities()
			So(caps.Has("steer"), ShouldBeTrue)
		})
		Convey("piagent 已注册", func() {
			r := agentruntime.RuntimeFor(agent_backend_entity.TypePiAgent)
			So(r, ShouldNotBeNil)
			caps := r.Capabilities()
			So(caps.Has("steer"), ShouldBeTrue)
		})
	})

	Convey("RegisteredRuntimes 快照含本地 backend type", t, func() {
		all := agentruntime.RegisteredRuntimes()
		So(len(all), ShouldBeGreaterThanOrEqualTo, 4)
		_, ok := all[agent_backend_entity.TypeClaudeCode]
		So(ok, ShouldBeTrue)
		_, ok = all[agent_backend_entity.TypeCodex]
		So(ok, ShouldBeTrue)
		_, ok = all[agent_backend_entity.TypeBuiltin]
		So(ok, ShouldBeTrue)
		_, ok = all[agent_backend_entity.TypePiAgent]
		So(ok, ShouldBeTrue)
	})

	Convey("SwapRuntimeForTest 临时替换可恢复", t, func() {
		orig := agentruntime.RuntimeFor(agent_backend_entity.TypeBuiltin)
		So(orig, ShouldNotBeNil)
		restore := agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeBuiltin, nil)
		So(agentruntime.RuntimeFor(agent_backend_entity.TypeBuiltin), ShouldBeNil)
		restore()
		So(agentruntime.RuntimeFor(agent_backend_entity.TypeBuiltin), ShouldEqual, orig)
	})
}
