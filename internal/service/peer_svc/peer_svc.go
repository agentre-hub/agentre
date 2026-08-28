package peer_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"go.uber.org/zap"

	"github.com/cago-frame/cago/pkg/logger"

	"github.com/agentre-hub/agentre/internal/peer"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// freshSessionPlaceholder 是 wire runtime.run 的正整数、非零会话占位（对端按「本机
// 查无此会话」判定新建，见 chat_svc.runFreshPeerSession）。用一个大的随机占位避开
// 本机任何真实自增 id，杜绝撞上对端既有会话被误续。
func freshSessionPlaceholder() int64 {
	return rand.Int63n(1<<62) + 1<<61 // [1<<61, 1<<62)
}

var defaultSvc PeerSvc

type service struct {
	dialer   Dialer
	emitter  Emitter
	self     FingerprintProvider
	agents   AgentLookup
	projects ProjectLookup

	mu    sync.Mutex
	conns map[string]*connEntry
}

type connEntry struct {
	out      *peer.Outbound
	sessions map[int64]struct{}
}

// New 构造 peer_svc。Production wiring 在 internal/app（composition root）：dialer
// 是 server_svc.Server()、emitter 是 Wails EventsEmit 适配、self 是 remote_device_svc、
// agents/projects 是 agent_repo / project_repo 的单例。单测注入假 dialer + spy。
func New(dialer Dialer, emitter Emitter, self FingerprintProvider, agents AgentLookup, projects ProjectLookup) PeerSvc {
	return &service{
		dialer:   dialer,
		emitter:  emitter,
		self:     self,
		agents:   agents,
		projects: projects,
		conns:    map[string]*connEntry{},
	}
}

// dialShort 拨一条到目标桌面端的短连接（list / run 用：连上、调一次、关掉）。
func (s *service) dialShort(ctx context.Context, fingerprint string) (*peer.Outbound, func(), error) {
	fp, err := s.selfFingerprint()
	if err != nil {
		return nil, nil, err
	}
	c, err := s.dialer.DialDesktopRelay(ctx, fingerprint, fp)
	if err != nil {
		return nil, nil, err
	}
	return peer.NewOutbound(c, fingerprint), func() { _ = c.Close() }, nil
}

func (s *service) selfFingerprint() (string, error) {
	fp, err := s.self.DeviceFingerprint()
	if err != nil {
		return "", fmt.Errorf("peer_svc.selfFingerprint: read device fingerprint: %w", err)
	}
	return fp, nil
}

// ensureConn 取（或建）到一台远端桌面端的常驻连接。新建时挂上事件订阅：对端把
// attached 会话的 canonical 帧推回时，经 Emitter 按会话路由给前端。连接断掉后从表里
// 摘除，下次 Attach 重新拨号（断连重连语义，R11）。
func (s *service) ensureConn(ctx context.Context, fingerprint string) (*connEntry, error) {
	s.mu.Lock()
	if e, ok := s.conns[fingerprint]; ok {
		s.mu.Unlock()
		return e, nil
	}
	s.mu.Unlock()

	out, _, err := s.dialShort(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	out.HandleEvent(func(f wire.EventFrame) error {
		// 这里是**唯一**该把密封事件序列化成 JSON 的地方:再往前一步是 Wails
		// 事件,前端只吃 JSON。传输链路本身(daemon ↔ 桌面)全程走 Protobuf,
		// 不再经手中间那层 json.RawMessage。
		raw, err := json.Marshal(f.Event)
		if err != nil {
			return err
		}
		s.emitter.Emit(PeerEvent{
			Fingerprint: fingerprint,
			SessionID:   f.SessionID,
			Seq:         f.Seq,
			Event:       raw,
		})
		return nil
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.conns[fingerprint]; ok {
		// 竞态输家：期间已有别人建好，关掉自己这条。
		_ = out.Close()
		return e, nil
	}
	e := &connEntry{out: out, sessions: map[int64]struct{}{}}
	s.conns[fingerprint] = e
	go s.watchClosed(fingerprint, e, out.Closed())
	return e, nil
}

// watchClosed 在常驻中继连接断掉时把对应 entry 从表里摘除（引用不保留，下次重拨）。
func (s *service) watchClosed(fingerprint string, e *connEntry, closed <-chan struct{}) {
	<-closed
	s.mu.Lock()
	if s.conns[fingerprint] == e {
		delete(s.conns, fingerprint)
	}
	s.mu.Unlock()
	logger.Default().Info("peer_svc.watchClosed: relay connection dropped",
		zap.String("fingerprint", fingerprint))
}

func (s *service) ListSessions(ctx context.Context, fingerprint string) (*wire.SessionListResult, error) {
	if fingerprint == "" {
		return nil, errors.New("peer_svc.ListSessions: empty fingerprint")
	}
	out, release, err := s.dialShort(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	defer release()
	return out.ListSessions(ctx)
}

func (s *service) RunFresh(ctx context.Context, req RunFreshRequest) (wire.RunAck, error) {
	if req.Fingerprint == "" {
		return wire.RunAck{}, errors.New("peer_svc.RunFresh: empty fingerprint")
	}
	agent, err := s.agents.Find(ctx, req.AgentID)
	if err != nil {
		return wire.RunAck{}, err
	}
	if agent == nil {
		return wire.RunAck{}, fmt.Errorf("peer_svc.RunFresh: agent %d not found", req.AgentID)
	}
	cwd := ""
	if req.ProjectID > 0 {
		p, perr := s.projects.Find(ctx, req.ProjectID)
		if perr != nil {
			return wire.RunAck{}, perr
		}
		if p != nil && !p.LocalPathMissing {
			cwd = p.Path
		}
	}
	out, release, err := s.dialShort(ctx, req.Fingerprint)
	if err != nil {
		return wire.RunAck{}, err
	}
	defer release()
	fp, err := s.selfFingerprint()
	if err != nil {
		return wire.RunAck{}, err
	}
	return out.RunFresh(ctx, wire.RunParams{
		SessionID:      freshSessionPlaceholder(),
		AgentSyncID:    agent.SyncID,
		Cwd:            cwd,
		Title:          req.Title,
		UserText:       req.UserText,
		PermissionMode: req.PermissionMode,
		LLMProviderKey: req.ProviderKey,
		LLMModelKey:    req.ModelKey,
		SourceDevice:   fp,
	})
}

func (s *service) Attach(ctx context.Context, req AttachRequest) (*wire.SessionAttachResult, error) {
	e, err := s.ensureConn(ctx, req.Fingerprint)
	if err != nil {
		return nil, err
	}
	result, err := e.out.Attach(ctx, wire.SessionAttachParams{SessionID: req.SessionID})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	e.sessions[req.SessionID] = struct{}{}
	s.mu.Unlock()
	return &result, nil
}

func (s *service) Pull(ctx context.Context, req PullRequest) (*wire.SessionPullResult, error) {
	e, err := s.ensureConn(ctx, req.Fingerprint)
	if err != nil {
		return nil, err
	}
	result, err := e.out.Pull(ctx, wire.SessionPullParams{
		SessionID: req.SessionID,
		Cursor:    req.Cursor,
		Limit:     req.Limit,
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *service) Steer(ctx context.Context, req SteerRequest) error {
	e, err := s.ensureConn(ctx, req.Fingerprint)
	if err != nil {
		return err
	}
	return e.out.Steer(ctx, wire.SteerParams{SessionID: req.SessionID, Text: req.Text})
}

func (s *service) SubmitAnswer(ctx context.Context, req SubmitAnswerRequest) (*wire.PeerSessionControlResult, error) {
	e, err := s.ensureConn(ctx, req.Fingerprint)
	if err != nil {
		return nil, err
	}
	answers := make([]agentruntime.AskAnswer, 0, len(req.Answers))
	for _, a := range req.Answers {
		answers = append(answers, agentruntime.AskAnswer{
			QuestionIndex: a.QuestionIndex,
			Labels:        a.Labels,
			OtherText:     a.OtherText,
		})
	}
	result, err := e.out.SubmitAnswer(ctx, wire.SubmitAnswerParams{
		SessionID: req.SessionID,
		RequestID: req.RequestID,
		Answers:   answers,
		Skipped:   req.Skipped,
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *service) SubmitToolPermission(ctx context.Context, req SubmitToolPermissionRequest) (*wire.PeerSessionControlResult, error) {
	e, err := s.ensureConn(ctx, req.Fingerprint)
	if err != nil {
		return nil, err
	}
	result, err := e.out.SubmitToolPermission(ctx, wire.SubmitToolPermissionParams{
		SessionID:          req.SessionID,
		RequestID:          req.RequestID,
		Allow:              req.Allow,
		AlwaysAllowSession: req.AlwaysAllowSession,
		DenyReason:         req.DenyReason,
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *service) Detach(ctx context.Context, fingerprint string, sessionID int64) error {
	s.mu.Lock()
	e, ok := s.conns[fingerprint]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	delete(e.sessions, sessionID)
	if len(e.sessions) > 0 {
		s.mu.Unlock()
		return nil
	}
	delete(s.conns, fingerprint)
	s.mu.Unlock()
	_ = e.out.Close()
	logger.Default().Info("peer_svc.Detach: last session detached, relay connection closed",
		zap.String("fingerprint", fingerprint), zap.Int64("sessionId", sessionID))
	return nil
}

func (s *service) Close() error {
	s.mu.Lock()
	conns := s.conns
	s.conns = map[string]*connEntry{}
	s.mu.Unlock()
	var firstErr error
	for _, e := range conns {
		if err := e.out.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
