package chat_svc

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
)

// 桌面重启后,承载 agrctl 的进程已随上一个桌面进程死掉:本机父会话里仍是 running/waiting
// 的 agrctl 卡永远等不到收尾(spec「卡片状态不说谎」「取消」「重载」)。启动清理把它们
// 收成已取消,走与运行期同一条写入分工(handlers.ApplyAgrctlInfo),单运行快照跟着翻。

// settleCutoff 是这些用例里的「启动时刻」;孤儿卡的宿主消息都建于它之前。
var settleCutoff = time.UnixMilli(1_000_000)

// hostedAt 让仓储交回 toolCallID 所在宿主消息,建于 createtime(毫秒)。
func hostedAt(repo *mock_transcript_repo.MockMessageRepo, sessionID int64, toolCallID string, createtime int64) {
	repo.EXPECT().FindAssistantBySubagentToolCallID(gomock.Any(), sessionID, toolCallID).
		Return(&transcript_entity.Message{Createtime: createtime}, nil).AnyTimes()
}

func agrctlCard(kind, status string) *blocks.SubagentStateBlock {
	return &blocks.SubagentStateBlock{
		ParentToolCallID: "tu1",
		Kind:             kind,
		Status:           status,
		Origin:           agentruntime.SubagentOriginAgrctl,
		Mode:             "single",
		Runs:             []agentruntime.SubagentRun{{ID: "r1", Agent: "reviewer", Status: status}},
	}
}

// settleWith 让仓储交回 cards 指定的卡,并把 PatchSubagentState 的 mutate 作用在 stored 上。
func settleWith(t *testing.T, stored *blocks.SubagentStateBlock) (int, int, error) {
	ctrl := gomock.NewController(t)
	repo := mock_transcript_repo.NewMockMessageRepo(ctrl)
	prev := transcript_repo.Message()
	transcript_repo.RegisterMessage(repo)
	t.Cleanup(func() { transcript_repo.RegisterMessage(prev) })

	patched := 0
	hostedAt(repo, 7, "tu1", 999_000)
	repo.EXPECT().ListOpenAgrctlCards(gomock.Any()).
		Return([]transcript_repo.OpenAgrctlCard{{SessionID: 7, ToolCallID: "tu1"}}, nil)
	repo.EXPECT().PatchSubagentState(gomock.Any(), int64(7), "tu1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int64, _ string, mutate func(*blocks.SubagentStateBlock) bool) (bool, error) {
			if mutate(stored) {
				patched++
			}
			return true, nil
		}).AnyTimes()
	n, err := SettleOrphanedAgrctlCards(context.Background(), settleCutoff)
	return n, patched, err
}

func TestSettleOrphanedAgrctlCards(t *testing.T) {
	Convey("given an agrctl card left running in a local parent by a dead desktop", t, func() {
		for _, kind := range []string{"local_bash", "agrctl_send"} {
			for _, status := range []string{"running", "waiting"} {
				Convey("when the card is a "+kind+" card in "+status+", then it is settled canceled including runs[0]", func() {
					card := agrctlCard(kind, status)
					n, patched, err := settleWith(t, card)
					So(err, ShouldBeNil)
					So(n, ShouldEqual, 1)
					So(patched, ShouldEqual, 1)
					So(card.Status, ShouldEqual, "canceled")
					So(card.Runs, ShouldHaveLength, 1)
					So(card.Runs[0].Status, ShouldEqual, "canceled")
				})
			}
		}

		Convey("when the card already completed, then it is untouched", func() {
			card := agrctlCard("local_bash", "completed")
			_, patched, err := settleWith(t, card)
			So(err, ShouldBeNil)
			So(patched, ShouldEqual, 0)
			So(card.Status, ShouldEqual, "completed")
			So(card.Runs[0].Status, ShouldEqual, "completed")
		})

		Convey("when the card is a native background card (no agrctl origin), then it is untouched", func() {
			card := agrctlCard("local_bash", "running")
			card.Origin = ""
			_, patched, err := settleWith(t, card)
			So(err, ShouldBeNil)
			So(patched, ShouldEqual, 0)
			So(card.Status, ShouldEqual, "running")
		})
	})
}

func TestSettleOrphanedAgrctlCards_NoOpenCardsPatchesNothing(t *testing.T) {
	Convey("given the repo reports no open agrctl cards in local parents (remote parents are filtered out by the query)", t, func() {
		ctrl := gomock.NewController(t)
		repo := mock_transcript_repo.NewMockMessageRepo(ctrl)
		prev := transcript_repo.Message()
		transcript_repo.RegisterMessage(repo)
		t.Cleanup(func() { transcript_repo.RegisterMessage(prev) })
		repo.EXPECT().ListOpenAgrctlCards(gomock.Any()).Return(nil, nil)

		n, err := SettleOrphanedAgrctlCards(context.Background(), settleCutoff)
		So(err, ShouldBeNil)
		So(n, ShouldEqual, 0)
	})
}

func TestSettleOrphanedAgrctlCards_ErrorsDoNotStopTheSweep(t *testing.T) {
	Convey("given one card's patch fails and another succeeds", t, func() {
		ctrl := gomock.NewController(t)
		repo := mock_transcript_repo.NewMockMessageRepo(ctrl)
		prev := transcript_repo.Message()
		transcript_repo.RegisterMessage(repo)
		t.Cleanup(func() { transcript_repo.RegisterMessage(prev) })
		repo.EXPECT().ListOpenAgrctlCards(gomock.Any()).Return([]transcript_repo.OpenAgrctlCard{
			{SessionID: 1, ToolCallID: "a"}, {SessionID: 2, ToolCallID: "b"},
		}, nil)
		hostedAt(repo, 1, "a", 1)
		hostedAt(repo, 2, "b", 1)
		repo.EXPECT().PatchSubagentState(gomock.Any(), int64(1), "a", gomock.Any()).Return(false, errors.New("db busy"))
		repo.EXPECT().PatchSubagentState(gomock.Any(), int64(2), "b", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ int64, _ string, m func(*blocks.SubagentStateBlock) bool) (bool, error) {
				return m(agrctlCard("local_bash", "running")), nil
			})

		n, err := SettleOrphanedAgrctlCards(context.Background(), settleCutoff)
		So(err, ShouldBeNil)
		So(n, ShouldEqual, 1)
	})
}

func TestSettleOrphanedAgrctlCards_ListFailureIsReturned(t *testing.T) {
	Convey("given listing fails", t, func() {
		ctrl := gomock.NewController(t)
		repo := mock_transcript_repo.NewMockMessageRepo(ctrl)
		prev := transcript_repo.Message()
		transcript_repo.RegisterMessage(repo)
		t.Cleanup(func() { transcript_repo.RegisterMessage(prev) })
		repo.EXPECT().ListOpenAgrctlCards(gomock.Any()).Return(nil, errors.New("boom"))

		_, err := SettleOrphanedAgrctlCards(context.Background(), settleCutoff)
		So(err, ShouldNotBeNil)
	})
}

func TestSettleOrphanedAgrctlCards_SkipsCardsCreatedAfterCutoff(t *testing.T) {
	Convey("given a settle that runs in the background after the chat service is live", t, func() {
		ctrl := gomock.NewController(t)
		repo := mock_transcript_repo.NewMockMessageRepo(ctrl)
		prev := transcript_repo.Message()
		transcript_repo.RegisterMessage(repo)
		t.Cleanup(func() { transcript_repo.RegisterMessage(prev) })
		repo.EXPECT().ListOpenAgrctlCards(gomock.Any()).Return([]transcript_repo.OpenAgrctlCard{
			{SessionID: 1, ToolCallID: "old"}, {SessionID: 2, ToolCallID: "live"},
		}, nil)
		hostedAt(repo, 1, "old", 999_999)
		hostedAt(repo, 2, "live", 1_000_000)
		// 只有启动前的孤儿卡会被改写;启动之后新开的卡属于本进程里活着的运行。
		repo.EXPECT().PatchSubagentState(gomock.Any(), int64(1), "old", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ int64, _ string, m func(*blocks.SubagentStateBlock) bool) (bool, error) {
				return m(agrctlCard("local_bash", "running")), nil
			})

		n, err := SettleOrphanedAgrctlCards(context.Background(), settleCutoff)
		So(err, ShouldBeNil)
		So(n, ShouldEqual, 1)
	})
}
