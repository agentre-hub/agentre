package chat_svc

import (
	"context"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

// SessionWrittenPaths 列出本会话里 AI 写过的文件路径,按首次出现顺序去重。
//
// 它实现 workspace_fs_svc 自己声明的 SessionWrittenPathsResolver 窄接口(由
// bootstrap 注入):工作根认领的第一个条件是"AI 的写入路径落在当前已认领的根
// 之外",而"哪些路径被写过"只有持有 chat 消息的这一侧答得出。放在 chat_svc 与
// ResolveSessionWorkspace 同源 —— workspace_fs_svc 因此不必跨域去读 chat 表。
//
// 判定口径是 canonical:只有能被 canonical.FromToolUse 归一成 file.write /
// file.edit 的工具调用才算写入,Read / Bash 一类只读或不可解析的调用不产生
// 路径。三家后端(claudecode / codex / pi)的工具名差异已经在那一层收敛,这里
// 不再各认一套名字。subagent 的嵌套调用同样计入 —— 那也是 AI 的写入。
//
// 返回的是工具调用里的原始路径:多数后端给绝对路径,少数给相对路径。这里不做
// 归一,因为"相对谁"要由持有 cwd 的调用方决定(相对路径天然落在会话 cwd 内,
// 认领不到新的工作根)。
func SessionWrittenPaths(ctx context.Context, sessionID int64) ([]string, error) {
	if sessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	msgs, err := chat_repo.Message().List(ctx, sessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}

	seen := make(map[string]struct{})
	paths := make([]string, 0, 8)
	add := func(p string) {
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}

	for _, m := range msgs {
		bs, berr := m.GetBlocks()
		if berr != nil {
			// 单条消息解码失败不致命:少认一条写入路径,顶多少认领一个工作根
			// (集合外仍然一律拒绝),不该让整个侧栏取数失败。
			logger.Ctx(ctx).Warn("chat_svc.SessionWrittenPaths: decode blocks failed",
				zap.Int64("sessionID", sessionID), zap.Int64("messageID", m.ID), zap.Error(berr))
			continue
		}
		for _, b := range bs {
			name, input, ok := toolUseOfBlock(b)
			if !ok {
				continue
			}
			c, ok := canonical.FromToolUse(name, input)
			if !ok {
				continue
			}
			for _, p := range writtenPathsOfCanonical(c) {
				add(p)
			}
		}
	}
	return paths, nil
}

// toolUseOfBlock 把一个持久化块还原成 (工具名, 入参)。外层 tool_use 与
// subagent 的 nested_tool_use 都算 —— 值/指针两种形态都要认,持久化解码出来
// 的具体形态取决于注册表的工厂(与 toChatMessage 的双 case 同一原因)。
func toolUseOfBlock(b blocks.ContentBlock) (string, map[string]any, bool) {
	switch tb := b.(type) {
	case blocks.ToolUseBlock:
		return tb.Name, tb.Input, true
	case *blocks.ToolUseBlock:
		if tb != nil {
			return tb.Name, tb.Input, true
		}
	case chatblocks.NestedToolUseBlock:
		return tb.Name, tb.Input, true
	case *chatblocks.NestedToolUseBlock:
		if tb != nil {
			return tb.Name, tb.Input, true
		}
	}
	return "", nil, false
}

// writtenPathsOfCanonical 取一次 canonical 调用写到的路径:file.edit 一次可以
// 带多个文件(MultiEdit / apply_patch),file.write 恒是一个。其余 canonical
// 种类(计划更新 / 子 agent 派发等)不写文件。
func writtenPathsOfCanonical(c canonical.CanonicalTool) []string {
	switch t := c.(type) {
	case canonical.FileWrite:
		return []string{t.Path}
	case *canonical.FileWrite:
		if t != nil {
			return []string{t.Path}
		}
	case canonical.FileEdit:
		return fileEditPaths(t.Files)
	case *canonical.FileEdit:
		if t != nil {
			return fileEditPaths(t.Files)
		}
	}
	return nil
}

func fileEditPaths(files []canonical.FileEditPatch) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}
