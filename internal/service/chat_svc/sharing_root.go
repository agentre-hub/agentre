package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/workspace_fs_svc"
)

// 「同一份代码上在跑」(spec 2026-11-24-chat-right-toolwindows 决策 22):关联工具窗的第四组。
// 只比较本桌面端拥有的会话 —— 其它宿主在同一台 agentred 上开的会话在本机库里没有行,拿不到
// 当前位置与变更,本来就比不了 —— 且只看正在运行(一轮在跑 / 等待用户,或有后台活动)的。
// 判定键是(执行设备, AI 当前所在的工作根)。

// SharingRootSession 是一个与请求会话在同一执行设备、同一工作根上运行的其它会话。
type SharingRootSession struct {
	SessionID int64  `json:"sessionId"`
	Title     string `json:"title"`
	AgentID   int64  `json:"agentId"`
	AgentName string `json:"agentName"`
	// Status 是 running / waiting(等待用户);只有后台活动的会话读作 running。
	Status string `json:"status"`
	// RootName / RootPath 是两个会话共处的那个工作根。
	RootName string `json:"rootName"`
	RootPath string `json:"rootPath"`
	// OverlapFirst 是对方写过、本会话也写过的第一个文件(按对方写入的先后),相对工作根
	// 显示;没有共同文件时为空。OverlapCount 是共同文件数,0 = 没有重叠。
	OverlapFirst string `json:"overlapFirst"`
	OverlapCount int    `json:"overlapCount"`
}

// runningStatuses 是 agent_status 里「有一轮在跑」的取值。
var runningStatuses = []string{"running", "waiting"}

func (s *chatSvc) ListSessionsSharingRoot(ctx context.Context, sessionID int64) ([]*SharingRootSession, error) {
	if sessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	self, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if self == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	selfRoots, err := s.workRootsPort().WorkRoots(ctx, sessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	selfRoot, ok := aiHereRoot(selfRoots)
	if !ok {
		return []*SharingRootSession{}, nil // 本会话没有可比较的根
	}

	candidates, err := s.runningCandidates(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := []*SharingRootSession{}
	var selfFiles map[string]struct{} // 惰性:没有同根的会话时不读转录
	for _, other := range candidates {
		if other.ExecDeviceID != self.ExecDeviceID {
			continue
		}
		roots, err := s.workRootsPort().WorkRoots(ctx, other.ID)
		if err != nil {
			// 一个同伴的根取不到(设备离线等)只跳过它,不拖垮整个关联窗。
			logger.Ctx(ctx).Warn("chat_svc.ListSessionsSharingRoot: peer work roots unavailable, skipped",
				zap.Int64("sessionId", sessionID), zap.Int64("peerSessionId", other.ID), zap.Error(err))
			continue
		}
		root, ok := aiHereRoot(roots)
		if !ok || !workspace_fs_svc.SamePath(root.Path, selfRoot.Path) {
			continue
		}
		item := &SharingRootSession{
			SessionID: other.ID, Title: other.Title, AgentID: other.AgentID,
			AgentName: agentNameOf(ctx, other.AgentID), Status: runStatusOf(other),
			RootName: selfRoot.Name, RootPath: selfRoot.Path,
		}
		if selfFiles == nil {
			selfFiles = s.writtenFileSet(ctx, sessionID, selfRoots)
		}
		item.OverlapFirst, item.OverlapCount = overlapWith(selfFiles, s.writtenFiles(ctx, other.ID, roots), selfRoot.Path)
		out = append(out, item)
	}
	return out, nil
}

// runningCandidates 取本桌面端正在跑的其它顶层会话:库里 agent_status 为 running / waiting 的,
// 加上只有后台活动(内存里的后台 subagent 集合非空)的。本库查不到、已删除或是子代理委派的
// 会话不属于「本桌面端拥有的顶层会话」,不进候选。
func (s *chatSvc) runningCandidates(ctx context.Context, selfID int64) ([]*chat_entity.Session, error) {
	rows, err := chat_repo.Session().ListActive(ctx, runningStatuses)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	seen := map[int64]struct{}{selfID: {}}
	var out []*chat_entity.Session
	add := func(sess *chat_entity.Session) {
		if sess == nil || !sess.IsActive() || sess.Purpose != "" {
			return
		}
		if _, dup := seen[sess.ID]; dup {
			return
		}
		seen[sess.ID] = struct{}{}
		if isInFlightStatus(sess.AgentStatus) || s.bgRunningActive(sess.ID) {
			out = append(out, sess)
		}
	}
	for _, sess := range rows {
		add(sess)
	}
	var bgIDs []int64
	s.bgRunning.Range(func(k, _ any) bool {
		if id, ok := k.(int64); ok && s.bgRunningActive(id) {
			bgIDs = append(bgIDs, id)
		}
		return true
	})
	for _, id := range bgIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		sess, err := chat_repo.Session().Find(ctx, id)
		if err != nil {
			continue // 本库没有这条会话:不是本桌面端拥有的
		}
		add(sess)
	}
	return out, nil
}

func runStatusOf(sess *chat_entity.Session) string {
	if sess.AgentStatus == "waiting" {
		return "waiting"
	}
	return "running"
}

func aiHereRoot(roots []workspace_fs_svc.WorkRootView) (workspace_fs_svc.WorkRootView, bool) {
	for _, r := range roots {
		if r.AIHere {
			return r, true
		}
	}
	return workspace_fs_svc.WorkRootView{}, false
}

func agentNameOf(ctx context.Context, agentID int64) string {
	if agentID <= 0 {
		return ""
	}
	a, err := agent_repo.Agent().Find(ctx, agentID)
	if err != nil || a == nil {
		return ""
	}
	return a.Name
}

// writtenFiles 取会话写过的文件的绝对路径(按首次写入的先后),相对路径按那次调用所在目录补全,
// 没有记录时按会话主根。取不到时按「没写过」处理 —— 少一个提醒,不影响列表。
func (s *chatSvc) writtenFiles(ctx context.Context, sessionID int64, roots []workspace_fs_svc.WorkRootView) []string {
	fn := s.writtenPaths
	if fn == nil {
		fn = SessionWrittenPaths
	}
	paths, err := fn(ctx, sessionID)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.ListSessionsSharingRoot: written paths unavailable, no overlap computed",
			zap.Int64("sessionId", sessionID), zap.Error(err))
		return nil
	}
	fallback := ""
	for _, r := range roots {
		if r.IsPrimary {
			fallback = r.Path
			break
		}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if f, ok := workspace_fs_svc.WrittenFile(p, fallback); ok {
			out = append(out, f)
		}
	}
	return out
}

func (s *chatSvc) writtenFileSet(ctx context.Context, sessionID int64, roots []workspace_fs_svc.WorkRootView) map[string]struct{} {
	set := map[string]struct{}{}
	for _, f := range s.writtenFiles(ctx, sessionID, roots) {
		set[f] = struct{}{}
	}
	return set
}

// overlapWith 返回 other 里本会话也写过的文件:第一个(按 other 的写入先后,相对 root 显示)
// 与去重后的个数。
func overlapWith(self map[string]struct{}, other []string, root string) (string, int) {
	first, n := "", 0
	seen := map[string]struct{}{}
	for _, f := range other {
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		if _, shared := self[f]; !shared {
			continue
		}
		if n == 0 {
			first = workspace_fs_svc.RelativeTo(root, f)
		}
		n++
	}
	return first, n
}
