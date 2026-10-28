package chat_svc

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
)

// 桌面重启后,承载 agrctl 的进程已随上一个桌面进程死掉:本机父会话里仍是 running/waiting
// 的 agrctl 卡永远等不到收尾(spec「卡片状态不说谎」「取消」「重载」)。启动清理把它们
// 收成已取消,走与运行期同一条写入分工(handlers.ApplyAgrctlInfo),单运行快照跟着翻。

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
func settleWith(t *testing.T, stored *blocks.SubagentStateBlock) (int, error, int) {
	ctrl := gomock.NewController(t)
	repo := mock_transcript_repo.NewMockMessageRepo(ctrl)
	prev := transcript_repo.Message()
	transcript_repo.RegisterMessage(repo)
	t.Cleanup(func() { transcript_repo.RegisterMessage(prev) })

	patched := 0
	repo.EXPECT().ListOpenAgrctlCards(gomock.Any()).
		Return([]transcript_repo.OpenAgrctlCard{{SessionID: 7, ToolCallID: "tu1"}}, nil)
	repo.EXPECT().PatchSubagentState(gomock.Any(), int64(7), "tu1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int64, _ string, mutate func(*blocks.SubagentStateBlock) bool) (bool, error) {
			if mutate(stored) {
				patched++
			}
			return true, nil
		}).AnyTimes()
	n, err := SettleOrphanedAgrctlCards(context.Background())
	return n, err, patched
}

func TestSettleOrphanedAgrctlCards(t *testing.T) {
	Convey("given an agrctl card left running in a local parent by a dead desktop", t, func() {
		for _, kind := range []string{"local_bash", "agrctl_send"} {
			for _, status := range []string{"running", "waiting"} {
				Convey("when the card is a "+kind+" card in "+status+", then it is settled canceled including runs[0]", func() {
					card := agrctlCard(kind, status)
					n, err, patched := settleWith(t, card)
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
			_, err, patched := settleWith(t, card)
			So(err, ShouldBeNil)
			So(patched, ShouldEqual, 0)
			So(card.Status, ShouldEqual, "completed")
			So(card.Runs[0].Status, ShouldEqual, "completed")
		})

		Convey("when the card is a native background card (no agrctl origin), then it is untouched", func() {
			card := agrctlCard("local_bash", "running")
			card.Origin = ""
			_, err, patched := settleWith(t, card)
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

		n, err := SettleOrphanedAgrctlCards(context.Background())
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
		repo.EXPECT().PatchSubagentState(gomock.Any(), int64(1), "a", gomock.Any()).Return(false, errors.New("db busy"))
		repo.EXPECT().PatchSubagentState(gomock.Any(), int64(2), "b", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ int64, _ string, m func(*blocks.SubagentStateBlock) bool) (bool, error) {
				return m(agrctlCard("local_bash", "running")), nil
			})

		n, err := SettleOrphanedAgrctlCards(context.Background())
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

		_, err := SettleOrphanedAgrctlCards(context.Background())
		So(err, ShouldNotBeNil)
	})
}
