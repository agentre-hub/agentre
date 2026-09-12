package wireinbound

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// TestProtobufJournaledNotificationCarriesCreatetime 钉住补齐这一跳在 Protobuf 线上
// 的那一半:日志行报出的发生时刻要原样落到 JournaledNotification 上。
//
// 这一跳是浏览器控制台唯一能拿到「这一帧什么时候发生」的地方 —— 它的转录是现折的,
// 没有本地库可查(桌面端读自己的 chat_messages.createtime)。丢了它,server 只能盖
// 收帧时刻,而补齐成批落地,一整段离线期间的帧会显示成同一分钟。
func TestProtobufJournaledNotificationCarriesCreatetime(t *testing.T) {
	entry := remotewire.JournaledNotification{
		Seq:        7,
		Method:     remotewire.NotifyEvent,
		Params:     &remotewire.EventFrame{ConversationID: "3f2a1c4e-0000-4000-8000-000000000001", Event: agentruntime.TextDelta{Text: "x"}},
		Createtime: 1700000000111,
	}

	got, err := JournaledNotificationToProto(entry)
	require.NoError(t, err)
	require.Equal(t, int64(7), got.GetSeq())
	require.Equal(t, int64(1700000000111), got.GetCreatetime())
	require.NotNil(t, got.GetPayload())
}

// 没报过时刻的对端(还没升级的 agentred)交出 0。0 必须原样过去而不是被就地补成当下:
// 「不知道」与「刚刚」在下游要走两条路,后者会给一条两天前的对话盖上今天的时间。
func TestProtobufJournaledNotificationKeepsAnUnreportedCreatetimeZero(t *testing.T) {
	entry := remotewire.JournaledNotification{
		Seq:    7,
		Method: remotewire.NotifyEvent,
		Params: &remotewire.EventFrame{ConversationID: "3f2a1c4e-0000-4000-8000-000000000001", Event: agentruntime.TextDelta{Text: "x"}},
	}

	got, err := JournaledNotificationToProto(entry)
	require.NoError(t, err)
	require.Zero(t, got.GetCreatetime())
}
