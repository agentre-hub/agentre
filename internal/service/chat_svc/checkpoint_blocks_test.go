package chat_svc

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// TestCheckpointAssistantPassesLastPersistedBody 钉住轮内 checkpoint 的写放大修复。
//
// 事故:turn 每收到一个 ToolResult 就 checkpoint 一次,而 checkpoint 走 Update →
// 整表替换(DELETE 全部块 + INSERT 全部块),于是第 k 次 checkpoint 重写当时已有的全部
// k 个块 —— 单条消息 O(N²)。用户库里消息 26382 最终 1723 块 / 2 MB,却被 checkpoint
// 840 次、DELETE 侧重写 723,550 行 / 910 MB,WAL 涨到 1.4 GB。
//
// 差分写要成立,checkpoint 必须把「上一次落库的正文」交给仓储。这里断言的正是这一点:
// 第二次 checkpoint 收到的 prev,逐字节等于第一次落库的那份 —— 否则差分会把整份正文
// 当成新内容重写,修复形同虚设。
func TestCheckpointAssistantPassesLastPersistedBody(t *testing.T) {
	Convey("轮内连续 checkpoint 把上一次落库的正文当作差分基准", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		msgRepo := mock_chat_repo.NewMockMessageRepo(ctrl)
		prevRepo := chat_repo.Message()
		chat_repo.RegisterMessage(msgRepo)
		t.Cleanup(func() { chat_repo.RegisterMessage(prevRepo) })

		var prevs, nexts []string
		msgRepo.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, m *chat_entity.Message, prev string) error {
				// ctx canceled 也要照落 —— 这条语义随 Update 一起搬过来。
				So(ctx.Err(), ShouldBeNil)
				prevs = append(prevs, prev)
				nexts = append(nexts, m.BlocksJSON)
				return nil
			}).Times(2)

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		acc := turn.New()
		acc.AddText("first chunk")
		msg := &chat_entity.Message{ID: 1, SessionID: 1, Role: "assistant"}
		svc := &chatSvc{emitter: NoopEmitter{}}

		svc.checkpointAssistantNew(canceledCtx, msg, acc)
		acc.AddThinking("second block")
		svc.checkpointAssistantNew(canceledCtx, msg, acc)

		So(prevs[0], ShouldEqual, "")
		So(prevs[1], ShouldEqual, nexts[0])
		So(nexts[1], ShouldNotEqual, nexts[0])
	})
}
