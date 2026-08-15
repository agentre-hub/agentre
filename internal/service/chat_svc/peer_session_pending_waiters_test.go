package chat_svc_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-ai/agentre/internal/service/chat_svc"
)

// peerWaiterAdapter 是 runtime.session.pendingWaiters 落到桌面端的入口形状,与
// internal/peer 的 inboundSessionAdapter 同构 —— 在 chat_svc 的测试里反向 import
// internal/peer 会把依赖方向倒过来。
type peerWaiterAdapter interface {
	PendingPeerSessionWaiters(
		ctx context.Context, params wire.SessionPendingWaitersParams,
	) (wire.SessionPendingWaitersResult, error)
}

// fakeWaiterRunner 是有审批协议的 backend runtime 替身:它实现读侧的
// agentruntime.WaiterLister,记录被问的会话键。
type fakeWaiterRunner struct {
	askedSession int64
	snapshot     agentruntime.WaiterSnapshot
}

func (*fakeWaiterRunner) Capabilities() capability.Capabilities { return capability.Capabilities{} }

func (*fakeWaiterRunner) Run(_ context.Context, _ agentruntime.RunRequest) (
	<-chan agentruntime.Event, *agentruntime.RunResult, error,
) {
	ch := make(chan agentruntime.Event)
	close(ch)
	return ch, &agentruntime.RunResult{}, nil
}

func (f *fakeWaiterRunner) PendingWaiters(_ context.Context, sessionID int64) agentruntime.WaiterSnapshot {
	f.askedSession = sessionID
	return f.snapshot
}

// Given a desktop-hosted session whose local backend is blocked on a tool
// permission and a question, when a browser peer asks for that session's
// pending waiters, then the desktop answers with the live snapshot in the same
// wire shape agentred uses — that snapshot is the only data source the browser
// has for drawing an approval card.
func TestPendingPeerSessionWaiters_GivenBlockedLocalBackend_ThenReturnsLiveSnapshot(t *testing.T) {
	m := setupChatTest(t)

	fake := &fakeWaiterRunner{snapshot: agentruntime.WaiterSnapshot{
		ToolPermissions: []agentruntime.PendingToolPermission{{
			RequestID: "perm-1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls -la"}`),
		}},
		AskUserQuestions: []agentruntime.PendingAskUserQuestion{{
			RequestID: "ask-1", Questions: []agentruntime.AskQuestion{{ID: "q1", Question: "继续?"}},
		}},
	}}
	t.Cleanup(agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, fake))

	m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
		ID: 42, AgentID: 7, Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
		ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
	}, nil)

	adapter, ok := m.svc.(peerWaiterAdapter)
	require.True(t, ok, "the desktop chat service must serve the pendingWaiters read half")
	got, err := adapter.PendingPeerSessionWaiters(m.ctx, wire.SessionPendingWaitersParams{SessionID: 42})

	require.NoError(t, err)
	assert.Equal(t, wire.SessionPendingWaitersResult{
		ToolPermissions:  fake.snapshot.ToolPermissions,
		AskUserQuestions: fake.snapshot.AskUserQuestions,
	}, got)
	assert.Equal(t, int64(42), fake.askedSession,
		"waiters are keyed by this desktop's own session id, the same key the answer half submits on")
}

// Given the session is pinned to a backend whose runtime has an approval write
// half but cannot enumerate its waiters, when the peer asks, then it gets an
// empty snapshot rather than an error (R7: an unimplemented reader is a normal
// case, and an error would make the browser show a failed load).
func TestPendingPeerSessionWaiters_GivenBackendWithoutWaiterLister_ThenEmptyWithoutError(t *testing.T) {
	m := setupChatTest(t)

	t.Cleanup(agentruntime.SwapRuntimeForTest(agent_backend_entity.TypeClaudeCode, &fakePermRunner{}))

	m.session.EXPECT().Find(m.ctx, int64(42)).Return(&chat_entity.Session{
		ID: 42, AgentID: 7, Status: consts.ACTIVE,
	}, nil)
	m.agent.EXPECT().Find(m.ctx, int64(7)).Return(&agent_entity.Agent{
		ID: 7, AgentBackendID: 12, Status: consts.ACTIVE,
	}, nil)
	m.backend.EXPECT().Find(m.ctx, int64(12)).Return(&agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), Status: consts.ACTIVE,
	}, nil)

	adapter, ok := m.svc.(peerWaiterAdapter)
	require.True(t, ok)
	got, err := adapter.PendingPeerSessionWaiters(m.ctx, wire.SessionPendingWaitersParams{SessionID: 42})

	require.NoError(t, err)
	assert.Equal(t, wire.SessionPendingWaitersResult{}, got)
}

// Given the peer names a session this desktop does not have, when it asks for
// pending waiters, then it gets the same typed not-found the rest of the peer
// session surface raises instead of an empty snapshot that would read as
// "nothing to approve".
func TestPendingPeerSessionWaiters_GivenUnknownSession_ThenSessionNotFound(t *testing.T) {
	m := setupChatTest(t)

	m.session.EXPECT().Find(m.ctx, int64(90001)).Return(nil, nil)

	adapter, ok := m.svc.(peerWaiterAdapter)
	require.True(t, ok)
	_, err := adapter.PendingPeerSessionWaiters(m.ctx, wire.SessionPendingWaitersParams{SessionID: 90001})

	require.ErrorIs(t, err, chat_svc.ErrPeerSessionNotFound)
}
