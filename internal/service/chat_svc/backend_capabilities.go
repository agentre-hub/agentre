package chat_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
)

// BackendCapabilities 报告一档后端此刻的能力集，known 说明这份能力集是不是真的知道。
//
// 本机档查 runtime 注册表；远端档只读本机已连着那台设备时缓存的矩阵（Peek +
// CachedCapabilities）：没有连接、预取没成功都是 known=false —— 这是只读查询，不拨号、
// 不占池引用、不落库，也不把占位矩阵当成设备上报的结果。
func (s *chatSvc) BackendCapabilities(ctx context.Context, be *agent_backend_entity.AgentBackend) (capability.Capabilities, bool, error) {
	if be == nil {
		return capability.Capabilities{}, false, nil
	}
	bt := agent_backend_entity.BackendType(be.Type)
	if !exec_target_svc.BackendTargetsRemote(be) {
		r := agentruntime.RuntimeFor(bt)
		if r == nil {
			return capability.Capabilities{}, false, nil
		}
		return r.Capabilities(), true, nil
	}
	deviceID, ok := exec_target_svc.LocalPairedDeviceID(ctx, be.DeviceFingerprint)
	if !ok {
		return capability.Capabilities{}, false, nil
	}
	rt := s.peekRemoteRuntime(deviceID)
	if rt == nil {
		return capability.Capabilities{}, false, nil
	}
	caps, cached := rt.CachedCapabilities(bt)
	return caps, cached, nil
}
