package ctlcmd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/relaypeer/relaypeertest"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// 账号模式会话命令的假世界：假 server（资源、派发计划、设备、保存）+ 假 relay + 一台假
// agentred（box）。agentred 按 auth.account 认出的身份记会话的发起方，个人 token 只能
// 动自己发起的会话，运行中的会话拒绝新的一轮——与真 agentred 同一组规则。

const (
	boxFP    = "sha256:box"
	laptopFP = "sha256:laptop"
	// ownFP 是本机 agrctl 登录成的 cli 设备的指纹（saveLogin 里的 Fingerprint）。
	ownFP = "sha256:own"
	// patFP 是假 agentred 给个人 token 记的身份。
	patFP = "pat:" + personalToken
)

// tierSpec 是假派发计划里的一档。
type tierSpec struct {
	backendSync, deviceName, fingerprint, backendType, kind, availability string
	deviceID                                                              int64
}

type world struct {
	t      *testing.T
	srv    *fakeServer
	relay  *relaypeertest.Relay
	box    *fakeAgentred
	laptop *fakeAgentred // 笔记本：登记在 relay 上但离线
	// saved 是账号的会话索引（/v1/agent-sessions），mirror 是 server 镜像的帧（/transcript）。
	saved  []map[string]any
	mirror map[string][]map[string]any
	mseq   int64
	tiers  map[string][]tierSpec // agent sync id → 执行目标链
	mu     sync.Mutex
	plans  []string // dispatch-target 收到的 query
	saves  []map[string]any
	saveOK bool
	// extraDevices 是 /v1/devices 在固定三台之外再报的设备（按需由用例加）。
	extraDevices []map[string]any
}

func newWorld(t *testing.T) *world {
	t.Helper()
	setAccountPoll(t, 5*time.Millisecond)
	t.Cleanup(setReconnectDelay(5 * time.Millisecond))
	dir := accountDirs(t)
	f := newFakeServer(t)
	f.saveLogin(t, dir)
	w := &world{t: t, srv: f, saveOK: true, tiers: map[string][]tierSpec{
		"AG-12": {
			{backendSync: "BE-5", deviceName: "laptop", fingerprint: laptopFP, backendType: "claudecode", kind: "desktop", availability: "offline", deviceID: 1},
			{backendSync: "BE-9", deviceName: "build-box", fingerprint: boxFP, backendType: "codex", kind: "agentred", availability: "available", deviceID: 2},
		},
		"AG-3": {{backendSync: "BE-PI", deviceName: "build-box", fingerprint: boxFP, backendType: "piagent", kind: "agentred", availability: "available", deviceID: 2}},
	}}
	w.syncIDs()
	w.relay = relaypeertest.New(func(bearer string) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return (!f.revoked && bearer == f.access) || bearer == personalToken
	})
	w.box = newFakeAgentred()
	w.relay.AddMachine(&relaypeertest.Machine{Fingerprint: boxFP, Registry: w.box.registry(), Identify: identify})
	w.laptop = newFakeAgentred()
	w.relay.AddMachine(&relaypeertest.Machine{Fingerprint: laptopFP, Registry: w.laptop.registry(), Identify: identify})
	w.relay.SetOffline(laptopFP, true)
	f.mux.Handle(w.relay.Path, w.relay)
	f.mux.HandleFunc("/v1/workspace/dispatch-target", w.serveDispatch)
	f.mux.HandleFunc("/v1/devices", w.serveDevices)
	f.mux.HandleFunc("/v1/saved-sessions", w.serveSave)
	f.mux.HandleFunc("/v1/agent-sessions", w.serveIndex)
	f.mux.HandleFunc("/v1/agent-sessions/transcript", w.serveTranscript)
	w.mirror = map[string][]map[string]any{}
	return w
}

// setAccountPoll 缩短账号模式 wait 复查会话状态的间隔。
func setAccountPoll(t *testing.T, d time.Duration) {
	t.Helper()
	prev := accountPollInterval
	accountPollInterval = d
	t.Cleanup(func() { accountPollInterval = prev })
}

// identify 是假 agentred 对凭据的自省：登录态的 access token 认成本机 cli 设备，个人 token
// 认成它自己的指纹。
func identify(credential string) string {
	if credential == personalToken {
		return patFP
	}
	return ownFP
}

// syncIDs 给假执行者的数据集补上账号执行者要的同步标识（真 server 由 Ctl* 文档的 sync_id 给出）；
// reviewer 有两档：笔记本上的 claude-local、build-box 上的 codex-remote。
func (w *world) syncIDs() {
	e := w.srv.exec
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, it := range e.items[agentrewire.CtlKind_CTL_KIND_AGENT] {
		a := it.GetAgent()
		a.SyncId = "AG-" + itoa(a.GetId())
		if a.GetName() == "reviewer" {
			a.BackendIds = []int64{5, 9}
		}
	}
	for _, it := range e.items[agentrewire.CtlKind_CTL_KIND_PROJECT] {
		it.GetProject().SyncId = "PJ-" + itoa(it.GetProject().GetId())
	}
	for _, it := range e.items[agentrewire.CtlKind_CTL_KIND_PROVIDER] {
		it.GetProvider().SyncId = "PV-" + itoa(it.GetProvider().GetId())
	}
	for _, it := range e.items[agentrewire.CtlKind_CTL_KIND_BACKEND] {
		it.GetBackend().SyncId = "BE-" + itoa(it.GetBackend().GetId())
	}
}

func (w *world) serveDispatch(rw http.ResponseWriter, r *http.Request) {
	if !w.srv.authed(rw, r, true) {
		return
	}
	q := r.URL.Query()
	w.mu.Lock()
	w.plans = append(w.plans, r.URL.RawQuery)
	w.mu.Unlock()
	tiers := []map[string]any{}
	var chosen map[string]any
	target := q.Get("target_backend_sync_id")
	for i, ts := range w.tiers[q.Get("agent_sync_id")] {
		tiers = append(tiers, map[string]any{"rank": i + 1, "backend_sync_id": ts.backendSync, "device_id": ts.deviceID,
			"device_name": ts.deviceName, "backend_type": ts.backendType, "kind": ts.kind, "availability": ts.availability})
		if chosen != nil || (target != "" && ts.backendSync != target) || ts.availability != "available" {
			continue
		}
		chosen = map[string]any{"device_fingerprint": ts.fingerprint, "device_id": ts.deviceID, "device_name": ts.deviceName,
			"backend_sync_id": ts.backendSync, "backend_type": ts.backendType, "kind": ts.kind}
		if p := q.Get("project_sync_id"); p != "" {
			chosen["cwd"] = "/srv/" + p
		}
	}
	writeEnvelope(rw, map[string]any{"agent_sync_id": q.Get("agent_sync_id"), "tiers": tiers, "chosen": chosen, "projects": []any{}})
}

func (w *world) serveDevices(rw http.ResponseWriter, r *http.Request) {
	if !w.srv.authed(rw, r, true) {
		return
	}
	w.mu.Lock()
	extra := w.extraDevices
	w.mu.Unlock()
	devices := make([]map[string]any, 0, 3+len(extra))
	devices = append(devices,
		map[string]any{"id": 1, "name": "laptop", "kind": "desktop", "fingerprint": laptopFP, "online": false, "version": "0.8.0", "last_seen_at": 1_700_000_000_000},
		map[string]any{"id": 2, "name": "build-box", "kind": "agentred", "fingerprint": boxFP, "online": true, "version": "0.9.0"},
		map[string]any{"id": 42, "name": "build-box", "kind": "cli", "fingerprint": ownFP, "online": false, "version": "agrctl abc1234", "last_seen_at": 1_750_000_000_000, "is_this_device": true},
	)
	devices = append(devices, extra...)
	writeEnvelope(rw, map[string]any{"devices": devices})
}

func (w *world) serveSave(rw http.ResponseWriter, r *http.Request) {
	if !w.srv.authed(rw, r, true) {
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	w.mu.Lock()
	w.saves = append(w.saves, body)
	ok := w.saveOK
	w.mu.Unlock()
	if !ok {
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = rw.Write([]byte(`{"code":500,"msg":"save failed","data":null}`))
		return
	}
	if id, _ := body["conversation_id"].(string); id != "" {
		w.relay.Save(id, body["device_fingerprint"].(string))
	}
	writeEnvelope(rw, map[string]any{})
}

func (w *world) savedBodies() []map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]map[string]any(nil), w.saves...)
}

func (w *world) planQueries() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.plans...)
}

// ─── 假 agentred ───

type fakeSession struct {
	summary *agentrewire.SessionSummary
	owner   string
	pending *agentrewire.SessionPendingWaitersResponse
	notes   []*agentrewire.DurableNotification
	queued  []*agentrewire.ConsumedSteer
}

// fakeAgentred 的 runtime 控制面与真 agentred 一样按连接：一条连接只能驱动它自己起的
// （runtime.run）或接管过的（session.attach）那一轮；没接管就作答 / 插话 / 取插话 / 中止，
// 拿到的是真 daemon 那句「no active turn」。agrctl 每条命令都是新通道，漏了接管在这里就红。
type fakeAgentred struct {
	mu       sync.Mutex
	sessions map[string]*fakeSession
	runs     []*agentrewire.RuntimeRunRequest
	steers   []*agentrewire.RuntimeSteerRequest
	calls    []string // 按序记下会话控制类方法
	seq      int64
	// onRun 在每次 runtime.run 接受之后被调用（锁外），测试据此让这一轮结束。
	onRun func(req *agentrewire.RuntimeRunRequest)
	// listed 每次 session.list 之后被调用（锁外），测试据此在等待途中制造断线等事件。
	listed     func(n int)
	lists      int
	answersLog *answerLog
	// engineTest / engineDiscover 是 engine.test / engine.discover 的作答（nil = 默认成功）；engine 记下收到的请求。
	engineTest     func(*agentrewire.EngineTestRequest) (*agentrewire.EngineTestResponse, error)
	engineDiscover func(*agentrewire.EngineDiscoverRequest) (*agentrewire.EngineDiscoverResponse, error)
	engine         []string

	// taken 是每条连接自己的会话表：runtime.run 与 session.attach 往里放。
	taken map[*protorpc.Conn]map[string]bool
}

func newFakeAgentred() *fakeAgentred {
	return &fakeAgentred{sessions: map[string]*fakeSession{}, taken: map[*protorpc.Conn]map[string]bool{}}
}

var errNoActiveTurn = &rpcerror.Error{Code: rpcerror.CodeRuntimeNoActiveTurn, Message: "agentruntime: no active turn for session"}

// take 记下调用连接接管了这条会话；要求持有 f.mu。
func (f *fakeAgentred) take(ctx context.Context, id string) {
	c := protorpc.ConnFromContext(ctx)
	if f.taken[c] == nil {
		f.taken[c] = map[string]bool{}
	}
	f.taken[c][id] = true
}

// control 是控制类方法过了归属检查之后的那道：调用连接没起过也没接管这条会话就解不出
// 那一轮。
func (f *fakeAgentred) control(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.taken[protorpc.ConnFromContext(ctx)][id] {
		return errNoActiveTurn
	}
	return nil
}

// submitControl 是作答类方法的那道：与真 agentred 的幂等一样，会话已不在跑时没接管
// 也算答过，仍在跑才如实报错。
func (f *fakeAgentred) submitControl(ctx context.Context, s *fakeSession) error {
	err := f.control(ctx, s.summary.GetConversationId())
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil && s.summary.GetLifecycleState() == "running" {
		return err
	}
	return nil
}

func caller(ctx context.Context) string {
	if c := protorpc.ConnFromContext(ctx); c != nil {
		return c.Auth().DeviceFingerprint
	}
	return ""
}

// guard 是假 agentred 的「个人 token 只能动自己发起的会话」。
func (f *fakeAgentred) guard(ctx context.Context, s *fakeSession) error {
	if who := caller(ctx); strings.HasPrefix(who, "pat:") && s.owner != who {
		return &rpcerror.Error{Code: rpcerror.CodeScopeDenied, Message: "token may only act on sessions it started"}
	}
	return nil
}

func (f *fakeAgentred) session(id string) *fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[id]
}

// seed 放一条已有的会话（发起方 owner、记在 origin 名下）。
func (f *fakeAgentred) seed(id, origin, owner, lifecycle string) *fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &fakeSession{owner: owner, summary: &agentrewire.SessionSummary{ConversationId: id, PeerFingerprint: origin,
		AgentSyncId: "AG-12", Title: "earlier", Cwd: "/srv/PJ-1", ProjectSyncId: "PJ-1", BackendType: "codex", LifecycleState: lifecycle}}
	f.sessions[id] = s
	return s
}

// finish 结束这条会话当前的一轮：落下用户消息、助手文本与收尾帧，生命周期落回 state。
func (f *fakeAgentred) finish(id, state, text, stopErr string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	add := func(n *agentrewire.RpcNotification) {
		f.seq++
		s.notes = append(s.notes, &agentrewire.DurableNotification{Seq: f.seq, Payload: n})
	}
	event := func(ev *agentrewire.RuntimeEventNotification) {
		add(&agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: ev}})
	}
	event(&agentrewire.RuntimeEventNotification{ConversationId: id, Event: &agentrewire.RuntimeEventNotification_UserMessage{UserMessage: &agentrewire.UserMessage{Text: "q"}}})
	for _, part := range strings.SplitAfter(text, " ") {
		if part != "" {
			event(&agentrewire.RuntimeEventNotification{ConversationId: id, Event: &agentrewire.RuntimeEventNotification_TextDelta{TextDelta: &agentrewire.TextDelta{Text: part}}})
		}
	}
	add(&agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RunResultDone{RunResultDone: &agentrewire.RunResultDoneNotification{ConversationId: id, StopErrorMessage: stopErr}}})
	s.summary.LifecycleState = state
	s.summary.LatestSeq = f.seq
}

func (f *fakeAgentred) setPending(id string, p *agentrewire.SessionPendingWaitersResponse, waitingFlag bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	s.pending = p
	s.summary.WaitingForInput = waitingFlag
}

func (f *fakeAgentred) runRequests() []*agentrewire.RuntimeRunRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*agentrewire.RuntimeRunRequest(nil), f.runs...)
}

func (f *fakeAgentred) steerRequests() []*agentrewire.RuntimeSteerRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*agentrewire.RuntimeSteerRequest(nil), f.steers...)
}

func (f *fakeAgentred) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeAgentred) note(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeAgentred) registry() *protorpc.Registry {
	reg := protorpc.NewRegistry()
	m := func(method agentrewire.RpcMethod) uint32 { return uint32(method) }

	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN),
		func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} },
		func(ctx context.Context, req *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
			f.mu.Lock()
			s := f.sessions[req.GetConversationId()]
			if s != nil {
				f.mu.Unlock()
				if err := f.guard(ctx, s); err != nil {
					return nil, err
				}
				f.mu.Lock()
				if s.summary.GetLifecycleState() == "running" && req.GetBackend().GetType() != "piagent" {
					f.mu.Unlock()
					return nil, &rpcerror.Error{Code: rpcerror.CodeRuntimeSessionRunning, Message: "runtime: conversation is running a turn"}
				}
			} else {
				origin := req.GetPeerFingerprint()
				if origin == "" {
					origin = caller(ctx)
				}
				s = &fakeSession{owner: caller(ctx), summary: &agentrewire.SessionSummary{ConversationId: req.GetConversationId(),
					PeerFingerprint: origin, AgentSyncId: req.GetAgentSyncId(), Title: req.GetTitle(), Cwd: req.GetCwd(),
					ProjectSyncId: req.GetProjectSyncId(), BackendType: req.GetBackend().GetType()}}
				f.sessions[req.GetConversationId()] = s
			}
			s.summary.LifecycleState = "running"
			f.take(ctx, req.GetConversationId())
			f.runs = append(f.runs, req)
			f.calls = append(f.calls, "run")
			n := len(f.runs)
			onRun := f.onRun
			f.mu.Unlock()
			if onRun != nil {
				go onRun(req)
			}
			resp := &agentrewire.RuntimeRunResponse{ConversationId: req.GetConversationId()}
			if req.GetBackend().GetType() == "piagent" {
				resp.ProviderSessionId = "pi-" + itoa(int64(n))
				if req.GetProviderSessionId() != "" {
					resp.ProviderSessionId = req.GetProviderSessionId()
				}
			}
			return resp, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER),
		func() *agentrewire.RuntimeSteerRequest { return &agentrewire.RuntimeSteerRequest{} },
		func(ctx context.Context, req *agentrewire.RuntimeSteerRequest) (*agentrewire.RuntimeSteerResponse, error) {
			s := f.session(req.GetConversationId())
			if s == nil {
				return nil, &rpcerror.Error{Code: rpcerror.CodeRuntimeSessionNotFound, Message: "no such session"}
			}
			if err := f.guard(ctx, s); err != nil {
				return nil, err
			}
			if err := f.control(ctx, req.GetConversationId()); err != nil {
				return nil, err
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.steers = append(f.steers, req)
			f.calls = append(f.calls, "steer")
			return &agentrewire.RuntimeSteerResponse{QueuedId: req.GetQueuedId(), Cancellable: true}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_DRAIN_PENDING),
		func() *agentrewire.RuntimeDrainPendingRequest { return &agentrewire.RuntimeDrainPendingRequest{} },
		func(ctx context.Context, req *agentrewire.RuntimeDrainPendingRequest) (*agentrewire.RuntimeDrainPendingResponse, error) {
			s := f.session(req.GetConversationId())
			if err := f.guard(ctx, s); err != nil {
				return nil, err
			}
			if err := f.control(ctx, req.GetConversationId()); err != nil {
				return nil, err
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			out := s.queued
			s.queued = nil
			f.calls = append(f.calls, "drain")
			return &agentrewire.RuntimeDrainPendingResponse{Steers: out}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT),
		func() *agentrewire.RuntimeAbortRequest { return &agentrewire.RuntimeAbortRequest{} },
		func(ctx context.Context, req *agentrewire.RuntimeAbortRequest) (*agentrewire.RuntimeAbortResponse, error) {
			s := f.session(req.GetConversationId())
			if err := f.guard(ctx, s); err != nil {
				return nil, err
			}
			if err := f.control(ctx, req.GetConversationId()); err != nil {
				return nil, err
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			s.summary.LifecycleState = "interrupted"
			origin := req.GetPeerFingerprint()
			if origin == "" { // 省略 = 调用方自己的对端（ResolveSessionPeer）
				origin = caller(ctx)
			}
			f.calls = append(f.calls, "abort:"+origin)
			return &agentrewire.RuntimeAbortResponse{}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH),
		func() *agentrewire.SessionAttachRequest { return &agentrewire.SessionAttachRequest{} },
		func(ctx context.Context, req *agentrewire.SessionAttachRequest) (*agentrewire.SessionAttachResponse, error) {
			s := f.session(req.GetConversationId())
			if s == nil {
				return nil, &rpcerror.Error{Code: rpcerror.CodeRuntimeSessionNotFound, Message: "agentruntime: session not found"}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			// 个人 token 跟随别人发起的会话只是读：照样受理，但不认下来。
			if who := caller(ctx); !strings.HasPrefix(who, "pat:") || s.owner == who {
				f.take(ctx, req.GetConversationId())
			}
			return &agentrewire.SessionAttachResponse{ConversationId: req.GetConversationId(), BackendType: s.summary.GetBackendType(),
				LifecycleState: s.summary.GetLifecycleState(), LatestSeq: s.summary.GetLatestSeq()}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST),
		func() *agentrewire.SessionListRequest { return &agentrewire.SessionListRequest{} },
		func(ctx context.Context, req *agentrewire.SessionListRequest) (*agentrewire.SessionListResponse, error) {
			f.mu.Lock()
			out := &agentrewire.SessionListResponse{}
			// 真 daemon（session_catchup.go List）只在别的对端发起的会话上标 peer_fingerprint，
			// 调用方自己的会话留空（空 = 调用方自己）。
			asListed := func(s *fakeSession) *agentrewire.SessionSummary {
				c := cloneSummary(s.summary)
				if c.PeerFingerprint == caller(ctx) {
					c.PeerFingerprint = ""
				}
				return c
			}
			if len(req.GetConversationIds()) == 0 { // 账号凭据：这台机器上所有对端的会话
				for _, s := range f.sessions {
					out.Sessions = append(out.Sessions, asListed(s))
				}
			}
			for _, id := range req.GetConversationIds() {
				if s := f.sessions[id]; s != nil {
					out.Sessions = append(out.Sessions, asListed(s))
				}
			}
			f.lists++
			n, listed := f.lists, f.listed
			f.mu.Unlock()
			if listed != nil {
				listed(n)
			}
			return out, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL),
		func() *agentrewire.SessionPullRequest { return &agentrewire.SessionPullRequest{} },
		func(_ context.Context, req *agentrewire.SessionPullRequest) (*agentrewire.SessionPullResponse, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			s := f.sessions[req.GetConversationId()]
			out := &agentrewire.SessionPullResponse{Cursor: req.GetCursor()}
			limit := int(req.GetLimit())
			for _, n := range s.notes {
				if n.GetSeq() <= req.GetCursor() {
					continue
				}
				if limit > 0 && len(out.Notifications) >= limit {
					out.HasMore = true
					break
				}
				out.Notifications = append(out.Notifications, n)
				out.Cursor = n.GetSeq()
			}
			return out, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS),
		func() *agentrewire.SessionPendingWaitersRequest { return &agentrewire.SessionPendingWaitersRequest{} },
		func(_ context.Context, req *agentrewire.SessionPendingWaitersRequest) (*agentrewire.SessionPendingWaitersResponse, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if p := f.sessions[req.GetConversationId()].pending; p != nil {
				return p, nil
			}
			return &agentrewire.SessionPendingWaitersResponse{}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET),
		func() *agentrewire.SetModelTargetRequest { return &agentrewire.SetModelTargetRequest{} },
		func(_ context.Context, req *agentrewire.SetModelTargetRequest) (*agentrewire.SetModelTargetResponse, error) {
			f.note("model:" + req.GetProviderKey() + "/" + req.GetModelKey())
			return &agentrewire.SetModelTargetResponse{}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_CLI_MODEL),
		func() *agentrewire.SetSessionCLIModelRequest { return &agentrewire.SetSessionCLIModelRequest{} },
		func(_ context.Context, req *agentrewire.SetSessionCLIModelRequest) (*agentrewire.SetSessionCLIModelResponse, error) {
			f.note("cli-model:" + req.GetMode() + "=" + req.GetValue())
			return &agentrewire.SetSessionCLIModelResponse{}, nil
		})
	protorpc.RegisterMethod(reg, m(agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_REASONING_EFFORT),
		func() *agentrewire.SetSessionReasoningEffortRequest {
			return &agentrewire.SetSessionReasoningEffortRequest{}
		},
		func(_ context.Context, req *agentrewire.SetSessionReasoningEffortRequest) (*agentrewire.SetSessionReasoningEffortResponse, error) {
			f.note("effort:" + req.GetReasoningEffort())
			return &agentrewire.SetSessionReasoningEffortResponse{}, nil
		})
	f.registerAnswers(reg)
	f.registerEngine(reg)
	return reg
}

func (f *fakeAgentred) registerEngine(reg *protorpc.Registry) {
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_ENGINE_TEST),
		func() *agentrewire.EngineTestRequest { return &agentrewire.EngineTestRequest{} },
		func(_ context.Context, req *agentrewire.EngineTestRequest) (*agentrewire.EngineTestResponse, error) {
			f.mu.Lock()
			f.engine = append(f.engine, "test:"+req.GetProviderKey()+"/"+req.GetModelKey())
			fn := f.engineTest
			f.mu.Unlock()
			if fn == nil {
				return &agentrewire.EngineTestResponse{Ok: true, Message: "connection succeeded"}, nil
			}
			return fn(req)
		})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_ENGINE_DISCOVER),
		func() *agentrewire.EngineDiscoverRequest { return &agentrewire.EngineDiscoverRequest{} },
		func(_ context.Context, req *agentrewire.EngineDiscoverRequest) (*agentrewire.EngineDiscoverResponse, error) {
			f.mu.Lock()
			f.engine = append(f.engine, "discover:"+req.GetProviderKey())
			fn := f.engineDiscover
			f.mu.Unlock()
			if fn == nil {
				return &agentrewire.EngineDiscoverResponse{}, nil
			}
			return fn(req)
		})
}

func (f *fakeAgentred) engineCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.engine...)
}

func cloneSummary(s *agentrewire.SessionSummary) *agentrewire.SessionSummary {
	return proto.Clone(s).(*agentrewire.SessionSummary)
}
