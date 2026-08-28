package sync_svc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo/mock_server_state_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
)

// ── 测试替身 ────────────────────────────────────────────────────────────────

// fakeTransport 是同步的网络出入口替身：记下每一次上行、按脚本回应。
type fakeTransport struct {
	pushed   [][]syncwire.PushItem
	results  func(items []syncwire.PushItem) ([]syncwire.PushResult, error)
	pages    []*syncwire.PullPage
	pulledAt []int64
	pullErr  error
	// pullErrs 按调用次序消耗一次：第 i 次 SyncPull 取 pullErrs[i]，非 nil 就返回它。
	// 与 pullErr（每一次都返回）互补——server 只在第一次拒绝，之后照常应答。
	pullErrs []error

	// 本机路径上报（R16）与头像（R16a）的替身状态。
	localPathReports [][]syncwire.LocalPathReportItem
	localPathErr     error
	avatarsPut       []avatarPutCall
	avatarsGet       map[string]avatarGetResult
	avatarGetErr     error
}

type avatarPutCall struct {
	Hash        string
	ContentType string
	Content     string
}

type avatarGetResult struct {
	Content     string
	ContentType string
}

func (f *fakeTransport) SyncPush(_ context.Context, items []syncwire.PushItem) ([]syncwire.PushResult, error) {
	cp := make([]syncwire.PushItem, len(items))
	copy(cp, items)
	f.pushed = append(f.pushed, cp)
	if f.results != nil {
		return f.results(items)
	}
	out := make([]syncwire.PushResult, 0, len(items))
	for i, it := range items {
		out = append(out, syncwire.PushResult{
			SyncID: it.SyncID, Kind: it.Kind,
			Version: int64(100 + i), Status: syncwire.PushStatusAccepted,
		})
	}
	return out, nil
}

func (f *fakeTransport) SyncPull(_ context.Context, cursor int64, _ int) (*syncwire.PullPage, error) {
	f.pulledAt = append(f.pulledAt, cursor)
	if len(f.pullErrs) > 0 {
		err := f.pullErrs[0]
		f.pullErrs = f.pullErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	if f.pullErr != nil {
		return nil, f.pullErr
	}
	if len(f.pages) == 0 {
		return &syncwire.PullPage{NextCursor: cursor}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *fakeTransport) ReportLocalPaths(_ context.Context, items []syncwire.LocalPathReportItem) error {
	if f.localPathErr != nil {
		return f.localPathErr
	}
	cp := make([]syncwire.LocalPathReportItem, len(items))
	copy(cp, items)
	f.localPathReports = append(f.localPathReports, cp)
	return nil
}

func (f *fakeTransport) PutAvatar(_ context.Context, contentHash, contentType, content string) error {
	f.avatarsPut = append(f.avatarsPut, avatarPutCall{Hash: contentHash, ContentType: contentType, Content: content})
	return nil
}

func (f *fakeTransport) GetAvatar(_ context.Context, contentHash string) (string, string, error) {
	if f.avatarGetErr != nil {
		return "", "", f.avatarGetErr
	}
	if r, ok := f.avatarsGet[contentHash]; ok {
		return r.Content, r.ContentType, nil
	}
	return "", "", errors.New("fakeTransport: avatar not found")
}

// fakeAdapter 是一种对象类型的替身：本地行存在内存里，引用按 needRef 声明。
type fakeAdapter struct {
	name    string
	rows    map[string]string // syncID → 载荷里的 name 字段
	needRef ref
	applied []string
	removed []string
	applyFn func(in *inbound) error
	loadFn  func(syncID string) (*outbound, error)
	deps    []relatedRow
	kids    []relatedRow
	// naturalKey 模拟「账号内自然键上现在站着哪个同步标识」（R4b），键是
	// 「项目同步标识|指纹」。
	naturalKey map[string]string
}

func newFakeAdapter(name string) *fakeAdapter {
	return &fakeAdapter{name: name, rows: map[string]string{}, naturalKey: map[string]string{}}
}

func (f *fakeAdapter) syncIDAtNaturalKey(_ context.Context, in *inbound) (string, error) {
	return f.naturalKey[in.ProjectSyncID+"|"+in.AgentredFingerprint], nil
}

func (f *fakeAdapter) kind() string { return f.name }

func (f *fakeAdapter) load(_ context.Context, syncID string) (*outbound, error) {
	if f.loadFn != nil {
		return f.loadFn(syncID)
	}
	name, ok := f.rows[syncID]
	if !ok {
		return nil, nil
	}
	payload, _ := json.Marshal(map[string]any{"name": name})
	return &outbound{SyncID: syncID, UpdatedAt: 1, Payload: payload}, nil
}

func (f *fakeAdapter) refs(*inbound) []ref { return []ref{f.needRef} }

func (f *fakeAdapter) apply(_ context.Context, in *inbound, _ map[string]int64) error {
	if f.applyFn != nil {
		if err := f.applyFn(in); err != nil {
			return err
		}
	}
	var p struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(in.Payload, &p)
	f.rows[in.SyncID] = p.Name
	f.applied = append(f.applied, in.SyncID+"@"+p.Name)
	return nil
}

func (f *fakeAdapter) remove(_ context.Context, in *inbound) error {
	delete(f.rows, in.SyncID)
	f.removed = append(f.removed, in.SyncID)
	return nil
}

func (f *fakeAdapter) dependents(context.Context, string) ([]relatedRow, error) {
	return f.deps, nil
}

func (f *fakeAdapter) children(context.Context, string) ([]relatedRow, error) {
	return f.kids, nil
}

// harness 把七个仓储替身与引擎装配起来。
type harness struct {
	svc            *service
	transport      *fakeTransport
	adapter        *fakeAdapter
	outbound       *fakeOutboundQueue
	inbound        *fakeInboundQueue
	lost           *fakeLostChange
	state          *fakeSyncState
	nowMs          int64
	backgroundRuns int
}

func newHarness(t *testing.T, loggedIn bool) *harness {
	t.Helper()
	ctrl := gomock.NewController(t)

	stateRepo := mock_server_state_repo.NewMockServerStateRepo(ctrl)
	row := &server_state_entity.ServerState{ID: 1}
	if loggedIn {
		row = &server_state_entity.ServerState{
			ID: 1, ServerUserID: 7, DeviceID: 3, KeychainAccount: "k",
		}
	}
	stateRepo.EXPECT().Get(gomock.Any()).Return(row, nil).AnyTimes()
	server_state_repo.RegisterServerState(stateRepo)

	app_setting_repo.RegisterAppSetting(newFakeSettings())

	h := &harness{
		transport: &fakeTransport{},
		adapter:   newFakeAdapter("project"),
		outbound:  &fakeOutboundQueue{},
		inbound:   &fakeInboundQueue{},
		lost:      &fakeLostChange{},
		state:     newFakeSyncState(),
		nowMs:     1_700_000_000_000,
	}
	syncqueue_repo.RegisterOutboundQueue(h.outbound)
	syncqueue_repo.RegisterInboundQueue(h.inbound)
	syncqueue_repo.RegisterLostChange(h.lost)
	syncstate_repo.RegisterSyncState(h.state)

	h.svc = &service{
		adapters:  map[string]adapter{h.adapter.kind(): h.adapter},
		now:       func() int64 { return h.nowMs },
		transport: h.transport,
	}
	h.svc.background = func(f func()) { h.backgroundRuns++; f() }
	return h
}

// ── ① 编辑当场上行 ──────────────────────────────────────────────────────────

// TestNotifyLocalChange_PushesImmediately R3：上行由编辑当场触发——一次本地改动
// 落库后，它当场就被推上去，不等 30 秒轮询。
func TestNotifyLocalChange_PushesImmediately(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "Alpha"

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", LocalID: 1, Op: OpCreate,
		Meta: syncmeta_entity.SyncMeta{SyncID: "p-1"},
	})

	require.Len(t, h.transport.pushed, 1, "编辑当场就有一次上行")
	require.Len(t, h.transport.pushed[0], 1)
	assert.Equal(t, "p-1", h.transport.pushed[0][0].SyncID)
	assert.Empty(t, h.outbound.rows, "上行成功后出队")
	assert.Equal(t, int64(100), h.state.meta["project:p-1"].SyncVersion,
		"server 分配的版本写回本行，它是下一次上行的基版本")
}

// TestNotifyLocalChange_GivenLoggedOut_DoesNothing R12：未登录时不入队、不发请求。
func TestNotifyLocalChange_GivenLoggedOut_DoesNothing(t *testing.T) {
	h := newHarness(t, false)
	h.adapter.rows["p-1"] = "Alpha"

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpCreate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1"},
	})

	assert.Empty(t, h.outbound.rows)
	assert.Empty(t, h.transport.pushed)
}

// TestNotifyRuntimeClaim_GivenLoggedOut_QueuesUntilAuthentication R13：认领不依赖
// 登录态；它先写同一条出站队列，认证后才归属账号并上传。
func TestNotifyRuntimeClaim_GivenLoggedOut_QueuesUntilAuthentication(t *testing.T) {
	h := newHarness(t, false)
	h.svc.NotifyRuntimeClaim(context.Background(), LocalChange{
		Kind: "project", LocalID: 1, Op: OpDelete,
		Meta: syncmeta_entity.SyncMeta{SyncID: "relative-old"},
	})
	require.Len(t, h.outbound.rows, 1)
	assert.Equal(t, int64(0), h.outbound.rows[0].SyncAccountID)
	assert.Empty(t, h.transport.pushed)

	require.NoError(t, h.svc.claimAnonymousQueue(context.Background(), 7))
	require.Len(t, h.outbound.rows, 1)
	assert.Equal(t, int64(7), h.outbound.rows[0].SyncAccountID)
	assert.Equal(t, "relative-old", h.outbound.rows[0].EntitySyncID)
}

// TestNotifyLocalChange_GivenRowOfAnotherAccount_DoesNotUpload R13a：本地行记录的
// 账号与当前登录账号不同时不上行、也不携带原同步标识。
func TestNotifyLocalChange_GivenRowOfAnotherAccount_DoesNotUpload(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "Alpha"

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpUpdate,
		Meta: syncmeta_entity.SyncMeta{SyncID: "p-1", SyncAccountID: 999},
	})

	assert.Empty(t, h.outbound.rows)
	assert.Empty(t, h.transport.pushed)
}

// ── ⑧ 同步不阻塞本地写 ─────────────────────────────────────────────────────

// TestNotifyLocalChange_GivenServerUnreachable_KeepsChangeQueued R7/R8：server 不
// 可达时本地改动照常成立，改动留在队列里等联网，且这条路径不返回任何错误。
func TestNotifyLocalChange_GivenServerUnreachable_KeepsChangeQueued(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "Alpha"
	h.transport.results = func([]syncwire.PushItem) ([]syncwire.PushResult, error) {
		return nil, errors.New("dial tcp: connection refused")
	}

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpCreate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1"},
	})

	require.Len(t, h.outbound.rows, 1, "推不上去就留在队列里")
	assert.Equal(t, "p-1", h.outbound.rows[0].EntitySyncID)
	assert.Empty(t, h.state.meta, "没有任何本地状态因为同步失败被改写")
}

// ── ⑥ 离线入队恢复后按序补齐且不重复应用 ───────────────────────────────────

// TestFlush_GivenOfflineBacklog_UploadsInOrderOnce R7：断连期间的增删改入队，恢复
// 后按入队顺序一次上行；同一行的多次改动折叠成一条，不重复投递。
func TestFlush_GivenOfflineBacklog_UploadsInOrderOnce(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "Alpha"
	h.adapter.rows["p-2"] = "Beta"
	h.transport.results = func([]syncwire.PushItem) ([]syncwire.PushResult, error) {
		return nil, errors.New("offline")
	}
	ctx := context.Background()
	for _, ch := range []LocalChange{
		{Kind: "project", Op: OpCreate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1"}},
		{Kind: "project", Op: OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1"}},
		{Kind: "project", Op: OpCreate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-2"}},
	} {
		h.svc.NotifyLocalChange(ctx, ch)
	}
	require.Len(t, h.outbound.rows, 3, "离线期间三条改动都入了队")

	h.transport.results = nil // 恢复连通
	require.NoError(t, h.svc.SyncOnce(ctx))

	last := h.transport.pushed[len(h.transport.pushed)-1]
	require.Len(t, last, 2, "同一行的两次改动折叠成一条，不重复投递")
	assert.Equal(t, "p-1", last[0].SyncID, "按入队顺序上行")
	assert.Equal(t, "p-2", last[1].SyncID)
	assert.Empty(t, h.outbound.rows, "补齐后队列清空")
}

// ── ③ 并发改同一行：两种到达顺序都收敛 ─────────────────────────────────────

// TestApplyInbound_ConvergesUnderBothArrivalOrders R4：两端并发改同一行时，两种到达
// 顺序下最终版本相同——版本号较大者胜，先到的更大版本不被后到的旧版本盖掉。
func TestApplyInbound_ConvergesUnderBothArrivalOrders(t *testing.T) {
	older := syncwire.PullItem{
		Kind: "project", SyncID: "p-1", Version: 5, OriginFingerprint: "fp-1",
		Payload: []byte(`{"name":"from-A"}`),
	}
	newer := syncwire.PullItem{
		Kind: "project", SyncID: "p-1", Version: 6, OriginFingerprint: "fp-2",
		Payload: []byte(`{"name":"from-B"}`),
	}

	for _, tc := range []struct {
		name  string
		order []syncwire.PullItem
	}{
		{"旧的先到", []syncwire.PullItem{older, newer}},
		{"新的先到", []syncwire.PullItem{newer, older}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, true)
			h.transport.pages = []*syncwire.PullPage{{Items: tc.order, NextCursor: 6}}

			require.NoError(t, h.svc.SyncOnce(context.Background()))

			assert.Equal(t, "from-B", h.adapter.rows["p-1"], "两种到达顺序收敛到同一版本")
			assert.Equal(t, int64(6), h.state.meta["project:p-1"].SyncVersion)
		})
	}
}

// TestPull_GivenSomethingLanded_AnnouncesTheKinds 下行落地后要有人喊一声。
//
// 界面没有别的办法知道「另一台设备刚建了个项目」：项目树没有任何推送通道，此前
// 全靠项目页那条 1 秒轮询兜着，轮询随单一会话索引一起删掉之后，同步过来的项目
// 会一直不出现，直到用户碰巧做了点别的事。
func TestPull_GivenSomethingLanded_AnnouncesTheKinds(t *testing.T) {
	h := newHarness(t, true)
	var announced [][]string
	h.svc.SetEmitter(func(kinds []string) {
		announced = append(announced, kinds)
	})
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{{
			Kind: "project", SyncID: "p-1", Version: 5, Payload: []byte(`{"name":"A"}`),
		}},
		NextCursor: 5,
	}}

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	require.Len(t, announced, 1, "落地了就喊一声")
	assert.Equal(t, []string{"project"}, announced[0])
}

// TestPull_GivenNothingLanded_StaysQuiet 空转的那些轮次不喊 —— 30 秒一次的轮询
// 如果每轮都喊，界面就会每 30 秒无谓地重拉一遍项目树。
func TestPull_GivenNothingLanded_StaysQuiet(t *testing.T) {
	h := newHarness(t, true)
	var announced [][]string
	h.svc.SetEmitter(func(kinds []string) {
		announced = append(announced, kinds)
	})
	item := syncwire.PullItem{
		Kind: "project", SyncID: "p-1", Version: 5, Payload: []byte(`{"name":"A"}`),
	}
	h.transport.pages = []*syncwire.PullPage{
		{Items: []syncwire.PullItem{item}, NextCursor: 5},
		{Items: []syncwire.PullItem{item}, NextCursor: 5},
	}
	ctx := context.Background()

	require.NoError(t, h.svc.SyncOnce(ctx))
	require.NoError(t, h.svc.SyncOnce(ctx))

	// 第二轮是重复投递，被版本守卫挡下——它没有改变本机任何东西，也就没什么可喊的。
	assert.Len(t, announced, 1, "只有真落地的那一轮喊")
}

// TestApplyInbound_GivenDuplicateDelivery_AppliesOnce R7：重复投递只应用一次。
func TestApplyInbound_GivenDuplicateDelivery_AppliesOnce(t *testing.T) {
	h := newHarness(t, true)
	item := syncwire.PullItem{
		Kind: "project", SyncID: "p-1", Version: 5, Payload: []byte(`{"name":"A"}`),
	}
	h.transport.pages = []*syncwire.PullPage{
		{Items: []syncwire.PullItem{item}, NextCursor: 5},
		{Items: []syncwire.PullItem{item}, NextCursor: 5},
	}
	ctx := context.Background()

	require.NoError(t, h.svc.SyncOnce(ctx))
	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Len(t, h.adapter.applied, 1, "同一条下行项落地一次")
}

// ── ④ 删除不复活 ───────────────────────────────────────────────────────────

// TestApplyInbound_GivenTombstone_RemovesLocalRow R6：A 端删除、本端持旧副本时，
// 墓碑到达即删除本地行。
func TestApplyInbound_GivenTombstone_RemovesLocalRow(t *testing.T) {
	h := newHarness(t, true)
	// 本机已经有这一行（早先某一轮下行落的，因此也有它的同步元数据）。
	h.adapter.rows["p-1"] = "Alpha"
	h.state.meta["project:p-1"] = syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 4}
	h.transport.pages = []*syncwire.PullPage{{
		Items:      []syncwire.PullItem{{Kind: "project", SyncID: "p-1", Version: 9, DeletedAt: 1700}},
		NextCursor: 9,
	}}

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	assert.NotContains(t, h.adapter.rows, "p-1")
	assert.Equal(t, []string{"p-1"}, h.adapter.removed)
	assert.Positive(t, h.state.meta["project:p-1"].SyncDeletedAt, "本地留下墓碑标记")
}

// TestApplyInbound_GivenTombstoneForUnknownRow_DoesNothing 本机从来没有这一行时，
// 墓碑既不需要删什么，也不该为了等它的引用目标挂进暂缓队列白等 30 天。
func TestApplyInbound_GivenTombstoneForUnknownRow_DoesNothing(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.needRef = ref{Kind: "project", SyncID: "parent-1"} // 本机解析不出
	h.transport.pages = []*syncwire.PullPage{{
		Items:      []syncwire.PullItem{{Kind: "project", SyncID: "ghost", Version: 9, DeletedAt: 1700}},
		NextCursor: 9,
	}}

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	assert.Empty(t, h.adapter.removed)
	assert.Empty(t, h.inbound.rows, "不为一条无处可删的墓碑排队")
}

// TestFlush_GivenPushRejectedAsDeleted_DoesNotResurrect R6/R5a：持旧副本的一端把它
// 推上去时被拒（server 上已是墓碑），本端跟着落墓碑而不是把它复活；那一版内容进
// 「没能同步的改动」，界面据此给「按这份内容新建」。
func TestFlush_GivenPushRejectedAsDeleted_DoesNotResurrect(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "Alpha"
	h.transport.results = func(items []syncwire.PushItem) ([]syncwire.PushResult, error) {
		return []syncwire.PushResult{{
			SyncID: items[0].SyncID, Kind: items[0].Kind, Version: 9,
			Status: syncwire.PushStatusRejected, Reason: syncwire.PushRejectReasonDeleted,
		}}, nil
	}

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpUpdate,
		Meta: syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 4},
	})

	assert.NotContains(t, h.adapter.rows, "p-1", "被拒的这一条不复活对象")
	require.Len(t, h.lost.rows, 1)
	assert.Equal(t, syncqueue_entity.ReasonRejected, h.lost.rows[0].Reason)
	assert.JSONEq(t, `{"name":"Alpha"}`, h.lost.rows[0].PayloadJSON, "内容留住，供「按这份内容新建」")
	assert.Empty(t, h.outbound.rows)
}

// TestEnqueue_GivenDelete_CascadesChildren R6：删项目时它名下的子行一并落墓碑。
func TestEnqueue_GivenDelete_CascadesChildren(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.kids = []relatedRow{{Kind: "project", SyncID: "loc-1", Version: 2}}
	h.transport.results = func([]syncwire.PushItem) ([]syncwire.PushResult, error) {
		return nil, errors.New("offline")
	}

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpDelete, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 3},
	})

	require.Len(t, h.outbound.rows, 2)
	assert.Equal(t, OpDelete, h.outbound.rows[0].Op)
	assert.Equal(t, "loc-1", h.outbound.rows[1].EntitySyncID)
	assert.Equal(t, OpDelete, h.outbound.rows[1].Op, "子行跟着落墓碑")
}

// ── ⑤ 乱序引用暂缓落地 ─────────────────────────────────────────────────────

// TestApplyInbound_GivenMissingReference_DefersInsteadOfDanglingWrite R2a：引用的
// 目标尚未到达时该行暂缓落地，不写悬空引用；目标到达后自动完成。
func TestApplyInbound_GivenMissingReference_DefersInsteadOfDanglingWrite(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.needRef = ref{Kind: "project", SyncID: "parent-1"}
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{{
			Kind: "project", SyncID: "child-1", Version: 5, Payload: []byte(`{"name":"Child"}`),
		}},
		NextCursor: 5,
	}}
	ctx := context.Background()

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Empty(t, h.adapter.applied, "引用目标没到，绝不写悬空引用")
	require.Len(t, h.inbound.rows, 1)
	assert.Equal(t, "project:parent-1", h.inbound.rows[0].MissingSyncID)

	// 目标到达：暂缓的那一行自动完成。
	h.state.ids["project:parent-1"] = 42
	h.transport.pages = []*syncwire.PullPage{{NextCursor: 5}}
	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Equal(t, "Child", h.adapter.rows["child-1"])
	assert.Empty(t, h.inbound.rows, "落地后出队")
}

// TestGCDeferred_GivenExpiredRow_DiscardsWithLostChange R2a：暂缓的行保留 30 天，
// 超期仍等不到目标就丢弃，并以「引用丢失」进 R5 的列表。
func TestGCDeferred_GivenExpiredRow_DiscardsWithLostChange(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.needRef = ref{Kind: "project", SyncID: "parent-1"}
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{{
			Kind: "project", SyncID: "child-1", Version: 5, Payload: []byte(`{"name":"Child"}`),
		}},
		NextCursor: 5,
	}}
	ctx := context.Background()
	require.NoError(t, h.svc.SyncOnce(ctx))
	require.Len(t, h.inbound.rows, 1)

	// 未到期：留着。
	h.nowMs += TombstoneWindow.Milliseconds() - int64(time.Hour/time.Millisecond)
	h.transport.pages = []*syncwire.PullPage{{NextCursor: 5}}
	require.NoError(t, h.svc.SyncOnce(ctx))
	assert.Len(t, h.inbound.rows, 1, "30 天之内照常等着")

	// 到期：丢弃并留记录。
	h.nowMs += int64(2 * time.Hour / time.Millisecond)
	h.transport.pages = []*syncwire.PullPage{{NextCursor: 5}}
	require.NoError(t, h.svc.SyncOnce(ctx))
	assert.Empty(t, h.inbound.rows)
	require.Len(t, h.lost.rows, 1)
	assert.Equal(t, syncqueue_entity.ReasonDiscarded, h.lost.rows[0].Reason)
}

// TestPull_GivenNaturalKeyMergeLoss_RecordsOverwritten R4b/R5：server 按自然键把两端
// 各自建的路径记录合并成一行，**落败的那份**要进「没能同步的改动」。落败那一份的
// 主人是另一台桌面端——它只收得到一份墓碑，server 的 MergedSyncID 只回给推上来的
// 那一端。判据是墓碑落地之后，同一个自然键上站着的是**另一个同步标识**的活行。
func TestPull_GivenNaturalKeyMergeLoss_RecordsOverwritten(t *testing.T) {
	h := newHarness(t, true)
	loc := newFakeAdapter(syncwire.KindProjectLocation)
	loc.rows["loc-mine"] = "/srv/mine"
	loc.loadFn = func(syncID string) (*outbound, error) {
		return &outbound{
			SyncID: syncID, ProjectSyncID: "proj-1", AgentredFingerprint: "fp-builder",
			Payload: []byte(`{"path":"/srv/mine"}`),
		}, nil
	}
	h.svc.adapters[loc.kind()] = loc
	h.state.meta["project_location:loc-mine"] = syncmeta_entity.SyncMeta{
		SyncID: "loc-mine", SyncVersion: 3,
	}
	loc.naturalKey["proj-1|fp-builder"] = "loc-winner"

	// server 一次合并发两条：先墓碑（较小版本），再胜者。
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{
			{Kind: syncwire.KindProjectLocation, SyncID: "loc-mine", Version: 10,
				ProjectSyncID: "proj-1", AgentredFingerprint: "fp-builder", DeletedAt: 1700},
			{Kind: syncwire.KindProjectLocation, SyncID: "loc-winner", Version: 11,
				ProjectSyncID: "proj-1", AgentredFingerprint: "fp-builder",
				Payload: []byte(`{"path":"/srv/theirs"}`)},
		},
		NextCursor: 11,
	}}

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	assert.NotContains(t, loc.rows, "loc-mine", "落败那一行照常落墓碑（R6）")
	require.Len(t, h.lost.rows, 1)
	assert.Equal(t, syncqueue_entity.ReasonOverwritten, h.lost.rows[0].Reason)
	assert.Equal(t, "loc-mine", h.lost.rows[0].EntitySyncID)
	assert.JSONEq(t, `{"path":"/srv/mine"}`, h.lost.rows[0].PayloadJSON)
	assert.Equal(t, "fp-builder", h.lost.rows[0].AgentredFingerprint)
}

// TestPull_GivenPlainTombstone_RecordsNothing 反面守卫：一次普通的远端删除不是
// 合并落败，不该往列表里塞东西——「列表为空是常态」。
func TestPull_GivenPlainTombstone_RecordsNothing(t *testing.T) {
	h := newHarness(t, true)
	loc := newFakeAdapter(syncwire.KindProjectLocation)
	loc.rows["loc-mine"] = "/srv/mine"
	loc.loadFn = func(syncID string) (*outbound, error) {
		return &outbound{
			SyncID: syncID, ProjectSyncID: "proj-1", AgentredFingerprint: "fp-builder",
			Payload: []byte(`{"path":"/srv/mine"}`),
		}, nil
	}
	h.svc.adapters[loc.kind()] = loc
	h.state.meta["project_location:loc-mine"] = syncmeta_entity.SyncMeta{
		SyncID: "loc-mine", SyncVersion: 3,
	}
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{{
			Kind: syncwire.KindProjectLocation, SyncID: "loc-mine", Version: 10,
			ProjectSyncID: "proj-1", AgentredFingerprint: "fp-builder", DeletedAt: 1700,
		}},
		NextCursor: 10,
	}}

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	assert.NotContains(t, loc.rows, "loc-mine")
	assert.Empty(t, h.lost.rows, "自然键上没有别人站着，就只是一次普通删除")
}

// TestGCLostChanges_DropsRowsPastTheWindow R5/决策 5：「没能同步的改动」本地留存
// **30 天**。此前只有入站暂缓队列会到期回收，这份列表只增不减，30 天那个承诺在
// 界面上就是一句空话。
func TestGCLostChanges_DropsRowsPastTheWindow(t *testing.T) {
	h := newHarness(t, true)
	h.lost.rows = []*syncqueue_entity.LostChange{
		{ID: 1, SyncAccountID: 7, EntityType: "project", EntitySyncID: "p-fresh",
			Reason: syncqueue_entity.ReasonOverwritten, Createtime: h.nowMs},
		{ID: 2, SyncAccountID: 7, EntityType: "project", EntitySyncID: "p-stale",
			Reason:     syncqueue_entity.ReasonOverwritten,
			Createtime: h.nowMs - TombstoneWindow.Milliseconds() - 1},
	}
	h.transport.pages = []*syncwire.PullPage{{NextCursor: 1}}

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	require.Len(t, h.lost.rows, 1)
	assert.Equal(t, "p-fresh", h.lost.rows[0].EntitySyncID, "未到期的照常留着")
}

// TestGCDeferred_GivenExpiredRow_KeepsBarePayloadAndNaturalKey R2a/R5：丢弃一条暂缓
// 行时，进列表的是**载荷正文**本身，不是暂缓队列存的那层信封——列表要「可展开查看
// 内容并恢复」，恢复走的又是 adapter.apply，喂信封给它只会解出一个空对象。路径记录
// 的自然键（项目同步标识 + 指纹）同样要留住，否则恢复时解析不出它属于谁（R5a）。
func TestGCDeferred_GivenExpiredRow_KeepsBarePayloadAndNaturalKey(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.needRef = ref{Kind: "project", SyncID: "parent-1"}
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{{
			Kind: "project", SyncID: "loc-1", Version: 5,
			ProjectSyncID: "proj-9", AgentredFingerprint: "fp-abc",
			Payload: []byte(`{"path":"/srv/work"}`),
		}},
		NextCursor: 5,
	}}
	ctx := context.Background()
	require.NoError(t, h.svc.SyncOnce(ctx))
	require.Len(t, h.inbound.rows, 1)

	h.nowMs += TombstoneWindow.Milliseconds() + 1
	h.transport.pages = []*syncwire.PullPage{{NextCursor: 5}}
	require.NoError(t, h.svc.SyncOnce(ctx))

	require.Len(t, h.lost.rows, 1)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.lost.rows[0].PayloadJSON), &body))
	assert.Equal(t, "/srv/work", body["path"], "存的是载荷正文，不是 inbound 信封")
	assert.NotContains(t, body, "sync_id", "信封字段不该混进正文")
	assert.Equal(t, "proj-9", h.lost.rows[0].ProjectSyncID)
	assert.Equal(t, "fp-abc", h.lost.rows[0].AgentredFingerprint)
}

// ── ② 30 秒轮询下行 ────────────────────────────────────────────────────────

// TestPull_AdvancesCursorAcrossRuns R3：下行按游标增量，游标持久化——下一轮从上次
// 停下的版本继续，不重复拉全量。
func TestPull_AdvancesCursorAcrossRuns(t *testing.T) {
	h := newHarness(t, true)
	h.transport.pages = []*syncwire.PullPage{{
		Items:      []syncwire.PullItem{{Kind: "project", SyncID: "p-1", Version: 11, Payload: []byte(`{"name":"A"}`)}},
		NextCursor: 11,
	}}
	ctx := context.Background()
	require.NoError(t, h.svc.SyncOnce(ctx))

	h.transport.pages = []*syncwire.PullPage{{NextCursor: 11}}
	require.NoError(t, h.svc.SyncOnce(ctx))

	// 第一轮：从 0 拉到那一条，然后补一次收尾空拉（R6a，见 resync_window_test.go）；
	// 第二轮：从 11 继续，第一页就是空的，不再补。
	assert.Equal(t, []int64{0, 11, 11}, h.transport.pulledAt, "第二轮从游标 11 继续")
	st, err := h.svc.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(11), st.Cursor)
	assert.Equal(t, h.nowMs, st.LastSuccessAt)
}

// TestPollIntervalIsThirtySeconds R3：下行按 30 秒周期轮询。
// TestBackoff_GrowsOnFailureAndResetsOnSuccess R7/R16：「同步失败**按退避重试**」。
// 轮询周期本身不变，退让体现在「失败后跳过几个周期」上——server 长时间不可达时
// 不该每 30 秒原样再撞一次，而设置里那条文案早就写着退避。
func TestBackoff_GrowsOnFailureAndResetsOnSuccess(t *testing.T) {
	var b backoff
	assert.True(t, b.due(), "没失败过就照常跑")

	b.fail()
	assert.False(t, b.due(), "第一次失败：跳过一个周期")
	assert.True(t, b.due())

	b.fail()
	assert.False(t, b.due())
	assert.False(t, b.due(), "第二次连续失败：退让窗口翻倍")
	assert.True(t, b.due())

	b.succeed()
	assert.True(t, b.due(), "成功即复位")

	for i := 0; i < 20; i++ {
		b.fail()
	}
	skipped := 0
	for !b.due() {
		skipped++
		require.Less(t, skipped, maxBackoffTicks+2, "退让有上限，不会退到永不重试")
	}
	assert.LessOrEqual(t, skipped, maxBackoffTicks)
}

func TestPollIntervalIsThirtySeconds(t *testing.T) {
	assert.Equal(t, 30*time.Second, PollInterval)
}

// ── ⑦ 超窗口设备先重同步 ───────────────────────────────────────────────────

// TestFlush_GivenResyncRequired_PullsSnapshotThenFiltersQueue R6a：超窗口设备的上行
// 被拒后先拉全量快照；队列里基版本为空的新建照常上行，基版本对不上的被拦下并以
// 「超时未上传」进 R5 列表。
func TestFlush_GivenResyncRequired_PullsSnapshotThenFiltersQueue(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-new"] = "Fresh"  // 离线期间新建：基版本为空
	h.adapter.rows["p-old"] = "Edited" // 离线期间改的旧行：基版本 4
	ctx := context.Background()

	h.transport.results = func([]syncwire.PushItem) ([]syncwire.PushResult, error) {
		return nil, errors.New("offline")
	}
	h.svc.NotifyLocalChange(ctx, LocalChange{
		Kind: "project", Op: OpCreate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-new"},
	})
	h.svc.NotifyLocalChange(ctx, LocalChange{
		Kind: "project", Op: OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-old", SyncVersion: 4},
	})

	// 联网后 server 判超窗口：先重同步。快照里 p-old 已经被别人改到版本 9。
	attempted := 0
	h.transport.results = func(items []syncwire.PushItem) ([]syncwire.PushResult, error) {
		attempted++
		if attempted == 1 {
			return nil, syncwire.ErrResyncRequired
		}
		out := make([]syncwire.PushResult, 0, len(items))
		for _, it := range items {
			out = append(out, syncwire.PushResult{
				SyncID: it.SyncID, Kind: it.Kind, Version: 20, Status: syncwire.PushStatusAccepted,
			})
		}
		return out, nil
	}
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{{
			Kind: "project", SyncID: "p-old", Version: 9, Payload: []byte(`{"name":"Theirs"}`),
		}},
		NextCursor: 9,
	}, {NextCursor: 9}}

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Equal(t, int64(0), h.transport.pulledAt[0], "先拉一份全量快照")
	last := h.transport.pushed[len(h.transport.pushed)-1]
	require.Len(t, last, 1)
	assert.Equal(t, "p-new", last[0].SyncID, "基版本为空的新建照常上行")
	require.Len(t, h.lost.rows, 1)
	assert.Equal(t, "p-old", h.lost.rows[0].EntitySyncID)
	assert.Equal(t, syncqueue_entity.ReasonRejected, h.lost.rows[0].Reason, "对不上快照的那条以「超时未上传」进列表")
	assert.Equal(t, "Theirs", h.adapter.rows["p-old"], "快照为准")
}

// ── 冲突落记录（R4a/R5） ───────────────────────────────────────────────────

// TestFlush_GivenConflict_RecordsOverwrittenChange R4a/R5：基版本不符时本次上行照常
// 生效，被覆盖的那一版按「被覆盖」进列表，并记下来源设备。
func TestFlush_GivenConflict_RecordsOverwrittenChange(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "Mine"
	h.transport.results = func(items []syncwire.PushItem) ([]syncwire.PushResult, error) {
		return []syncwire.PushResult{{
			SyncID: items[0].SyncID, Kind: items[0].Kind, Version: 12,
			Status: syncwire.PushStatusConflict, OverwrittenVersion: 11, OverwrittenOriginFingerprint: "fp-5",
		}}, nil
	}

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 8},
	})

	require.Len(t, h.lost.rows, 1)
	assert.Equal(t, syncqueue_entity.ReasonOverwritten, h.lost.rows[0].Reason)
	assert.Equal(t, int64(11), h.lost.rows[0].BaseVersion)
	assert.Equal(t, "fp-5", h.lost.rows[0].OriginDevice)
	assert.Equal(t, int64(12), h.state.meta["project:p-1"].SyncVersion, "本次上行照常生效")
}

// TestSyncOnce_GivenRowsFromBeforeLogin_ClaimsThemAndUploadsOnce R12a：登录前已有的
// 行（还不属于任何账号）在登录后归入当前账号，并带着自己那个同步标识正常上行。
// 不做这一步的话，一个先用后登录的用户要挨个去碰每一行才能把工作区带上云。
func TestSyncOnce_GivenRowsFromBeforeLogin_ClaimsThemAndUploadsOnce(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-old"] = "登录前建的项目"
	h.state.unowned["project"] = []syncstate_repo.ClaimedRow{{SyncID: "p-old"}}
	ctx := context.Background()

	require.NoError(t, h.svc.SyncOnce(ctx))

	require.Len(t, h.transport.pushed, 1)
	require.Len(t, h.transport.pushed[0], 1)
	assert.Equal(t, "p-old", h.transport.pushed[0][0].SyncID)
	assert.Equal(t, int64(0), h.transport.pushed[0][0].BaseVersion, "server 没见过它，按新建走（R4a）")
	assert.Equal(t, int64(7), h.state.claimedBy["project:p-old"], "行归入当前账号")

	// 第二轮：已经归属了，不再重复认领、也不再重复上行。
	require.NoError(t, h.svc.SyncOnce(ctx))
	assert.Len(t, h.transport.pushed, 1)
	assert.Empty(t, h.outbound.rows)
}

// TestFlush_GivenDownlinkBetweenEditAndPush_CarriesEditTimeBaseVersion R4a/决策 27：
// 上行带的基版本是「本端**编辑时**见到的那一版」——入队时记下的那个，而不是推送时
// 重新读一遍行上的版本。编辑与出队之间落了一次他端下行时，后者恰好等于当前版本，
// server 判不出冲突，R5 的「被覆盖」永不写——那正是决策 27 要兜住的场景。
func TestFlush_GivenDownlinkBetweenEditAndPush_CarriesEditTimeBaseVersion(t *testing.T) {
	h := newHarness(t, true)
	h.svc.transport = nil // 先只入队，不推送
	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 5},
	})
	require.Len(t, h.outbound.rows, 1)
	require.Equal(t, int64(5), h.outbound.rows[0].BaseVersion)

	// 他端的一次更新在这中间落地：行上的 SyncVersion 已经被推到了 server 分配的 9。
	h.state.meta["project:p-1"] = syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 9}
	h.adapter.loadFn = func(syncID string) (*outbound, error) {
		return &outbound{SyncID: syncID, UpdatedAt: 2, Payload: []byte(`{"name":"Mine"}`)}, nil
	}
	h.svc.transport = h.transport

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	require.Len(t, h.transport.pushed, 1)
	assert.Equal(t, int64(5), h.transport.pushed[0][0].BaseVersion,
		"带的是编辑时那一版，server 才判得出这次编辑被他端覆盖过")
}

// TestRestoreLostChange_GivenTombstonedTarget_RefusesAndOffersRecreate R5a：恢复的
// 目标已被删除时明确失败、不复活；「按这份内容新建」分配新的同步标识正常上行。
func TestRestoreLostChange_GivenTombstonedTarget_RefusesAndOffersRecreate(t *testing.T) {
	h := newHarness(t, true)
	h.lost.rows = []*syncqueue_entity.LostChange{{
		ID: 1, SyncAccountID: 7, EntityType: "project", EntitySyncID: "p-1",
		Reason: syncqueue_entity.ReasonRejected, PayloadJSON: `{"name":"Alpha"}`,
	}}
	h.state.meta["project:p-1"] = syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 9, SyncDeletedAt: 1}
	ctx := context.Background()

	outcome, err := h.svc.RestoreLostChange(ctx, 1)
	require.NoError(t, err)
	assert.True(t, outcome.TargetDeleted, "恢复不会让被删除的对象回来")
	assert.False(t, outcome.Restored)
	assert.NotContains(t, h.adapter.rows, "p-1")

	require.NoError(t, h.svc.RecreateFromLostChange(ctx, 1))
	require.Len(t, h.adapter.applied, 1)
	newSyncID := h.transport.pushed[0][0].SyncID
	assert.NotEqual(t, "p-1", newSyncID, "新建分配的是一个新的同步标识")
	assert.Empty(t, h.lost.rows, "处理完就从列表里消失")
}

// TestBuildPushItem_GivenPayloadWithLocalID_SkipsItem R2 的守卫断言：载荷里混进本地
// 自增 ID 时这一条根本发不出去。
func TestBuildPushItem_GivenPayloadWithLocalID_SkipsItem(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.loadFn = func(syncID string) (*outbound, error) {
		return &outbound{SyncID: syncID, Payload: []byte(`{"name":"x","parent_id":7}`)}, nil
	}

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1"},
	})

	assert.Empty(t, h.transport.pushed, "带本地自增 ID 的载荷不上行")
	assert.Empty(t, h.outbound.rows, "也不会永远堵在队列里")
}
