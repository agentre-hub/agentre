package agent_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

func syncedMember() *agent_entity.Agent {
	a := member(1, 0, 0)
	a.SystemBadge = agent_entity.SystemBadgeDefault
	a.SyncMeta = syncmeta_entity.SyncMeta{SyncID: "ag-1"}
	return a
}

func assertOneAgentUpdate(t *testing.T, rec *recordingSync) {
	t.Helper()
	require.Len(t, rec.changes, 1)
	assert.Equal(t, syncwire.KindAgent, rec.changes[0].Kind)
	assert.Equal(t, sync_svc.OpUpdate, rec.changes[0].Op)
	assert.Equal(t, "ag-1", rec.changes[0].Meta.SyncID)
}

func TestMemberEngineSettings_NotifySync(t *testing.T) {
	t.Run("engine reference", func(t *testing.T) {
		ctx, agentMock, _, profiles, svc := setupEngineSvc(t)
		rec := registerRecordingSync(t)
		agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(syncedMember(), nil)
		profiles.EXPECT().Find(gomock.Any(), int64(5)).Return(engineProfile(5, "Codex", "codex"), nil).AnyTimes()
		profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(engineProfile(3, "D", "claudecode"), nil).AnyTimes()
		agentMock.EXPECT().UpdateEngineSettings(gomock.Any(), gomock.Any()).Return(nil)
		_, err := svc.SetMemberEngine(ctx, &SetMemberEngineRequest{AgentID: 1, EngineProfileID: 5})
		require.NoError(t, err)
		assertOneAgentUpdate(t, rec)
	})
	t.Run("exec constraint", func(t *testing.T) {
		ctx, agentMock, _, profiles, svc := setupEngineSvc(t)
		rec := registerRecordingSync(t)
		agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(syncedMember(), nil)
		profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(engineProfile(3, "D", "claudecode"), nil).AnyTimes()
		agentMock.EXPECT().UpdateEngineSettings(gomock.Any(), gomock.Any()).Return(nil)
		_, err := svc.SetMemberExecConstraint(ctx, &SetMemberExecConstraintRequest{
			AgentID: 1, Constraint: ExecConstraintDTO{Mode: "labels", Labels: []string{"gpu"}}})
		require.NoError(t, err)
		assertOneAgentUpdate(t, rec)
	})
	t.Run("skill grants", func(t *testing.T) {
		ctx, agentMock, _, profiles, svc := setupEngineSvc(t)
		rec := registerRecordingSync(t)
		agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(syncedMember(), nil)
		profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(engineProfile(3, "D", "codex"), nil).AnyTimes()
		agentMock.EXPECT().UpdateEngineSettings(gomock.Any(), gomock.Any()).Return(nil)
		_, err := svc.SetMemberSkillGrants(ctx, &SetMemberSkillGrantsRequest{
			AgentID: 1, Skills: []department_svc.AgentSkillDTO{{ID: "p", Enabled: true}}})
		require.NoError(t, err)
		assertOneAgentUpdate(t, rec)
	})
	t.Run("failed write queues nothing", func(t *testing.T) {
		ctx, agentMock, _, profiles, svc := setupEngineSvc(t)
		rec := registerRecordingSync(t)
		agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(syncedMember(), nil)
		profiles.EXPECT().Find(gomock.Any(), int64(5)).Return(engineProfile(5, "Codex", "codex"), nil).AnyTimes()
		agentMock.EXPECT().UpdateEngineSettings(gomock.Any(), gomock.Any()).Return(context.DeadlineExceeded)
		_, err := svc.SetMemberEngine(ctx, &SetMemberEngineRequest{AgentID: 1, EngineProfileID: 5})
		require.Error(t, err)
		assert.Empty(t, rec.changes)
	})
}
