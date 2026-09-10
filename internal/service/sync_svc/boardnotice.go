package sync_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
)

// boardJoinNoticeKey 是「看板首次并入同步组」那条一次性说明的存放位置。它是本机
// 说过没说过，不是账号级对象，因此和下行游标一样住在本地 key-value 设置表里。
const boardJoinNoticeKey = "sync.board_join_notice"

// 一次性说明的两个状态：还没说过 / 已经说过。没有这一行 = 看板还没并入同步组。
const (
	boardJoinNoticePending = "pending"
	boardJoinNoticeDone    = "done"
)

// boardKinds 是看板并入同步组带来的三个对象类型。
var boardKinds = map[string]struct{}{
	syncwire.KindLabel:      {},
	syncwire.KindIssue:      {},
	syncwire.KindIssueLabel: {},
}

func isBoardKind(kind string) bool {
	_, ok := boardKinds[kind]
	return ok
}

// markBoardJoinNotice 在看板真的被并进账号（认领到第一批看板行，R12a）时记下
// 「还有一次说明没给」。
//
// 为什么非说不可：两台机器各自积累的历史任务，首次同步后会**合并**出现在同一个
// 账号下，而且不可逆——撤销只能靠逐条删除。这是「任务随账号走」的应有之义，不是
// 缺陷，但它必须说在前面，而不是静默合并（规格「首次上行的后果要说在前面」）。
//
// 只写第一次：已经排上队（pending）或已经说过（done）都原样不动，说明因此至多
// 出现一次。写失败不回传——同步不因一条提示而中断（R8 的同一口径）。
func (s *service) markBoardJoinNotice(ctx context.Context) {
	row, err := app_setting_repo.AppSetting().Get(ctx, boardJoinNoticeKey)
	if err == nil && (row == nil || row.Value == "") {
		err = app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
			Key:        boardJoinNoticeKey,
			Value:      boardJoinNoticePending,
			Updatetime: s.now(),
		})
	}
	if err != nil {
		logger.Ctx(ctx).Warn("sync_svc.markBoardJoinNotice: could not record the one-time notice",
			zap.Error(err))
	}
}

// BoardJoinNoticePending 报告那条一次性说明还欠着没给。
func (s *service) BoardJoinNoticePending(ctx context.Context) (bool, error) {
	row, err := app_setting_repo.AppSetting().Get(ctx, boardJoinNoticeKey)
	if err != nil {
		return false, err
	}
	return row != nil && row.Value == boardJoinNoticePending, nil
}

// AcknowledgeBoardJoinNotice 记下说明已经给过：之后永不再出现。
func (s *service) AcknowledgeBoardJoinNotice(ctx context.Context) error {
	return app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key:        boardJoinNoticeKey,
		Value:      boardJoinNoticeDone,
		Updatetime: s.now(),
	})
}
