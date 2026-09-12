package exec_target_svc_test

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// registerCapabilityRepos 注册 agent_repo + agent_backend_repo + AgentExecTarget mock
// (并在测试后还原),让 session → agent → backend 的解析链走得通。执行目标列表桩为空,
// 解析据此退化直接用 a.AgentBackendID,不必单独搭执行目标行。
func registerCapabilityRepos(t *testing.T, ctrl *gomock.Controller) (
	*mock_agent_repo.MockAgentRepo,
	*mock_agent_backend_repo.MockAgentBackendRepo,
) {
	t.Helper()
	agentMock := mock_agent_repo.NewMockAgentRepo(ctrl)
	backendMock := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	execTargetMock := mock_agent_repo.NewMockAgentExecTargetRepo(ctrl)
	execTargetMock.EXPECT().ListByAgent(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	prevAgent := agent_repo.Agent()
	prevBackend := agent_backend_repo.AgentBackend()
	prevExecTarget := agent_repo.AgentExecTarget()
	agent_repo.RegisterAgent(agentMock)
	agent_backend_repo.RegisterAgentBackend(backendMock)
	agent_repo.RegisterAgentExecTarget(execTargetMock)
	t.Cleanup(func() {
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
		agent_repo.RegisterAgentExecTarget(prevExecTarget)
	})
	return agentMock, backendMock
}

// pairChatTestDevices 把给定几台机器登记成本机配对表的全部内容。backend 的 DeviceID
// 是规范指纹，派发边界要在那张表里解析出行 ID 才拨得动号。
func pairChatTestDevices(t *testing.T, deviceIDs ...int64) {
	t.Helper()
	ctrl := gomock.NewController(t)
	rows := make([]*remote_device_svc.DeviceView, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		rows = append(rows, &remote_device_svc.DeviceView{
			ID: id, DaemonFingerprint: devicefp.Carrier(fmt.Sprintf("sha256:device-%d", id)), Online: true,
		})
	}
	rds := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	rds.EXPECT().DeviceFingerprint().Return(devicefp.Carrier("sha256:self"), nil).AnyTimes()
	rds.EXPECT().List(gomock.Any()).Return(rows, nil).AnyTimes()
	rds.EXPECT().Get(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64) (*remote_device_svc.DeviceView, error) {
			for _, row := range rows {
				if row.ID == id {
					return row, nil
				}
			}
			return nil, nil
		}).AnyTimes()
	rds.EXPECT().ListDeviceProviders(gomock.Any()).Return(nil).AnyTimes()
	prev := remote_device_svc.Default()
	remote_device_svc.SetDefault(rds)
	t.Cleanup(func() { remote_device_svc.SetDefault(prev) })
}
