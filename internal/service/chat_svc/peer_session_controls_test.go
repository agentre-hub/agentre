package chat_svc

import (
	"errors"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// Given a remote peer starts a desktop session turn, when its source is
// persisted on the normal user row, then reload and peer history retain the
// source pill metadata; a local row remains byte-for-byte source-free.
func TestPeerSessionMessageSource_GivenRemoteAndLocalRows_ThenPersistsRemoteSourceWithoutChangingLocalRows(t *testing.T) {
	remote := &chat_entity.Message{SessionID: 41, Role: "user"}
	require.NoError(t, remote.SetBlocks([]cagoblocks.ContentBlock{&cagoblocks.TextBlock{Text: "ship it"}}))
	require.NoError(t, persistPeerMessageSource(remote, peerMessageSource{
		Device: "sha256:phone", Name: "Pixel",
	}))

	loaded, err := toChatMessage(remote)
	require.NoError(t, err)
	assert.Equal(t, "sha256:phone", loaded.SourceDevice)
	assert.Equal(t, "Pixel", loaded.SourceDeviceName)

	history, _, err := synthesizePeerHistory(convID(41), []*chat_entity.Message{remote})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, agentruntime.UserMessageEvent{
		Text: "ship it", SourceDevice: "sha256:phone", SourceDeviceName: "Pixel",
	}, history[0].Event)

	local := &chat_entity.Message{SessionID: 41, Role: "user"}
	require.NoError(t, local.SetBlocks([]cagoblocks.ContentBlock{&cagoblocks.TextBlock{Text: "ship it"}}))
	before := local.BlocksJSON
	require.NoError(t, persistPeerMessageSource(local, peerMessageSource{}))
	assert.Equal(t, before, local.BlocksJSON, "local source-free messages must not acquire metadata")
	localLoaded, err := toChatMessage(local)
	require.NoError(t, err)
	assert.Empty(t, localLoaded.SourceDevice)
	assert.Empty(t, localLoaded.SourceDeviceName)
}

// Given the desktop and a peer race to answer one waiting control, when the
// second answer reaches the session adapter, then it gets the typed
// already-handled result instead of a generic failure or silent success.
func TestPeerSessionControlResult_GivenWaiterAlreadyConsumed_ThenReturnsAlreadyHandled(t *testing.T) {
	result, err := peerSessionControlResult(agentruntime.ErrWaiterNotFound)
	require.NoError(t, err)
	assert.Equal(t, PeerSessionControlResult{AlreadyHandled: true}, result)
}

// Given a desktop session is pinned to an unavailable agentred, when a peer
// starts another turn, then the distinct typed result keeps history readable
// while rejecting the write; ordinary read errors do not get that mapping.
func TestPeerSessionExecutionResult_GivenPinnedAgentredUnavailable_ThenRejectsWriteButKeepsHistoryReadable(t *testing.T) {
	result, err := PeerSessionExecutionResult(ErrPeerExecutionUnavailable)
	require.NoError(t, err)
	assert.Equal(t, PeerSessionRunResult{
		Accepted: false, HistoryAvailable: true, ExecutionUnavailable: true,
	}, result)

	_, err = PeerSessionExecutionResult(errors.New("database unavailable"))
	require.Error(t, err)

}
