package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// ackRecordingSync 只关心「一次性说明被销掉了没有」，其余方法交给内嵌接口。
type ackRecordingSync struct {
	sync_svc.SyncSvc
	acked int
	err   error
}

func (r *ackRecordingSync) AcknowledgeBoardJoinNotice(context.Context) error {
	r.acked++
	return r.err
}

// TestSyncAcknowledgeBoardJoinNotice_DelegatesToTheEngine 看板首次并入同步组的
// 那条一次性说明由 Status.BoardJoinNoticePending 报出来；没有一个销掉它的绑定，
// 它就会在每次启动时再弹一次，永远弹下去——比不给这条说明更糟。
func TestSyncAcknowledgeBoardJoinNotice_DelegatesToTheEngine(t *testing.T) {
	rec := &ackRecordingSync{}
	sync_svc.SetDefault(rec)
	t.Cleanup(func() { sync_svc.SetDefault(nil) })

	require.NoError(t, (&App{}).SyncAcknowledgeBoardJoinNotice())
	assert.Equal(t, 1, rec.acked)
}

// TestSyncAcknowledgeBoardJoinNotice_SurfacesTheError 绑定层只做 parse →
// svc → return：写不下去时错误原样交给前端，不在这里吞掉。
func TestSyncAcknowledgeBoardJoinNotice_SurfacesTheError(t *testing.T) {
	rec := &ackRecordingSync{err: errors.New("database is locked")}
	sync_svc.SetDefault(rec)
	t.Cleanup(func() { sync_svc.SetDefault(nil) })

	assert.EqualError(t, (&App{}).SyncAcknowledgeBoardJoinNotice(), "database is locked")
}
