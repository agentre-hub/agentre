package workspace_fs_svc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	mockRD "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// ── helpers ─────────────────────────────────────────────────────────────────

type rig struct {
	ctx      context.Context
	rd       *mockRD.MockRemoteDeviceSvc
	pool     *mockRD.MockConnPool
	lease    *mockRD.MockLease
	client   *workspaceProtoClient
	calls    map[string][]*workspaceProtoCall
	fallback map[string]func(context.Context, string, any, any) error
	svc      *workspaceFsImpl
}

// newRig 装配一个只有 mock 依赖的 svc。resolver 是本服务自声明的窄接口,单测
// 直接注入闭包 —— 不连 DB,也不 import chat_svc。
func newRig(t *testing.T, deviceID int64, cwd string) *rig {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	r := &rig{
		ctx:      context.Background(),
		rd:       mockRD.NewMockRemoteDeviceSvc(ctrl),
		pool:     mockRD.NewMockConnPool(ctrl),
		lease:    mockRD.NewMockLease(ctrl),
		calls:    make(map[string][]*workspaceProtoCall),
		fallback: make(map[string]func(context.Context, string, any, any) error),
	}
	r.client = newWorkspaceProtoClient(t, r)
	r.svc = &workspaceFsImpl{
		rdSvc: r.rd,
		resolver: func(context.Context, int64) (int64, string, error) {
			return deviceID, cwd, nil
		},
	}
	return r
}

// expectCall 声明一次完整的远端往返(Borrow → Client → Call → Release)。
func (r *rig) expectCall(method string, req any) *workspaceProtoCall {
	r.rd.EXPECT().Pool().Return(r.pool)
	r.pool.EXPECT().Borrow(r.ctx, gomock.Any()).Return(r.lease, nil)
	r.lease.EXPECT().Client().Return(r.client)
	r.lease.EXPECT().Release()
	return r.expectProto(method, req)
}

func (r *rig) expectProto(method string, req any) *workspaceProtoCall {
	call := &workspaceProtoCall{method: method, request: req}
	r.calls[method] = append(r.calls[method], call)
	return call
}

// expectCallCtx 与 expectCall 相同,只是 ctx 只做 Any 匹配:SearchFiles 会给这
// 一跳套一层超时 ctx(遍历不能无限期挂着),匹配不到调用方原来的那个 ctx。
func (r *rig) expectCallCtx(method string, req any) *workspaceProtoCall {
	r.rd.EXPECT().Pool().Return(r.pool)
	r.pool.EXPECT().Borrow(gomock.Any(), gomock.Any()).Return(r.lease, nil)
	r.lease.EXPECT().Client().Return(r.client)
	r.lease.EXPECT().Release()
	call := &workspaceProtoCall{method: method, request: req}
	r.calls[method] = append(r.calls[method], call)
	return call
}

type workspaceProtoCall struct {
	method  string
	request any
	run     func(context.Context, string, any, any) error
	err     error
}

func (c *workspaceProtoCall) DoAndReturn(run func(context.Context, string, any, any) error) *workspaceProtoCall {
	c.run = run
	return c
}

func (c *workspaceProtoCall) Return(err error) *workspaceProtoCall {
	c.err = err
	return c
}

func (r *rig) invoke(ctx context.Context, method string, request, response any) error {
	if len(r.calls[method]) == 0 && r.fallback[method] != nil {
		return r.fallback[method](ctx, method, request, response)
	}
	require.NotEmpty(tFromContext(ctx), r.calls[method], "unexpected Protobuf method %s", method)
	call := r.calls[method][0]
	r.calls[method] = r.calls[method][1:]
	if matcher, ok := call.request.(gomock.Matcher); ok {
		require.True(tFromContext(ctx), matcher.Matches(request), "request mismatch for %s: %v", method, request)
	} else {
		require.True(tFromContext(ctx), reflect.DeepEqual(call.request, request), "request mismatch for %s: want %#v got %#v", method, call.request, request)
	}
	if call.run != nil {
		return call.run(ctx, method, request, response)
	}
	return call.err
}

type workspaceProtoClient struct {
	conn *protorpc.Conn
	done <-chan struct{}
}

func (c *workspaceProtoClient) Conn() *protorpc.Conn    { return c.conn }
func (c *workspaceProtoClient) Closed() <-chan struct{} { return c.done }
func (c *workspaceProtoClient) Close() error            { return c.conn.Close() }

type workspaceProtoPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func (p *workspaceProtoPipe) ReadFrame() ([]byte, error) {
	select {
	case v := <-p.in:
		return v, nil
	case <-p.done:
		return nil, errors.New("closed")
	}
}
func (p *workspaceProtoPipe) WriteFrame(v []byte) error {
	select {
	case p.out <- append([]byte(nil), v...):
		return nil
	case <-p.done:
		return errors.New("closed")
	}
}
func (p *workspaceProtoPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *workspaceProtoPipe) Done() <-chan struct{} { return p.done }

type workspaceTestKey struct{}

func tFromContext(ctx context.Context) *testing.T { return ctx.Value(workspaceTestKey{}).(*testing.T) }

func newWorkspaceProtoClient(t *testing.T, r *rig) *workspaceProtoClient {
	registry := protorpc.NewRegistry()
	registerWorkspaceTestMethods(registry, r)
	a, b, done, once := make(chan []byte, 16), make(chan []byte, 16), make(chan struct{}), &sync.Once{}
	clientTransport := &workspaceProtoPipe{in: a, out: b, done: done, once: once}
	serverTransport := &workspaceProtoPipe{in: b, out: a, done: done, once: once}
	clientConn := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	serverConn := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), workspaceTestKey{}, t))
	t.Cleanup(func() { cancel(); _ = clientConn.Close(); _ = serverConn.Close() })
	go clientConn.Serve(ctx)
	go serverConn.Serve(ctx)
	return &workspaceProtoClient{conn: clientConn, done: done}
}

func registerWorkspaceTestMethods(registry *protorpc.Registry, r *rig) {
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_LIST_DIR), func() *agentrewire.WorkspaceFsListDirRequest { return &agentrewire.WorkspaceFsListDirRequest{} }, func(ctx context.Context, p *agentrewire.WorkspaceFsListDirRequest) (*agentrewire.WorkspaceFsListDirResponse, error) {
		out := &wire.ListDirResp{}
		err := r.invoke(ctx, wire.MethodListDir, wire.ListDirReq{Root: p.Root, RelPath: p.RelPath, IncludeIgnored: p.IncludeIgnored}, out)
		resp := &agentrewire.WorkspaceFsListDirResponse{Path: out.Path, Truncated: out.Truncated}
		for _, e := range out.Entries {
			resp.Entries = append(resp.Entries, &agentrewire.WorkspaceFsEntry{Name: e.Name, IsDir: e.IsDir, Size: e.Size, ModTime: e.ModTime, Symlink: e.Symlink, GitIgnored: e.GitIgnored})
		}
		return resp, protoTestErr(err)
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_BRANCHES), func() *agentrewire.WorkspaceFsGitBranchesRequest { return &agentrewire.WorkspaceFsGitBranchesRequest{} }, func(ctx context.Context, p *agentrewire.WorkspaceFsGitBranchesRequest) (*agentrewire.WorkspaceFsGitBranchesResponse, error) {
		out := &wire.GitBranchesResp{}
		err := r.invoke(ctx, wire.MethodGitBranches, wire.GitBranchesReq{Root: p.Root}, out)
		resp := &agentrewire.WorkspaceFsGitBranchesResponse{NotARepo: out.NotARepo, CurrentBranch: out.CurrentBranch, DefaultBaseline: out.DefaultBaseline}
		for _, b := range out.Branches {
			resp.Branches = append(resp.Branches, &agentrewire.WorkspaceFsBranch{Name: b.Name, Remote: b.Remote})
		}
		return resp, protoTestErr(err)
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_STATE), func() *agentrewire.WorkspaceFsGitStateRequest { return &agentrewire.WorkspaceFsGitStateRequest{} }, func(ctx context.Context, p *agentrewire.WorkspaceFsGitStateRequest) (*agentrewire.WorkspaceFsGitStateResponse, error) {
		out := &wire.GitStateResp{}
		err := r.invoke(ctx, wire.MethodGitState, wire.GitStateReq{Root: p.Root}, out)
		return protowire.WorkspaceGitStateResponseToProto(*out), protoTestErr(err)
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_CHANGES), func() *agentrewire.WorkspaceFsGitChangesRequest { return &agentrewire.WorkspaceFsGitChangesRequest{} }, func(ctx context.Context, p *agentrewire.WorkspaceFsGitChangesRequest) (*agentrewire.WorkspaceFsGitChangesResponse, error) {
		out := &wire.GitChangesResp{}
		err := r.invoke(ctx, wire.MethodGitChanges, wire.GitChangesReq{Root: p.Root, Scope: p.Scope, BaseRef: p.BaseRef}, out)
		return protowire.WorkspaceGitChangesResponseToProto(*out), protoTestErr(err)
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE), func() *agentrewire.WorkspaceFsReadFileRequest { return &agentrewire.WorkspaceFsReadFileRequest{} }, func(ctx context.Context, p *agentrewire.WorkspaceFsReadFileRequest) (*agentrewire.WorkspaceFsReadFileResponse, error) {
		out := &wire.ReadFileResp{}
		err := r.invoke(ctx, wire.MethodReadFile, wire.ReadFileReq{Root: p.Root, RelPath: p.RelPath}, out)
		resp, convErr := protowire.WorkspaceReadFileResponseToProto(*out)
		if convErr != nil {
			return nil, convErr
		}
		return resp, protoTestErr(err)
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_FILE_CONTENT), func() *agentrewire.WorkspaceFsGitFileContentRequest {
		return &agentrewire.WorkspaceFsGitFileContentRequest{}
	}, func(ctx context.Context, p *agentrewire.WorkspaceFsGitFileContentRequest) (*agentrewire.WorkspaceFsGitFileContentResponse, error) {
		out := &wire.GitFileContentResp{}
		err := r.invoke(ctx, wire.MethodGitFileContent, wire.GitFileContentReq{Root: p.Root, RelPath: p.RelPath}, out)
		return &agentrewire.WorkspaceFsGitFileContentResponse{Content: []byte(out.Content), NotARepo: out.NotARepo, HasHead: out.HasHead}, protoTestErr(err)
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_SEARCH_FILES), func() *agentrewire.WorkspaceFsSearchFilesRequest { return &agentrewire.WorkspaceFsSearchFilesRequest{} }, func(ctx context.Context, p *agentrewire.WorkspaceFsSearchFilesRequest) (*agentrewire.WorkspaceFsSearchFilesResponse, error) {
		out := &wire.SearchFilesResp{}
		err := r.invoke(ctx, wire.MethodSearchFiles, wire.SearchFilesReq{Root: p.Root, Query: p.Query, IncludeIgnored: p.IncludeIgnored}, out)
		resp := &agentrewire.WorkspaceFsSearchFilesResponse{Truncated: out.Truncated}
		for _, h := range out.Hits {
			resp.Hits = append(resp.Hits, &agentrewire.WorkspaceFsSearchHit{Path: h.Path, IsDir: h.IsDir})
		}
		return resp, protoTestErr(err)
	})
}

func protoTestErr(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) {
		return &protorpc.Error{Code: int32(rpcErr.Code), Message: rpcErr.Message}
	}
	return err
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // G204: test helper, args 来自测试内常量
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}

// initRepo 建一个真实临时 git 仓库(本机分支直接调叶子包,需要真目录;这不是
// 数据库依赖)。
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

// ── 路由分叉:deviceID 空/非空 ───────────────────────────────────────────────

func TestListDir_RoutesByDeviceID(t *testing.T) {
	convey.Convey("ListDir 按 deviceID 路由", t, func() {
		convey.Convey("deviceID=0 → 本机 in-process,不借租约", func() {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644))
			require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
			r := newRig(t, 0, dir)
			// rd 上没有任何 EXPECT:一旦走了远端分支,gomock 会直接判错。

			view, err := r.svc.ListDir(r.ctx, 42, "", "", false)
			require.NoError(t, err)
			assert.Equal(t, dir, view.Path)
			names := map[string]bool{}
			for _, e := range view.Entries {
				names[e.Name] = e.IsDir
			}
			assert.Contains(t, names, "a.txt")
			assert.True(t, names["sub"])
		})

		convey.Convey("deviceID≠0 → 走 RPC,root 用服务解析出的 cwd", func() {
			r := newRig(t, 7, "/remote/work")
			r.expectCall(wire.MethodListDir, wire.ListDirReq{
				Root: "/remote/work", RelPath: "sub", IncludeIgnored: true,
			}).DoAndReturn(func(_ context.Context, _ string, _ any, out any) error {
				resp := out.(*wire.ListDirResp)
				resp.Path = "/remote/work/sub"
				resp.Entries = []wire.Entry{{Name: "f.go", Size: 12, ModTime: 1700000000, GitIgnored: true}}
				resp.Truncated = true
				return nil
			})

			view, err := r.svc.ListDir(r.ctx, 42, "", "sub", true)
			require.NoError(t, err)
			assert.Equal(t, "/remote/work/sub", view.Path)
			assert.True(t, view.Truncated)
			require.Len(t, view.Entries, 1)
			assert.Equal(t, "f.go", view.Entries[0].Name)
			assert.True(t, view.Entries[0].GitIgnored)
		})
	})
}

// ── 会话解析失败 / cwd 为空的降级 ───────────────────────────────────────────

func TestWorkspaceResolution_Degrades(t *testing.T) {
	convey.Convey("会话解析", t, func() {
		convey.Convey("cwd 为空 → 报错且不借租约", func() {
			r := newRig(t, 7, "") // 远端会话但该设备上没配项目路径
			_, err := r.svc.ListDir(r.ctx, 42, "", "", false)
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsNoCwd).Error(), err.Error())
		})

		convey.Convey("resolver 报错 → 原样透出(会话不存在等由 chat_svc 决定文案)", func() {
			r := newRig(t, 0, "/tmp")
			sentinel := errors.New("session not found")
			r.svc.resolver = func(context.Context, int64) (int64, string, error) {
				return 0, "", sentinel
			}
			_, err := r.svc.GitBranches(r.ctx, 42)
			assert.ErrorIs(t, err, sentinel)
		})

		// R10: 「本机未配置路径」在文件面板上必须是一个专用、可与
		// WorkspaceFsNoCwd 区分的错误 —— resolver 返回该错误时(chat_svc 的
		// ResolveSessionWorkspace 经 resolveSessionCwd 传上来)必须原样透出,
		// 不能落进「cwd 为空」那个通用分支被吞成 WorkspaceFsNoCwd(三种「没有
		// cwd」混成同一提示,用户看不出该去哪里修)。
		convey.Convey("resolver 报本机未配置路径 → 原样透出,不被折叠成 WorkspaceFsNoCwd", func() {
			r := newRig(t, 0, "/tmp")
			r.svc.resolver = func(context.Context, int64) (int64, string, error) {
				return 0, "", i18n.NewError(r.ctx, code.ProjectLocalPathMissing)
			}
			_, err := r.svc.GitBranches(r.ctx, 42)
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.ProjectLocalPathMissing).Error(), err.Error())
			assert.NotEqual(t, i18n.NewError(r.ctx, code.WorkspaceFsNoCwd).Error(), err.Error())
		})

		convey.Convey("sessionID 非法 → 不调 resolver", func() {
			r := newRig(t, 0, "/tmp")
			r.svc.resolver = func(context.Context, int64) (int64, string, error) {
				t.Fatal("resolver must not be called for an invalid sessionID")
				return 0, "", nil
			}
			_, err := r.svc.ListDir(r.ctx, 0, "", "", false)
			assert.Error(t, err)
		})
	})
}

// ── 越界路径:两端都拒 ───────────────────────────────────────────────────────

func TestListDir_PathRefused(t *testing.T) {
	convey.Convey("越界路径", t, func() {
		convey.Convey("本机:叶子包 sentinel → WorkspaceFsPathRefused", func() {
			r := newRig(t, 0, t.TempDir())
			_, err := r.svc.ListDir(r.ctx, 42, "", "../etc", false)
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsPathRefused).Error(), err.Error())
		})

		convey.Convey("远端:wire code → 同一个 WorkspaceFsPathRefused", func() {
			r := newRig(t, 7, "/remote/work")
			r.expectCall(wire.MethodListDir, wire.ListDirReq{Root: "/remote/work", RelPath: "../etc"}).
				Return(&rpcerror.Error{Code: wire.ErrCodePathRefused, Message: "refused"})
			_, err := r.svc.ListDir(r.ctx, 42, "", "../etc", false)
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsPathRefused).Error(), err.Error())
		})
	})
}

func TestRemote_BorrowFailure_IsDeviceOffline(t *testing.T) {
	convey.Convey("借不到租约 → 设备不在线", t, func() {
		r := newRig(t, 7, "/remote/work")
		r.rd.EXPECT().Pool().Return(r.pool)
		r.pool.EXPECT().Borrow(r.ctx, int64(7)).Return(nil, errors.New("dial fail"))

		_, err := r.svc.GitBranches(r.ctx, 42)
		require.Error(t, err)
		assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsDeviceOffline).Error(), err.Error())
	})

	convey.Convey("设备不存在 / 凭据失效各有自己的文案", t, func() {
		r := newRig(t, 7, "/remote/work")
		r.rd.EXPECT().Pool().Return(r.pool)
		r.pool.EXPECT().Borrow(r.ctx, int64(7)).Return(nil, remote_device_svc.ErrDeviceNotFound)
		_, err := r.svc.GitBranches(r.ctx, 42)
		require.Error(t, err)
		assert.Equal(t, i18n.NewError(r.ctx, code.RemoteDeviceNotFound).Error(), err.Error())
	})
}

// ── GitBranches:currentBranch + defaultBaseline 过 RPC ─────────────────────

func TestGitBranches_RemoteCarriesCurrentBranchAndDefaultBaseline(t *testing.T) {
	convey.Convey("远端会话拿得到当前分支与推断出的默认基线", t, func() {
		r := newRig(t, 7, "/remote/work")
		r.expectCall(wire.MethodGitBranches, wire.GitBranchesReq{Root: "/remote/work"}).
			DoAndReturn(func(_ context.Context, _ string, _ any, out any) error {
				resp := out.(*wire.GitBranchesResp)
				resp.Branches = []wire.Branch{{Name: "main"}, {Name: "origin/main", Remote: true}}
				resp.CurrentBranch = "feature/x"
				resp.DefaultBaseline = "origin/main"
				return nil
			})

		view, err := r.svc.GitBranches(r.ctx, 42)
		require.NoError(t, err)
		assert.False(t, view.NotARepo)
		assert.Equal(t, "feature/x", view.CurrentBranch)
		assert.Equal(t, "origin/main", view.DefaultBaseline)
		require.Len(t, view.Branches, 2)
		assert.True(t, view.Branches[1].Remote)
	})
}

func TestGitBranches_LocalUsesLeafPackage(t *testing.T) {
	convey.Convey("本机会话的当前分支与默认基线同样来自叶子包", t, func() {
		dir := initRepo(t)
		runGit(t, dir, "checkout", "-q", "-b", "feature/x")
		r := newRig(t, 0, dir)

		view, err := r.svc.GitBranches(r.ctx, 42)
		require.NoError(t, err)
		assert.Equal(t, "feature/x", view.CurrentBranch)
		assert.Equal(t, "main", view.DefaultBaseline)
	})

	convey.Convey("非 git 目录 → NotARepo,不报错", t, func() {
		r := newRig(t, 0, t.TempDir())
		view, err := r.svc.GitBranches(r.ctx, 42)
		require.NoError(t, err)
		assert.True(t, view.NotARepo)
	})
}

// ── GitState:分支 / worktree / dirty / ahead·behind / commonDir ───────────

func TestGitState_RemoteCarriesRealSnapshot(t *testing.T) {
	convey.Convey("远端会话拿到与本地同形的 git 状态快照", t, func() {
		r := newRig(t, 7, "/remote/work")
		r.expectCall(wire.MethodGitState, wire.GitStateReq{Root: "/remote/work"}).
			DoAndReturn(func(_ context.Context, _ string, _ any, out any) error {
				resp := out.(*wire.GitStateResp)
				resp.Branch = "main"
				resp.Worktree = "feat-wt"
				resp.Dirty = 2
				resp.Ahead = 1
				resp.Behind = 3
				resp.HasUpstream = true
				resp.CommonDir = "/remote/work/.git"
				return nil
			})

		view, err := r.svc.GitState(r.ctx, 42, "")
		require.NoError(t, err)
		assert.False(t, view.NotARepo)
		assert.Equal(t, "main", view.Branch)
		assert.Equal(t, "feat-wt", view.Worktree)
		assert.Equal(t, 2, view.Dirty)
		assert.Equal(t, 1, view.Ahead)
		assert.Equal(t, 3, view.Behind)
		assert.True(t, view.HasUpstream)
		assert.Equal(t, "/remote/work/.git", view.CommonDir)
	})
}

func TestGitState_LocalUsesLeafPackage(t *testing.T) {
	convey.Convey("本机会话直接调叶子包,拿到真实分支与 commonDir", t, func() {
		dir := initRepo(t)
		r := newRig(t, 0, dir)

		view, err := r.svc.GitState(r.ctx, 42, "")
		require.NoError(t, err)
		assert.False(t, view.NotARepo)
		assert.Equal(t, "main", view.Branch)
		assert.NotEmpty(t, view.CommonDir)
	})

	convey.Convey("非 git 目录 → NotARepo,不报错", t, func() {
		r := newRig(t, 0, t.TempDir())
		view, err := r.svc.GitState(r.ctx, 42, "")
		require.NoError(t, err)
		assert.True(t, view.NotARepo)
	})
}

// TestGitState_RootOverridesSessionCwd 验证显式 root 覆盖会话解析出的默认
// cwd——这是任务 2「多工作根」要消费的口子:调用方可以问"另一个已认领 root
// 的 git 状态",而不是永远只能问会话自己的 cwd。覆盖的取值范围与 ListDir
// 一致(已认领的工作根集合),越界的那一半由
// TestRootParam_OutsideClaimedSetRefused 守着。
func TestGitState_RootOverridesSessionCwd(t *testing.T) {
	convey.Convey("显式 root 覆盖会话解析出的 cwd", t, func() {
		wr := newWorktreeRig(t)
		r := newRig(t, 0, wr.main)
		r.withWritten(filepath.Join(wr.wt, "pkg", "x.go"))

		mainView, err := r.svc.GitState(r.ctx, 42, "")
		require.NoError(t, err)
		assert.Equal(t, "main", mainView.Branch)
		assert.Empty(t, mainView.Worktree)

		wtView, err := r.svc.GitState(r.ctx, 42, wr.wt)
		require.NoError(t, err)
		assert.Equal(t, "side", wtView.Branch)
		assert.NotEmpty(t, wtView.Worktree)
		// 同一主仓库:两个根报回同一个 commonDir,这正是认领判定的依据。
		assert.Equal(t, mainView.CommonDir, wtView.CommonDir)
	})
}

// ── GitChanges:基线解析 ────────────────────────────────────────────────────

func TestGitChanges_ScopeValidation(t *testing.T) {
	convey.Convey("未知 scope → 参数错误,不解析会话", t, func() {
		r := newRig(t, 0, "/tmp")
		r.svc.resolver = func(context.Context, int64) (int64, string, error) {
			t.Fatal("resolver must not be called for an unknown scope")
			return 0, "", nil
		}
		_, err := r.svc.GitChanges(r.ctx, 42, "", "bogus", "")
		assert.Error(t, err)
	})
}

func TestGitChanges_UncommittedScope_SkipsBaselineLookup(t *testing.T) {
	convey.Convey("未提交档不需要基线 → 只发一次 RPC", t, func() {
		r := newRig(t, 7, "/remote/work")
		r.expectCall(wire.MethodGitChanges, wire.GitChangesReq{
			Root: "/remote/work", Scope: wire.ScopeUncommitted,
		}).DoAndReturn(func(_ context.Context, _ string, _ any, out any) error {
			resp := out.(*wire.GitChangesResp)
			resp.Changes = []wire.Change{{Path: "a.go", Status: "modified", Added: 3, Deleted: 1}}
			return nil
		})

		view, err := r.svc.GitChanges(r.ctx, 42, "", "uncommitted", "")
		require.NoError(t, err)
		assert.Empty(t, view.BaseRef, "未提交档没有基线可言")
		require.Len(t, view.Changes, 1)
		assert.Equal(t, "a.go", view.Changes[0].Path)
		assert.Equal(t, 3, view.Changes[0].Added)
	})
}

func TestGitChanges_BranchScope_BaselineResolution(t *testing.T) {
	branchesResp := func(defaultBaseline string, names ...string) func(context.Context, string, any, any) error {
		return func(_ context.Context, _ string, _ any, out any) error {
			resp := out.(*wire.GitBranchesResp)
			for _, n := range names {
				resp.Branches = append(resp.Branches, wire.Branch{Name: n})
			}
			resp.DefaultBaseline = defaultBaseline
			return nil
		}
	}

	convey.Convey("baseRef 为空 → 用远端推断出的默认基线,并回报实际用的那个", t, func() {
		r := newRig(t, 7, "/remote/work")
		r.expectCall(wire.MethodGitBranches, wire.GitBranchesReq{Root: "/remote/work"}).
			DoAndReturn(branchesResp("origin/main", "main", "origin/main"))
		r.expectCall(wire.MethodGitChanges, wire.GitChangesReq{
			Root: "/remote/work", Scope: wire.ScopeBranch, BaseRef: "origin/main",
		}).Return(nil)

		view, err := r.svc.GitChanges(r.ctx, 42, "", "branch", "")
		require.NoError(t, err)
		assert.Equal(t, "origin/main", view.BaseRef)
	})

	convey.Convey("用户选过的基线仍在清单里 → 优先于默认推断", t, func() {
		r := newRig(t, 7, "/remote/work")
		r.expectCall(wire.MethodGitBranches, wire.GitBranchesReq{Root: "/remote/work"}).
			DoAndReturn(branchesResp("main", "main", "develop"))
		r.expectCall(wire.MethodGitChanges, wire.GitChangesReq{
			Root: "/remote/work", Scope: wire.ScopeBranch, BaseRef: "develop",
		}).Return(nil)

		view, err := r.svc.GitChanges(r.ctx, 42, "", "branch", "develop")
		require.NoError(t, err)
		assert.Equal(t, "develop", view.BaseRef)
	})

	convey.Convey("持久化的基线已不存在 → 回落默认推断(设计决策 9)", t, func() {
		r := newRig(t, 7, "/remote/work")
		r.expectCall(wire.MethodGitBranches, wire.GitBranchesReq{Root: "/remote/work"}).
			DoAndReturn(branchesResp("main", "main"))
		r.expectCall(wire.MethodGitChanges, wire.GitChangesReq{
			Root: "/remote/work", Scope: wire.ScopeBranch, BaseRef: "main",
		}).Return(nil)

		view, err := r.svc.GitChanges(r.ctx, 42, "", "branch", "deleted-branch")
		require.NoError(t, err)
		assert.Equal(t, "main", view.BaseRef, "回落到默认基线而不是拿一个不存在的 ref 去算 merge-base")
	})

	convey.Convey("推断不出基线 → 成功返回空基线的空结果,由前端出 C5 空态", t, func() {
		r := newRig(t, 7, "/remote/work")
		r.expectCall(wire.MethodGitBranches, wire.GitBranchesReq{Root: "/remote/work"}).
			DoAndReturn(branchesResp("", "trunk"))
		// 不再发 gitChanges:空基线送过去只会换回 ErrBaselineRequired。

		view, err := r.svc.GitChanges(r.ctx, 42, "", "branch", "")
		require.NoError(t, err)
		assert.Empty(t, view.BaseRef)
		assert.Empty(t, view.Changes)
		assert.False(t, view.NotARepo)
	})

	convey.Convey("非 git 仓库 → NotARepo,不再问变动", t, func() {
		r := newRig(t, 7, "/remote/work")
		r.expectCall(wire.MethodGitBranches, wire.GitBranchesReq{Root: "/remote/work"}).
			DoAndReturn(func(_ context.Context, _ string, _ any, out any) error {
				out.(*wire.GitBranchesResp).NotARepo = true
				return nil
			})

		view, err := r.svc.GitChanges(r.ctx, 42, "", "branch", "")
		require.NoError(t, err)
		assert.True(t, view.NotARepo)
	})
}

func TestGitChanges_LocalBranchScope_EndToEnd(t *testing.T) {
	convey.Convey("本机会话:基线推断与变动计算都走叶子包", t, func() {
		dir := initRepo(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644))
		runGit(t, dir, "add", "a.txt")
		runGit(t, dir, "commit", "-q", "-m", "seed")
		runGit(t, dir, "checkout", "-q", "-b", "feature")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nmore\n"), 0o644))
		r := newRig(t, 0, dir)

		view, err := r.svc.GitChanges(r.ctx, 42, "", "branch", "")
		require.NoError(t, err)
		assert.Equal(t, "main", view.BaseRef, "本机同样按 origin/HEAD→main→master 推断")
		require.Len(t, view.Changes, 1)
		assert.Equal(t, "a.txt", view.Changes[0].Path)
		assert.Equal(t, "modified", view.Changes[0].Status)
		assert.Equal(t, 1, view.Changes[0].Added)
	})

	convey.Convey("本机会话:未提交档", t, func() {
		dir := initRepo(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("a\nb\n"), 0o644))
		r := newRig(t, 0, dir)

		view, err := r.svc.GitChanges(r.ctx, 42, "", "uncommitted", "")
		require.NoError(t, err)
		require.Len(t, view.Changes, 1)
		assert.Equal(t, "new.txt", view.Changes[0].Path)
		assert.Equal(t, "untracked", view.Changes[0].Status)
		assert.Equal(t, 2, view.Changes[0].Added)
	})
}

// ── ReadFile:按 deviceID 路由 + 视图字段透传 ───────────────────────────────

func TestReadFile_RoutesByDeviceID(t *testing.T) {
	convey.Convey("ReadFile 按 deviceID 路由", t, func() {
		convey.Convey("deviceID=0 → 本机 in-process,不借租约", func() {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644))
			r := newRig(t, 0, dir)
			// rd 上没有任何 EXPECT:一旦走了远端分支,gomock 会直接判错。

			view, err := r.svc.ReadFile(r.ctx, 42, "", "a.txt")
			require.NoError(t, err)
			assert.False(t, view.Binary)
			assert.False(t, view.TooLarge)
			assert.Equal(t, "hello\n", view.Content)
		})

		convey.Convey("deviceID≠0 → 走 RPC,root 用服务解析出的 cwd", func() {
			r := newRig(t, 7, "/remote/work")
			r.expectCall(wire.MethodReadFile, wire.ReadFileReq{Root: "/remote/work", RelPath: "a.txt"}).
				DoAndReturn(func(_ context.Context, _ string, _ any, out any) error {
					resp := out.(*wire.ReadFileResp)
					resp.Content = "remote body\n"
					return nil
				})

			view, err := r.svc.ReadFile(r.ctx, 42, "", "a.txt")
			require.NoError(t, err)
			assert.Equal(t, "remote body\n", view.Content)
			assert.False(t, view.Binary)
			assert.False(t, view.TooLarge)
		})
	})
}

func TestReadFile_ViewFlagsPassThrough(t *testing.T) {
	convey.Convey("binary / tooLarge / contentType 是视图字段,不是错误码", t, func() {
		convey.Convey("本机:含 NUL 的文件 → Binary 标志,不报错", func() {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "bin.dat"), []byte("a\x00b"), 0o644))
			r := newRig(t, 0, dir)

			view, err := r.svc.ReadFile(r.ctx, 42, "", "bin.dat")
			require.NoError(t, err)
			assert.True(t, view.Binary)
			assert.Empty(t, view.Content)
		})

		convey.Convey("远端:wire 字段原样透传(图片 base64 + contentType)", func() {
			r := newRig(t, 7, "/remote/work")
			r.expectCall(wire.MethodReadFile, wire.ReadFileReq{Root: "/remote/work", RelPath: "img.png"}).
				DoAndReturn(func(_ context.Context, _ string, _ any, out any) error {
					resp := out.(*wire.ReadFileResp)
					resp.Content = "aGVsbG8="
					resp.ContentType = "image/png"
					return nil
				})

			view, err := r.svc.ReadFile(r.ctx, 42, "", "img.png")
			require.NoError(t, err)
			assert.Equal(t, "aGVsbG8=", view.Content)
			assert.Equal(t, "image/png", view.ContentType)
		})
	})
}

func TestReadFile_ErrorMapping(t *testing.T) {
	convey.Convey("ReadFile 错误映射复用 20800 段", t, func() {
		convey.Convey("cwd 为空 → WorkspaceFsNoCwd,不借租约", func() {
			r := newRig(t, 7, "")
			_, err := r.svc.ReadFile(r.ctx, 42, "", "a.txt")
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsNoCwd).Error(), err.Error())
		})

		convey.Convey("本机越界 → WorkspaceFsPathRefused", func() {
			r := newRig(t, 0, t.TempDir())
			_, err := r.svc.ReadFile(r.ctx, 42, "", "../etc/passwd")
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsPathRefused).Error(), err.Error())
		})

		convey.Convey("远端越界 → 同一个 WorkspaceFsPathRefused", func() {
			r := newRig(t, 7, "/remote/work")
			r.expectCall(wire.MethodReadFile, gomock.Any()).
				Return(&rpcerror.Error{Code: wire.ErrCodePathRefused, Message: "refused"})
			_, err := r.svc.ReadFile(r.ctx, 42, "", "../etc/passwd")
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsPathRefused).Error(), err.Error())
		})

		convey.Convey("借不到租约 → WorkspaceFsDeviceOffline", func() {
			r := newRig(t, 7, "/remote/work")
			r.rd.EXPECT().Pool().Return(r.pool)
			r.pool.EXPECT().Borrow(r.ctx, int64(7)).Return(nil, errors.New("dial fail"))
			_, err := r.svc.ReadFile(r.ctx, 42, "", "a.txt")
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsDeviceOffline).Error(), err.Error())
		})
	})
}

// ── GitFileContent:按 deviceID 路由 + 错误映射 ──────────────────────────────

func TestGitFileContent_RoutesByDeviceID(t *testing.T) {
	convey.Convey("GitFileContent 按 deviceID 路由", t, func() {
		convey.Convey("deviceID=0 → 本机叶子包读 HEAD 版本", func() {
			dir := initRepo(t)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1\n"), 0o644))
			runGit(t, dir, "add", "a.txt")
			runGit(t, dir, "commit", "-q", "-m", "seed")
			// 工作区被改动后,对比档左列必须仍取 HEAD 版本,而不是工作区内容。
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2\n"), 0o644))
			r := newRig(t, 0, dir)

			view, err := r.svc.GitFileContent(r.ctx, 42, "", "a.txt")
			require.NoError(t, err)
			assert.False(t, view.NotARepo)
			assert.True(t, view.HasHead)
			assert.Equal(t, "v1\n", view.Content)
		})

		convey.Convey("deviceID=0 未跟踪 → 空基线标志,不报错", func() {
			dir := initRepo(t)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\n"), 0o644))
			r := newRig(t, 0, dir)

			view, err := r.svc.GitFileContent(r.ctx, 42, "", "new.txt")
			require.NoError(t, err)
			assert.False(t, view.NotARepo)
			assert.False(t, view.HasHead)
			assert.Empty(t, view.Content)
		})

		convey.Convey("deviceID=0 非 git 目录 → NotARepo,不报错", func() {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644))
			r := newRig(t, 0, dir)

			view, err := r.svc.GitFileContent(r.ctx, 42, "", "a.txt")
			require.NoError(t, err)
			assert.True(t, view.NotARepo)
			assert.False(t, view.HasHead)
			assert.Empty(t, view.Content)
		})

		convey.Convey("deviceID≠0 → 走 RPC,root 用服务解析出的 cwd", func() {
			r := newRig(t, 7, "/remote/work")
			r.expectCall(wire.MethodGitFileContent, wire.GitFileContentReq{Root: "/remote/work", RelPath: "a.txt"}).
				DoAndReturn(func(_ context.Context, _ string, _ any, out any) error {
					resp := out.(*wire.GitFileContentResp)
					resp.Content = "head body\n"
					resp.HasHead = true
					return nil
				})

			view, err := r.svc.GitFileContent(r.ctx, 42, "", "a.txt")
			require.NoError(t, err)
			assert.False(t, view.NotARepo)
			assert.True(t, view.HasHead)
			assert.Equal(t, "head body\n", view.Content)
		})
	})
}

func TestGitFileContent_ErrorMapping(t *testing.T) {
	convey.Convey("GitFileContent 错误映射复用 20800 段", t, func() {
		convey.Convey("本机越界 → WorkspaceFsPathRefused", func() {
			dir := initRepo(t)
			r := newRig(t, 0, dir)
			_, err := r.svc.GitFileContent(r.ctx, 42, "", "../outside")
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsPathRefused).Error(), err.Error())
		})

		convey.Convey("远端越界 → 同一个 WorkspaceFsPathRefused", func() {
			r := newRig(t, 7, "/remote/work")
			r.expectCall(wire.MethodGitFileContent, gomock.Any()).
				Return(&rpcerror.Error{Code: wire.ErrCodePathRefused, Message: "refused"})
			_, err := r.svc.GitFileContent(r.ctx, 42, "", "../etc/passwd")
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsPathRefused).Error(), err.Error())
		})

		convey.Convey("借不到租约 → WorkspaceFsDeviceOffline", func() {
			r := newRig(t, 7, "/remote/work")
			r.rd.EXPECT().Pool().Return(r.pool)
			r.pool.EXPECT().Borrow(r.ctx, int64(7)).Return(nil, errors.New("dial fail"))
			_, err := r.svc.GitFileContent(r.ctx, 42, "", "a.txt")
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsDeviceOffline).Error(), err.Error())
		})
	})
}

// ── SearchFiles:按 deviceID 路由 + 截断信号 + 错误映射 ──────────────────────

func TestSearchFiles_RoutesByDeviceID(t *testing.T) {
	convey.Convey("SearchFiles 按 deviceID 路由", t, func() {
		convey.Convey("deviceID=0 → 本机 in-process 递归遍历,不借租约", func() {
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "Target.go"), nil, 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), nil, 0o644))
			r := newRig(t, 0, dir)
			// rd 上没有任何 EXPECT:一旦走了远端分支,gomock 会直接判错。

			view, err := r.svc.SearchFiles(r.ctx, 42, "", "target", false)
			require.NoError(t, err)
			assert.False(t, view.Truncated)
			require.Len(t, view.Hits, 1)
			assert.Equal(t, "src/Target.go", view.Hits[0].Path)
			assert.False(t, view.Hits[0].IsDir)
		})

		convey.Convey("deviceID≠0 → 走 RPC,root 用服务解析出的 cwd,截断标志透传", func() {
			r := newRig(t, 7, "/remote/work")
			r.expectCallCtx(wire.MethodSearchFiles, wire.SearchFilesReq{
				Root: "/remote/work", Query: "target", IncludeIgnored: true,
			}).DoAndReturn(func(_ context.Context, _ string, _ any, out any) error {
				resp := out.(*wire.SearchFilesResp)
				resp.Hits = []wire.SearchHit{{Path: "src/target.go"}, {Path: "target-dir", IsDir: true}}
				resp.Truncated = true
				return nil
			})

			view, err := r.svc.SearchFiles(r.ctx, 42, "", "target", true)
			require.NoError(t, err)
			assert.True(t, view.Truncated)
			require.Len(t, view.Hits, 2)
			assert.Equal(t, "src/target.go", view.Hits[0].Path)
			assert.True(t, view.Hits[1].IsDir)
		})
	})
}

func TestSearchFiles_ErrorMapping(t *testing.T) {
	convey.Convey("SearchFiles 错误映射复用 20800 段", t, func() {
		convey.Convey("cwd 为空 → WorkspaceFsNoCwd,不借租约", func() {
			r := newRig(t, 7, "")
			_, err := r.svc.SearchFiles(r.ctx, 42, "", "target", false)
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsNoCwd).Error(), err.Error())
		})

		convey.Convey("借不到租约 → WorkspaceFsDeviceOffline", func() {
			r := newRig(t, 7, "/remote/work")
			r.rd.EXPECT().Pool().Return(r.pool)
			r.pool.EXPECT().Borrow(gomock.Any(), int64(7)).Return(nil, errors.New("dial fail"))
			_, err := r.svc.SearchFiles(r.ctx, 42, "", "target", false)
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(r.ctx, code.WorkspaceFsDeviceOffline).Error(), err.Error())
		})

		convey.Convey("本机遍历被 ctx 取消 → 读取失败,而不是一份看着完整的空结果", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			r := newRig(t, 0, t.TempDir())
			_, err := r.svc.SearchFiles(ctx, 42, "", "target", false)
			require.Error(t, err)
			assert.Equal(t, i18n.NewError(ctx, code.WorkspaceFsReadFailed).Error(), err.Error())
		})
	})
}

// ── 包级注入 ────────────────────────────────────────────────────────────────

func TestRegisterSessionWorkspaceResolver(t *testing.T) {
	convey.Convey("composition root 注入的 resolver 被 Default() 实例用上", t, func() {
		t.Cleanup(func() { RegisterSessionWorkspaceResolver(nil) })
		dir := t.TempDir()
		RegisterSessionWorkspaceResolver(func(_ context.Context, sessionID int64) (int64, string, error) {
			assert.Equal(t, int64(99), sessionID)
			return 0, dir, nil
		})

		view, err := Default().ListDir(context.Background(), 99, "", "", false)
		require.NoError(t, err)
		assert.Equal(t, dir, view.Path)
	})

	convey.Convey("没有注入 resolver → 报无工作目录,而不是 panic", t, func() {
		t.Cleanup(func() { RegisterSessionWorkspaceResolver(nil) })
		RegisterSessionWorkspaceResolver(nil)
		_, err := Default().ListDir(context.Background(), 99, "", "", false)
		assert.Error(t, err)
	})
}

// SelfFingerprint 满足 client.ProtobufConnection:这个假连接从没握过手,本端指纹为空。
func (c *workspaceProtoClient) SelfFingerprint() string { return "" }
