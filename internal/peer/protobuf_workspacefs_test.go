package peer

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	workspacewire "github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 控制台的文件预览按**会话目标机**拨号,不筛 kind(filePreviewPorts.ts:117 / :137
// 用的是详情页那条通道)。桌面端不挂 workspacefs 的话,用户在桌面机器的会话里点开
// 任何一个文件,收到的都是 -32601 —— 与「这个文件是空的」在界面上分不出来。
//
// 断言打在**生产装配**上(productionProtobufInboundDeps + NewProtobufInboundRegistry),
// 不是测试自己拼的端口集:漏挂端口正是这条缺陷的形状,测试若自己装配就永远看不见它
// (与 protobuf_transcriptimport_test.go / protobuf_engine_test.go 同一条理由)。

// requireWorkspaceFSRefusal 断言这次调用是被 workspacefs 自己的拒绝语挡下的,
// 而不是被「这台机器不认识这个方法」挡下的。
//
// 少了中间那一句,两条边界用例在缺陷仍在时会照样绿:方法没挂上时它们同样拿得到
// 一个 error。
func requireWorkspaceFSRefusal(t *testing.T, err error, wantCode int32, message string) {
	t.Helper()
	require.Error(t, err, message)
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr, "拒绝没有走 typed RPC error,调用方读不出是哪一类")
	require.NotEqual(t, protorpc.CodeMethodNotFound, rpcErr.Code,
		"这条用例是被 -32601 满足的,不是被真正的拒绝满足的")
	require.Equal(t, wantCode, rpcErr.Code, message)
}

func peerTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // G204: 测试内常量参数,不含外部输入
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestProductionInboundServesWorkspaceFSReadFile 钉住「控制台在桌面机器的会话里
// 点开一个文件,读得出正文」。
func TestProductionInboundServesWorkspaceFSReadFile(t *testing.T) {
	ctx, client := dialProductionInbound(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.md"), []byte("# hello\n"), 0o600))

	got, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE),
		&agentrewire.WorkspaceFsReadFileRequest{Root: root, RelPath: "notes.md"},
		func() *agentrewire.WorkspaceFsReadFileResponse { return &agentrewire.WorkspaceFsReadFileResponse{} })

	require.NoError(t, err, "桌面端答不出 workspacefs.readFile:控制台在桌面机器上点开的每一个文件都是空的")
	require.Equal(t, "# hello\n", string(got.GetContent()), "正文没过得了线")
	require.False(t, got.GetBinary())
	require.False(t, got.GetTooLarge())
}

// 越界的 relPath 要被 workspacefs 自己的路径闸门挡下(20800 段的 pathRefused),
// 而不是被读出来。这条同时是「挂上的是那一份真 handler」的证据:一个只回空结果的
// 假实现不会给出这个码。
func TestProductionInboundRefusesWorkspaceFSEscapingRelPath(t *testing.T) {
	ctx, client := dialProductionInbound(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(root), "outside.txt"), []byte("secret\n"), 0o600))

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE),
		&agentrewire.WorkspaceFsReadFileRequest{Root: root, RelPath: "../outside.txt"},
		func() *agentrewire.WorkspaceFsReadFileResponse { return &agentrewire.WorkspaceFsReadFileResponse{} })

	requireWorkspaceFSRefusal(t, err, workspacewire.ErrCodePathRefused,
		"逃出 root 的 relPath 没有被路径闸门挡下")
}

// TestProductionInboundServesWorkspaceFSGitFileContent 钉住对比档左列:同一个文件
// 在 HEAD 的那一版要读得出来,且是**提交时**那份内容而不是工作区里改过的那份 ——
// 拿错一边的话,对比视图会显示成「没有任何改动」。
func TestProductionInboundServesWorkspaceFSGitFileContent(t *testing.T) {
	ctx, client := dialProductionInbound(t)
	root := t.TempDir()
	peerTestGit(t, root, "init", "-q", "-b", "main")
	peerTestGit(t, root, "config", "user.email", "t@t")
	peerTestGit(t, root, "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("committed\n"), 0o600))
	peerTestGit(t, root, "add", "a.txt")
	peerTestGit(t, root, "commit", "-q", "-m", "seed")
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("working copy\n"), 0o600))

	got, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_FILE_CONTENT),
		&agentrewire.WorkspaceFsGitFileContentRequest{Root: root, RelPath: "a.txt"},
		func() *agentrewire.WorkspaceFsGitFileContentResponse {
			return &agentrewire.WorkspaceFsGitFileContentResponse{}
		})

	require.NoError(t, err, "桌面端答不出 workspacefs.gitFileContent:控制台的对比档在桌面机器上没有左列")
	require.False(t, got.GetNotARepo())
	require.True(t, got.GetHasHead(), "文件明明在 HEAD 里,却被答成空基线")
	require.Equal(t, "committed\n", string(got.GetContent()), "拿回来的是工作区那份,不是 HEAD 那份")
}

// root 为空是会话还没解析出 cwd,与「路径越界」是两回事:两者共用一个码的话,
// 控制台只能给出一句笼统的「读不了」。
func TestProductionInboundRejectsWorkspaceFSGitFileContentWithoutCwd(t *testing.T) {
	ctx, client := dialProductionInbound(t)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_FILE_CONTENT),
		&agentrewire.WorkspaceFsGitFileContentRequest{RelPath: "a.txt"},
		func() *agentrewire.WorkspaceFsGitFileContentResponse {
			return &agentrewire.WorkspaceFsGitFileContentResponse{}
		})

	requireWorkspaceFSRefusal(t, err, workspacewire.ErrCodeNoCwd,
		"空 cwd 没有被答成 noCwd")
}
