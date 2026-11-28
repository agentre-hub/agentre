package canonical

import (
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/diff"
)

// NewFileWrite 构造带上限截断的 FileWrite(字节上限防止 GB 级写入撑爆事件序列化)。
// 各 runtime 的实时识别与 FromToolUse 重算共用。
func NewFileWrite(path, content string) FileWrite {
	bytes := len(content)
	truncated := false
	if bytes > WriteContentByteCap {
		content = content[:WriteContentByteCap]
		truncated = true
	}
	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n")
		if !strings.HasSuffix(content, "\n") {
			lines++
		}
	}
	return FileWrite{
		Path:      path,
		Content:   content,
		Lines:     lines,
		Bytes:     bytes,
		Truncated: truncated,
	}
}

// hermesPatchFromInput 识别 Hermes 的 `patch` 工具(mode=replace)为 FileEdit。其它
// mode(create/append/multi_replace)的 wire 形状无法干净映射成单个 diff,半成品的
// DiffCard 不如原始工具卡,故不识别。
func hermesPatchFromInput(input map[string]any) (CanonicalTool, bool) {
	if mode, _ := input["mode"].(string); mode != "replace" {
		return nil, false
	}
	path, _ := input["path"].(string)
	oldString, _ := input["old_string"].(string)
	newString, _ := input["new_string"].(string)
	if strings.TrimSpace(path) == "" || oldString == "" {
		return nil, false
	}
	payload := diff.FromEdit(map[string]any{
		"file_path":  path,
		"old_string": oldString,
		"new_string": newString,
	})
	totalHunks := 0
	for _, f := range payload.Files {
		totalHunks += len(f.Hunks)
	}
	if len(payload.Files) == 0 || totalHunks == 0 {
		return nil, false
	}
	return FileEdit{Files: PatchesFromDiff(payload)}, true
}

// ACPKindInputKey / ACPPathsInputKey 是 ACP translator 写进工具入参的标记:ACP 落库
// 的工具名是 title(任意文案),kind 与 locations 不落库,重算只能读这两个键。
const (
	ACPKindInputKey  = "acp_kind"
	ACPPathsInputKey = "acp_paths"
)

// ACPEditFromCall 把 ACP 编辑类调用(kind = edit / delete / move)映射成 canonical:
// delete → FileEdit(deleted);edit 且入参带完整 content → FileWrite;其余 →
// FileEdit(modified,ACP 的 hunk 在 tool_call_update 里,入参没有 diff)。
// paths 为空或 kind 不是编辑类时不识别。实时识别与 FromToolUse 重算共用。
func ACPEditFromCall(kind string, paths []string, input map[string]any) (CanonicalTool, bool) {
	switch kind {
	case "edit", "delete", "move":
	default:
		return nil, false
	}
	if len(paths) == 0 {
		return nil, false
	}
	if kind == "delete" {
		files := make([]FileEditPatch, 0, len(paths))
		for _, p := range paths {
			files = append(files, FileEditPatch{Path: p, Kind: ChangeDeleted})
		}
		return FileEdit{Files: files}, true
	}
	if content, ok := input["content"].(string); ok && kind == "edit" {
		return NewFileWrite(paths[0], content), true
	}
	files := make([]FileEditPatch, 0, len(paths))
	for _, p := range paths {
		files = append(files, FileEditPatch{Path: p, Kind: ChangeModified})
	}
	return FileEdit{Files: files}, true
}

// ACPPathsFromInput 读 ACP 入参里的路径:优先 translator 写入的 ACPPathsInputKey,
// 否则取 file_path / path / codex 风格 changes[].path 里的第一个。
func ACPPathsFromInput(m map[string]any) []string {
	if raw, ok := m[ACPPathsInputKey].([]any); ok {
		out := make([]string, 0, len(raw))
		for _, r := range raw {
			if s, ok := r.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if p := ACPInputPath(m); p != "" {
		return []string{p}
	}
	return nil
}

// ACPInputPath 从 rawInput 的键里取路径:file_path、path,或 codex 风格 changes[].path 的第一个。
func ACPInputPath(m map[string]any) string {
	for _, key := range []string{"file_path", "path"} {
		if s, ok := m[key].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	if changes, ok := m["changes"].([]any); ok {
		for _, raw := range changes {
			c, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := c["path"].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}
