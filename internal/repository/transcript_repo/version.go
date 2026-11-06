package transcript_repo

import (
	"context"
	"sync"
	"time"

	"github.com/cago-frame/cago/database/db"
)

// 消息版本:chat_messages.updatetime。
//
// 它是增量读的游标(LoadSession / LoadSessionBlocksByType 的 SinceVersion):前端带上
// 手上最大的版本,后端只交回版本更新的消息。游标要成立,版本必须满足两条:
//
//  1. 每一次改动消息正文或元数据的写入都推进它 —— 包括轮次收尾很久以后才改写旧消息
//     块行的那几条路(后台任务翻转 / 进度 / 子块追加),它们以前不碰 updatetime;
//  2. 提交顺序与版本顺序一致。写事务以 _txlock=immediate 打开、彼此串行,版本在
//     事务**内**(拿到写锁之后)签发,于是任何读者读到版本 v 之后再提交的写入,
//     版本一定大于 v。先签版本再排队等锁,则可能带着小版本晚提交,落在游标后面。
//
// 删除不推版本:被删的消息从 ListVersions 里消失,读侧据此告诉前端。

// versionNow 是版本签发器读的时钟(毫秒)。测试替换它来制造同一毫秒 / 时钟回拨。
var versionNow = func() int64 { return time.Now().UnixMilli() }

// versions 记着本进程签发过的最大版本。
var versions struct {
	mu   sync.Mutex
	last int64
}

// nextVersion 签发一个新的消息版本:取毫秒时钟,但绝不小于等于上一次签发的版本。
func nextVersion() int64 {
	versions.mu.Lock()
	defer versions.mu.Unlock()
	v := versionNow()
	if v <= versions.last {
		v = versions.last + 1
	}
	versions.last = v
	return v
}

// touchMessage 在调用方的写事务里把一条消息推进到新版本。块行被单独改写(而不是经
// 整行 Save)时用它让宿主消息的版本跟上。
func touchMessage(txCtx context.Context, messageID int64) error {
	return db.Ctx(txCtx).Exec(
		"UPDATE `chat_messages` SET `updatetime`=? WHERE id = ?",
		nextVersion(), messageID,
	).Error
}
