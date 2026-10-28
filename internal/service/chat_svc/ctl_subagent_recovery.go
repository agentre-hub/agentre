package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/handlers"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// SettleOrphanedAgrctlCards 桌面启动清理的一步:把本机父会话里仍是 running / waiting 的
// agrctl 绑定卡收成已取消,交回实际改写的张数。
//
// 承载 agrctl 的进程(shell 里的 `agrctl send --wait`)与它背后的子会话都随上一个桌面
// 进程死掉,进程内没有任何东西能再给这些卡写终态;不收,卡永远 running,后台面板只读
// 持久状态,会一直列着它(spec「卡片状态不说谎」「取消」)。远端父会话不在其列(仓储查询
// 已排除):执行方是 agentred,由 CatchUpRemoteSessions 与 agentred 的转录补齐。
//
// 状态走与运行期同一条写入分工(handlers.ApplyAgrctlInfo),单运行快照 runs[0] 跟着翻;
// 单张卡写失败只记 warn,不挡住其余卡。
func SettleOrphanedAgrctlCards(ctx context.Context) (int, error) {
	repo := transcript_repo.Message()
	cards, err := repo.ListOpenAgrctlCards(ctx)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.SettleOrphanedAgrctlCards: list open agrctl cards failed", zap.Error(err))
		return 0, err
	}
	settled := 0
	for _, c := range cards {
		changed := false
		_, err := repo.PatchSubagentState(ctx, c.SessionID, c.ToolCallID, func(b *blocks.SubagentStateBlock) bool {
			changed = cancelOrphanedAgrctlCard(b)
			return changed
		})
		if err != nil {
			logger.Ctx(ctx).Warn("chat_svc.SettleOrphanedAgrctlCards: settle card failed",
				zap.Int64("sessionId", c.SessionID), zap.String("toolUseId", c.ToolCallID), zap.Error(err))
			continue
		}
		if changed {
			settled++
		}
	}
	if settled > 0 {
		logger.Ctx(ctx).Info("chat_svc.SettleOrphanedAgrctlCards: settled orphaned agrctl cards as canceled",
			zap.Int("count", settled))
	}
	return settled, nil
}

// cancelOrphanedAgrctlCard 把一张未收尾的 agrctl 卡写成已取消。后台卡的状态归 CLI、前台卡
// 归注入方,所以两种身份的帧各试一次,谁拥有状态谁生效;已收尾的卡、非 agrctl 的原生卡
// 一律不动。
func cancelOrphanedAgrctlCard(b *blocks.SubagentStateBlock) bool {
	if b.Origin != agentruntime.SubagentOriginAgrctl || (b.Status != "running" && b.Status != "waiting") {
		return false
	}
	const canceled = "canceled"
	for _, origin := range []string{"", agentruntime.SubagentOriginAgrctl} {
		handlers.ApplyAgrctlInfo(b, agentruntime.SubagentInfo{Origin: origin, Status: canceled}, canceled)
		if b.Status == canceled {
			return true
		}
	}
	return false
}
