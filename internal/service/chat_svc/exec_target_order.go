package chat_svc

import (
	"context"

	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// selfFingerprint 取本机设备指纹（R5 硬不变量：与 LAN 配对 / 账号登录共用同一指纹）。
// keychain 缺失 / 服务未初始化时返回空串 —— 空 = 没有可识别的「自己」，R14 的自我
// 提前不触发（浏览器语境正是这种情况）。
func (s *chatSvc) selfFingerprint(ctx context.Context) string {
	rds := remote_device_svc.Default()
	if rds == nil {
		return ""
	}
	fp, err := rds.DeviceFingerprint()
	if err != nil {
		return ""
	}
	return fp
}

// beTargetsRemote 报告一个 backend 是否要派到「别的机器」上跑。判据本身住在
// remote_device_svc.TargetsAnotherMachine（单一事实来源）；这里只补 entity 的 nil
// 语义——nil be 与空 DeviceID 一样是「本机」，与 IsRemote() 的 nil 约定一致。
//
// 全仓一律用它 / TargetsAnotherMachine 提问，**不要再写 be.IsRemote()**：R13 认领后
// 本机 backend 的 DeviceID 就是本机指纹，IsRemote() 单独用会把本机判成远端。
func beTargetsRemote(be *agent_backend_entity.AgentBackend) bool {
	return be != nil && remote_device_svc.TargetsAnotherMachine(be.DeviceFingerprint)
}

// selfBackendIDs 找出一个 Agent 执行目标列表里指向本机（DeviceID == 本机指纹）的那
// 几档 backend id。没有指纹 / 服务缺失时返回空 —— 「没有自己可排」。
func (s *chatSvc) selfBackendIDs(ctx context.Context, targets []*agent_entity.AgentExecTarget) ([]int64, error) {
	fp := s.selfFingerprint(ctx)
	if fp == "" || len(targets) == 0 {
		return nil, nil
	}
	selfBackends, err := agent_backend_repo.AgentBackend().ListByDevice(ctx, fp)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.String("deviceId", fp))
	}
	self := make(map[int64]bool, len(selfBackends))
	for _, be := range selfBackends {
		if be != nil {
			self[be.ID] = true
		}
	}
	if len(self) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(self))
	for _, t := range targets {
		if t != nil && self[t.AgentBackendID] {
			ids = append(ids, t.AgentBackendID)
		}
	}
	return ids, nil
}

// resolvedExecTargets 取一个 Agent 的执行目标并按 R14 解析出本端顺序：本端有覆盖用
// 覆盖；无覆盖时桌面端把「自己」（指向本机指纹的档）提到最前、浏览器语境（无指纹 /
// 无匹配档）原样用账号默认。派发（PickExecTarget）与组织架构页
// （ListExecTargetAvailability）共用这一份解析，避免两处各写一份慢慢漂移。
func (s *chatSvc) resolvedExecTargets(ctx context.Context, agentID int64) ([]*agent_entity.AgentExecTarget, error) {
	targets, err := agent_repo.AgentExecTarget().ListByAgent(ctx, agentID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("agentId", agentID))
	}
	var override []int64
	if repo := agent_repo.AgentExecTargetOverride(); repo != nil {
		o, err := repo.Get(ctx, agentID)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err, zap.Int64("agentId", agentID))
		}
		if o != nil {
			override = o.GetOrder()
		}
	}
	if len(override) > 0 {
		return agent_entity.ResolveExecTargetOrder(targets, override, nil), nil
	}
	selfIDs, err := s.selfBackendIDs(ctx, targets)
	if err != nil {
		return nil, err
	}
	return agent_entity.ResolveExecTargetOrder(targets, nil, selfIDs), nil
}

// hasExecTargetOverride 报告某 Agent 是否有本端顺序覆盖（R16 组织架构页的覆盖态标注）。
func (s *chatSvc) hasExecTargetOverride(ctx context.Context, agentID int64) bool {
	if repo := agent_repo.AgentExecTargetOverride(); repo == nil {
		return false
	}
	o, err := agent_repo.AgentExecTargetOverride().Get(ctx, agentID)
	if err != nil || o == nil {
		return false
	}
	return len(o.GetOrder()) > 0
}
