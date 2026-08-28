package project_svc_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo/mock_server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// recordingSync 记下域服务交出来的每一条改动，并按脚本失败。
type recordingSync struct {
	sync_svc.SyncSvc
	changes []sync_svc.LocalChange
}

func (r *recordingSync) NotifyLocalChange(_ context.Context, ch sync_svc.LocalChange) {
	r.changes = append(r.changes, ch)
}

func registerRecordingSync(t *testing.T) *recordingSync {
	t.Helper()
	rec := &recordingSync{}
	sync_svc.SetDefault(rec)
	t.Cleanup(func() { sync_svc.SetDefault(nil) })
	return rec
}

// TestProjectCreate_NotifiesSyncOnce R3：编辑当场触发上行——项目落库成功后，同步层
// 拿到这一行的同步标识。
func TestProjectCreate_NotifiesSyncOnce(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	rec := registerRecordingSync(t)
	tmp := t.TempDir()
	mp.EXPECT().FindByName(ctx, int64(0), "Agentre").Return(nil, nil)
	mp.EXPECT().NextSortOrder(ctx, int64(0)).Return(1, nil)
	mp.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p *project_entity.Project) error {
		p.ID = 42
		p.SyncID = "proj-sync-1"
		return nil
	})

	_, err := svc.Create(ctx, &project_svc.CreateProjectRequest{Name: "Agentre", Path: tmp})
	require.NoError(t, err)

	require.Len(t, rec.changes, 1)
	assert.Equal(t, syncwire.KindProject, rec.changes[0].Kind)
	assert.Equal(t, sync_svc.OpCreate, rec.changes[0].Op)
	assert.Equal(t, "proj-sync-1", rec.changes[0].Meta.SyncID)
	assert.Equal(t, int64(42), rec.changes[0].LocalID)
}

// TestProjectCreate_GivenServerUnreachable_StillSucceeds R8：同步不阻塞本地操作。
// server 不可达时，本地新建照常返回成功、照常落库，改动留在出站队列里等联网补齐
// （R7），一次也不回滚。
func TestProjectCreate_GivenServerUnreachable_StillSucceeds(t *testing.T) {
	ctx, mp, _, _, svc := setupProjectSvc(t)
	tmp := t.TempDir()

	ctrl := gomock.NewController(t)
	stateRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
	stateRepo.EXPECT().Get(gomock.Any()).Return(&server_state_entity.ServerState{
		ID: 1, ServerUserID: 7, DeviceID: 3, KeychainAccount: "k",
	}, nil).AnyTimes()
	server_state_repo.RegisterServerState(stateRepo)

	queue := registerSyncQueues(t)
	sync_svc.SetDefault(sync_svc.New(unreachableTransport{}))
	t.Cleanup(func() { sync_svc.SetDefault(nil) })

	var created *project_entity.Project
	mp.EXPECT().FindByName(ctx, int64(0), "Agentre").Return(nil, nil)
	mp.EXPECT().NextSortOrder(ctx, int64(0)).Return(1, nil)
	mp.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p *project_entity.Project) error {
		p.ID = 42
		p.SyncID = "proj-sync-1"
		created = p
		return nil
	})

	got, err := svc.Create(ctx, &project_svc.CreateProjectRequest{Name: "Agentre", Path: tmp})

	require.NoError(t, err, "server 不可达不该让本地新建失败")
	require.NotNil(t, got)
	assert.Equal(t, int64(42), got.ID)
	assert.Same(t, created, got, "落库的就是返回的那一行，没有任何回滚")
	assert.Equal(t, 1, queue.createCount(), "改动留在出站队列里，联网后补齐")
}

// unreachableTransport 模拟 server 不可达。
type unreachableTransport struct{}

func (unreachableTransport) SyncPush(context.Context, []syncwire.PushItem) ([]syncwire.PushResult, error) {
	return nil, errors.New("dial tcp: connection refused")
}

func (unreachableTransport) SyncPull(context.Context, int64, int) (*syncwire.PullPage, error) {
	return nil, errors.New("dial tcp: connection refused")
}

func (unreachableTransport) ReportLocalPaths(context.Context, []syncwire.LocalPathReportItem) error {
	return errors.New("dial tcp: connection refused")
}

func (unreachableTransport) PutAvatar(context.Context, string, string, string) error {
	return errors.New("dial tcp: connection refused")
}

func (unreachableTransport) GetAvatar(context.Context, string) (string, string, error) {
	return "", "", errors.New("dial tcp: connection refused")
}

// ── 同步侧仓储替身 ─────────────────────────────────────────────────────────
//
// 「编辑当场上行」的推送在后台跑，与本测试的断言并发，因此这里用带锁的内存替身
// 而不是 gomock：期望链在并发下既不好写也不稳。

type countingOutboundQueue struct {
	mu      sync.Mutex
	created int
	rows    []*syncqueue_entity.OutboundQueueItem
	nextID  int64
}

func (q *countingOutboundQueue) createCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.created
}

func (q *countingOutboundQueue) Create(_ context.Context, row *syncqueue_entity.OutboundQueueItem) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.created++
	q.nextID++
	row.ID = q.nextID
	q.rows = append(q.rows, row)
	return nil
}

func (q *countingOutboundQueue) ListByAccount(context.Context, int64) ([]*syncqueue_entity.OutboundQueueItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*syncqueue_entity.OutboundQueueItem, len(q.rows))
	copy(out, q.rows)
	return out, nil
}

func (q *countingOutboundQueue) Delete(context.Context, int64) error       { return nil }
func (q *countingOutboundQueue) DeleteMany(context.Context, []int64) error { return nil }

type noopInboundQueue struct{}

func (noopInboundQueue) Create(context.Context, *syncqueue_entity.InboundQueueItem) error { return nil }
func (noopInboundQueue) ListByAccount(context.Context, int64) ([]*syncqueue_entity.InboundQueueItem, error) {
	return nil, nil
}
func (noopInboundQueue) Delete(context.Context, int64) error { return nil }

type noopLostChange struct{}

func (noopLostChange) Create(context.Context, *syncqueue_entity.LostChange) error { return nil }
func (noopLostChange) ListByAccount(context.Context, int64) ([]*syncqueue_entity.LostChange, error) {
	return nil, nil
}
func (noopLostChange) Delete(context.Context, int64) error { return nil }

type emptySyncState struct{}

func (emptySyncState) FindLocalID(context.Context, string, string) (int64, error) { return 0, nil }
func (emptySyncState) FindVersion(context.Context, string, string) (int64, bool, bool, error) {
	return 0, false, false, nil
}
func (emptySyncState) FindRow(context.Context, string, string, any) (bool, error) { return false, nil }
func (emptySyncState) ClaimUnowned(context.Context, string, int64) ([]syncstate_repo.ClaimedRow, error) {
	return nil, nil
}
func (emptySyncState) SaveMeta(context.Context, string, string, syncmeta_entity.SyncMeta) error {
	return nil
}
func (emptySyncState) ResetVersions(context.Context, string, int64) error { return nil }
func (emptySyncState) ListUnversioned(context.Context, string, int64) ([]string, error) {
	return nil, nil
}

// memorySettings 是本地 key-value 设置表的替身（下行游标住在这里）。
type memorySettings struct {
	mu   sync.Mutex
	rows map[string]*app_setting_entity.AppSetting
}

func (m *memorySettings) Get(_ context.Context, key string) (*app_setting_entity.AppSetting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows[key], nil
}

func (m *memorySettings) Set(_ context.Context, s *app_setting_entity.AppSetting) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[s.Key] = s
	return nil
}

func (m *memorySettings) Delete(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[key]; !ok {
		return 0, nil
	}
	delete(m.rows, key)
	return 1, nil
}

func (m *memorySettings) List(context.Context) ([]*app_setting_entity.AppSetting, error) {
	return nil, nil
}

func registerSyncQueues(t *testing.T) *countingOutboundQueue {
	t.Helper()
	queue := &countingOutboundQueue{}
	app_setting_repo.RegisterAppSetting(&memorySettings{rows: map[string]*app_setting_entity.AppSetting{}})
	syncqueue_repo.RegisterOutboundQueue(queue)
	syncqueue_repo.RegisterInboundQueue(noopInboundQueue{})
	syncqueue_repo.RegisterLostChange(noopLostChange{})
	syncstate_repo.RegisterSyncState(emptySyncState{})
	return queue
}
