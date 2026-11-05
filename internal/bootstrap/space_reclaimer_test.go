package bootstrap

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newVacuumTestDB 建一个真实临时 SQLite(本包允许),autoVacuum 取 "0"/"2",
// 并制造出一批空闲页:写入大块 blob 后全部删除。
func newVacuumTestDB(t *testing.T, autoVacuum string) *gorm.DB {
	t.Helper()
	g, err := gorm.Open(sqlite.Open(sqliteDSN(filepath.Join(t.TempDir(), "v.db"))), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := g.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1) // pragma auto_vacuum 须在建表前、同一连接上
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, g.Exec("PRAGMA auto_vacuum = "+autoVacuum).Error)
	require.NoError(t, g.Exec("CREATE TABLE blobs (id INTEGER PRIMARY KEY, b BLOB)").Error)
	for i := 0; i < 200; i++ {
		require.NoError(t, g.Exec("INSERT INTO blobs (b) VALUES (zeroblob(8192))").Error)
	}
	require.NoError(t, g.Exec("DELETE FROM blobs").Error)
	return g
}

func freelist(t *testing.T, g *gorm.DB) int {
	t.Helper()
	var n int
	require.NoError(t, g.Raw("PRAGMA freelist_count").Scan(&n).Error)
	return n
}

func TestSpaceReclaimerDrainsFreelistInBatches(t *testing.T) {
	g := newVacuumTestDB(t, "2")
	before := freelist(t, g)
	require.Greater(t, before, 100)

	r := newSpaceReclaimer(g, spaceReclaimerOptions{BatchPages: 20, MaxBatches: 2})
	batches, err := r.reclaimOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, batches, "budget caps the batches of one run")
	require.Equal(t, before-40, freelist(t, g), "each batch releases at most BatchPages")

	r = newSpaceReclaimer(g, spaceReclaimerOptions{BatchPages: 20, MaxBatches: 1000})
	_, err = r.reclaimOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, freelist(t, g), "stops at an empty freelist")
}

func TestSpaceReclaimerSkipsWhenNotIncremental(t *testing.T) {
	g := newVacuumTestDB(t, "0")
	before := freelist(t, g)
	require.Greater(t, before, 0)

	r := newSpaceReclaimer(g, spaceReclaimerOptions{BatchPages: 20, MaxBatches: 100})
	batches, err := r.reclaimOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, batches)
	require.Equal(t, before, freelist(t, g))
}

func TestSpaceReclaimerStopsOnCanceledContext(t *testing.T) {
	g := newVacuumTestDB(t, "2")
	before := freelist(t, g)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := newSpaceReclaimer(g, spaceReclaimerOptions{BatchPages: 20, MaxBatches: 100})
	batches, _ := r.reclaimOnce(ctx)
	require.Zero(t, batches)
	require.Equal(t, before, freelist(t, g))
}

func TestSpaceReclaimerTriggerCoalescesAndStopJoins(t *testing.T) {
	g := newVacuumTestDB(t, "2")
	r := newSpaceReclaimer(g, spaceReclaimerOptions{BatchPages: 50, MaxBatches: 1000, Interval: time.Hour})
	r.Start(context.Background())
	for i := 0; i < 100; i++ { // a burst of deletes must never block
		r.Trigger()
	}
	require.Eventually(t, func() bool { return freelist(t, g) == 0 }, 5*time.Second, 20*time.Millisecond)
	stopped := make(chan struct{})
	go func() { r.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not join the worker goroutine")
	}
	r.Stop()    // idempotent
	r.Trigger() // safe after stop
}
