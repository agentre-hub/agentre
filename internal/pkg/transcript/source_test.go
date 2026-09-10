package transcript_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/transcript"
)

// StampUserMessageSource 是两个宿主共用的那一份:同一句「别人发过来的话」在 agentred
// 与桌面端上必须落成逐字节相同的块,否则同一条对话换台机器托管就渲染出不同的来源。
// 这几条钉的正是那份字节。
func TestStampUserMessageSource(t *testing.T) {
	t.Parallel()

	const plain = `[{"type":"text","data":{"text":"follow-up"}}]`

	for _, tc := range []struct {
		name   string
		in     string
		device string
		who    string
		want   string
		reason string
	}{
		{
			name: "本机发的:原样交回", in: plain, device: "", who: "",
			want:   plain,
			reason: "空来源与「没有来源」必须是同一种字节,否则两台宿主投影出不同的帧",
		},
		{
			name: "带设备与名字", in: plain, device: "sha256:peer", who: "test-mac",
			want: `[{"type":"text","data":{"sourceDevice":"sha256:peer",` +
				`"sourceDeviceName":"test-mac","text":"follow-up"}}]`,
		},
		{
			name: "只有设备指纹", in: plain, device: "sha256:peer", who: "",
			want:   `[{"type":"text","data":{"sourceDevice":"sha256:peer","text":"follow-up"}}]`,
			reason: "名字拿不到时只盖指纹,消费方回退成指纹显示",
		},
		{
			name: "附件在前:盖的仍是第一个**文本**块",
			in: `[{"type":"image","data":{"media_type":"image/png","source":{"inline":"UE5H"}}},` +
				`{"type":"text","data":{"text":"look"}}]`,
			device: "sha256:peer", who: "test-mac",
			want: `[{"type":"image","data":{"media_type":"image/png","source":{"inline":"UE5H"}}},` +
				`{"type":"text","data":{"sourceDevice":"sha256:peer","sourceDeviceName":"test-mac","text":"look"}}]`,
			reason: "来源是整条消息的属性,盖在文本块上;附件块一个字节都不该动",
		},
		{
			name:   "一个文本块都没有:原样交回",
			in:     `[{"type":"image","data":{"media_type":"image/png","source":{"inline":"UE5H"}}}]`,
			device: "sha256:peer", who: "test-mac",
			want:   `[{"type":"image","data":{"media_type":"image/png","source":{"inline":"UE5H"}}}]`,
			reason: "没有可盖的地方时不得凭空造一个块",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := transcript.StampUserMessageSource(tc.in, tc.device, tc.who)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got, tc.reason)
		})
	}
}

// 正文解不开时报错而不是静默交回原样:静默会让这条消息带着「没有来源」上线,而
// 来源正是消费方区分「我发的」与「别人发的」的唯一依据。
func TestStampUserMessageSource_GivenUndecodableBlocks_ThenErrors(t *testing.T) {
	t.Parallel()

	_, err := transcript.StampUserMessageSource(`not json`, "sha256:peer", "test-mac")
	require.Error(t, err)
}
