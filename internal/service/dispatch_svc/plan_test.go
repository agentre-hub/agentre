package dispatch_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/syncwire/dispatch"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func TestPlan_DeviceOrderAndChoice(t *testing.T) {
	ctx := context.Background()
	Convey("给定本机与一台已配对 agentred 都能跑成员的 Claude Code 引擎配置", t, func() {
		w := newWorld()

		Convey("本机优先开着：本机排第一并被选中，每台设备都有结果", func() {
			w.order = []dispatch_svc.DevicePref{{Fingerprint: boxFP}, {Fingerprint: selfFP}}
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
			So(err, ShouldBeNil)
			So(p.Found, ShouldBeTrue)
			So(p.Chosen, ShouldEqual, selfFP)
			So(p.ChosenBy, ShouldEqual, string(dispatch.ByAccountOrder))
			So(fingerprints(p), ShouldResemble, []devicefp.Carrier{selfFP, boxFP})
			So(deviceOf(p, selfFP).IsSelf, ShouldBeTrue)
			So(deviceOf(p, selfFP).Chosen, ShouldBeTrue)
			So(deviceOf(p, boxFP).Name, ShouldEqual, "机房")
			So(deviceOf(p, boxFP).Kind, ShouldEqual, string(dispatch.KindAgentred))
			So(deviceOf(p, boxFP).Eligible, ShouldBeTrue)
			So(deviceOf(p, boxFP).Chosen, ShouldBeFalse)
			So(p.Engine.ID, ShouldEqual, int64(11))
			So(p.Engine.ModelSource, ShouldEqual, string(dispatch.ModelNative))
		})

		Convey("本机优先关着：按账号设备顺序，agentred 在前就派给它", func() {
			w.localFirst = false
			w.order = []dispatch_svc.DevicePref{{Fingerprint: boxFP}, {Fingerprint: selfFP}}
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
			So(err, ShouldBeNil)
			So(p.Chosen, ShouldEqual, boxFP)
			So(fingerprints(p), ShouldResemble, []devicefp.Carrier{boxFP, selfFP})
		})

		Convey("本机没装运行时：跳到 agentred，本机写出原因", func() {
			w.reports[selfFP] = runtimeFacts("claudecode", false, "")
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
			So(err, ShouldBeNil)
			So(p.Chosen, ShouldEqual, boxFP)
			So(deviceOf(p, selfFP).Eligible, ShouldBeFalse)
			So(deviceOf(p, selfFP).Reason.Code, ShouldEqual, string(dispatch.ReasonRuntimeMissing))
			So(deviceOf(p, selfFP).Reason.Runtime, ShouldEqual, "claudecode")
		})

		Convey("已配对的 agentred 离线：不可选，原因带上次在线时间", func() {
			w.reports[selfFP] = runtimeFacts("claudecode", false, "")
			w.paired[0].Online = false
			w.paired[0].LastSeenAt = 1700000000000
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
			So(err, ShouldBeNil)
			So(p.Found, ShouldBeFalse)
			So(deviceOf(p, boxFP).Reason.Code, ShouldEqual, string(dispatch.ReasonOffline))
			So(deviceOf(p, boxFP).Reason.LastSeen, ShouldEqual, int64(1700000000000))
		})

		Convey("设备没有上报情况：按没装运行时判，不当成能跑", func() {
			delete(w.reports, boxFP)
			w.reports[selfFP] = runtimeFacts("claudecode", false, "")
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
			So(err, ShouldBeNil)
			So(p.Found, ShouldBeFalse)
			So(deviceOf(p, boxFP).Reason.Code, ShouldEqual, string(dispatch.ReasonRuntimeMissing))
		})
	})
}

func TestPlan_AccountDevices(t *testing.T) {
	ctx := context.Background()
	Convey("给定已登录账号，账号里另有一台桌面端与一台本机没配对的 agentred", t, func() {
		w := newWorld()
		w.accountOK = true
		w.account = []dispatch_svc.AccountDevice{
			{Fingerprint: selfFP, Name: "我的 Mac", Kind: "desktop", Online: true},
			{Fingerprint: otherFP, Name: "公司台式机", Kind: "desktop", Online: false},
			{Fingerprint: "sha256:unpaired", Name: "云主机", Kind: "agentred", Online: true},
			{Fingerprint: "sha256:phone", Name: "手机", Kind: "mobile", Online: true},
			{Fingerprint: boxFP, Name: "机房(账号名)", Kind: "agentred", Online: false},
		}

		p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
		So(err, ShouldBeNil)

		Convey("另一台桌面端的 Agentre 没开：写那台电脑上的 Agentre 没开", func() {
			So(deviceOf(p, otherFP).Kind, ShouldEqual, string(dispatch.KindDesktop))
			So(deviceOf(p, otherFP).Reason.Code, ShouldEqual, string(dispatch.ReasonAppNotRunning))
		})
		Convey("账号里有但本机没配对的 agentred：写这台电脑未配对它", func() {
			So(deviceOf(p, "sha256:unpaired").Reason.Code, ShouldEqual, string(dispatch.ReasonNotPaired))
		})
		Convey("手机等非派发设备不出现", func() {
			So(deviceOf(p, "sha256:phone"), ShouldBeNil)
		})
		Convey("已配对的 agentred 以本机配对连接的在线态为准，名称取账号里的", func() {
			So(deviceOf(p, boxFP).Eligible, ShouldBeTrue)
			So(deviceOf(p, selfFP).Name, ShouldEqual, "我的 Mac")
		})
	})
}

func TestPlan_ProjectAndManual(t *testing.T) {
	ctx := context.Background()
	Convey("给定绑定项目 5：本机没配路径，agentred 配了", t, func() {
		w := newWorld()
		w.projects[5] = &project_entity.Project{ID: 5, Name: "官网", Path: "/self/site", LocalPathMissing: true, Status: consts.ACTIVE}
		w.locations[5] = []*project_location_entity.ProjectLocation{location(5, boxFP, "/srv/site")}

		Convey("本机写没有项目路径，派给 agentred 并带出那台上的路径", func() {
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1, ProjectID: 5})
			So(err, ShouldBeNil)
			So(p.Chosen, ShouldEqual, boxFP)
			So(deviceOf(p, selfFP).Reason.Code, ShouldEqual, string(dispatch.ReasonNoProjectPath))
			So(deviceOf(p, selfFP).Reason.Project, ShouldEqual, "官网")
			So(deviceOf(p, boxFP).ProjectPath, ShouldEqual, "/srv/site")
		})

		Convey("本机也有路径且项目默认设备是 agentred：按项目默认设备派", func() {
			w.projects[5].LocalPathMissing = false
			w.projects[5].DefaultDeviceFingerprint = boxFP
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1, ProjectID: 5})
			So(err, ShouldBeNil)
			So(p.Chosen, ShouldEqual, boxFP)
			So(p.ChosenBy, ShouldEqual, string(dispatch.ByProjectDefault))
			So(deviceOf(p, selfFP).ProjectPath, ShouldEqual, "/self/site")
		})

		Convey("手动指定一台能跑的设备：按手动派", func() {
			w.projects[5].LocalPathMissing = false
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1, ProjectID: 5, ManualDevice: boxFP})
			So(err, ShouldBeNil)
			So(p.Chosen, ShouldEqual, boxFP)
			So(p.ChosenBy, ShouldEqual, string(dispatch.ByManual))
			So(p.ManualUnavailable, ShouldBeFalse)
		})

		Convey("手动指定的设备变得不可用：回落到自动结果并标出来", func() {
			w.projects[5].LocalPathMissing = false
			w.paired[0].Online = false
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1, ProjectID: 5, ManualDevice: boxFP})
			So(err, ShouldBeNil)
			So(p.Chosen, ShouldEqual, selfFP)
			So(p.ManualUnavailable, ShouldBeTrue)
		})

		Convey("项目不存在：报项目不存在", func() {
			_, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1, ProjectID: 99})
			requireCode(t, err, code.ProjectNotFound)
		})
	})
}

func TestPlan_EngineAndConstraint(t *testing.T) {
	ctx := context.Background()
	Convey("给定成员与引擎配置的各种情形", t, func() {
		w := newWorld()

		Convey("本会话换了一份 Codex 引擎配置：按它判，结果里带出它", func() {
			w.profiles[12] = profile(12, "Codex", "codex", "")
			w.reports[boxFP] = runtimeFacts("codex", true, syncwire.NativeLoginUnknown)
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1, EngineProfileID: 12})
			So(err, ShouldBeNil)
			So(p.Engine.ID, ShouldEqual, int64(12))
			So(p.Engine.Runtime, ShouldEqual, "codex")
			So(p.Chosen, ShouldEqual, boxFP)
		})

		Convey("换的引擎配置已删除：报引擎配置不存在", func() {
			deleted := profile(13, "Old", "codex", "")
			deleted.Status = consts.DELETE
			w.profiles[13] = deleted
			_, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1, EngineProfileID: 13})
			requireCode(t, err, code.EngineProfileNotFound)
		})

		Convey("成员没有任何生效的引擎配置：报需要引擎配置", func() {
			delete(w.effective, 1)
			_, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
			requireCode(t, err, code.EngineProfileRequired)
		})

		Convey("成员不存在：报成员不存在", func() {
			_, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 404})
			requireCode(t, err, code.AgentNotFound)
		})

		Convey("成员要求 gpu 标签：只有带标签的设备能跑，其余写缺的标签", func() {
			a := &agent_entity.Agent{ID: 1}
			So(a.SetExecConstraint(ctx, agent_entity.ExecConstraint{Mode: agent_entity.ExecConstraintLabels, Labels: []string{"gpu"}}), ShouldBeNil)
			w.agents[1] = a
			w.order = []dispatch_svc.DevicePref{{Fingerprint: selfFP}, {Fingerprint: boxFP, Labels: []string{"gpu"}}}
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
			So(err, ShouldBeNil)
			So(p.Chosen, ShouldEqual, boxFP)
			So(deviceOf(p, selfFP).Reason.Code, ShouldEqual, string(dispatch.ReasonLabelMissing))
			So(deviceOf(p, selfFP).Reason.Labels, ShouldResemble, []string{"gpu"})
		})

		Convey("引擎配置走只有 agentred 连得到的供应商：本机写连不到", func() {
			w.effective[1] = profile(11, "Claude", "claudecode", "ollama")
			prov := &llm_provider_entity.LLMProvider{ProviderKey: "ollama", Name: "本地 Ollama"}
			So(prov.SetReach(ctx, llm_provider_entity.ReachDevices, []devicefp.Carrier{boxFP}), ShouldBeNil)
			w.providers["ollama"] = prov
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
			So(err, ShouldBeNil)
			So(p.Engine.ModelSource, ShouldEqual, string(dispatch.ModelProvider))
			So(p.Engine.ProviderName, ShouldEqual, "本地 Ollama")
			So(p.Chosen, ShouldEqual, boxFP)
			So(deviceOf(p, selfFP).Reason.Code, ShouldEqual, string(dispatch.ReasonProviderUnreachable))
			So(deviceOf(p, selfFP).Reason.Provider, ShouldEqual, "本地 Ollama")
		})

		Convey("本机指纹读不出（钥匙串缺失）：没有本机可排，照样判已配对设备", func() {
			w.selfErr = errSelf
			p, err := w.svc(t).Plan(ctx, &dispatch_svc.PlanRequest{AgentID: 1})
			So(err, ShouldBeNil)
			So(fingerprints(p), ShouldResemble, []devicefp.Carrier{boxFP})
		})
	})
}

func TestAvailability_IgnoresProject(t *testing.T) {
	Convey("成员不带项目的可用性：不做项目路径检查", t, func() {
		w := newWorld()
		p, err := w.svc(t).Availability(context.Background(), 1)
		So(err, ShouldBeNil)
		So(p.Found, ShouldBeTrue)
		So(deviceOf(p, selfFP).Eligible, ShouldBeTrue)
		So(deviceOf(p, boxFP).Eligible, ShouldBeTrue)
	})
}

func TestValidateManual(t *testing.T) {
	ctx := context.Background()
	Convey("校验手动指定的设备", t, func() {
		w := newWorld()
		Convey("能跑：通过", func() {
			So(w.svc(t).ValidateManual(ctx, &dispatch_svc.PlanRequest{AgentID: 1, ManualDevice: boxFP}), ShouldBeNil)
		})
		Convey("不能跑：报指定的设备不可用", func() {
			w.paired[0].Online = false
			requireCode(t, w.svc(t).ValidateManual(ctx, &dispatch_svc.PlanRequest{AgentID: 1, ManualDevice: boxFP}),
				code.DispatchManualDeviceUnavailable)
		})
		Convey("不是参与派发的设备：同样报不可用", func() {
			requireCode(t, w.svc(t).ValidateManual(ctx, &dispatch_svc.PlanRequest{AgentID: 1, ManualDevice: "sha256:nobody"}),
				code.DispatchManualDeviceUnavailable)
		})
		Convey("没给设备：参数错误", func() {
			requireCode(t, w.svc(t).ValidateManual(ctx, &dispatch_svc.PlanRequest{AgentID: 1}), code.InvalidParameter)
		})
	})
}
