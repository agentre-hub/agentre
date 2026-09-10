package relayenvelope_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/relayenvelope"
)

func TestWrapUnwrap_GivenAFrame_ThenTheChannelIDAndPayloadComeBackIntact(t *testing.T) {
	t.Parallel()

	envelope, err := relayenvelope.Wrap("c0ffee", []byte{0x01, 0x02, 0x03})
	require.NoError(t, err)

	channelID, payload, err := relayenvelope.Unwrap(envelope)
	require.NoError(t, err)
	require.Equal(t, "c0ffee", channelID)
	require.Equal(t, []byte{0x01, 0x02, 0x03}, payload)
}

// 空载荷是合法的:它是「这条通道关了」的信号。
func TestWrapUnwrap_GivenAnEmptyPayload_ThenItSurvivesAsTheChannelClosedSignal(t *testing.T) {
	t.Parallel()

	envelope, err := relayenvelope.Wrap("c0ffee", nil)
	require.NoError(t, err)

	channelID, payload, err := relayenvelope.Unwrap(envelope)
	require.NoError(t, err)
	require.Equal(t, "c0ffee", channelID)
	require.Empty(t, payload)
}

func TestWrap_GivenAnUnusableChannelID_ThenItIsRejected(t *testing.T) {
	t.Parallel()

	for name, channelID := range map[string]string{
		"empty":    "",
		"too long": strings.Repeat("a", relayenvelope.MaxChannelIDBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := relayenvelope.Wrap(channelID, []byte{0x01})
			require.Error(t, err)
		})
	}
}

func TestWrap_GivenAChannelIDExactlyAtTheLimit_ThenItIsAccepted(t *testing.T) {
	t.Parallel()

	channelID := strings.Repeat("a", relayenvelope.MaxChannelIDBytes)
	envelope, err := relayenvelope.Wrap(channelID, []byte{0x01})
	require.NoError(t, err)
	require.LessOrEqual(t, int64(len(envelope)-1), relayenvelope.MaxEnvelopeBytes)

	got, _, err := relayenvelope.Unwrap(envelope)
	require.NoError(t, err)
	require.Equal(t, channelID, got)
}

// 中继上收到的每一帧都是**别的设备**发来的字节。三个宿主从前各写一份解析,校验各不
// 相同 —— 浏览器那份只查截断,长度 0 照收(通道 ID 成空串、整段载荷当帧交出去),
// 非法 UTF-8 静默替换成 U+FFFD。这一组用例是那三份的公约数,现在只有一份实现。
func TestUnwrap_GivenAMalformedEnvelope_ThenItIsRejected(t *testing.T) {
	t.Parallel()

	tooLong := append(
		[]byte{0x00, byte(relayenvelope.MaxChannelIDBytes + 1)},
		strings.Repeat("a", relayenvelope.MaxChannelIDBytes+1)...,
	)

	for name, envelope := range map[string][]byte{
		"shorter than the header":   {0x00},
		"declares a zero-length ID": {0x00, 0x00, 0x01, 0x02},
		"truncated before the ID":   {0x00, 0x08, 'a', 'b'},
		"declares an oversized ID":  tooLong,
		"channel ID is not UTF-8":   {0x00, 0x02, 0xff, 0xfe, 0x01},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, err := relayenvelope.Unwrap(envelope)
			require.Error(t, err)
		})
	}
}
