package sync_svc

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
)

// 本文件回答一个问题：**本机存着的这套同步状态是属于谁的。**
//
// 「谁」不是账号主键一个整数。游标、行上的版本号、出站队列里的基版本，全都是
// **某一套 server 的版本序列**里的坐标；换一套 server，同一个整数指向的就是另一段
// 历史。而自建部署里两套 server 的第一个用户都是 user_id = 1 —— 光比账号主键，
// 换 server 会被认成「还是同一个账号」，本机于是把 A 的坐标原样用在 B 上。
//
// 那条错法不会报错，只会静默漏数据：server 端「游标超出序列的头」的守卫只在返回
// 空页时才判（agentre-server 的 sync_svc.Pull），B 的序列只要比这个游标长，拉回来
// 的页就是非空的，守卫不触发，B 上版本号 ≤ 旧游标的对象一条也拉不到；游标只增，
// 它们再也不会被拉到。
//
// 所以身份是 (server 地址, 账号主键) 这一对，两者任一变了都按「换了一套 server」
// 处置 —— 那与 rebase.go 处置的失效是同一件事，因此走同一条恢复路径。

// identitySettingKey 是身份记录的存放位置。与游标同理，它是本机的同步状态而不是
// 账号级对象，住在本地 key-value 设置表里。
const identitySettingKey = "sync.server_identity"

// serverIdentity 是「本机这套同步状态属于哪一套 server 的哪个账号」。
type serverIdentity struct {
	ServerURL string `json:"server_url"`
	AccountID int64  `json:"account_id"`
}

// normalizeServerURL 把同一个地址的不同写法归一。判据要能容忍首尾空白与末尾斜杠：
// 它们不是换 server，当成换 server 会凭空触发一次全量重同步。
//
// 只做这两件事，不动大小写、不补默认端口：更激进的归一会把**真的**换了 server 的
// 两个地址判成同一个，而那个方向的错代价大得多（漏数据 vs 多拉一次全量）。
func normalizeServerURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func (s *service) loadServerIdentity(ctx context.Context) (serverIdentity, error) {
	row, err := app_setting_repo.AppSetting().Get(ctx, identitySettingKey)
	if err != nil {
		return serverIdentity{}, err
	}
	if row == nil || row.Value == "" {
		return serverIdentity{}, nil
	}
	var id serverIdentity
	if err := json.Unmarshal([]byte(row.Value), &id); err != nil {
		// 存坏了的身份当成「没记过」：代价是下一次真的换 server 时少了一道主动
		// 判据（server 的游标守卫仍在），比让同步停摆好。
		return serverIdentity{}, nil //nolint:nilerr // 见上
	}
	return id, nil
}

func (s *service) saveServerIdentity(ctx context.Context, id serverIdentity) error {
	body, err := json.Marshal(id)
	if err != nil {
		return err
	}
	return app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key:        identitySettingKey,
		Value:      string(body),
		Updatetime: s.now(),
	})
}

// ensureServerIdentity 在每一轮同步开工前核对身份，换了就先把本机重建到能与新
// server 对话的状态。
//
// 三种局面：
//
//   - 没有记录（老装机升级上来）：记下当前身份就走。**不**当成换 server —— 那会让
//     所有存量用户在升级后各做一次全量重同步，而真的在升级前换过 server 的那些，
//     server 的游标守卫仍然接得住。
//   - 记录与当前一致：什么都不做。
//   - 记录与当前不一致：走 rebase —— 忘掉上一套序列的版本号、拉一份全量快照、把
//     快照没送来的本地行重新入队上行。这与「server 不认识我们的游标」是同一种
//     失效（本地坐标属于另一段历史），没有理由用第二套恢复逻辑。
//
// 身份只在恢复真的做完之后才落库：中途失败却记下新身份，下一轮就会认为「已经是 B
// 了」，而本地那套 A 的版本号还留在原处 —— 一个没人会再来纠正的半吊子状态。
func (s *service) ensureServerIdentity(ctx context.Context, current serverIdentity) error {
	recorded, err := s.loadServerIdentity(ctx)
	if err != nil {
		return err
	}
	if recorded == current {
		return nil
	}
	if recorded == (serverIdentity{}) {
		return s.saveServerIdentity(ctx, current)
	}
	if err := s.rebaseOntoNewServer(ctx, recorded, current); err != nil {
		return err
	}
	return s.saveServerIdentity(ctx, current)
}

// rebaseOntoNewServer 把本机从上一套 server 的坐标系里摘出来。游标单独清一次：
// rebase 只管版本号与快照，而游标是「换了身份」这条路径上独有的一份陈旧坐标
// （loadCursor 的账号守卫拦得住换账号，拦不住换 server）。
func (s *service) rebaseOntoNewServer(ctx context.Context, from, to serverIdentity) error {
	logger.Ctx(ctx).Warn("sync_svc.ensureServerIdentity: the local sync state belongs to another server or account, rebuilding",
		zap.String("fromServer", from.ServerURL), zap.Int64("fromAccountId", from.AccountID),
		zap.String("toServer", to.ServerURL), zap.Int64("toAccountId", to.AccountID))
	if err := s.saveCursor(ctx, cursorState{AccountID: to.AccountID}); err != nil {
		return err
	}
	return s.rebase(ctx, to.AccountID)
}
