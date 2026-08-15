package sync_svc

import (
	"context"
	"sort"
	"strings"

	"github.com/agentre-ai/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-ai/agentre/internal/repository/syncstate_repo"
)

// 队列与同步元数据这几个仓储在本包的测试里是**有状态的存储**：引擎的行为（折叠、
// 出队、暂缓、重放、30 天回收）恰恰体现在「写进去的东西下一步还在不在」上，用
// mockgen 的期望链表达等于把被测逻辑再写一遍。因此这里用最小的内存替身，
// 只有真正「一次调用一个答案」的 server_state 走 mockgen（见 sync_test.go）。

type fakeOutboundQueue struct {
	rows   []*syncqueue_entity.OutboundQueueItem
	nextID int64
}

func (f *fakeOutboundQueue) Create(_ context.Context, row *syncqueue_entity.OutboundQueueItem) error {
	f.nextID++
	row.ID = f.nextID
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeOutboundQueue) ListByAccount(_ context.Context, accountID int64) ([]*syncqueue_entity.OutboundQueueItem, error) {
	out := make([]*syncqueue_entity.OutboundQueueItem, 0, len(f.rows))
	for _, row := range f.rows {
		if row.SyncAccountID == accountID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeOutboundQueue) Delete(_ context.Context, id int64) error {
	kept := f.rows[:0]
	for _, row := range f.rows {
		if row.ID != id {
			kept = append(kept, row)
		}
	}
	f.rows = kept
	return nil
}

type fakeInboundQueue struct {
	rows   []*syncqueue_entity.InboundQueueItem
	nextID int64
}

func (f *fakeInboundQueue) Create(_ context.Context, row *syncqueue_entity.InboundQueueItem) error {
	f.nextID++
	row.ID = f.nextID
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeInboundQueue) ListByAccount(_ context.Context, accountID int64) ([]*syncqueue_entity.InboundQueueItem, error) {
	out := make([]*syncqueue_entity.InboundQueueItem, 0, len(f.rows))
	for _, row := range f.rows {
		if row.SyncAccountID == accountID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeInboundQueue) Delete(_ context.Context, id int64) error {
	kept := f.rows[:0]
	for _, row := range f.rows {
		if row.ID != id {
			kept = append(kept, row)
		}
	}
	f.rows = kept
	return nil
}

type fakeLostChange struct {
	rows   []*syncqueue_entity.LostChange
	nextID int64
}

func (f *fakeLostChange) Create(_ context.Context, row *syncqueue_entity.LostChange) error {
	f.nextID++
	if row.ID == 0 {
		row.ID = f.nextID
	}
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeLostChange) ListByAccount(_ context.Context, accountID int64) ([]*syncqueue_entity.LostChange, error) {
	out := make([]*syncqueue_entity.LostChange, 0, len(f.rows))
	for _, row := range f.rows {
		if row.SyncAccountID == accountID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeLostChange) Delete(_ context.Context, id int64) error {
	kept := f.rows[:0]
	for _, row := range f.rows {
		if row.ID != id {
			kept = append(kept, row)
		}
	}
	f.rows = kept
	return nil
}

// fakeSyncState 是七张表同步元数据列的内存替身，键是「对象类型:同步标识」。
type fakeSyncState struct {
	meta map[string]syncmeta_entity.SyncMeta
	ids  map[string]int64
	// unowned 按对象类型放「还没归属任何账号」的行（R12a），认领一次即清空；
	// claimedBy 记下它们被收进了哪个账号，供断言。
	unowned   map[string][]syncstate_repo.ClaimedRow
	claimedBy map[string]int64
	// hardDeleted 标出「这一类对象是硬删」（成员关系、执行目标——它们没有 status
	// 软删列）。真仓储的 SaveMeta 是一条 `UPDATE … WHERE sync_id = ?`：行被硬删之后
	// 那条语句命中 0 行，**同步元数据根本落不下去**。替身以前无条件写，等于凭空给了
	// 这两类一个真实环境里不存在的版本记忆——版本守卫的漏洞因此在测试里全都看不见。
	hardDeleted map[string]bool
}

func newFakeSyncState() *fakeSyncState {
	return &fakeSyncState{
		meta:        map[string]syncmeta_entity.SyncMeta{},
		ids:         map[string]int64{},
		unowned:     map[string][]syncstate_repo.ClaimedRow{},
		claimedBy:   map[string]int64{},
		hardDeleted: map[string]bool{},
	}
}

func (f *fakeSyncState) ClaimUnowned(_ context.Context, kind string, accountID int64) ([]syncstate_repo.ClaimedRow, error) {
	rows := f.unowned[kind]
	if len(rows) == 0 {
		return nil, nil
	}
	delete(f.unowned, kind)
	for _, row := range rows {
		f.claimedBy[kind+":"+row.SyncID] = accountID
	}
	return rows, nil
}

// ResetVersions 清掉某账号名下全部行的版本号（换了一套 server 之后旧序列的版本号
// 既比不了大小，也会把新序列的快照挡在版本守卫外面）。墓碑标记不动。
func (f *fakeSyncState) ResetVersions(_ context.Context, kind string, accountID int64) error {
	for key, meta := range f.meta {
		if !strings.HasPrefix(key, kind+":") || meta.SyncAccountID != accountID {
			continue
		}
		meta.SyncVersion = 0
		f.meta[key] = meta
	}
	return nil
}

// ListUnversioned 列出「server 从没给过版本号」的存活行：版本号为 0 且不是墓碑。
func (f *fakeSyncState) ListUnversioned(_ context.Context, kind string, accountID int64) ([]string, error) {
	var out []string
	for key, meta := range f.meta {
		if !strings.HasPrefix(key, kind+":") || meta.SyncAccountID != accountID {
			continue
		}
		if meta.SyncVersion != 0 || meta.SyncDeletedAt != 0 || meta.SyncID == "" {
			continue
		}
		out = append(out, meta.SyncID)
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeSyncState) key(kind, syncID string) string { return kind + ":" + syncID }

func (f *fakeSyncState) FindLocalID(_ context.Context, kind, syncID string) (int64, error) {
	return f.ids[f.key(kind, syncID)], nil
}

func (f *fakeSyncState) FindVersion(_ context.Context, kind, syncID string) (int64, bool, bool, error) {
	m, ok := f.meta[f.key(kind, syncID)]
	if !ok {
		return 0, false, false, nil
	}
	return m.SyncVersion, m.SyncDeletedAt > 0, true, nil
}

func (f *fakeSyncState) FindRow(context.Context, string, string, any) (bool, error) {
	return false, nil
}

func (f *fakeSyncState) SaveMeta(_ context.Context, kind, syncID string, meta syncmeta_entity.SyncMeta) error {
	if f.hardDeleted[kind] && meta.SyncDeletedAt > 0 {
		// 行已经被硬删，真仓储那条 UPDATE 命中 0 行：什么都没落下。
		delete(f.meta, f.key(kind, syncID))
		return nil
	}
	f.meta[f.key(kind, syncID)] = meta
	return nil
}

// fakeSettings 是本地 key-value 设置表的内存替身（游标住在这里）。
type fakeSettings struct {
	rows map[string]*app_setting_entity.AppSetting
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{rows: map[string]*app_setting_entity.AppSetting{}}
}

func (f *fakeSettings) Get(_ context.Context, key string) (*app_setting_entity.AppSetting, error) {
	row, ok := f.rows[key]
	if !ok {
		return nil, nil
	}
	return row, nil
}

func (f *fakeSettings) Set(_ context.Context, s *app_setting_entity.AppSetting) error {
	f.rows[s.Key] = s
	return nil
}

func (f *fakeSettings) List(context.Context) ([]*app_setting_entity.AppSetting, error) {
	out := make([]*app_setting_entity.AppSetting, 0, len(f.rows))
	for _, row := range f.rows {
		out = append(out, row)
	}
	return out, nil
}
