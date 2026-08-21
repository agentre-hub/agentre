package sync_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-ai/agentre/internal/model/entity/project_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
	"github.com/agentre-ai/agentre/internal/repository/project_repo"
	"github.com/agentre-ai/agentre/internal/repository/project_repo/mock_project_repo"
)

// registerProjects 装配一个 project_repo 桩，只期望 List 被调用一次——本机路径
// 上报是纯读取（R17），任何 Create/Update/Delete 调用在 gomock 严格模式下都会
// 让测试当场失败，这就是「上报不写本地库」的守卫。
func registerProjects(t *testing.T, rows []*project_entity.Project) *mock_project_repo.MockProjectRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := mock_project_repo.NewMockProjectRepo(ctrl)
	m.EXPECT().List(gomock.Any()).Return(rows, nil).AnyTimes()
	project_repo.RegisterProject(m)
	return m
}

func projectRow(syncID, path string, localPathMissing bool) *project_entity.Project {
	return &project_entity.Project{
		Path: path, LocalPathMissing: localPathMissing,
		SyncMeta: syncmeta_entity.SyncMeta{SyncID: syncID},
	}
}

// projectRowForAccount 造一个已归属某个账号的项目行，用来验 R13a 的换账号边界。
func projectRowForAccount(syncID, path string, accountID int64) *project_entity.Project {
	return &project_entity.Project{
		Path: path,
		SyncMeta: syncmeta_entity.SyncMeta{
			SyncID: syncID, SyncAccountID: accountID,
		},
	}
}

// TestReportLocalPathsOnce_GivenRowsOfAnotherAccount_ThenExcludedFromSnapshot
// R13a：「登录的账号与本地行记录的账号不同时，这些行**不上行、也不携带原同步
// 标识**」。本机路径快照同样受这条约束——它带的是上一个账号的项目同步标识与
// 那台机器上的**绝对路径**，隐私一节写明「一台共用机器上换账号登录，不应把上一个
// 账号的项目名、Agent 与路径发到新账号下」。
func TestReportLocalPathsOnce_GivenRowsOfAnotherAccount_ThenExcludedFromSnapshot(t *testing.T) {
	h := newHarness(t, true) // 当前登录账号 = 7
	registerProjects(t, []*project_entity.Project{
		projectRowForAccount("mine", "/Users/me/mine", 7),
		projectRowForAccount("theirs", "/Users/me/secret-other-account", 99),
		projectRowForAccount("unclaimed", "/Users/me/unclaimed", 0), // R12a：尚未认领，照常上报
	})

	require.NoError(t, h.svc.reportLocalPathsOnce(context.Background()))

	require.Len(t, h.transport.localPathReports, 1)
	assert.Equal(t, []syncwire.LocalPathReportItem{
		{ProjectSyncID: "mine", Path: "/Users/me/mine"},
		{ProjectSyncID: "unclaimed", Path: "/Users/me/unclaimed"},
	}, h.transport.localPathReports[0])
}

// TestReportLocalPathsOnce_GivenSnapshot_ThenServerListMatchesLocal R16：本地
// 增删改后下一次上报使服务端清单与本地一致——已配置路径的项目进快照，未配置
// 路径 / 空路径的项目被过滤掉（对服务端而言就是「没有这一条」）。
func TestReportLocalPathsOnce_GivenSnapshot_ThenServerListMatchesLocal(t *testing.T) {
	h := newHarness(t, true)
	registerProjects(t, []*project_entity.Project{
		projectRow("proj-a", "/Users/me/a", false),
		projectRow("proj-b", "", true),  // 本机未配置路径：不进快照
		projectRow("proj-c", "", false), // 数据不一致的防御性兜底：空路径也不进快照
	})

	err := h.svc.reportLocalPathsOnce(context.Background())
	require.NoError(t, err)

	require.Len(t, h.transport.localPathReports, 1)
	assert.Equal(t, []syncwire.LocalPathReportItem{{ProjectSyncID: "proj-a", Path: "/Users/me/a"}},
		h.transport.localPathReports[0])
}

// TestReportLocalPathsOnce_GivenUnchangedSnapshot_ThenSkipsSecondReport 内容
// 指纹没变就不发：连续两轮快照相同，只有第一轮真的调用了网络请求。
func TestReportLocalPathsOnce_GivenUnchangedSnapshot_ThenSkipsSecondReport(t *testing.T) {
	h := newHarness(t, true)
	registerProjects(t, []*project_entity.Project{projectRow("proj-a", "/Users/me/a", false)})

	require.NoError(t, h.svc.reportLocalPathsOnce(context.Background()))
	require.NoError(t, h.svc.reportLocalPathsOnce(context.Background()))

	assert.Len(t, h.transport.localPathReports, 1, "指纹没变，第二轮不该再发")
}

// TestReportLocalPathsOnce_GivenSnapshotChanges_ThenReportsAgain 与上一条对照：
// 快照内容变了（新增一个项目路径）就必须再发一次。
func TestReportLocalPathsOnce_GivenSnapshotChanges_ThenReportsAgain(t *testing.T) {
	h := newHarness(t, true)
	registerProjects(t, []*project_entity.Project{projectRow("proj-a", "/Users/me/a", false)})
	require.NoError(t, h.svc.reportLocalPathsOnce(context.Background()))

	registerProjects(t, []*project_entity.Project{
		projectRow("proj-a", "/Users/me/a", false),
		projectRow("proj-b", "/Users/me/b", false),
	})
	require.NoError(t, h.svc.reportLocalPathsOnce(context.Background()))

	assert.Len(t, h.transport.localPathReports, 2, "快照变了，必须再发一次")
}

// TestReportLocalPathsOnce_NeverWritesProjectRepo R17：上报过程不写本地库。
// registerProjects 只给 project_repo 桩装配了 List 的期望；gomock 是严格 mock，
// 任何未预期的 Create/Update/Delete 调用都会让测试直接失败——这就是这条规则的
// 机械守卫,不是靠肉眼检查实现代码。
func TestReportLocalPathsOnce_NeverWritesProjectRepo(t *testing.T) {
	h := newHarness(t, true)
	registerProjects(t, []*project_entity.Project{projectRow("proj-a", "/Users/me/a", false)})

	require.NoError(t, h.svc.reportLocalPathsOnce(context.Background()))
	require.NoError(t, h.svc.reportLocalPathsOnce(context.Background()))
}

// TestNotifyLocalChange_DoesNotTriggerLocalPathReport 本地编辑不即时触发上报
// （R16 与 R3 刻意不同）：编辑一个账号级对象走 NotifyLocalChange → 当场 SyncOnce，
// 但本机路径上报只挂在 30 秒轮询 ticker 上，不该被这条路径捎带触发。
func TestNotifyLocalChange_DoesNotTriggerLocalPathReport(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "Alpha"
	registerProjects(t, []*project_entity.Project{projectRow("proj-a", "/Users/me/a", false)})

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", LocalID: 1, Op: OpCreate,
		Meta: syncmeta_entity.SyncMeta{SyncID: "p-1"},
	})

	assert.Empty(t, h.transport.localPathReports, "编辑账号级对象不该捎带触发本机路径上报")
}

// TestReportLocalPathsOnce_GivenReportFails_KeepsPreviousFingerprintForRetry
// R16：上报失败时保留服务端上一次的清单，按退避重试——不推进已保存的指纹，
// 下一轮内容指纹依旧不同，自然重试。
func TestReportLocalPathsOnce_GivenReportFails_KeepsPreviousFingerprintForRetry(t *testing.T) {
	h := newHarness(t, true)
	registerProjects(t, []*project_entity.Project{projectRow("proj-a", "/Users/me/a", false)})
	h.transport.localPathErr = errors.New("server unreachable")

	err := h.svc.reportLocalPathsOnce(context.Background())
	require.Error(t, err)
	assert.Empty(t, h.transport.localPathReports, "失败的那次不算数")

	h.transport.localPathErr = nil
	require.NoError(t, h.svc.reportLocalPathsOnce(context.Background()))
	assert.Len(t, h.transport.localPathReports, 1, "下一轮内容指纹依旧不同，自然重试")
}

// TestReportLocalPathsOnce_GivenLoggedOut_DoesNothing R12 的自然延伸：未登录时
// 不发任何上报请求。
func TestReportLocalPathsOnce_GivenLoggedOut_DoesNothing(t *testing.T) {
	h := newHarness(t, false)
	err := h.svc.reportLocalPathsOnce(context.Background())
	require.NoError(t, err)
	assert.Empty(t, h.transport.localPathReports)
}

// ReportLocalPathsNow 是「刚刚有人从 web 改了本机路径，别等 30 秒」那一下（规格
// 2026-08-21 决策 4）。它与 R16 的轮询共用同一条上报路径与同一枚内容指纹，只是
// 触发时机不同——web 上写完那一刻界面就要能读到新值，等一轮轮询会让人以为没生效。

func TestReportLocalPathsNow_GivenChangedSnapshot_ThenReportsImmediately(t *testing.T) {
	h := newHarness(t, true)
	registerProjects(t, []*project_entity.Project{projectRow("proj-a", "/Users/me/a", false)})

	require.NoError(t, h.svc.ReportLocalPathsNow(context.Background()))

	require.Len(t, h.transport.localPathReports, 1)
	assert.Equal(t, []syncwire.LocalPathReportItem{{ProjectSyncID: "proj-a", Path: "/Users/me/a"}},
		h.transport.localPathReports[0])
}

// 它复用同一枚内容指纹：没变就不发，否则 web 上每点一次都会平白多一个请求。
func TestReportLocalPathsNow_GivenUnchangedSnapshot_ThenSkips(t *testing.T) {
	h := newHarness(t, true)
	registerProjects(t, []*project_entity.Project{projectRow("proj-a", "/Users/me/a", false)})

	require.NoError(t, h.svc.ReportLocalPathsNow(context.Background()))
	require.NoError(t, h.svc.ReportLocalPathsNow(context.Background()))

	assert.Len(t, h.transport.localPathReports, 1, "指纹没变，第二次不该再发")
}

// 包级入口在同步未装配时是空操作：单机构建 / 单元测试里没有同步引擎，
// 写本机路径这条路径不该因此断掉（与 Notify 同一条口径）。
func TestReportLocalPathsNowPackageEntry_GivenNoSvc_ThenNoop(t *testing.T) {
	saved := defaultSvc
	defaultSvc = nil
	t.Cleanup(func() { defaultSvc = saved })

	require.NoError(t, ReportLocalPathsNow(context.Background()))
}
