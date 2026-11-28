package chat_svc

import (
	"context"
	"os"
	"testing"

	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/sessionengine"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/sessionengine/sessionenginetest"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// TestMain 给整个测试二进制（本包与 chat_svc_test）装两样默认：
//
//   - 没有任何 CLI 路径覆盖的仓储：每一轮都按（运行时, 设备）读一次覆盖，绝大多数用例不关心
//     它；要验证覆盖的用例自己注册 mock（cli_overlay_test.go）。
//   - 「旧夹具桥」（sessionenginetest）：这批既有用例按引擎配置出现之前的形状搭夹具。成员生效
//     的配置与按 ID 读配置都答成那一行后端摊成的配置，派发答成「选中那一行所在的设备」，于是
//     这些用例原样走新的第一轮派发与钉住路径，每一轮仍只读一次那一行后端。验证派发与钉住本身
//     的用例在 engine_pin_test.go 里注册自己的派发、配置仓储与生效配置，结束时把桥装回来。
func TestMain(m *testing.M) {
	device_pref_repo.RegisterCLIPath(noCLIPathOverrides{})
	sessionengine.RegisterEffective(sessionenginetest.LegacyEffective)
	agent_backend_repo.RegisterEngineProfile(sessionenginetest.LegacyProfiles{})
	dispatch_svc.RegisterDispatch(legacyDispatch{})
	os.Exit(m.Run())
}

type noCLIPathOverrides struct{ device_pref_repo.CLIPathRepo }

func (noCLIPathOverrides) Find(context.Context, string, devicefp.Carrier) (*device_pref_entity.CLIPathOverride, error) {
	return nil, nil
}

// legacyDispatch 把派发答成「选中桥刚答出的那份配置所在的设备」（手动指定时就是那台）。
type legacyDispatch struct{ dispatch_svc.DispatchSvc }

// ValidateManual：旧夹具里的设备都算通过（要验复核本身的用例注册自己的派发）。
func (legacyDispatch) ValidateManual(context.Context, *dispatch_svc.PlanRequest) error { return nil }

func (legacyDispatch) Plan(_ context.Context, req *dispatch_svc.PlanRequest) (*dispatch_svc.Plan, error) {
	device := sessionenginetest.DeviceOf(req.EngineProfileID)
	if req.ManualDevice != "" {
		device = req.ManualDevice
	}
	return &dispatch_svc.Plan{Found: true, Chosen: device, Devices: []*dispatch_svc.DeviceResult{{
		Fingerprint: device, IsSelf: !remote_device_svc.TargetsAnotherMachine(device), Eligible: true, Chosen: true,
	}}}, nil
}
