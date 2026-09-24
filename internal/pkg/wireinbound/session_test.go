package wireinbound

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// TestProtobufDurableNotificationCarriesCreatetime 钉住补齐这一跳在 Protobuf 线上
// 的那一半:转录行报出的发生时刻要原样落到 DurableNotification 上。
//
// 这一跳是浏览器控制台唯一能拿到「这一帧什么时候发生」的地方 —— 它的转录是现折的,
// 没有本地库可查(桌面端读自己的 chat_messages.createtime)。丢了它,server 只能盖
// 收帧时刻,而补齐成批落地,一整段离线期间的帧会显示成同一分钟。
func TestProtobufDurableNotificationCarriesCreatetime(t *testing.T) {
	entry := remotewire.DurableNotification{
		Seq:        7,
		Method:     remotewire.NotifyEvent,
		Params:     &remotewire.EventFrame{ConversationID: "3f2a1c4e-0000-4000-8000-000000000001", Event: agentruntime.TextDelta{Text: "x"}},
		Createtime: 1700000000111,
	}

	got, err := DurableNotificationToProto(entry)
	require.NoError(t, err)
	require.Equal(t, int64(7), got.GetSeq())
	require.Equal(t, int64(1700000000111), got.GetCreatetime())
	require.NotNil(t, got.GetPayload())
}

// 没报过时刻的对端(还没升级的 agentred)交出 0。0 必须原样过去而不是被就地补成当下:
// 「不知道」与「刚刚」在下游要走两条路,后者会给一条两天前的对话盖上今天的时间。
func TestProtobufDurableNotificationKeepsAnUnreportedCreatetimeZero(t *testing.T) {
	entry := remotewire.DurableNotification{
		Seq:    7,
		Method: remotewire.NotifyEvent,
		Params: &remotewire.EventFrame{ConversationID: "3f2a1c4e-0000-4000-8000-000000000001", Event: agentruntime.TextDelta{Text: "x"}},
	}

	got, err := DurableNotificationToProto(entry)
	require.NoError(t, err)
	require.Zero(t, got.GetCreatetime())
}

// TestSubmitAnswerParamsOfKeepsEveryQuestionField 钉住答复入站这一跳与 protowire
// 的 AskQuestion 编解码同形:后端逐题的 DisallowOther / IsSecret 等开关一格都不能
// 在这里丢 —— 这里曾自带一份逐字段搬运,新增字段时漏搬也不会有任何东西报错。
func TestSubmitAnswerParamsOfKeepsEveryQuestionField(t *testing.T) {
	questions := []agentruntime.AskQuestion{{
		ID: "q1", Question: "Pick", Header: "H", MultiSelect: true, IsOther: true, IsSecret: true, DisallowOther: true,
		Options: []agentruntime.AskOption{{Label: "A", Description: "first", Preview: "pa"}},
	}}
	request := &agentrewire.RuntimeSubmitAnswerRequest{
		ConversationId: "conv-1", RequestId: "ask-1",
		Questions: protowire.AskQuestionsToProto(questions),
	}

	params := SubmitAnswerParamsOf(request)

	require.Equal(t, questions, params.Questions)
}
