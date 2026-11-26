package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
)

// stubWorkspaceFs 只实现认领相关的两个方法;嵌入的接口留 nil,碰到别的方法当场 panic。
type stubWorkspaceFs struct {
	workspace_fs_svc.WorkspaceFsSvc

	roots    []workspace_fs_svc.WorkRootView
	removed  []workspace_fs_svc.WorkRootView
	claimErr error
	calls    []string

	gotClaim struct {
		sessionID  int64
		candidates []string
	}
}

func (s *stubWorkspaceFs) Claim(_ context.Context, sessionID int64, candidates []string) ([]workspace_fs_svc.WorkRootView, error) {
	s.calls = append(s.calls, "Claim")
	s.gotClaim.sessionID, s.gotClaim.candidates = sessionID, candidates
	return nil, s.claimErr
}

func (s *stubWorkspaceFs) WorkRoots(_ context.Context, sessionID int64) ([]workspace_fs_svc.WorkRootView, error) {
	s.calls = append(s.calls, "WorkRoots")
	return s.roots, nil
}

func (s *stubWorkspaceFs) RefreshWorkRoots(_ context.Context, sessionID int64) ([]workspace_fs_svc.WorkRootView, error) {
	s.calls = append(s.calls, "RefreshWorkRoots")
	return s.roots, nil
}

func withStubWorkspaceFs(t *testing.T, stub *stubWorkspaceFs) *App {
	t.Helper()
	original := workspace_fs_svc.Default()
	t.Cleanup(func() { workspace_fs_svc.RegisterWorkspaceFs(original) })
	workspace_fs_svc.RegisterWorkspaceFs(stub)
	return &App{ctx: context.Background()}
}

// 用户点击一个绝对路径:把它作为认领候选交给服务层,再交回认领之后的完整工作根集合,
// 前端据此找包含该路径的最深的根去预览(已认领时同样能找到)。
func TestWorkspaceFsClaimPath_ClaimsThenReturnsWorkRoots(t *testing.T) {
	roots := []workspace_fs_svc.WorkRootView{
		{Path: "/repo", Name: "repo", IsPrimary: true},
		{Path: "/repo/.worktrees/x", Name: "x", IsWorktree: true},
	}
	stub := &stubWorkspaceFs{roots: roots}
	a := withStubWorkspaceFs(t, stub)

	got, err := a.WorkspaceFsClaimPath(42, "/repo/.worktrees/x/a.go")
	require.NoError(t, err)
	assert.Equal(t, roots, got)
	assert.Equal(t, []string{"Claim", "WorkRoots"}, stub.calls)
	assert.Equal(t, int64(42), stub.gotClaim.sessionID)
	assert.Equal(t, []string{"/repo/.worktrees/x/a.go"}, stub.gotClaim.candidates)
}

func TestWorkspaceFsClaimPath_ClaimErrorSurfaces(t *testing.T) {
	stub := &stubWorkspaceFs{claimErr: errors.New("disk full")}
	a := withStubWorkspaceFs(t, stub)

	_, err := a.WorkspaceFsClaimPath(42, "/repo/.worktrees/x/a.go")
	require.Error(t, err)
	assert.Equal(t, []string{"Claim"}, stub.calls, "认领失败时不再取集合")
}

// 界面取工作根(挂载 / 轮次结束)走重验的那条:进程里被删掉的 worktree 由此出集合
// (spec「worktree 被移除」)。
func TestWorkspaceFsWorkRoots_ReverifiesClaimedRoots(t *testing.T) {
	roots := []workspace_fs_svc.WorkRootView{{Path: "/repo", Name: "repo", IsPrimary: true}}
	stub := &stubWorkspaceFs{roots: roots}
	a := withStubWorkspaceFs(t, stub)

	got, err := a.WorkspaceFsWorkRoots(42)
	require.NoError(t, err)
	assert.Equal(t, roots, got)
	assert.Equal(t, []string{"RefreshWorkRoots", "RemovedWorkRoots"}, stub.calls)
}

func (s *stubWorkspaceFs) RemovedWorkRoots(_ context.Context, sessionID int64) ([]workspace_fs_svc.WorkRootView, error) {
	s.calls = append(s.calls, "RemovedWorkRoots")
	return s.removed, nil
}

// 界面取工作根时同时拿到从集合中消失的根(带 removed 标记,排在集合之后):
// 会话没开着时被移除的根,前端只能从这里知道它曾是一个根(spec「worktree 被移除」的
// 「（已移除）」分组)。
func TestWorkspaceFsWorkRoots_AppendsRemovedRootsAfterTheSet(t *testing.T) {
	roots := []workspace_fs_svc.WorkRootView{{Path: "/repo", Name: "repo", IsPrimary: true}}
	removed := []workspace_fs_svc.WorkRootView{{Path: "/repo/.claude/worktrees/x", Name: "x", IsWorktree: true, Removed: true}}
	stub := &stubWorkspaceFs{roots: roots, removed: removed}
	a := withStubWorkspaceFs(t, stub)

	got, err := a.WorkspaceFsWorkRoots(42)
	require.NoError(t, err)
	assert.Equal(t, append(append([]workspace_fs_svc.WorkRootView(nil), roots...), removed...), got)
	assert.Equal(t, []string{"RefreshWorkRoots", "RemovedWorkRoots"}, stub.calls,
		"the removed list is read after the re-verification that may add to it")
}
