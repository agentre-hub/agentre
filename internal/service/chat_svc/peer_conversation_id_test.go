package chat_svc_test

import (
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

// convID 是这台桌面端为它本机第 n 条会话交出去的对话身份,与生产的 peerConversationID
// 同输入同算法(本机设备指纹 + 本地会话 id)。写死一份等价实现是有意的:用例断言的
// 因此是**约定的值**,而不是"两边调了同一个函数"这种恒真命题。
func convID(n int64) string {
	return conversationid.Derive(conversationid.Namespace, peerTestFingerprint, strconv.FormatInt(n, 10))
}

// wirePeerConversations 装上「conversation_id → 本地主键」反查要的两样东西:本机指纹,
// 与一份本机会话清单。conversation_id 落库并建唯一索引之前,反查只能靠枚举本机会话重铸
// 一遍身份(见 chat_svc.ResolvePeerConversation);备忘录是进程级的,同一条对话在一次
// 进程里只重建一次,所以两个期望都用 AnyTimes。
func wirePeerConversations(t *testing.T, sessions *mock_chat_repo.MockSessionRepo, ids ...int64) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	device := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	device.EXPECT().DeviceFingerprint().Return(peerTestFingerprint, nil).AnyTimes()
	prev := remote_device_svc.Default()
	remote_device_svc.SetDefault(device)
	t.Cleanup(func() { remote_device_svc.SetDefault(prev) })

	rows := make([]*chat_entity.Session, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, &chat_entity.Session{ID: id, Status: consts.ACTIVE})
	}
	sessions.EXPECT().ListIndexPaged(gomock.Any(), gomock.Any(), 0, gomock.Any()).Return(rows, nil).AnyTimes()
}
