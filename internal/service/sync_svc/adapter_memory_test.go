package sync_svc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo/mock_memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo/mock_syncstate_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

func stateReturningMemory(t *testing.T, ctrl *gomock.Controller, syncID string, row *memory_entity.Memory) {
	t.Helper()
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	call := state.EXPECT().FindRow(gomock.Any(), syncwire.KindMemory, syncID, gomock.Any())
	if row == nil {
		call.Return(false, nil)
	} else {
		call.DoAndReturn(func(_ context.Context, _, _ string, dest any) (bool, error) {
			*dest.(*memory_entity.Memory) = *row
			return true, nil
		})
	}
	syncstate_repo.RegisterSyncState(state)
}

func TestSyncKinds_GivenMemory_HasAnAdapterAfterItsOwners(t *testing.T) {
	assert.NotNil(t, defaultAdapters(nil)[syncwire.KindMemory])
	for _, owner := range []string{syncwire.KindDepartment, syncwire.KindAgent, syncwire.KindProject} {
		assert.Less(t, indexOfSyncKind(owner), indexOfSyncKind(syncwire.KindMemory), owner)
	}
}

// 推：状态、置顶、上一版和归属（用同步标识表达）都在载荷里，且没有本地 ID。
func TestMemoryAdapter_LoadCarriesStatePinnedPreviousAndOwnerSyncID(t *testing.T) {
	ctrl := gomock.NewController(t)
	stateReturningMemory(t, ctrl, "mem-1", &memory_entity.Memory{
		ID: 3, OwnerKind: memory_entity.OwnerAgent, OwnerID: 5,
		Summary: "uses tabs", Detail: "d", PrevSummary: "uses spaces", PrevDetail: "pd",
		Pinned: true, State: memory_entity.StateArchived,
		CreatorKind: memory_entity.ActorAI, CreatorAgentID: 5, CreatorSessionID: 70,
		ChangeAction: memory_entity.ActionArchived, ChangeActorKind: memory_entity.ActorUser, ChangedAt: 1700,
		Updatetime: 1800, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "mem-1"},
	})
	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	agents.EXPECT().Find(gomock.Any(), int64(5)).Return(&agent_entity.Agent{
		ID: 5, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-5"},
	}, nil)
	agent_repo.RegisterAgent(agents)

	out, err := memoryAdapter{}.load(context.Background(), "mem-1")
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "mem-1", out.SyncID)
	assert.Equal(t, int64(1800), out.UpdatedAt)

	var p syncwire.MemoryPayload
	require.NoError(t, json.Unmarshal(out.Payload, &p))
	assert.Equal(t, syncwire.MemoryPayload{
		OwnerKind: "agent", OwnerSyncID: "agent-5",
		Summary: "uses tabs", Detail: "d", PrevSummary: "uses spaces", PrevDetail: "pd",
		Pinned: true, State: "archived", CreatorKind: "ai",
		ChangeAction: "archived", ChangeActorKind: "user", ChangedAt: 1700,
	}, p)
	assert.NoError(t, syncwire.GuardPayload(syncwire.KindMemory, out.Payload))
}

func TestMemoryAdapter_LoadGlobalHasNoOwnerSyncID(t *testing.T) {
	ctrl := gomock.NewController(t)
	stateReturningMemory(t, ctrl, "mem-1", &memory_entity.Memory{
		ID: 3, OwnerKind: memory_entity.OwnerGlobal, Summary: "s", State: memory_entity.StateNormal,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "mem-1"},
	})
	out, err := memoryAdapter{}.load(context.Background(), "mem-1")
	require.NoError(t, err)
	require.NotNil(t, out)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(out.Payload, &raw))
	assert.NotContains(t, raw, "owner_sync_id")
	assert.Equal(t, "global", raw["owner_kind"])
}

// 归属在本机表达不出跨机引用（行不在、或还没有同步标识）时一条都不发：发出去是一条
// 对别的设备落不了库的孤儿。
func TestMemoryAdapter_LoadOwnerWithoutSyncIDSendsNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner memory_entity.Owner
		setup func(ctrl *gomock.Controller)
	}{
		{"department missing", memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 2}, func(ctrl *gomock.Controller) {
			m := mock_department_repo.NewMockDepartmentRepo(ctrl)
			m.EXPECT().Find(gomock.Any(), int64(2)).Return(nil, nil)
			department_repo.RegisterDepartment(m)
		}},
		{"project unclaimed", memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 9}, func(ctrl *gomock.Controller) {
			m := mock_project_repo.NewMockProjectRepo(ctrl)
			m.EXPECT().Find(gomock.Any(), int64(9)).Return(&project_entity.Project{ID: 9}, nil)
			project_repo.RegisterProject(m)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			stateReturningMemory(t, ctrl, "mem-1", &memory_entity.Memory{
				ID: 3, OwnerKind: tc.owner.Kind, OwnerID: tc.owner.ID, Summary: "s",
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: "mem-1"},
			})
			tc.setup(ctrl)
			out, err := memoryAdapter{}.load(context.Background(), "mem-1")
			require.NoError(t, err)
			assert.Nil(t, out)
		})
	}
}

func TestMemoryAdapter_LoadRowGoneReturnsNil(t *testing.T) {
	ctrl := gomock.NewController(t)
	stateReturningMemory(t, ctrl, "mem-1", nil)
	out, err := memoryAdapter{}.load(context.Background(), "mem-1")
	require.NoError(t, err)
	assert.Nil(t, out)
}

// 拉：新条目带着状态、置顶、上一版和最近变更落库，沿用源端同步标识，归属解析成本机 ID。
func TestMemoryAdapter_ApplyCreatesWithEveryFieldAndResolvedOwner(t *testing.T) {
	ctrl := gomock.NewController(t)
	stateReturningMemory(t, ctrl, "mem-1", nil)
	mems := mock_memory_repo.NewMockMemoryRepo(ctrl)
	var created *memory_entity.Memory
	mems.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, m *memory_entity.Memory) error { created = m; return nil })
	memory_repo.RegisterMemory(mems)

	in := &inbound{Kind: syncwire.KindMemory, SyncID: "mem-1", Version: 2, Payload: mustJSON(t, syncwire.MemoryPayload{
		OwnerKind: "project", OwnerSyncID: "proj-9", Summary: "s", Detail: "d",
		PrevSummary: "ps", PrevDetail: "pd", Pinned: true, State: "archived", CreatorKind: "ai",
		ChangeAction: "edited", ChangeActorKind: "user", ChangedAt: 1700,
	})}
	require.NoError(t, memoryAdapter{}.apply(context.Background(), in,
		map[string]int64{"project:proj-9": 9}))

	require.NotNil(t, created)
	assert.Equal(t, "mem-1", created.SyncID)
	assert.Equal(t, memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 9}, created.Owner())
	assert.Equal(t, "s", created.Summary)
	assert.Equal(t, "d", created.Detail)
	assert.Equal(t, "ps", created.PrevSummary)
	assert.Equal(t, "pd", created.PrevDetail)
	assert.True(t, created.Pinned)
	assert.Equal(t, memory_entity.StateArchived, created.State)
	assert.Equal(t, memory_entity.ActorAI, created.CreatorKind)
	assert.Equal(t, memory_entity.ActionEdited, created.ChangeAction)
	assert.Equal(t, memory_entity.ActorUser, created.ChangeActorKind)
	assert.Equal(t, int64(1700), created.ChangedAt)
}

// 后写的为准：已有的行被下行内容整体覆盖，本机主键与创建时间保留。
func TestMemoryAdapter_ApplyOverwritesAnExistingRowKeepingItsLocalKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	stateReturningMemory(t, ctrl, "mem-1", &memory_entity.Memory{
		ID: 3, OwnerKind: memory_entity.OwnerGlobal, Summary: "local", Createtime: 11,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: "mem-1", SyncVersion: 1},
	})
	mems := mock_memory_repo.NewMockMemoryRepo(ctrl)
	var updated *memory_entity.Memory
	mems.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, m *memory_entity.Memory) error { updated = m; return nil })
	memory_repo.RegisterMemory(mems)

	require.NoError(t, memoryAdapter{}.apply(context.Background(), &inbound{
		Kind: syncwire.KindMemory, SyncID: "mem-1", Version: 2,
		Payload: mustJSON(t, syncwire.MemoryPayload{OwnerKind: "global", Summary: "remote", State: "normal", Pinned: true}),
	}, map[string]int64{}))

	require.NotNil(t, updated)
	assert.Equal(t, int64(3), updated.ID)
	assert.Equal(t, int64(11), updated.Createtime)
	assert.Equal(t, "remote", updated.Summary)
	assert.True(t, updated.Pinned)
}

// 归属在本机还不存在：落地暂缓（R2a），不写悬空引用、也不崩。
func TestMemoryAdapter_ApplyWithDanglingOwnerDefersInsteadOfWriting(t *testing.T) {
	ctrl := gomock.NewController(t)
	memory_repo.RegisterMemory(mock_memory_repo.NewMockMemoryRepo(ctrl)) // 任何调用都是失败

	in := &inbound{Kind: syncwire.KindMemory, SyncID: "mem-1", Payload: mustJSON(t, syncwire.MemoryPayload{
		OwnerKind: "agent", OwnerSyncID: "agent-gone", Summary: "s", State: "normal",
	})}
	ad := memoryAdapter{}
	refs := ad.refs(in)
	assert.Contains(t, refs, ref{Kind: syncwire.KindAgent, SyncID: "agent-gone"})

	_, _, err := resolveRefsWith(t, ctrl, refs)
	require.ErrorIs(t, err, errRefMissing)
	require.ErrorIs(t, ad.apply(context.Background(), in, map[string]int64{}), errRefMissing)
}

func TestMemoryAdapter_RefsPerOwnerKind(t *testing.T) {
	for kind, want := range map[string]string{
		"department": syncwire.KindDepartment, "agent": syncwire.KindAgent, "project": syncwire.KindProject,
	} {
		refs := memoryAdapter{}.refs(&inbound{Payload: mustJSON(t, syncwire.MemoryPayload{OwnerKind: kind, OwnerSyncID: "x"})})
		assert.Equal(t, []ref{{Kind: want, SyncID: "x"}}, refs, kind)
	}
	assert.Empty(t, memoryAdapter{}.refs(&inbound{Payload: mustJSON(t, syncwire.MemoryPayload{OwnerKind: "global"})}))
}

// 墓碑到达：删行；行本来就不在是空操作。
func TestMemoryAdapter_RemoveDeletesTheLocalRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	stateReturningMemory(t, ctrl, "mem-1", &memory_entity.Memory{ID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "mem-1"}})
	mems := mock_memory_repo.NewMockMemoryRepo(ctrl)
	mems.EXPECT().Delete(gomock.Any(), int64(3)).Return(nil)
	memory_repo.RegisterMemory(mems)
	require.NoError(t, memoryAdapter{}.remove(context.Background(), &inbound{SyncID: "mem-1", DeletedAt: 9}))
}

func TestMemoryAdapter_RemoveMissingRowIsNoop(t *testing.T) {
	ctrl := gomock.NewController(t)
	stateReturningMemory(t, ctrl, "mem-1", nil)
	memory_repo.RegisterMemory(mock_memory_repo.NewMockMemoryRepo(ctrl))
	require.NoError(t, memoryAdapter{}.remove(context.Background(), &inbound{SyncID: "mem-1", DeletedAt: 9}))
}

// ── agent 的记忆开关 ────────────────────────────────────────────────────────

func TestAgentAdapter_LoadCarriesTheMemoryToggle(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		ctrl := gomock.NewController(t)
		state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
		state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).DoAndReturn(
			func(_ context.Context, _, _ string, dest any) (bool, error) {
				*dest.(*agent_entity.Agent) = agent_entity.Agent{
					ID: 5, Name: "Ava", MemoryEnabled: enabled, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-1"},
				}
				return true, nil
			})
		syncstate_repo.RegisterSyncState(state)

		out, err := (&agentAdapter{}).load(context.Background(), "agent-1")
		require.NoError(t, err)
		var p syncwire.AgentPayload
		require.NoError(t, json.Unmarshal(out.Payload, &p))
		require.NotNil(t, p.MemoryEnabled)
		assert.Equal(t, enabled, *p.MemoryEnabled)
	}
}

func TestAgentAdapter_ApplyUpdatesTheMemoryToggleOnAnExistingAgent(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, dest any) (bool, error) {
			*dest.(*agent_entity.Agent) = agent_entity.Agent{
				ID: 5, Name: "Ava", MemoryEnabled: true, Status: consts.ACTIVE,
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-1"},
			}
			return true, nil
		}).Times(2)
	syncstate_repo.RegisterSyncState(state)
	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	var saved []*agent_entity.Agent
	agents.EXPECT().UpdateRow(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { saved = append(saved, a); return nil }).Times(2)
	agent_repo.RegisterAgent(agents)

	ad := &agentAdapter{}
	require.NoError(t, ad.apply(context.Background(), &inbound{SyncID: "agent-1",
		Payload: []byte(`{"name":"Ava","memory_enabled":false}`)}, map[string]int64{}))
	assert.False(t, saved[0].MemoryEnabled)
	// 旧版本对端的载荷没有这个键：保持本机现状，不当成「关」。
	require.NoError(t, ad.apply(context.Background(), &inbound{SyncID: "agent-1",
		Payload: []byte(`{"name":"Ava"}`)}, map[string]int64{}))
	assert.True(t, saved[1].MemoryEnabled)
}

// 新落地的 agent 关着记忆：gorm 对带 default:1 的 bool 零值会用库默认（开），所以创建后
// 要再落一次开关。
func TestAgentAdapter_ApplyCreatesAnAgentWithMemoryOff(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).Return(false, nil)
	syncstate_repo.RegisterSyncState(state)
	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	agents.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { a.ID = 8; return nil })
	agents.EXPECT().SetMemoryEnabled(gomock.Any(), int64(8), false).Return(nil)
	agent_repo.RegisterAgent(agents)

	require.NoError(t, (&agentAdapter{}).apply(context.Background(), &inbound{SyncID: "agent-1",
		Payload: []byte(`{"name":"Ava","memory_enabled":false}`)}, map[string]int64{}))
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// resolveRefsWith 用一份「什么都解析不出」的 syncstate 跑 resolveRefs。
func resolveRefsWith(t *testing.T, ctrl *gomock.Controller, refs []ref) (map[string]int64, ref, error) {
	t.Helper()
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindLocalID(gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(0), nil).AnyTimes()
	syncstate_repo.RegisterSyncState(state)
	return resolveRefs(context.Background(), refs)
}

// 后写的为准：两台设备改同一条记忆，后到的上行照常生效，被覆盖的那一版带着记忆的种类
// 名进「丢失的变更」。
func TestFlush_GivenMemoryConflict_RecordsTheOverwrittenVersionUnderTheMemoryKind(t *testing.T) {
	h := newHarness(t, true)
	h.adapter = newFakeAdapter(syncwire.KindMemory)
	h.svc.adapters = map[string]adapter{syncwire.KindMemory: h.adapter}
	h.adapter.rows["mem-1"] = "mine"
	h.transport.results = func(items []syncwire.PushItem) ([]syncwire.PushResult, error) {
		return []syncwire.PushResult{{
			SyncID: items[0].SyncID, Kind: items[0].Kind, Version: 12,
			Status: syncwire.PushStatusConflict, OverwrittenVersion: 11, OverwrittenOriginFingerprint: "fp-5",
			OverwrittenPayload: json.RawMessage(`{"summary":"the other device"}`),
		}}, nil
	}

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: syncwire.KindMemory, Op: OpUpdate,
		Meta: syncmeta_entity.SyncMeta{SyncID: "mem-1", SyncVersion: 8},
	})

	require.Len(t, h.lost.rows, 1)
	assert.Equal(t, syncwire.KindMemory, h.lost.rows[0].EntityType)
	assert.Equal(t, "overwritten", string(h.lost.rows[0].Reason))
	assert.JSONEq(t, `{"summary":"the other device"}`, h.lost.rows[0].PayloadJSON)
	assert.Equal(t, int64(12), h.state.meta["memory:mem-1"].SyncVersion, "本次上行照常生效")
}
