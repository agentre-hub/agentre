package chat_import_svc

import (
	"context"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
)

// dbTxRunner 是 TxRunner 的真实现:一次导入的全部写入跑在同一个数据库事务里,
// 中途任何一步失败整条回滚 —— 不留半截会话(spec「导入与落库·原子性」)。
//
// txCtx 走 db.WithContextDB:repo 层一律 db.Ctx(ctx),这样同一批 repo 方法在事务
// 内外是同一份代码,不必为导入再开一套带 tx 参数的重载。
type dbTxRunner struct{}

func (dbTxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(db.WithContextDB(ctx, tx))
	})
}
