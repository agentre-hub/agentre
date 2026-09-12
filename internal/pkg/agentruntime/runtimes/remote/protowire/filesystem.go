package protowire

import (
	"encoding/base64"
	"fmt"
	"strings"

	remotewire "github.com/agentre-hub/agentre/internal/pkg/remotefs/wire"
	workspacewire "github.com/agentre-hub/agentre/internal/pkg/workspacefs/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func RemoteFsListResponseToProto(value remotewire.ListDirResp) *agentrewire.RemoteFsListDirResponse {
	out := &agentrewire.RemoteFsListDirResponse{Path: value.Path, Truncated: value.Truncated}
	for _, entry := range value.Entries {
		out.Entries = append(out.Entries, &agentrewire.RemoteFsEntry{Name: entry.Name, IsDir: entry.IsDir, Size: entry.Size, ModTime: entry.ModTime, Symlink: entry.Symlink})
	}
	return out
}
func RemoteFsListResponseFromProto(value *agentrewire.RemoteFsListDirResponse) remotewire.ListDirResp {
	out := remotewire.ListDirResp{Path: value.GetPath(), Truncated: value.GetTruncated(), Entries: make([]remotewire.Entry, 0, len(value.GetEntries()))}
	for _, entry := range value.GetEntries() {
		out.Entries = append(out.Entries, remotewire.Entry{Name: entry.GetName(), IsDir: entry.GetIsDir(), Size: entry.GetSize(), ModTime: entry.GetModTime(), Symlink: entry.GetSymlink()})
	}
	return out
}

func WorkspaceReadFileResponseToProto(value workspacewire.ReadFileResp) (*agentrewire.WorkspaceFsReadFileResponse, error) {
	content := []byte(value.Content)
	if strings.HasPrefix(value.ContentType, "image/") && value.Content != "" {
		decoded, err := base64.StdEncoding.DecodeString(value.Content)
		if err != nil {
			return nil, fmt.Errorf("protowire: decode image content: %w", err)
		}
		content = decoded
	}
	return &agentrewire.WorkspaceFsReadFileResponse{Content: content, ContentType: value.ContentType, Binary: value.Binary, TooLarge: value.TooLarge}, nil
}
func WorkspaceReadFileResponseFromProto(value *agentrewire.WorkspaceFsReadFileResponse) workspacewire.ReadFileResp {
	content := string(value.GetContent())
	if strings.HasPrefix(value.GetContentType(), "image/") && len(value.GetContent()) > 0 {
		content = base64.StdEncoding.EncodeToString(value.GetContent())
	}
	return workspacewire.ReadFileResp{Content: content, ContentType: value.GetContentType(), Binary: value.GetBinary(), TooLarge: value.GetTooLarge()}
}

func WorkspaceGitChangesResponseToProto(value workspacewire.GitChangesResp) *agentrewire.WorkspaceFsGitChangesResponse {
	out := &agentrewire.WorkspaceFsGitChangesResponse{NotARepo: value.NotARepo, Truncated: value.Truncated}
	for _, change := range value.Changes {
		out.Changes = append(out.Changes, &agentrewire.WorkspaceFsChange{Path: change.Path, OldPath: change.OldPath, Status: change.Status, Added: int32(change.Added), Deleted: int32(change.Deleted), Binary: change.Binary})
	}
	return out
}
func WorkspaceGitChangesResponseFromProto(value *agentrewire.WorkspaceFsGitChangesResponse) workspacewire.GitChangesResp {
	out := workspacewire.GitChangesResp{NotARepo: value.GetNotARepo(), Truncated: value.GetTruncated(), Changes: make([]workspacewire.Change, 0, len(value.GetChanges()))}
	for _, change := range value.GetChanges() {
		out.Changes = append(out.Changes, workspacewire.Change{Path: change.GetPath(), OldPath: change.GetOldPath(), Status: change.GetStatus(), Added: int(change.GetAdded()), Deleted: int(change.GetDeleted()), Binary: change.GetBinary()})
	}
	return out
}
func WorkspaceGitStateResponseToProto(value workspacewire.GitStateResp) *agentrewire.WorkspaceFsGitStateResponse {
	return &agentrewire.WorkspaceFsGitStateResponse{NotARepo: value.NotARepo, Branch: value.Branch, Worktree: value.Worktree, Dirty: int32(value.Dirty), Ahead: int32(value.Ahead), Behind: int32(value.Behind), HasUpstream: value.HasUpstream, CommonDir: value.CommonDir}
}
func WorkspaceGitStateResponseFromProto(value *agentrewire.WorkspaceFsGitStateResponse) workspacewire.GitStateResp {
	return workspacewire.GitStateResp{NotARepo: value.GetNotARepo(), Branch: value.GetBranch(), Worktree: value.GetWorktree(), Dirty: int(value.GetDirty()), Ahead: int(value.GetAhead()), Behind: int(value.GetBehind()), HasUpstream: value.GetHasUpstream(), CommonDir: value.GetCommonDir()}
}
