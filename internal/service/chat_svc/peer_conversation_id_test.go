package chat_svc_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// peerTestFingerprint 是这些用例里那台桌面端的设备指纹 —— 对话身份派生的第一个输入。
const peerTestFingerprint = "sha256:self"

// convID 是这些用例里第 n 条本机会话落库的那个 conversation_id。取值形态无所谓
// (库里存什么就是什么),这里沿用一个确定性派生,只是为了让用例里「同一条会话」
// 在多处写出同一个字面值。
func convID(n int64) string {
	return conversationid.Derive(conversationid.Namespace, peerTestFingerprint, strconv.FormatInt(n, 10))
}

// peerSessionRow 是一条带对话身份的本机会话行 —— 落列之后,任何要被对端寻址的
// 会话行都必须有它。
func peerSessionRow(id int64) *chat_entity.Session {
	return &chat_entity.Session{ID: id, ConversationID: convID(id), Status: consts.ACTIVE}
}

// wirePeerConversations 装上「conversation_id → 本地主键」反查:那是
// chat_sessions.conversation_id 唯一索引上的一次查询(见 chat_svc.ResolvePeerConversation)。
// 点名的 id 反查得到,其余一律「不在本机」。
func wirePeerConversations(t *testing.T, sessions *mock_chat_repo.MockSessionRepo, ids ...int64) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	device := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	device.EXPECT().DeviceFingerprint().Return(peerTestFingerprint, nil).AnyTimes()
	prev := remote_device_svc.Default()
	remote_device_svc.SetDefault(device)
	t.Cleanup(func() { remote_device_svc.SetDefault(prev) })

	known := make(map[string]int64, len(ids))
	for _, id := range ids {
		known[convID(id)] = id
	}
	sessions.EXPECT().FindByConversationID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, conversationID string) (*chat_entity.Session, error) {
			if id, ok := known[conversationID]; ok {
				return peerSessionRow(id), nil
			}
			return nil, nil
		}).AnyTimes()
}
