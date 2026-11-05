package chat_svc

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
)

// TestCheckpointAssistantWritesOnlyChangedBlocks 钉住轮内 checkpoint 的写放大修复。
//
// 事故:turn 每收到一个 ToolResult 就 checkpoint 一次,而 checkpoint 曾走 Update →
// 整表替换(DELETE 全部块 + INSERT 全部块),于是第 k 次 checkpoint 重写当时已有的全部
// k 个块 —— 单条消息 O(N²)。用户库里消息 26382 最终 1723 块 / 2 MB,却被 checkpoint
// 840 次、DELETE 侧重写 723,550 行 / 910 MB,WAL 涨到 1.4 GB。之后改成整份差分,写省了,
// 编码与解码仍是每次 O(消息大小)。
//
// 现在 checkpoint 只把新增与被改动的块编成行交给仓储;写库失败的那些块下一次照样交出,
// 不会因为内存里的正文已经推进而丢掉;ctx 被取消也照落。
func TestCheckpointAssistantWritesOnlyChangedBlocks(t *testing.T) {
	Convey("轮内连续 checkpoint 只交出新增与被改动的块", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		msgRepo := mock_transcript_repo.NewMockMessageRepo(ctrl)
		prevRepo := transcript_repo.Message()
		transcript_repo.RegisterMessage(msgRepo)
		t.Cleanup(func() { transcript_repo.RegisterMessage(prevRepo) })

		type call struct {
			idx      []int
			truncate int
			body     string
		}
		var calls []call
		failNext := false
		msgRepo.EXPECT().UpsertBlocks(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, m *chat_entity.Message, rows []*transcript_entity.MessageBlock, truncateFrom int) error {
				// ctx canceled 也要照落 —— 这条语义随 Update 一起搬过来。
				So(ctx.Err(), ShouldBeNil)
				c := call{truncate: truncateFrom, body: m.BlocksJSON}
				for _, r := range rows {
					So(r.MessageID, ShouldEqual, m.ID)
					c.idx = append(c.idx, r.Idx)
				}
				calls = append(calls, c)
				if failNext {
					failNext = false
					return errors.New("disk full")
				}
				return nil
			}).Times(4)

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		acc := turn.New()
		acc.AddText("first chunk")
		msg := &chat_entity.Message{ID: 1, SessionID: 1, Role: "assistant", BlocksJSON: "[]"}
		svc := &chatSvc{emitter: NoopEmitter{}}

		svc.checkpointAssistantNew(canceledCtx, msg, acc)
		acc.AddThinking("second block")
		failNext = true
		svc.checkpointAssistantNew(canceledCtx, msg, acc)
		svc.checkpointAssistantNew(canceledCtx, msg, acc)
		svc.checkpointAssistantNew(canceledCtx, msg, acc)

		So(calls[0].idx, ShouldResemble, []int{0})
		// 思考插到正文前面:两个位置都变了。
		So(calls[1].idx, ShouldResemble, []int{0, 1})
		// 上一次写库失败:同一批块再交一次。
		So(calls[2].idx, ShouldResemble, []int{0, 1})
		// 已经落库、之后没变:不再交出任何块。
		So(calls[3].idx, ShouldBeEmpty)
		for _, c := range calls {
			So(c.truncate, ShouldEqual, -1)
		}
		So(msg.BlocksJSON, ShouldEqual, fullSetBlocksJSON(t, acc))
	})
}
