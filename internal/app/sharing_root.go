package app

import (
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// ListSessionsSharingRoot 汇总与本会话在同一执行设备、同一工作根上运行的其它会话(本桌面端
// 拥有、正在运行含后台活动),每项带会话标题 / Agent / 根名,以及与本会话写过同一文件的重叠
// (第一个文件 + 数量)。关联工具窗「同一份代码上在跑」一组的数据源。
func (a *App) ListSessionsSharingRoot(sessionID int64) ([]*chat_svc.SharingRootSession, error) {
	return chat_svc.Chat().ListSessionsSharingRoot(a.ctx, sessionID)
}
