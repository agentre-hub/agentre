package chat_svc

import (
	"context"
	"sort"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// cleanup.go 是设置里「清理对话历史」的桌面端后端（spec 2026-11-12「批量清理」）：
// PreviewCleanup 按条件数出会删哪些对话与各份副本各删几条，ExecuteCleanup 删预览那一刻
// 的那批对话。日志只记数量与对话身份，不记标题和内容。
//
// 两条口径：
//   - 进行中（running）/ 等用户操作（waiting）的对话永远保留，预览计入 Retained，执行时
//     逐条重新判断。
//   - 子 agent 委派会话（列表里本来就隐藏）按同样条件一起删，但不计入任何展示的数字：
//     预览把它们单列在 SubagentSessionIDs，执行时由调用方原样回传。它们没有指向父会话的
//     字段，所以无法只凭顶层 id 在执行时推出来，只能由预览给出。

// cleanupConcurrency 是执行时同时处理的会话数上限。单条删除里慢的是传播（server 不应答
// 或冷拨离线的 agentred 各可能挂 15s），串行就是 N×15s；并发之外，已经知道够不着的对端
// 不再重复试（见 cleanupRun）。
const cleanupConcurrency = 4

func isInFlightStatus(status string) bool { return status == "running" || status == "waiting" }

// cleanupEnv 是预览 / 执行开始时对「账号登录态 + 配对机器」的一次快照。
type cleanupEnv struct {
	loggedIn bool
	devices  map[int64]*remote_device_svc.DeviceView
}

func (s *chatSvc) cleanupEnv(ctx context.Context) cleanupEnv {
	env := cleanupEnv{devices: map[int64]*remote_device_svc.DeviceView{}}
	if srv := server_svc.Server(); srv != nil {
		if st, err := srv.GetState(ctx); err == nil && st != nil {
			env.loggedIn = st.IsLoggedIn()
		}
	}
	if rds := remote_device_svc.Default(); rds != nil {
		if rows, err := rds.List(ctx); err == nil {
			for _, row := range rows {
				if row != nil {
					env.devices[row.ID] = row
				}
			}
		}
	}
	return env
}

// PreviewCleanup 见 cleanup.go 头部与 PreviewCleanupRequest。
//
// 账号那一行：这里没有便宜的办法知道哪些对话在账号里存过，所以已登录时按「全部候选都会
// 去问 server」计数（server 对没存过的对话是无操作）。已从配对里移除的执行端不单独列出
// （决策 13）。
func (s *chatSvc) PreviewCleanup(ctx context.Context, req *PreviewCleanupRequest) (*PreviewCleanupResponse, error) {
	if req == nil {
		req = &PreviewCleanupRequest{}
	}
	filter := chat_repo.SessionCleanupFilter{BeforeMs: req.BeforeMs, AgentID: req.AgentID, ProjectID: req.ProjectID}
	visible, err := chat_repo.Session().ListForCleanup(ctx, filter)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	filter.Subagent = true
	subs, err := chat_repo.Session().ListForCleanup(ctx, filter)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	env := s.cleanupEnv(ctx)

	resp := &PreviewCleanupResponse{SessionIDs: []int64{}, SubagentSessionIDs: []int64{}, Copies: []CleanupCopy{}}
	perDevice := map[int64]int64{}
	for _, sess := range visible {
		if isInFlightStatus(sess.AgentStatus) {
			resp.Retained++
			continue
		}
		resp.Count++
		resp.SessionIDs = append(resp.SessionIDs, sess.ID)
		if sess.ExecDeviceID > 0 {
			perDevice[sess.ExecDeviceID]++
		}
	}
	for _, sess := range subs {
		if !isInFlightStatus(sess.AgentStatus) {
			resp.SubagentSessionIDs = append(resp.SubagentSessionIDs, sess.ID)
		}
	}

	resp.Copies = append(resp.Copies, CleanupCopy{Kind: CleanupCopyLocal, Count: resp.Count})
	if env.loggedIn {
		resp.Copies = append(resp.Copies, CleanupCopy{Kind: CleanupCopyAccount, Count: resp.Count})
	}
	var machines []CleanupCopy
	for deviceID, n := range perDevice {
		view := env.devices[deviceID]
		if view == nil {
			continue
		}
		machines = append(machines, CleanupCopy{
			Kind: CleanupCopyMachine, Name: view.Name, Online: view.Online, LanDirect: !env.loggedIn, Count: n})
	}
	sort.Slice(machines, func(i, j int) bool { return machines[i].Name < machines[j].Name })
	resp.Copies = append(resp.Copies, machines...)

	logger.Ctx(ctx).Info("chat_svc.PreviewCleanup",
		zap.Int64("count", resp.Count), zap.Int64("retained", resp.Retained),
		zap.Int("subagent", len(resp.SubagentSessionIDs)), zap.Bool("loggedIn", env.loggedIn))
	return resp, nil
}

// cleanupRun 是一次执行里各个并发 worker 共享的「哪些对端已经证明够不着」。
type cleanupRun struct {
	env cleanupEnv

	mu          sync.Mutex
	serverDown  bool
	deviceDown  map[int64]bool
	pending     map[string]*CleanupPending // key: machine name
	pendingKeys []string
}

func (r *cleanupRun) isServerDown() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.serverDown }
func (r *cleanupRun) markServerDown()    { r.mu.Lock(); r.serverDown = true; r.mu.Unlock() }

// deviceKnownOffline：快照里就是离线，或本次执行里已经试过一次没成。
func (r *cleanupRun) deviceKnownOffline(id int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deviceDown[id] {
		return true
	}
	v := r.env.devices[id]
	return v != nil && !v.Online
}
func (r *cleanupRun) markDeviceDown(id int64) { r.mu.Lock(); r.deviceDown[id] = true; r.mu.Unlock() }

func (r *cleanupRun) addPending(machine string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pending[machine]
	if p == nil {
		p = &CleanupPending{Kind: CleanupCopyMachine, Machine: machine}
		r.pending[machine] = p
		r.pendingKeys = append(r.pendingKeys, machine)
	}
	p.Count++
}

func (r *cleanupRun) machineName(sess *chat_entity.Session) string {
	if v := r.env.devices[sess.ExecDeviceID]; v != nil {
		return v.Name
	}
	return string(sess.ExecDeviceFingerprint)
}

// propagateForCleanup 是 DeleteByUser 里 propagateDelete 那一步的批量版：同样的传播与待删除语义，
// 只是对已经知道够不着的对端（server 刚失败过 / 机器离线）不再重复拨号，直接记待删除。
func (s *chatSvc) propagateForCleanup(ctx context.Context, run *cleanupRun, sess *chat_entity.Session) deletePropagation {
	out := deletePropagation{}
	if sess.ExecDeviceID > 0 {
		out.carrierFingerprint = sess.ExecDeviceFingerprint
	}
	switch {
	case run.env.loggedIn && run.isServerDown() && sess.ConversationID != "":
		s.recordPendingDelete(ctx, &chat_entity.PendingDelete{
			ConversationID:     sess.ConversationID,
			Target:             chat_entity.PendingDeleteTargetServer,
			MachineFingerprint: string(out.carrierFingerprint),
		})
		out.serverPending = true
	case !run.env.loggedIn && sess.ExecDeviceID > 0 && sess.ConversationID != "" && run.deviceKnownOffline(sess.ExecDeviceID):
		s.recordPendingDelete(ctx, &chat_entity.PendingDelete{
			ConversationID:     sess.ConversationID,
			Target:             chat_entity.PendingDeleteTargetAgentred,
			MachineFingerprint: string(out.carrierFingerprint),
			DeviceID:           sess.ExecDeviceID,
		})
		out.carrierOffline = true
	default:
		out = s.propagateDelete(ctx, sess)
		if out.serverPending {
			run.markServerDown()
		}
		if !run.env.loggedIn && out.carrierOffline && sess.ExecDeviceID > 0 {
			run.markDeviceDown(sess.ExecDeviceID)
		}
	}
	return out
}

type cleanupOutcome struct {
	deleted  bool
	failed   string
	retained bool
}

func (s *chatSvc) cleanupOne(ctx context.Context, run *cleanupRun, id int64, countPending bool) cleanupOutcome {
	sess, err := chat_repo.Session().Find(ctx, id)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.ExecuteCleanup: read session failed", zap.Int64("sessionId", id), zap.Error(err))
		return cleanupOutcome{failed: CleanupFailedRead}
	}
	if sess == nil {
		return cleanupOutcome{}
	}
	if isInFlightStatus(sess.AgentStatus) {
		return cleanupOutcome{retained: true}
	}
	// 本机那份失败：不传播，照旧只报这一条。
	if _, err := s.Delete(ctx, &DeleteRequest{SessionID: id}); err != nil {
		logger.Ctx(ctx).Warn("chat_svc.ExecuteCleanup: local delete failed", zap.Int64("sessionId", id), zap.Error(err))
		return cleanupOutcome{failed: CleanupFailedDelete}
	}
	out := s.propagateForCleanup(ctx, run, sess)
	if countPending && out.carrierOffline {
		run.addPending(run.machineName(sess))
	}
	return cleanupOutcome{deleted: true}
}

// ExecuteCleanup 见 cleanup.go 头部。每条会话各自：重新读、跳过已不在的、保留已变成
// 进行中的、删本机、再传播。一条失败不影响其余；多条并发（cleanupConcurrency）处理。
//
// Pending 只数顶层对话，只报执行端那份没删成的离线机器（完成提示只写离线机器，spec
// 「执行」）；server 够不着的那份照样记了本地待删除，联系上再补发，不进结果。
func (s *chatSvc) ExecuteCleanup(ctx context.Context, req *ExecuteCleanupRequest) (*ExecuteCleanupResponse, error) {
	if req == nil {
		req = &ExecuteCleanupRequest{}
	}
	run := &cleanupRun{env: s.cleanupEnv(ctx), deviceDown: map[int64]bool{}, pending: map[string]*CleanupPending{}}

	type job struct {
		id    int64
		top   bool
		index int
	}
	var jobs []job
	seen := map[int64]bool{}
	for _, id := range req.SessionIDs {
		if !seen[id] {
			seen[id] = true
			jobs = append(jobs, job{id: id, top: true, index: len(jobs)})
		}
	}
	for _, id := range req.SubagentSessionIDs {
		if !seen[id] {
			seen[id] = true
			jobs = append(jobs, job{id: id, index: len(jobs)})
		}
	}
	results := make([]cleanupOutcome, len(jobs))

	ch := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < cleanupConcurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				results[j.index] = s.cleanupOne(ctx, run, j.id, j.top)
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()

	resp := &ExecuteCleanupResponse{Deleted: []int64{}, Failed: []CleanupFailure{}, Pending: []CleanupPending{}}
	for i, j := range jobs {
		r := results[i]
		switch {
		case r.retained:
			if j.top {
				resp.Retained++
			}
		case r.failed != "":
			if j.top {
				resp.Failed = append(resp.Failed, CleanupFailure{ID: j.id, Reason: r.failed})
			}
		case r.deleted && j.top:
			resp.Deleted = append(resp.Deleted, j.id)
		}
	}
	for _, key := range run.pendingKeys {
		resp.Pending = append(resp.Pending, *run.pending[key])
	}
	sort.SliceStable(resp.Pending, func(i, k int) bool { return resp.Pending[i].Machine < resp.Pending[k].Machine })
	logger.Ctx(ctx).Info("chat_svc.ExecuteCleanup",
		zap.Int("deleted", len(resp.Deleted)), zap.Int("failed", len(resp.Failed)),
		zap.Int64("retained", resp.Retained), zap.Int("pendingTargets", len(resp.Pending)))
	return resp, nil
}
