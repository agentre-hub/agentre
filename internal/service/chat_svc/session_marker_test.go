package chat_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// Given 会话带有 conversation_id，When 组本轮 RunRequest，
// Then 交给 runner 的请求携带同一个 conversation_id（CLI 环境的 AGENTRE_SESSION 来源）。
func TestPrepareTurnRun_CarriesConversationIDForSessionMarker(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	svc := &chatSvc{}
	installMockPool(t, ctrl, svc, 7)

	sess := &chat_entity.Session{ID: 104, AgentID: 7, ConversationID: "00000000-0000-7000-8000-000000000104"}
	a := &agent_entity.Agent{ID: 7, AgentBackendID: 12}
	be := &agent_backend_entity.AgentBackend{
		ID: 12, Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: testDeviceFingerprint(7),
	}

	prepared, err := svc.prepareTurnRun(context.Background(), sess, a, be, nil, nil, nil, "", false, false)
	require.NoError(t, err)
	require.NotNil(t, prepared)
	assert.Equal(t, sess.ConversationID, prepared.req.ConversationID)
}
