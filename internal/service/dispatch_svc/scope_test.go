package dispatch_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/dispatch_svc"
)

func TestResolveLocalCommandScope_GivenUnpinnedLocalSession_ThenPlansSelfAndUsesSelfCwd(t *testing.T) {
	w := newWorld()
	w.sessions[71] = &chat_entity.Session{ID: 71, AgentID: 1}
	w.selfCwd = func(sess *chat_entity.Session) (string, error) {
		require.Equal(t, int64(71), sess.ID)
		return "/self/agents/1", nil
	}
	scope, err := w.svc(t).ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{SessionID: 71})
	require.NoError(t, err)
	assert.Equal(t, &dispatch_svc.CommandScope{DeviceID: "", Cwd: "/self/agents/1"}, scope)
}

func TestResolveLocalCommandScope_GivenStoredCwd_ThenStoredCwdWins(t *testing.T) {
	w := newWorld()
	w.sessions[72] = &chat_entity.Session{ID: 72, AgentID: 1, EngineProfileID: 11, Cwd: "/imported/here"}
	scope, err := w.svc(t).ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{SessionID: 72})
	require.NoError(t, err)
	assert.Equal(t, &dispatch_svc.CommandScope{Cwd: "/imported/here"}, scope)
}

// 钉住的设备优先：即使现在排第一、能跑的是本机，会话也回到它钉住的那台 agentred。
func TestResolveLocalCommandScope_GivenSessionPinnedToAgentred_ThenFollowsPinNotCurrentPlan(t *testing.T) {
	w := newWorld()
	w.sessions[73] = &chat_entity.Session{ID: 73, AgentID: 1, ProjectID: 5, ExecDeviceID: 7, ExecDeviceFingerprint: boxFP}
	w.locations[5] = []*project_location_entity.ProjectLocation{location(5, boxFP, "/srv/site")}
	scope, err := w.svc(t).ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{SessionID: 73})
	require.NoError(t, err)
	assert.Equal(t, &dispatch_svc.CommandScope{DeviceID: boxFP, Cwd: "/srv/site"}, scope)
}

func TestResolveLocalCommandScope_GivenPinnedRemoteFreeChat_ThenEmptyCwdForDeviceDefault(t *testing.T) {
	w := newWorld()
	w.sessions[74] = &chat_entity.Session{ID: 74, AgentID: 1, ExecDeviceFingerprint: boxFP}
	scope, err := w.svc(t).ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{SessionID: 74})
	require.NoError(t, err)
	assert.Equal(t, &dispatch_svc.CommandScope{DeviceID: boxFP, Cwd: ""}, scope)
}

func TestResolveLocalCommandScope_GivenPinnedRemoteWithoutLocation_ThenProjectLocationMissing(t *testing.T) {
	w := newWorld()
	w.sessions[75] = &chat_entity.Session{ID: 75, AgentID: 1, ProjectID: 5, ExecDeviceFingerprint: boxFP}
	deleted := location(5, boxFP, "/srv/old")
	deleted.Status = consts.DELETE
	w.locations[5] = []*project_location_entity.ProjectLocation{deleted}
	_, err := w.svc(t).ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{SessionID: 75})
	requireCode(t, err, code.ProjectLocationMissing)
}

func TestResolveLocalCommandScope_GivenPinnedDeviceNotPairedHere_ThenRejectsWithoutLocalFallback(t *testing.T) {
	w := newWorld()
	w.sessions[76] = &chat_entity.Session{ID: 76, AgentID: 1, ExecDeviceFingerprint: "sha256:gone"}
	_, err := w.svc(t).ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{SessionID: 76})
	requireCode(t, err, code.AgentBackendInvalidDevice)
}

// 旧会话只钉了执行目标档（任务 7 之前 chat_svc 的写法）：设备取那一档 backend 的设备；
// 本机指纹的档就是本机。
func TestResolveLocalCommandScope_GivenLegacyPinnedBackend_ThenUsesThatBackendsDevice(t *testing.T) {
	w := newWorld()
	w.legacy[51] = &agent_backend_entity.AgentBackend{ID: 51, Type: "claudecode", DeviceFingerprint: selfFP}
	w.legacy[52] = &agent_backend_entity.AgentBackend{ID: 52, Type: "claudecode", DeviceFingerprint: boxFP}
	w.sessions[77] = &chat_entity.Session{ID: 77, AgentID: 1, ExecAgentBackendID: 51}
	w.sessions[78] = &chat_entity.Session{ID: 78, AgentID: 1, ExecAgentBackendID: 52}
	svc := w.svc(t)

	scope, err := svc.ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{SessionID: 77})
	require.NoError(t, err)
	assert.Equal(t, &dispatch_svc.CommandScope{Cwd: "/self/default"}, scope)

	scope, err = svc.ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{SessionID: 78})
	require.NoError(t, err)
	assert.Equal(t, &dispatch_svc.CommandScope{DeviceID: boxFP}, scope)
}

func TestResolveLocalCommandScope_GivenPreSessionRemoteProject_ThenPlansWithoutCreatingSession(t *testing.T) {
	w := newWorld()
	w.projects[5] = &project_entity.Project{ID: 5, Name: "官网", LocalPathMissing: true, Status: consts.ACTIVE}
	w.locations[5] = []*project_location_entity.ProjectLocation{location(5, boxFP, "/srv/site")}
	scope, err := w.svc(t).ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{AgentID: 1, ProjectID: 5})
	require.NoError(t, err)
	assert.Equal(t, &dispatch_svc.CommandScope{DeviceID: boxFP, Cwd: "/srv/site"}, scope)
	assert.Empty(t, w.sessions, "预会话只构造值对象，不建会话")
}

func TestResolveLocalCommandScope_GivenPreSessionNowhereToRun_ThenNoEligibleDevice(t *testing.T) {
	w := newWorld()
	w.paired = nil
	w.reports[selfFP] = runtimeFacts("claudecode", false, "")
	_, err := w.svc(t).ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{AgentID: 1})
	requireCode(t, err, code.DispatchNoEligibleDevice)
}

func TestResolveLocalCommandScope_GivenInvalidTargetForms_ThenRejects(t *testing.T) {
	w := newWorld()
	svc := w.svc(t)
	for name, req := range map[string]*dispatch_svc.ScopeRequest{
		"nil":                nil,
		"empty":              {},
		"session and agent":  {SessionID: 1, AgentID: 1},
		"negative project":   {AgentID: 1, ProjectID: -1},
		"project without ag": {ProjectID: 5},
	} {
		_, err := svc.ResolveLocalCommandScope(context.Background(), req)
		requireCode(t, err, code.InvalidParameter)
		_ = name
	}
	_, err := svc.ResolveLocalCommandScope(context.Background(), &dispatch_svc.ScopeRequest{SessionID: 404})
	requireCode(t, err, code.ChatSessionNotFound)
}

func TestResolveSessionWorkspace(t *testing.T) {
	w := newWorld()
	w.sessions[81] = &chat_entity.Session{ID: 81, AgentID: 1, EngineProfileID: 11}
	w.sessions[82] = &chat_entity.Session{ID: 82, AgentID: 1, ProjectID: 5, ExecDeviceFingerprint: boxFP}
	w.sessions[83] = &chat_entity.Session{ID: 83, AgentID: 1, ExecDeviceFingerprint: otherFP}
	w.locations[5] = []*project_location_entity.ProjectLocation{location(5, boxFP, "/srv/site")}
	svc := w.svc(t)
	ctx := context.Background()

	deviceID, cwd, err := svc.ResolveSessionWorkspace(ctx, 81)
	require.NoError(t, err)
	assert.Equal(t, int64(0), deviceID, "本机会话 deviceID 为 0")
	assert.Equal(t, "/self/default", cwd)

	deviceID, cwd, err = svc.ResolveSessionWorkspace(ctx, 82)
	require.NoError(t, err)
	assert.Equal(t, int64(7), deviceID, "远端会话交出本机配对表的行 ID")
	assert.Equal(t, "/srv/site", cwd)

	_, _, err = svc.ResolveSessionWorkspace(ctx, 83)
	requireCode(t, err, code.RemoteDeviceNotFound)

	_, _, err = svc.ResolveSessionWorkspace(ctx, 0)
	requireCode(t, err, code.InvalidParameter)
	_, _, err = svc.ResolveSessionWorkspace(ctx, 404)
	requireCode(t, err, code.ChatSessionNotFound)
}
