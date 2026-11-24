package chat_svc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/shellloc"
	chatblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
)

// AI 当前位置与工作根认领(spec 2026-11-24-chat-right-toolwindows「识别信号」)。
//
// 每个会话维护「AI 当前位置」:执行设备上的一个绝对目录,空串 = 没有位置记录(即会话
// 工作目录)。位置信号一到就更新它 —— 在轮次进行中生效,不等轮末:
//   - 工具调用 / 工具结果事件上的 Cwd(各后端在执行侧产生,远端随事件过来);
//   - 不随工具事件到达的信号(Claude Code init 帧目录、PostToolUse hook 的 cwd、
//     ExitWorktree 选择删除)经 ObserveAILocation 进来,与工具事件走同一条路。
//
// 子代理的位置变化只参与认领,不移动主位置(决策 16)。位置的每次变化、写入目标所在目录、
// `git worktree add` 的路径都是认领候选;认领在后台按会话串行地跑(本机是若干次 git,远端
// 是若干跳 RPC,不能压在事件循环上),集合或 AI 所在的根变了就在 chat:workroots:<sid>
// 推一发完整集合。重启或重新打开会话后,位置取最后一个带位置记录的外层工具块。

// WorkRootsEventPrefix 是会话级「工作根变化」流的前缀。
const WorkRootsEventPrefix = "chat:workroots"

// WorkRootsStreamName 返回某会话的工作根变化流名:"chat:workroots:<sessionID>"。前端在
// 挂载会话时常驻订阅它。
func WorkRootsStreamName(sessionID int64) string {
	return fmt.Sprintf("%s:%d", WorkRootsEventPrefix, sessionID)
}

// WorkRootsEvent 是工作根变化流上的载荷:变化之后的完整集合,形态与 WorkspaceFsWorkRoots
// 的返回值相同(第一项恒是主根,AIHere 标出 AI 当前所在的根)。
type WorkRootsEvent struct {
	SessionID int64                           `json:"sessionId"`
	Roots     []workspace_fs_svc.WorkRootView `json:"roots"`
}

// AILocationSignal 是一条与后端无关的位置信号。
type AILocationSignal struct {
	// Dir 是 AI 此刻所在的目录(执行设备上的绝对路径);空 = 这条信号不带位置。
	Dir string
	// FromSubagent 信号来自子代理(hook 带子代理标识 / 嵌套工具事件):Dir 只参与认领,
	// 不移动主会话的位置。
	FromSubagent bool
	// RevokeRoot 非空时撤销这个已认领的根(Claude Code ExitWorktree 选择删除)。
	RevokeRoot string
	// Candidates 是另外的认领候选目录(写入目标所在目录、`git worktree add` 的路径),
	// 绝对路径;相对路径被忽略。
	Candidates []string
}

// aiLocationBook 是各会话在本进程里的位置状态。
type aiLocationBook struct {
	mu       sync.Mutex
	sessions map[int64]*aiLocationState
}

// aiLocationState 是一个会话的位置状态与待跑的认领。mu 只守内存字段,认领本身在锁外跑
// (认领方取工作根时会回头问 AILocation)。
type aiLocationState struct {
	mu       sync.Mutex
	restored bool
	dir      string
	// offered 记已交给认领的候选目录:同一目录不重复认领。撤销一个根之后整份清空 ——
	// 同一路径之后可能又被建成 worktree。
	offered  map[string]struct{}
	jobs     []workRootsJob
	draining bool
}

// workRootsJob 是一条信号要做的认领工作。
type workRootsJob struct {
	ctx        context.Context
	revokeRoot string
	candidates []string
	// moved:主位置从 from 移到了 to,要判 AI 所在的根变没变。
	moved    bool
	from, to string
}

func (b *aiLocationBook) state(sessionID int64) *aiLocationState {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sessions == nil {
		b.sessions = map[int64]*aiLocationState{}
	}
	st, ok := b.sessions[sessionID]
	if !ok {
		st = &aiLocationState{offered: map[string]struct{}{}}
		b.sessions[sessionID] = st
	}
	return st
}

// offer 报告 dir 是否是本会话第一次交给认领的候选,并记下它。调用方持有 st.mu。
func (st *aiLocationState) offer(dir string) bool {
	if _, done := st.offered[dir]; done {
		return false
	}
	st.offered[dir] = struct{}{}
	return true
}

// AILocation 返回会话的 AI 当前位置;空串 = 没有位置记录(会话工作目录)。本进程里第一次
// 问时从转录恢复。
func (s *chatSvc) AILocation(ctx context.Context, sessionID int64) (string, error) {
	st := s.aiLocations.state(sessionID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := restoreAILocationLocked(ctx, sessionID, st); err != nil {
		return "", err
	}
	return st.dir, nil
}

// restoreAILocationLocked 在本进程第一次用到某会话的位置时,从转录取最后一个带位置记录的
// 外层工具块(结果目录优先于调用目录)。子代理的嵌套块不参与:它们本来就不移动主位置。
// 调用方持有 st.mu。
func restoreAILocationLocked(ctx context.Context, sessionID int64, st *aiLocationState) error {
	if st.restored {
		return nil
	}
	repo := transcript_repo.Message()
	versions, err := repo.ListVersions(ctx, sessionID)
	if err != nil {
		return operationFailedWithCause(ctx, err)
	}
	msgs := make([]*chat_entity.Message, 0, len(versions))
	for _, v := range versions {
		msgs = append(msgs, &chat_entity.Message{ID: v.ID, SessionID: sessionID})
	}
	if len(msgs) > 0 {
		if err := repo.FillBlocksByType(ctx, msgs, []string{chatblocks.ToolCwdBlock{}.Type()}); err != nil {
			return operationFailedWithCause(ctx, err)
		}
	}
	st.dir = lastRecordedLocation(ctx, sessionID, msgs)
	st.restored = true
	return nil
}

// lastRecordedLocation 从后往前找第一个带目录的 ToolCwdBlock。
func lastRecordedLocation(ctx context.Context, sessionID int64, msgs []*chat_entity.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		bs, err := msgs[i].GetBlocks()
		if err != nil {
			logger.Ctx(ctx).Warn("chat_svc.AILocation: decode blocks failed, message skipped",
				zap.Int64("sessionId", sessionID), zap.Int64("messageId", msgs[i].ID), zap.Error(err))
			continue
		}
		for j := len(bs) - 1; j >= 0; j-- {
			rec := toolCwdOfBlock(bs[j])
			if rec == nil {
				continue
			}
			if rec.ResultCwd != "" {
				return rec.ResultCwd
			}
			if rec.Cwd != "" {
				return rec.Cwd
			}
		}
	}
	return ""
}

func toolCwdOfBlock(b blocks.ContentBlock) *chatblocks.ToolCwdBlock {
	switch rec := b.(type) {
	case chatblocks.ToolCwdBlock:
		return &rec
	case *chatblocks.ToolCwdBlock:
		return rec
	}
	return nil
}

// ObserveAILocation 是位置信号的统一入口(与后端无关):更新会话的 AI 当前位置(子代理
// 信号除外),把位置与候选交给认领,按需撤销一个根;集合或 AI 所在的根变了就推一发
// chat:workroots:<sessionID>。位置在返回前已更新;认领与推送在后台按会话串行地跑。
// 远端会话的目录是执行设备上的路径,认领方在那台设备上做 git 判定。
func (s *chatSvc) ObserveAILocation(ctx context.Context, sessionID int64, sig AILocationSignal) {
	st := s.aiLocations.state(sessionID)
	st.mu.Lock()
	job := workRootsJob{ctx: context.WithoutCancel(ctx), revokeRoot: sig.RevokeRoot}
	if job.revokeRoot != "" {
		clear(st.offered)
	}
	if dir, ok := workspace_fs_svc.CleanLocation(sig.Dir); ok {
		// 只有主位置的信号要知道「之前在哪」;只带候选的信号不读转录。
		if !sig.FromSubagent {
			if err := restoreAILocationLocked(ctx, sessionID, st); err != nil {
				// 恢复不了就从「没有位置记录」起算:最坏多认领一次、多推一发,不丢这条信号。
				logger.Ctx(ctx).Warn("chat_svc.ObserveAILocation: restore location failed, starting from session cwd",
					zap.Int64("sessionId", sessionID), zap.Error(err))
				st.restored = true
			}
		}
		if !sig.FromSubagent && dir != st.dir {
			job.moved, job.from, job.to = true, st.dir, dir
			st.dir = dir
			logger.Ctx(ctx).Debug("chat_svc.ObserveAILocation: ai location moved",
				zap.Int64("sessionId", sessionID))
		}
		if (job.moved || sig.FromSubagent) && st.offer(dir) {
			job.candidates = append(job.candidates, dir)
		}
	}
	for _, c := range sig.Candidates {
		if dir, ok := workspace_fs_svc.CleanLocation(c); ok && st.offer(dir) {
			job.candidates = append(job.candidates, dir)
		}
	}
	st.mu.Unlock()
	if !job.moved && job.revokeRoot == "" && len(job.candidates) == 0 {
		return
	}
	s.enqueueWorkRootsJob(sessionID, st, job)
}

// enqueueWorkRootsJob 把一条认领工作排进会话的队列;队列空闲时起一个排空者。同一会话的
// 认领因此按信号顺序串行,不同会话互不等待。
func (s *chatSvc) enqueueWorkRootsJob(sessionID int64, st *aiLocationState, job workRootsJob) {
	st.mu.Lock()
	st.jobs = append(st.jobs, job)
	if st.draining {
		st.mu.Unlock()
		return
	}
	st.draining = true
	st.mu.Unlock()
	run := s.workRootsRunner
	if run == nil {
		run = func(f func()) { go f() }
	}
	run(func() {
		for {
			st.mu.Lock()
			if len(st.jobs) == 0 {
				st.draining = false
				st.mu.Unlock()
				return
			}
			next := st.jobs[0]
			st.jobs = st.jobs[1:]
			st.mu.Unlock()
			s.settleWorkRoots(sessionID, next)
		}
	})
}

// settleWorkRoots 跑一条认领工作。认领 / 撤销失败只降级(记 Warn、不推),不影响对话。
func (s *chatSvc) settleWorkRoots(sessionID int64, job workRootsJob) {
	ctx := job.ctx
	port := s.workRootsPort()
	changed := false
	if job.revokeRoot != "" {
		revoked, err := port.Revoke(ctx, sessionID, job.revokeRoot)
		if err != nil {
			logger.Ctx(ctx).Warn("chat_svc.settleWorkRoots: revoke work root failed",
				zap.Int64("sessionId", sessionID), zap.Error(err))
		}
		changed = changed || revoked
	}
	if len(job.candidates) > 0 {
		added, err := port.Claim(ctx, sessionID, job.candidates)
		if err != nil {
			logger.Ctx(ctx).Warn("chat_svc.settleWorkRoots: claim work roots failed",
				zap.Int64("sessionId", sessionID), zap.Int("candidates", len(job.candidates)), zap.Error(err))
		}
		changed = changed || len(added) > 0
	}
	if !changed && !job.moved {
		return
	}
	roots, err := port.WorkRoots(ctx, sessionID)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.settleWorkRoots: list work roots failed, change not pushed",
			zap.Int64("sessionId", sessionID), zap.Error(err))
		return
	}
	if !changed && workspace_fs_svc.AIRootIndex(roots, job.from) == workspace_fs_svc.AIRootIndex(roots, job.to) {
		return // 只是在同一个根里换了目录
	}
	s.emitter.Emit(ctx, WorkRootsStreamName(sessionID), WorkRootsEvent{SessionID: sessionID, Roots: roots})
}

// observeToolEvent 把一条工具调用 / 工具结果事件翻成位置信号:事件上的 Cwd 是位置,写入
// 目标(相对路径按这次调用的目录补全)与 `git worktree add` 的路径是候选。嵌套在子代理里
// 的事件只参与认领。其余事件什么都不做。
func (s *chatSvc) observeToolEvent(ctx context.Context, sessionID int64, ev agentruntime.Event) {
	var sig AILocationSignal
	switch e := ev.(type) {
	case agentruntime.ToolCall:
		sig.Dir, sig.FromSubagent = e.Cwd, e.ParentToolCallID != "" || e.SubagentRunID != ""
		sig.Candidates = toolCallCandidates(e, func() string { return s.anchorFor(ctx, sessionID, e.Cwd, sig.FromSubagent) })
	case agentruntime.ToolResult:
		sig.Dir, sig.FromSubagent = e.Cwd, e.ParentToolCallID != "" || e.SubagentRunID != ""
	default:
		return
	}
	if sig.Dir == "" && len(sig.Candidates) == 0 {
		return
	}
	s.ObserveAILocation(ctx, sessionID, sig)
}

// anchorFor 交回补全这次调用里相对写入路径的目录:调用自己报的目录优先;主会话的调用没报
// 目录时用 AI 当前位置(仍为空时由认领方按会话工作目录 —— 这里不知道它,交回空串,相对
// 路径因此不成候选,轮末取工作根时再按会话工作目录补喂)。
func (s *chatSvc) anchorFor(ctx context.Context, sessionID int64, cwd string, fromSubagent bool) string {
	if cwd != "" || fromSubagent {
		return cwd
	}
	dir, err := s.AILocation(ctx, sessionID)
	if err != nil {
		return ""
	}
	return dir
}

// toolCallCandidates 取一次工具调用的认领候选:写入目标所在目录(file.write / file.edit,
// 与 SessionWrittenPaths 同一口径)与 shellloc 标出的 `git worktree add` 路径。
// anchor 只在遇到相对写入路径时才被调用(它可能要读转录恢复位置)。
func toolCallCandidates(call agentruntime.ToolCall, anchor func() string) []string {
	var out []string
	if call.Canonical != nil {
		for _, p := range writtenPathsOfCanonical(call.Canonical) {
			w := workspace_fs_svc.WrittenPath{Path: p}
			if _, abs := workspace_fs_svc.CleanLocation(p); !abs && p != "" {
				w.Dir = anchor()
			}
			if dir, ok := workspace_fs_svc.WrittenDir(w, ""); ok {
				out = append(out, dir)
			}
		}
	}
	// 先做一次字节级预筛:写文件的入参动辄几百 KB,不为找一个键整份解析。
	if bytes.Contains(call.Input, []byte(shellloc.WorktreeAddKey)) {
		var in map[string]json.RawMessage
		if json.Unmarshal(call.Input, &in) == nil {
			var p string
			if raw, ok := in[shellloc.WorktreeAddKey]; ok && json.Unmarshal(raw, &p) == nil && p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}
