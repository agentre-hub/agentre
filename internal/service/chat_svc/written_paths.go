package chat_svc

import (
	"context"
	"slices"
	"sync"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	chatblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
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
//
// 增量维护(spec 2026-11-05 决策 4):WorkRoots 每次轮末都要这份清单,而会话的工具历史
// 绝大多数自上一轮起没变过。这里只取每条消息的 (id, 版本),按会话缓存每条消息写过的
// 路径;只有版本变了的(新增 / 被改写)消息重读工具调用块,不在列的(被删 / 截断)直接
// 丢掉。版本由仓储在每次写入时于写锁内签发,所以「版本没变」就是「块没变」。
func SessionWrittenPaths(ctx context.Context, sessionID int64) ([]string, error) {
	if sessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	repo := transcript_repo.Message()
	versions, err := repo.ListVersions(ctx, sessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	prev := writtenPathsCache.get(sessionID)
	var stale []*chat_entity.Message
	for _, v := range versions {
		if prev != nil {
			if cached, ok := prev[v.ID]; ok && cached.version == v.Version {
				continue
			}
		}
		stale = append(stale, &chat_entity.Message{ID: v.ID, SessionID: sessionID})
	}
	// 写入路径只会出现在工具调用块里:按类型点查,其余正文不进内存。
	fresh := make(map[int64][]string, len(stale))
	if len(stale) > 0 {
		if err := repo.FillBlocksByType(ctx, stale, toolUseBlockTypes); err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
		for _, m := range stale {
			fresh[m.ID] = writtenPathsOfMessage(ctx, sessionID, m)
		}
	}

	next := make(map[int64]messageWrittenPaths, len(versions))
	seen := make(map[string]struct{})
	paths := make([]string, 0, 8)
	for _, v := range versions {
		ps, ok := fresh[v.ID]
		if !ok {
			ps = prev[v.ID].paths
		}
		next[v.ID] = messageWrittenPaths{version: v.Version, paths: ps}
		for _, p := range ps {
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			paths = append(paths, p)
		}
	}
	writtenPathsCache.put(sessionID, next)
	return paths, nil
}

// writtenPathsOfMessage 取一条(已按工具调用类型补过块的)消息里写到的路径,保序、不去重。
func writtenPathsOfMessage(ctx context.Context, sessionID int64, m *chat_entity.Message) []string {
	bs, err := m.GetBlocks()
	if err != nil {
		// 单条消息解码失败不致命:少认一条写入路径,顶多少认领一个工作根
		// (集合外仍然一律拒绝),不该让整个侧栏取数失败。
		logger.Ctx(ctx).Warn("chat_svc.SessionWrittenPaths: decode blocks failed",
			zap.Int64("sessionID", sessionID), zap.Int64("messageID", m.ID), zap.Error(err))
		return nil
	}
	var out []string
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
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// messageWrittenPaths 是一条消息在某个版本下写过的路径。
type messageWrittenPaths struct {
	version int64
	paths   []string
}

// writtenPathsCacheSessions 是写过路径缓存最多记住几条会话。缓存的是「侧栏正开着的
// 那几条会话」,超出时丢掉最久没问过的那条 —— 被丢掉的下次问时整条重读一次,结论不变。
const writtenPathsCacheSessions = 64

// writtenPathsCache 按会话记着每条消息写过的路径。存进去的 map 不再被改写(每次都换
// 新的一份),读出来的那份可以在锁外使用。
var writtenPathsCache = &sessionWrittenPathsCache{}

type sessionWrittenPathsCache struct {
	mu        sync.Mutex
	bySession map[int64]map[int64]messageWrittenPaths
	recent    []int64 // 最久没问过的在前
}

func (c *sessionWrittenPathsCache) get(sessionID int64) map[int64]messageWrittenPaths {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bySession[sessionID]
}

func (c *sessionWrittenPathsCache) put(sessionID int64, entry map[int64]messageWrittenPaths) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bySession == nil {
		c.bySession = make(map[int64]map[int64]messageWrittenPaths)
	}
	c.recent = slices.DeleteFunc(c.recent, func(id int64) bool { return id == sessionID })
	c.recent = append(c.recent, sessionID)
	c.bySession[sessionID] = entry
	for len(c.recent) > writtenPathsCacheSessions {
		delete(c.bySession, c.recent[0])
		c.recent = c.recent[1:]
	}
}

// toolUseBlockTypes 是 toolUseOfBlock 认得的两类块的存储类型,两处须同步。
var toolUseBlockTypes = []string{blocks.ToolUseBlock{}.Type(), chatblocks.NestedToolUseBlock{}.Type()}

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
