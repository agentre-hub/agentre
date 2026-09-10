package agent_backend_svc

import (
	"context"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// tombstoneRetentionPeriod 是决策 24 的保留期:一条后端墓碑至少要躺够这么久才
// 允许被物理回收，给悬空引用巡检与人工排查留出窗口。
const tombstoneRetentionPeriod = 30 * 24 * time.Hour

// ReclaimTombstonedBackends 回收无人引用且超过保留期的后端墓碑(决策 24)。
//
// 判据是「墓碑 AND 无任何会话/执行目标引用 AND 超过保留期」——三个条件都满足才
// 物理删除；有引用的墓碑一律保留（哪怕早过了保留期），交给
// SurveyDanglingBackendReferences 巡检报出，不在这里顺手改写引用方。引用完整性
// 靠 service 校验而非外键（本仓一贯做法），所以这里就是那份配套的回收。
func (s *agentBackendSvc) ReclaimTombstonedBackends(ctx context.Context, _ *ReclaimTombstonedBackendsRequest) (*ReclaimTombstonedBackendsResponse, error) {
	cutoff := s.now() - tombstoneRetentionPeriod.Milliseconds()
	tombstones, err := agent_backend_repo.AgentBackend().ListTombstonesOlderThan(ctx, cutoff)
	if err != nil {
		return nil, err
	}
	resp := &ReclaimTombstonedBackendsResponse{}
	if len(tombstones) == 0 {
		return resp, nil
	}

	referenced, err := s.referencedBackendIDs(ctx)
	if err != nil {
		return nil, err
	}

	reclaimable := make([]int64, 0, len(tombstones))
	for _, b := range tombstones {
		if referenced[b.ID] {
			resp.KeptReferencedIDs = append(resp.KeptReferencedIDs, b.ID)
			continue
		}
		reclaimable = append(reclaimable, b.ID)
	}
	if len(reclaimable) > 0 {
		if _, err := agent_backend_repo.AgentBackend().PurgeTombstones(ctx, reclaimable); err != nil {
			return nil, err
		}
		resp.ReclaimedIDs = reclaimable
	}
	logger.Ctx(ctx).Info("agent_backend_svc.ReclaimTombstonedBackends: reclaimed unreferenced backend tombstones past retention",
		zap.Int("reclaimedCount", len(resp.ReclaimedIDs)),
		zap.Int("keptReferencedCount", len(resp.KeptReferencedIDs)))
	return resp, nil
}

// SurveyDanglingBackendReferences 巡检指向非 ACTIVE 后端(墓碑或已不存在)的会话
// / 执行目标引用，只报出、不擅自改写——决策 24 明确拒绝"顺手改写"这个选项：
// 悬空的成因（软删后无人清理引用方）本身就是本轮要暴露的问题，改写会掩盖它。
func (s *agentBackendSvc) SurveyDanglingBackendReferences(ctx context.Context, _ *SurveyDanglingBackendReferencesRequest) (*SurveyDanglingBackendReferencesResponse, error) {
	active, err := agent_backend_repo.AgentBackend().List(ctx)
	if err != nil {
		return nil, err
	}
	activeIDs := make(map[int64]bool, len(active))
	for _, b := range active {
		activeIDs[b.ID] = true
	}

	sessionRefs, err := chat_repo.Session().ListExecAgentBackendRefs(ctx)
	if err != nil {
		return nil, err
	}
	execTargetRefs, err := agent_backend_repo.AgentBackend().ListExecTargetBackendRefs(ctx)
	if err != nil {
		return nil, err
	}

	resp := &SurveyDanglingBackendReferencesResponse{}
	for _, ref := range sessionRefs {
		if !activeIDs[ref.AgentBackendID] {
			resp.Dangling = append(resp.Dangling, DanglingBackendReference{
				Kind: "session", RefID: ref.SessionID, BackendID: ref.AgentBackendID,
			})
		}
	}
	for _, ref := range execTargetRefs {
		if !activeIDs[ref.AgentBackendID] {
			resp.Dangling = append(resp.Dangling, DanglingBackendReference{
				Kind: "exec_target", RefID: ref.ExecTargetID, BackendID: ref.AgentBackendID,
			})
		}
	}
	if len(resp.Dangling) > 0 {
		logger.Ctx(ctx).Warn("agent_backend_svc.SurveyDanglingBackendReferences: found dangling backend references",
			zap.Int("danglingCount", len(resp.Dangling)))
	}
	return resp, nil
}

// referencedBackendIDs 是回收判据"无任何引用"那一半：任何会话
// exec_agent_backend_id 或任何执行目标 agent_backend_id 提到过的 id。不区分
// 引用方自身是否也已软删——一条软删会话仍然"提到过"这个 backend，回收不能因为
// 会话本身被删了就当没人提过。
func (s *agentBackendSvc) referencedBackendIDs(ctx context.Context) (map[int64]bool, error) {
	referenced := map[int64]bool{}
	sessionRefs, err := chat_repo.Session().ListExecAgentBackendRefs(ctx)
	if err != nil {
		return nil, err
	}
	for _, ref := range sessionRefs {
		referenced[ref.AgentBackendID] = true
	}
	execTargetRefs, err := agent_backend_repo.AgentBackend().ListExecTargetBackendRefs(ctx)
	if err != nil {
		return nil, err
	}
	for _, ref := range execTargetRefs {
		referenced[ref.AgentBackendID] = true
	}
	return referenced, nil
}
