package protowire

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Given 线上 goal 请求带 conversation_id，When 翻成领域请求，
// Then GoalRequest.ConversationID 保留它（goal 路径起的 CLI 要靠它带会话标记）。
func TestGoalRequestToDomain_GivenConversationID_ThenCarriesIt(t *testing.T) {
	got, err := GoalRequestToDomain(&agentrewire.RuntimeGoalRequest{
		ConversationId: "11111111-1111-7111-8111-111111111111",
	})
	require.NoError(t, err)
	assert.Equal(t, "11111111-1111-7111-8111-111111111111", got.ConversationID)
}
