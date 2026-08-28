package protowire

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	remotewire "github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	workspacewire "github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
)

func TestRemoteFsListResponseRoundTrip(t *testing.T) {
	want := remotewire.ListDirResp{Path: "/home", Truncated: true, Entries: []remotewire.Entry{{Name: "a", IsDir: true, Size: 7, ModTime: 9, Symlink: true}}}
	require.Equal(t, want, RemoteFsListResponseFromProto(RemoteFsListResponseToProto(want)))
}

func TestWorkspaceReadFileUsesRawBinaryBytes(t *testing.T) {
	raw := []byte{0, 1, 255}
	legacy := workspacewire.ReadFileResp{Content: base64.StdEncoding.EncodeToString(raw), ContentType: "image/png"}
	pb, err := WorkspaceReadFileResponseToProto(legacy)
	require.NoError(t, err)
	require.Equal(t, raw, pb.Content)
	got := WorkspaceReadFileResponseFromProto(pb)
	require.Equal(t, legacy, got)

	text := workspacewire.ReadFileResp{Content: "你好", ContentType: "text/plain"}
	pb, err = WorkspaceReadFileResponseToProto(text)
	require.NoError(t, err)
	require.Equal(t, []byte("你好"), pb.Content)
	require.Equal(t, text, WorkspaceReadFileResponseFromProto(pb))
}

func TestWorkspaceGitShapesRoundTrip(t *testing.T) {
	changes := workspacewire.GitChangesResp{Changes: []workspacewire.Change{{Path: "a", OldPath: "b", Status: "renamed", Added: 2, Deleted: 1, Binary: true}}, Truncated: true}
	require.Equal(t, changes, WorkspaceGitChangesResponseFromProto(WorkspaceGitChangesResponseToProto(changes)))
	state := workspacewire.GitStateResp{Branch: "main", Worktree: "repo", Dirty: 2, Ahead: 1, Behind: 3, HasUpstream: true, CommonDir: "/git"}
	require.Equal(t, state, WorkspaceGitStateResponseFromProto(WorkspaceGitStateResponseToProto(state)))
}
