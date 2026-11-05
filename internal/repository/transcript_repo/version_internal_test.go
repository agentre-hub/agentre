package transcript_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
)

// withVersionClock 把版本签发器的时钟换成 now,并清掉签发器记住的上一次版本;
// 返回一个计数器,记录签发器读了几次时钟(= 签发了几个版本)。
func withVersionClock(t *testing.T, now func() int64) *int {
	t.Helper()
	calls := 0
	prevNow := versionNow
	versionNow = func() int64 { calls++; return now() }
	versions.mu.Lock()
	prevLast := versions.last
	versions.last = 0
	versions.mu.Unlock()
	t.Cleanup(func() {
		versionNow = prevNow
		versions.mu.Lock()
		versions.last = prevLast
		versions.mu.Unlock()
	})
	return &calls
}

// 游标比较的是版本大小,所以同一毫秒里的两次写入、或者系统时钟被往回拨之后的写入,
// 都必须拿到比上一次更大的版本 —— 否则后一次写入与前端已持有的游标相等(或更小),
// 增量读把它当成「没变过」。
func TestNextVersion_StrictlyIncreasing(t *testing.T) {
	clock := int64(1000)
	withVersionClock(t, func() int64 { return clock })

	assert.Equal(t, int64(1000), nextVersion())
	assert.Equal(t, int64(1001), nextVersion(), "同一毫秒里的第二次写入")
	clock = 500
	assert.Equal(t, int64(1002), nextVersion(), "时钟往回拨")
	clock = 5000
	assert.Equal(t, int64(5000), nextVersion(), "时钟走到前面以后跟上时钟")
}

// 每一条写入路径都只在拿到写锁(BEGIN 成功)之后才签发版本。SQLite 的写事务以
// _txlock=immediate 串行,版本在锁内签发,提交顺序就等于版本顺序:前端读到版本 v,
// 之后提交的写入版本一定大于 v。BEGIN 失败的写入一个版本都不该签。
func TestVersionIsIssuedOnlyAfterTheWriteLockIsHeld(t *testing.T) {
	r := &messageRepo{}
	msg := func() *transcript_entity.Message {
		return &transcript_entity.Message{ID: 42, SessionID: 3, Role: "assistant", BlocksJSON: "[]", Seq: 1}
	}
	writers := map[string]func(ctx context.Context) error{
		"Create":           func(ctx context.Context) error { return r.Create(ctx, msg()) },
		"CreateAtNextSeq":  func(ctx context.Context) error { return r.CreateAtNextSeq(ctx, msg()) },
		"Update":           func(ctx context.Context) error { return r.Update(ctx, msg()) },
		"CheckpointBlocks": func(ctx context.Context) error { return r.CheckpointBlocks(ctx, msg(), "[]") },
		"UpsertBlocks":     func(ctx context.Context) error { return r.UpsertBlocks(ctx, msg(), nil, -1) },
		"UpdateUsage": func(ctx context.Context) error {
			return r.UpdateUsage(ctx, 42, MessageUsage{PromptTokens: 1})
		},
		"UpdateErrorText": func(ctx context.Context) error { return r.UpdateErrorText(ctx, 42, "boom") },
		"AppendSubagentChildren": func(ctx context.Context) error {
			return r.AppendSubagentChildren(ctx, 3, "tu1", `[{"type":"text","data":{"text":"x"}}]`, []string{"c1"})
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			calls := withVersionClock(t, func() int64 { return 1000 })
			ctx, _, mock := testutils.Database(t)
			mock.ExpectBegin().WillReturnError(assert.AnError)

			require.Error(t, write(ctx))
			assert.Zero(t, *calls, "BEGIN 失败 = 没拿到写锁,不该签发版本")
		})
	}
}
