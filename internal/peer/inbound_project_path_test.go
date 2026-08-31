package peer_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	remotefswire "github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo/mock_syncstate_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

// 本文件锁住规格 2026-08-21「桌面端的项目路径也能从 web 配」：浏览器经中继直接
// 喊这台桌面端，由**它自己**写本机路径并当场重报快照——服务端往上报组直写会被
// 下一次上报冲掉，所以那条路根本不走。
//
// 四个方法与 session.* 共用同一道账号门：未完成 auth.account 的连接一律拒。

// fakeReportingSync 只实现 ReportLocalPathsNow 一个方法：这条测试要断言的是
// 「写完当场重报」，同步引擎其余部分与它无关。嵌入接口让未实现的方法在被调用时
// panic，而不是静默返回零值——真被调到了应该看得见。
type fakeReportingSync struct {
	sync_svc.SyncSvc
	reports atomic.Int32
	err     error
}

func (f *fakeReportingSync) ReportLocalPathsNow(context.Context) error {
	f.reports.Add(1)
	return f.err
}

type projectPathStubs struct {
	projects *mock_project_repo.MockProjectRepo
	state    *mock_syncstate_repo.MockSyncStateRepo
	sync     *fakeReportingSync
}

func registerProjectPathStubs(t *testing.T) *projectPathStubs {
	t.Helper()
	ctrl := gomock.NewController(t)
	projects := mock_project_repo.NewMockProjectRepo(ctrl)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	fake := &fakeReportingSync{}

	prevProjects := project_repo.Project()
	prevState := syncstate_repo.SyncState()
	prevSync := sync_svc.Default()
	project_repo.RegisterProject(projects)
	syncstate_repo.RegisterSyncState(state)
	sync_svc.SetDefault(fake)
	t.Cleanup(func() {
		project_repo.RegisterProject(prevProjects)
		syncstate_repo.RegisterSyncState(prevState)
		sync_svc.SetDefault(prevSync)
		ctrl.Finish()
	})
	return &projectPathStubs{projects: projects, state: state, sync: fake}
}

func authorizePeer(t *testing.T, ws *websocket.Conn, id string) {
	t.Helper()
	authenticated := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(id), Method: "auth.account",
		Params: mustJSON(t, auth.AccountParams{Credential: "same-account-device-jwt"}),
	})
	require.Nil(t, authenticated.Error)
}

// Given 一台已登录的桌面端在线，When 同账号的浏览器经中继给某个项目指定本机路径，
// Then 这台机器自己写下路径、解除「本机未配置路径」，并**当场**重报一次整份快照。
func TestInbound_GivenAuthorizedPeer_WhenSettingLocalPath_ThenWritesLocallyAndReportsAtOnce(t *testing.T) {
	stubs := registerProjectPathStubs(t)
	ws := startInboundPeer(t)
	dir := t.TempDir()

	// 账号门：写本机路径比读更该在门后。
	unauthenticated := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`1`), Method: wire.MethodProjectSetLocalPath,
		Params: mustJSON(t, wire.ProjectSetLocalPathParams{ProjectSyncID: "proj-a", Path: dir}),
	})
	require.NotNil(t, unauthenticated.Error)
	assert.Equal(t, rpcerror.ErrUnauthorized.Code, unauthenticated.Error.Code)

	authorizePeer(t, ws, `2`)

	stubs.state.EXPECT().FindLocalID(gomock.Any(), syncwire.KindProject, "proj-a").Return(int64(9), nil)
	stubs.projects.EXPECT().Find(gomock.Any(), int64(9)).
		Return(&project_entity.Project{ID: 9, Name: "hub", LocalPathMissing: true}, nil)
	stubs.projects.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p *project_entity.Project) error {
			assert.Equal(t, dir, p.Path)
			assert.False(t, p.LocalPathMissing)
			return nil
		})

	set := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`3`), Method: wire.MethodProjectSetLocalPath,
		Params: mustJSON(t, wire.ProjectSetLocalPathParams{ProjectSyncID: "proj-a", Path: dir}),
	})
	require.Nil(t, set.Error, "授权对端必须配得了这台机器上的项目路径")
	var result wire.ProjectLocalPathResult
	require.NoError(t, json.Unmarshal(set.Result, &result))
	assert.Equal(t, dir, result.Path, "应答要带回生效后的路径：浏览器据此就地更新那一行")
	assert.True(t, result.Configured)

	assert.Equal(t, int32(1), stubs.sync.reports.Load(),
		"写完必须当场重报：等 30 秒轮询会让人以为没生效")
}

// Given 这台机器还没同步到那个项目，When 浏览器给它配路径，Then 回一个**可分辨的**
// 错误——把它折进「写失败了」会让用户去查权限和磁盘，而实际上等一会儿就好。
func TestInbound_GivenProjectNotSyncedHere_WhenSettingLocalPath_ThenTellsThemApart(t *testing.T) {
	stubs := registerProjectPathStubs(t)
	ws := startInboundPeer(t)
	authorizePeer(t, ws, `1`)

	stubs.state.EXPECT().FindLocalID(gomock.Any(), syncwire.KindProject, "not-here").Return(int64(0), nil)

	res := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`2`), Method: wire.MethodProjectSetLocalPath,
		Params: mustJSON(t, wire.ProjectSetLocalPathParams{ProjectSyncID: "not-here", Path: t.TempDir()}),
	})
	require.NotNil(t, res.Error)
	assert.Equal(t, int32(wire.ErrCodeProjectNotSynced), res.Error.Code)
	assert.Zero(t, stubs.sync.reports.Load(), "没写成就不该重报")
}

// 路径不存在与「项目不在这台机器上」必须分得开：两者的出路完全不同。
func TestInbound_GivenNonexistentPath_WhenSettingLocalPath_ThenReportsPathNotFound(t *testing.T) {
	stubs := registerProjectPathStubs(t)
	ws := startInboundPeer(t)
	authorizePeer(t, ws, `1`)

	stubs.state.EXPECT().FindLocalID(gomock.Any(), syncwire.KindProject, "proj-a").Return(int64(9), nil)
	stubs.projects.EXPECT().Find(gomock.Any(), int64(9)).
		Return(&project_entity.Project{ID: 9, Name: "hub", LocalPathMissing: true}, nil)

	res := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`2`), Method: wire.MethodProjectSetLocalPath,
		Params: mustJSON(t, wire.ProjectSetLocalPathParams{
			ProjectSyncID: "proj-a", Path: filepath.Join(t.TempDir(), "does-not-exist"),
		}),
	})
	require.NotNil(t, res.Error)
	assert.Equal(t, int32(wire.ErrCodeProjectPathNotFound), res.Error.Code)
}

// Given 那台机器上这个项目已经配过路径，When 浏览器点「移除路径」，Then 它回到
// 「本机未配置路径」——**机器上的目录一个字节都不动**。
func TestInbound_GivenAuthorizedPeer_WhenClearingLocalPath_ThenMarksMissingAndReports(t *testing.T) {
	stubs := registerProjectPathStubs(t)
	ws := startInboundPeer(t)

	unauthenticated := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`1`), Method: wire.MethodProjectClearLocalPath,
		Params: mustJSON(t, wire.ProjectClearLocalPathParams{ProjectSyncID: "proj-a"}),
	})
	require.NotNil(t, unauthenticated.Error)
	assert.Equal(t, rpcerror.ErrUnauthorized.Code, unauthenticated.Error.Code)

	authorizePeer(t, ws, `2`)

	dir := t.TempDir()
	stubs.state.EXPECT().FindLocalID(gomock.Any(), syncwire.KindProject, "proj-a").Return(int64(9), nil)
	stubs.projects.EXPECT().Find(gomock.Any(), int64(9)).
		Return(&project_entity.Project{ID: 9, Name: "hub", Path: dir}, nil)
	stubs.projects.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p *project_entity.Project) error {
			assert.Equal(t, "", p.Path)
			assert.True(t, p.LocalPathMissing)
			return nil
		})

	cleared := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`3`), Method: wire.MethodProjectClearLocalPath,
		Params: mustJSON(t, wire.ProjectClearLocalPathParams{ProjectSyncID: "proj-a"}),
	})
	require.Nil(t, cleared.Error)
	var result wire.ProjectLocalPathResult
	require.NoError(t, json.Unmarshal(cleared.Result, &result))
	assert.Equal(t, "", result.Path)
	assert.False(t, result.Configured)

	assert.Equal(t, int32(1), stubs.sync.reports.Load())

	// 目录还在：移除的只是「这个项目在这台机器上落在哪」这条记录。
	_, statErr := os.Stat(dir)
	require.NoError(t, statErr, "移除路径不得动机器上的目录")
}

// 上报失败不改变写的成败判定：本地已经写成了，界面不该因为一次网络抖动显示成没写。
func TestInbound_GivenReportFails_WhenSettingLocalPath_ThenStillSucceeds(t *testing.T) {
	stubs := registerProjectPathStubs(t)
	stubs.sync.err = assert.AnError
	ws := startInboundPeer(t)
	authorizePeer(t, ws, `1`)

	dir := t.TempDir()
	stubs.state.EXPECT().FindLocalID(gomock.Any(), syncwire.KindProject, "proj-a").Return(int64(9), nil)
	stubs.projects.EXPECT().Find(gomock.Any(), int64(9)).
		Return(&project_entity.Project{ID: 9, Name: "hub", LocalPathMissing: true}, nil)
	stubs.projects.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	set := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`2`), Method: wire.MethodProjectSetLocalPath,
		Params: mustJSON(t, wire.ProjectSetLocalPathParams{ProjectSyncID: "proj-a", Path: dir}),
	})
	require.Nil(t, set.Error, "本地已经写成了，上报失败不该把它说成失败")
	assert.Equal(t, int32(1), stubs.sync.reports.Load())
}

// 缺参数一律 InvalidParams，不许当成「清空路径」那种更有破坏力的语义。
func TestInbound_GivenMissingParams_WhenCallingProjectMethods_ThenRejects(t *testing.T) {
	registerProjectPathStubs(t)
	ws := startInboundPeer(t)
	authorizePeer(t, ws, `1`)

	for i, method := range []string{wire.MethodProjectSetLocalPath, wire.MethodProjectClearLocalPath} {
		res := relayRequest(t, ws, "desktop-peer", relayTestFrame{
			ID: json.RawMessage(json.RawMessage{byte('0' + byte(i) + 2)}), Method: method,
		})
		require.NotNil(t, res.Error, method)
		assert.Equal(t, rpcerror.ErrInvalidParams.Code, res.Error.Code, method)
	}
}

// Given 浏览器要给桌面端挑目录，When 它调 remotefs.*，Then 桌面端认识这两个方法
// （不是 -32601），并与 agentred 用同一份错误分类——浏览器只有一份界面。
func TestInbound_GivenAuthorizedPeer_WhenBrowsingDirectories_ThenAnswersLikeAgentred(t *testing.T) {
	registerProjectPathStubs(t)
	ws := startInboundPeer(t)
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "child"), 0o755))

	unauthenticated := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`1`), Method: remotefswire.MethodListDir,
		Params: mustJSON(t, remotefswire.ListDirReq{Path: dir}),
	})
	require.NotNil(t, unauthenticated.Error, "目录浏览不能绕过账号鉴权")
	assert.Equal(t, rpcerror.ErrUnauthorized.Code, unauthenticated.Error.Code)

	authorizePeer(t, ws, `2`)

	listed := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`3`), Method: remotefswire.MethodListDir,
		Params: mustJSON(t, remotefswire.ListDirReq{Path: dir}),
	})
	require.Nil(t, listed.Error, "桌面端必须认识 remotefs.listDir，不能回 method-not-found")
	var resp remotefswire.ListDirResp
	require.NoError(t, json.Unmarshal(listed.Result, &resp))
	assert.Equal(t, dir, resp.Path)
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "child", resp.Entries[0].Name)
	assert.True(t, resp.Entries[0].IsDir)

	made := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`4`), Method: remotefswire.MethodMkdir,
		Params: mustJSON(t, remotefswire.MkdirReq{Parent: dir, Name: "fresh"}),
	})
	require.Nil(t, made.Error)
	info, statErr := os.Stat(filepath.Join(dir, "fresh"))
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())

	// 与 agentred 同一份错误分类：重名不是「写失败了」，它有自己的码。
	again := relayRequest(t, ws, "desktop-peer", relayTestFrame{
		ID: json.RawMessage(`5`), Method: remotefswire.MethodMkdir,
		Params: mustJSON(t, remotefswire.MkdirReq{Parent: dir, Name: "fresh"}),
	})
	require.NotNil(t, again.Error)
	assert.Equal(t, int32(remotefswire.ErrCodeMkdirExists), again.Error.Code)
}
