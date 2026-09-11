package project_svc

import (
	"context"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
)

// TxRunner 把一次合并的全部读写收进一个原子单元：要么整体落库，要么一步都不留
// （R11a 合并、spec 2026-09-11 要求 4）。抽成端口是因为服务单测不连库——真实现走
// db.Ctx(ctx).Transaction，单测顶一个记录提交 / 回滚的假实现（同 chat_import_svc）。
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// dbTxRunner 是 TxRunner 的真实现。回调拿到的 ctx 经 db.WithContextDB 带着 tx：repo
// 层一律 db.Ctx(ctx)，同一批 repo 方法在事务内外是同一份代码。
type dbTxRunner struct{}

func (dbTxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(db.WithContextDB(ctx, tx))
	})
}
