package remote

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// recordingLeaseSource 记下被要租约的供应商,并给每个发一份租约。
type recordingLeaseSource struct{ asked [][]string }

func (s *recordingLeaseSource) ProviderLeases(_ context.Context, keys []string) []*agentrewire.ProviderLease {
	s.asked = append(s.asked, keys)
	out := make([]*agentrewire.ProviderLease, 0, len(keys))
	for _, key := range keys {
		out = append(out, &agentrewire.ProviderLease{ProviderKey: key, ApiKey: "desk-" + key, ExpiresAt: 1})
	}
	return out
}

// directConn 是一条经局域网直连到 agentred 的连接(而不是经账号中继)。
type directConn struct{ *fakeConn }

func (directConn) DirectRoute() bool { return true }

func leaseRunParams(t *testing.T) wire.RunParams {
	t.Helper()
	backend, err := json.Marshal(&agent_backend_entity.AgentBackend{
		Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "pk-bound",
		ModelRoutes: `{"OPUS":{"providerKey":"pk-route","modelKey":"mk"},"HAIKU":{"providerKey":"pk-bound"}}`,
	})
	require.NoError(t, err)
	return wire.RunParams{Backend: backend, ConversationID: "c-1", LLMProviderKey: "pk-session"}
}

// Given 桌面端经局域网直连派这一轮,When 组 runtime.run,Then 这一轮可能用到的每个供应商
// (会话的、引擎配置绑定的、模型路由里的)各附一份租约,只问一次、不重复。
func TestBuildRunRequest_GivenADirectConnection_ThenItAttachesALeasePerProviderTheTurnMayUse(t *testing.T) {
	source := &recordingLeaseSource{}
	r := New(directConn{newFakeConn()}, WithProviderLeases(source))

	request, err := r.buildRunRequest(context.Background(), leaseRunParams(t))
	require.NoError(t, err)
	require.Len(t, source.asked, 1)
	assert.Equal(t, []string{"pk-bound", "pk-route", "pk-session"}, source.asked[0])
	keys := make([]string, 0, len(request.GetProviderLeases()))
	for _, lease := range request.GetProviderLeases() {
		keys = append(keys, lease.GetProviderKey())
	}
	assert.Equal(t, []string{"pk-bound", "pk-route", "pk-session"}, keys)
}

// 经账号中继的一轮不附带租约:agentred 自己向账号服务器申请,key 不经中继过线。
func TestBuildRunRequest_GivenARelayedConnection_ThenNoLeaseIsAttached(t *testing.T) {
	source := &recordingLeaseSource{}
	r := New(newFakeConn(), WithProviderLeases(source))

	request, err := r.buildRunRequest(context.Background(), leaseRunParams(t))
	require.NoError(t, err)
	assert.Empty(t, request.GetProviderLeases())
	assert.Empty(t, source.asked)
}

// 没有供应商的一轮(CLI 自己的登录态)什么都不附带,也不去要。
func TestBuildRunRequest_GivenATurnWithoutProviders_ThenNothingIsAsked(t *testing.T) {
	source := &recordingLeaseSource{}
	r := New(directConn{newFakeConn()}, WithProviderLeases(source))
	backend, err := json.Marshal(&agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)})
	require.NoError(t, err)

	request, err := r.buildRunRequest(context.Background(), wire.RunParams{Backend: backend, ConversationID: "c-2"})
	require.NoError(t, err)
	assert.Empty(t, request.GetProviderLeases())
	assert.Empty(t, source.asked)
}
