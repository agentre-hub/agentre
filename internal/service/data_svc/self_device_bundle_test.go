package data_svc_test

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/service/data_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// bundle 是**可移植**配置：本机档的含义是「跑在导入它的那台机器上」，pre-R13 时它的
// DeviceID 就是空串，导出的 bundle 因此不带设备引用。R13 运行期认领把本机 backend 的
// DeviceID 改写成本机指纹后，导出侧照抄该指纹 —— 而 bundle 的 devices 段只收 paired
// agentred（本机不会和自己配对），导入时 deviceRefs.StableKey 必然落空，整条
// 报 DataImportDanglingRef。也就是：认领之后，任何含本机后端的导出包都导不回来。
//
// 契约与 DisplayDeviceID 同一条：跨边界时本机指纹收敛回「本机」。
func TestExport_GivenSelfFingerprintBackend_ThenBundleCarriesNoDeviceRef(t *testing.T) {
	m := setupDataSvcTest(t)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	rd := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rd.EXPECT().DeviceFingerprint().Return("sha256:self", nil).AnyTimes()
	prev := remote_device_svc.Default()
	remote_device_svc.SetDefault(rd)
	t.Cleanup(func() { remote_device_svc.SetDefault(prev) })

	m.backends.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{
		{ID: 30, Type: "claudecode", Name: "Local CC", DeviceFingerprint: "sha256:self"},
		{ID: 31, Type: "codex", Name: "Remote Codex", DeviceFingerprint: "sha256:other-box"},
	}, nil)
	// 有真远端档，导出仍要拉配对表做 uuid 翻译。
	m.devices.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()

	Convey("本机指纹档导出成可移植的本机档，真远端档保留设备引用", t, func() {
		res, err := m.svc.Export(m.ctx, &data_svc.ExportRequest{
			Scopes: []string{string(data_svc.ScopeAgentBackends)},
		})
		So(err, ShouldBeNil)
		var b data_svc.BundleV1
		So(json.Unmarshal(res.JSON, &b), ShouldBeNil)
		So(b.Items.AgentBackends, ShouldHaveLength, 2)

		byName := map[string]data_svc.BundleAgentBackend{}
		for _, bk := range b.Items.AgentBackends {
			byName[bk.Name] = bk
		}
		So(byName["Local CC"].DeviceID, ShouldEqual, "")
		So(byName["Remote Codex"].DeviceID, ShouldEqual, "sha256:other-box")
	})
}
