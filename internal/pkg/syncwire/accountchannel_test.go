package syncwire_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

func TestDecodeAccountChannelFrameProtobufCompatibility(t *testing.T) {
	// Given protobuf(AccountSyncVersion{version: 42}) and a future field 99.
	payload := []byte{0x0a, 0x04, 0x0a, 0x02, 0x08, 0x2a, 0x98, 0x06, 0x07}

	// When the public account-channel boundary decodes the frame.
	frame, known, err := syncwire.DecodeAccountChannelFrame(payload)

	// Then the known signal survives the field added by a future sender.
	require.NoError(t, err)
	require.True(t, known)
	require.Equal(t, syncwire.AccountChannelFrame{
		Type:    syncwire.AccountChannelSyncVersion,
		Version: 42,
	}, frame)
}
