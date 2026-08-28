package chat_svc

import (
	"context"
	"sync"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
)

// bgRunningSet 是单会话「运行中后台 subagent 的 tool_use_id 集合」。用集合而非计数器：
// add/remove 幂等，杜绝加减泄漏。
type bgRunningSet struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func (s *chatSvc) bgSet(sessionID int64) *bgRunningSet {
	v, _ := s.bgRunning.LoadOrStore(sessionID, &bgRunningSet{ids: map[string]struct{}{}})
	return v.(*bgRunningSet)
}

// addBgRunning 把 ids 加入会话集合，有真正新增时返 true。
func (s *chatSvc) addBgRunning(sessionID int64, ids ...string) bool {
	if sessionID <= 0 || len(ids) == 0 {
		return false
	}
	set := s.bgSet(sessionID)
	set.mu.Lock()
	defer set.mu.Unlock()
	changed := false
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := set.ids[id]; !ok {
			set.ids[id] = struct{}{}
			changed = true
		}
	}
	return changed
}

// removeBgRunning 移除一个 id，有真正移除时返 true。
func (s *chatSvc) removeBgRunning(sessionID int64, id string) bool {
	if sessionID <= 0 || id == "" {
		return false
	}
	v, ok := s.bgRunning.Load(sessionID)
	if !ok {
		return false
	}
	set := v.(*bgRunningSet)
	set.mu.Lock()
	defer set.mu.Unlock()
	if _, ok := set.ids[id]; !ok {
		return false
	}
	delete(set.ids, id)
	return true
}

// clearBgRunning 清空会话集合，原本非空时返 true。
func (s *chatSvc) clearBgRunning(sessionID int64) bool {
	if sessionID <= 0 {
		return false
	}
	v, ok := s.bgRunning.Load(sessionID)
	if !ok {
		return false
	}
	set := v.(*bgRunningSet)
	set.mu.Lock()
	defer set.mu.Unlock()
	if len(set.ids) == 0 {
		return false
	}
	set.ids = map[string]struct{}{}
	return true
}

// bgRunningActive 报告会话是否有后台 subagent 在跑（集合非空）。
func (s *chatSvc) bgRunningActive(sessionID int64) bool {
	if sessionID <= 0 {
		return false
	}
	v, ok := s.bgRunning.Load(sessionID)
	if !ok {
		return false
	}
	set := v.(*bgRunningSet)
	set.mu.Lock()
	defer set.mu.Unlock()
	return len(set.ids) > 0
}

// emitBgRunningStatus 推一帧 session_status，携带当前 agentStatus/needsAttention + 最新
// bgRunning。后台 subagent 起/完成时调用，让前端 store 即时刷新。stream 为空则不 emit。
func (s *chatSvc) emitBgRunningStatus(ctx context.Context, sess *chat_entity.Session, stream string) {
	if sess == nil || stream == "" {
		return
	}
	s.emitter.Emit(ctx, stream, ChatStreamEvent{
		Kind: StreamSessionStatus,
		SessionStatus: &ChatSessionStatusPatch{
			AgentStatus:    sess.AgentStatus,
			NeedsAttention: sess.NeedsAttention,
			BgRunning:      s.bgRunningActive(sess.ID),
		},
	})
}

// reconcileBgRunningOnFinalize 在一轮 finalize 后，把该轮新起的后台 subagent 加入会话集合，
// 有变化则 emit session_status。主轮 / 自主轮 finalize 都调它。
func (s *chatSvc) reconcileBgRunningOnFinalize(ctx context.Context, sess *chat_entity.Session, finalBlocks []cagoblocks.ContentBlock, stream string) {
	if sess == nil {
		return
	}
	ids := runningBgSubagentIDs(finalBlocks)
	if s.addBgRunning(sess.ID, ids...) {
		s.emitBgRunningStatus(ctx, sess, stream)
	}
}

// reconcileBgRunningOnComplete 后台 subagent 完成时从集合移除，有变化则 emit。
func (s *chatSvc) reconcileBgRunningOnComplete(ctx context.Context, sess *chat_entity.Session, toolCallID, stream string) {
	if sess == nil {
		return
	}
	if s.removeBgRunning(sess.ID, toolCallID) {
		s.emitBgRunningStatus(ctx, sess, stream)
	}
}

// runningBgSubagentIDs 从一批已 finalize 的块里挑出「运行中后台 subagent」的父 tool_use_id。
// 判据与前端 background-tasks/derive.ts 同款：SubagentStateBlock.Status=="running" 且
// isBackgroundSubagent(kind, 父 tool_use 入参) 为真。前台 / 同步 subagent 不纳入。
func runningBgSubagentIDs(finalBlocks []cagoblocks.ContentBlock) []string {
	inputByToolUse := map[string]map[string]any{}
	for _, b := range finalBlocks {
		switch tu := b.(type) {
		case *cagoblocks.ToolUseBlock:
			inputByToolUse[tu.ID] = tu.Input
		case cagoblocks.ToolUseBlock:
			inputByToolUse[tu.ID] = tu.Input
		}
	}
	var out []string
	for _, b := range finalBlocks {
		sb, ok := b.(*blocks.SubagentStateBlock)
		if !ok || sb.Status != "running" || sb.ParentToolCallID == "" {
			continue
		}
		if isBackgroundSubagent(sb.Kind, inputByToolUse[sb.ParentToolCallID]) {
			out = append(out, sb.ParentToolCallID)
		}
	}
	return out
}

// isBackgroundSubagent 判定一个 subagent 是否「后台」。两种工具默认相反,判据按 kind
// 分叉(与前端 background-tasks/derive.ts 的 isBackground 同款):
//   - local_bash(Bash)默认前台,仅工具入参 run_in_background===true 为后台;
//   - local_agent(subagent / Agent 工具)默认后台,run_in_background 缺省即后台,
//     仅显式 run_in_background===false 才是前台(同步)。
//
// 空/未知 kind 不纳入,与前端 derive 的 kind 白名单一致。
func isBackgroundSubagent(kind string, input map[string]any) bool {
	rib, present := input["run_in_background"].(bool)
	switch kind {
	case "local_bash":
		return present && rib
	case "local_agent":
		return !present || rib
	default:
		return false
	}
}

// clearBgRunningOnSourceClosed 后台活动 channel 关闭(子进程 evict/CloseSession)时清空会话
// 集合——CLI 子进程死了它派的后台 subagent 也都死了，防止 bgRunning 永久泄漏。
func (s *chatSvc) clearBgRunningOnSourceClosed(sessionID int64) {
	s.clearBgRunning(sessionID)
}
