package ipc

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

func TestCapabilitiesFor_ClaudeCode(t *testing.T) {
	Convey("claudecode backend 返回完整能力集 + plan/acceptEdits 等 mode", t, func() {
		caps := capabilitiesFor(agent_backend_entity.TypeClaudeCode)
		So(caps.Has(capability.CapAbort), ShouldBeTrue)
		So(caps.Has(capability.CapSetPermission), ShouldBeTrue)
		So(caps.PermissionModeMeta.AllowedModes, ShouldContain, "plan")
		So(caps.PermissionModeMeta.DefaultMode, ShouldEqual, "acceptEdits")
		So(caps.PermissionModeMeta.SwitchableDuringTurn, ShouldBeTrue)
	})
}

func TestCapabilitiesFor_Codex(t *testing.T) {
	Convey("codex backend 有 caps but SwitchableDuringTurn=false", t, func() {
		caps := capabilitiesFor(agent_backend_entity.TypeCodex)
		So(caps.PermissionModeMeta.SwitchableDuringTurn, ShouldBeFalse)
	})
}

func TestCapabilitiesFor_Builtin(t *testing.T) {
	Convey("builtin backend 返回非空 caps", t, func() {
		caps := capabilitiesFor(agent_backend_entity.TypeBuiltin)
		So(caps.Set, ShouldNotBeNil)
	})
}

func TestCapabilitiesFor_Unknown(t *testing.T) {
	Convey("未知 backend type 返空 caps", t, func() {
		caps := capabilitiesFor("unknown")
		So(len(caps.Set), ShouldEqual, 0)
	})
}

func TestGetBackendCapabilities_ClaudeCode(t *testing.T) {
	Convey("按 backendType 取 caps：claudecode 返回完整 PermissionModeMeta，前端新对话用", t, func() {
		resp, err := GetBackendCapabilities(context.Background(), &GetBackendCapabilitiesRequest{
			BackendType: string(agent_backend_entity.TypeClaudeCode),
		})
		So(err, ShouldBeNil)
		So(resp, ShouldNotBeNil)
		So(resp.Capabilities, ShouldContain, string(capability.CapSetPermission))
		So(resp.PermissionModeMeta.AllowedModes, ShouldContain, "plan")
		So(resp.PermissionModeMeta.DefaultMode, ShouldEqual, "acceptEdits")
		So(resp.PermissionModeMeta.SwitchableDuringTurn, ShouldBeTrue)
	})
}

func TestGetBackendCapabilities_Codex(t *testing.T) {
	Convey("codex 返回受限 mode 集合（default/plan），SwitchableDuringTurn=false", t, func() {
		resp, err := GetBackendCapabilities(context.Background(), &GetBackendCapabilitiesRequest{
			BackendType: string(agent_backend_entity.TypeCodex),
		})
		So(err, ShouldBeNil)
		So(resp.PermissionModeMeta.SwitchableDuringTurn, ShouldBeFalse)
	})
}

func TestGetBackendCapabilities_PiAgent(t *testing.T) {
	Convey("Given the Pi backend When querying capabilities Then fork_session is advertised", t, func() {
		resp, err := GetBackendCapabilities(context.Background(), &GetBackendCapabilitiesRequest{
			BackendType: string(agent_backend_entity.TypePiAgent),
		})
		So(err, ShouldBeNil)
		So(resp.Capabilities, ShouldContain, string(capability.CapForkSession))
	})
}

func TestGetBackendCapabilities_EmptyType(t *testing.T) {
	Convey("空 backendType 报参数错误（前端 sessionId<=0 且未选 backend 时不该调）", t, func() {
		_, err := GetBackendCapabilities(context.Background(), &GetBackendCapabilitiesRequest{
			BackendType: "",
		})
		So(err, ShouldNotBeNil)
	})
}

func TestGetBackendCapabilities_UnknownType(t *testing.T) {
	Convey("未知 backendType 返空 caps + 不报错（向前兼容新增 backend）", t, func() {
		resp, err := GetBackendCapabilities(context.Background(), &GetBackendCapabilitiesRequest{
			BackendType: "future-backend",
		})
		So(err, ShouldBeNil)
		So(len(resp.Capabilities), ShouldEqual, 0)
		So(resp.PermissionModeMeta.AllowedModes, ShouldBeEmpty)
	})
}

func TestCapListToStrings_OnlyTrueKeys(t *testing.T) {
	Convey("capListToStrings 只输出 true bool", t, func() {
		c := capability.Capabilities{
			Set: map[capability.Capability]bool{
				capability.CapAbort:       true,
				capability.CapForkSession: false,
			},
		}
		got := capListToStrings(c)
		So(got, ShouldContain, string(capability.CapAbort))
		So(got, ShouldNotContain, string(capability.CapForkSession))
	})
}
