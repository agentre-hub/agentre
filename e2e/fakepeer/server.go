// Package fakepeer implements the runner-owned loopback agentred protocol boundary.
package fakepeer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Fault controls the next runtime.run behavior through the loopback-only control API.
type Fault string

const (
	FaultNone                  Fault = ""
	FaultDisconnect            Fault = "disconnect"
	FaultRecoverableDisconnect Fault = "recoverable-disconnect"
	FaultProtocol              Fault = "protocol"
)

// Options contains only generated E2E identity and loopback configuration.
type Options struct {
	DeviceFingerprint string
	DeviceToken       string
	InstanceUUID      string
	ControlToken      string
}

// RecordedRequest is safe to expose to Playwright and failure artifacts.
type RecordedRequest struct {
	ConnectionID int64  `json:"connectionId"`
	Method       string `json:"method"`
	Params       any    `json:"params,omitempty"`
}

// Snapshot is a defensive, sanitized copy of the fake's observable protocol history.
type Snapshot struct {
	Requests []RecordedRequest `json:"requests"`
	Sessions []SessionSnapshot `json:"sessions"`
}

// SessionSnapshot exposes only journal progress needed by the independent E2E oracle.
type SessionSnapshot struct {
	SessionID int64 `json:"sessionId"`
	LatestSeq int64 `json:"latestSeq"`
}

type session struct {
	id                int64
	providerSessionID string
	lifecycle         string
	latestSeq         int64
	notifications     []*agentrewire.JournaledNotification
}

// Server is a loopback binary Protobuf peer plus an authenticated control endpoint.
type Server struct {
	opts Options

	listener net.Listener
	http     *http.Server

	mu          sync.Mutex
	connections map[int64]*protorpc.Conn
	requests    []RecordedRequest
	sessions    map[int64]*session
	nextFault   Fault
	closed      bool
	nextConnID  atomic.Int64
}

// Start binds a dynamic loopback port and serves /rpc plus /__control/*.
func Start(ctx context.Context, opts Options) (*Server, error) {
	if strings.TrimSpace(opts.DeviceFingerprint) == "" || strings.TrimSpace(opts.DeviceToken) == "" ||
		strings.TrimSpace(opts.InstanceUUID) == "" || strings.TrimSpace(opts.ControlToken) == "" {
		return nil, errors.New("fakepeer: generated identity and control token are required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{
		opts:        opts,
		listener:    listener,
		connections: map[int64]*protorpc.Conn{},
		sessions:    map[int64]*session{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", s.handleWebSocket)
	mux.HandleFunc("/__control/fault", s.handleFault)
	mux.HandleFunc("/__control/snapshot", s.handleSnapshot)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()
	go func() {
		_ = s.http.Serve(listener)
	}()
	return s, nil
}

// URL returns the exact agentred WebSocket endpoint.
func (s *Server) URL() string { return "ws://" + s.listener.Addr().String() + "/rpc" }

// ControlURL returns the loopback HTTP control root.
func (s *Server) ControlURL() string { return "http://" + s.listener.Addr().String() + "/__control" }

// DaemonFingerprint is the TOFU identity the desktop must seed.
func (s *Server) DaemonFingerprint() string { return identity.DaemonFingerprint(s.opts.InstanceUUID) }

// SetNextRunFault configures one deterministic boundary failure.
func (s *Server) SetNextRunFault(fault Fault) {
	s.mu.Lock()
	s.nextFault = fault
	s.mu.Unlock()
}

// Snapshot returns sanitized requests only; credential values are replaced at record time.
func (s *Server) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := make([]RecordedRequest, len(s.requests))
	copy(requests, s.requests)
	ids := make([]int64, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	sessions := make([]SessionSnapshot, 0, len(ids))
	for _, id := range ids {
		sessions = append(sessions, SessionSnapshot{SessionID: id, LatestSeq: s.sessions[id].latestSeq})
	}
	return Snapshot{Requests: requests, Sessions: sessions}
}

// Close stops listener and every upgraded socket. It is idempotent.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	connections := make([]*protorpc.Conn, 0, len(s.connections))
	for _, conn := range s.connections {
		connections = append(connections, conn)
	}
	s.connections = map[int64]*protorpc.Conn{}
	s.mu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.http.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	requested := websocket.Subprotocols(r)
	if len(requested) != 1 || requested[0] != protorpc.Subprotocol {
		http.Error(w, "required WebSocket subprotocol is missing", http.StatusBadRequest)
		return
	}
	upgrader := websocket.Upgrader{
		Subprotocols: []string{protorpc.Subprotocol},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	id := s.nextConnID.Add(1)
	registry := protorpc.NewRegistry()
	conn := protorpc.NewConn(protorpc.NewWebSocketFrameConn(ws), registry)
	s.register(conn, registry, id)
	s.mu.Lock()
	s.connections[id] = conn
	s.mu.Unlock()
	go func() {
		conn.Serve(context.Background())
		s.mu.Lock()
		delete(s.connections, id)
		s.mu.Unlock()
	}()
}

func (s *Server) register(conn *protorpc.Conn, registry *protorpc.Registry, connectionID int64) {
	auth := func(ctx context.Context) error {
		if !protorpc.ConnFromContext(ctx).Auth().Authenticated {
			return &protorpc.Error{Code: -32001, Message: "unauthorized"}
		}
		return nil
	}
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT), func() *agentrewire.AuthConnectRequest { return &agentrewire.AuthConnectRequest{} }, func(ctx context.Context, req *agentrewire.AuthConnectRequest) (*agentrewire.AuthConnectResponse, error) {
		s.record(connectionID, "auth.connect", req)
		if req.DeviceFingerprint != s.opts.DeviceFingerprint || req.DeviceToken != s.opts.DeviceToken || req.ExpectedDaemonFingerprint != s.DaemonFingerprint() {
			return nil, &protorpc.Error{Code: -32001, Message: "unauthorized"}
		}
		protorpc.ConnFromContext(ctx).SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: req.DeviceFingerprint})
		return &agentrewire.AuthConnectResponse{Ok: true, InstanceUuid: s.opts.InstanceUUID, ProtocolVersion: wireversion.Protocol}, nil
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING), func() *agentrewire.HealthPingRequest { return &agentrewire.HealthPingRequest{} }, func(ctx context.Context, req *agentrewire.HealthPingRequest) (*agentrewire.HealthPingResponse, error) {
		if err := auth(ctx); err != nil {
			return nil, err
		}
		s.record(connectionID, "health.ping", req)
		return &agentrewire.HealthPingResponse{InstanceUuid: s.opts.InstanceUUID, ServerTimeMs: time.Now().UnixMilli(), Capabilities: []string{wire.CapLLMModelTargetV1}}, nil
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES), func() *agentrewire.RuntimeCapabilitiesRequest { return &agentrewire.RuntimeCapabilitiesRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeCapabilitiesRequest) (*agentrewire.RuntimeCapabilitiesResponse, error) {
		if err := auth(ctx); err != nil {
			return nil, err
		}
		if req.BackendType == "" {
			return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "backend type required"}
		}
		s.record(connectionID, wire.MethodCapabilities, req)
		return &agentrewire.RuntimeCapabilitiesResponse{Capabilities: []*agentrewire.CapabilityEntry{{Name: "abort", Enabled: true}}, PermissionMode: &agentrewire.PermissionModeMeta{}}, nil
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST), func() *agentrewire.SessionListRequest { return &agentrewire.SessionListRequest{} }, func(ctx context.Context, req *agentrewire.SessionListRequest) (*agentrewire.SessionListResponse, error) {
		if err := auth(ctx); err != nil {
			return nil, err
		}
		s.record(connectionID, wire.MethodSessionList, req)
		return s.sessionList(), nil
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH), func() *agentrewire.SessionAttachRequest { return &agentrewire.SessionAttachRequest{} }, func(ctx context.Context, req *agentrewire.SessionAttachRequest) (*agentrewire.SessionAttachResponse, error) {
		if err := auth(ctx); err != nil {
			return nil, err
		}
		s.record(connectionID, wire.MethodSessionAttach, req)
		return s.attach(req.SessionId)
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL), func() *agentrewire.SessionPullRequest { return &agentrewire.SessionPullRequest{} }, func(ctx context.Context, req *agentrewire.SessionPullRequest) (*agentrewire.SessionPullResponse, error) {
		if err := auth(ctx); err != nil {
			return nil, err
		}
		s.record(connectionID, wire.MethodSessionPull, req)
		return s.pull(req), nil
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS), func() *agentrewire.SessionPendingWaitersRequest { return &agentrewire.SessionPendingWaitersRequest{} }, func(ctx context.Context, req *agentrewire.SessionPendingWaitersRequest) (*agentrewire.SessionPendingWaitersResponse, error) {
		if err := auth(ctx); err != nil {
			return nil, err
		}
		s.record(connectionID, wire.MethodSessionPendingWaiters, req)
		return &agentrewire.SessionPendingWaitersResponse{}, nil
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
		if err := auth(ctx); err != nil {
			return nil, err
		}
		if req.SessionId <= 0 {
			return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "session id required"}
		}
		s.record(connectionID, wire.MethodRun, req)
		fault := s.beginRun(req.SessionId)
		provider := fmt.Sprintf("e2e-remote-session-%d", req.SessionId)
		go s.streamRun(conn, req, provider, fault)
		return &agentrewire.RuntimeRunResponse{SessionId: req.SessionId, ProviderSessionId: provider}, nil
	})
}

func (s *Server) beginRun(sessionID int64) Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	fault := s.nextFault
	s.nextFault = FaultNone
	s.sessions[sessionID] = &session{
		id:                sessionID,
		providerSessionID: fmt.Sprintf("e2e-remote-session-%d", sessionID),
		lifecycle:         wire.SessionLifecycleRunning,
	}
	return fault
}

func (s *Server) streamRun(conn *protorpc.Conn, params *agentrewire.RuntimeRunRequest, providerSessionID string, fault Fault) {
	// Ensure runtime.run's response is written before the first notification.
	time.Sleep(20 * time.Millisecond)
	chunks := []string{"remote-peer-", "reply: ", params.UserText}
	if fault == FaultDisconnect {
		chunks = []string{"remote-peer-partial: " + params.UserText}
	}
	disconnected := false
	for index, chunk := range chunks {
		n := &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: &agentrewire.RuntimeEventNotification{SessionId: params.SessionId, Event: &agentrewire.RuntimeEventNotification_TextDelta{TextDelta: &agentrewire.TextDelta{Text: chunk}}}}}
		s.appendNotification(params.SessionId, n)
		if !disconnected {
			if err := conn.Notify(n); err != nil {
				return
			}
		}
		if fault == FaultRecoverableDisconnect && index == 0 {
			disconnected = true
			_ = conn.Close()
			time.Sleep(25 * time.Millisecond)
		}
		if fault == FaultDisconnect {
			s.setLifecycle(params.SessionId, wire.SessionLifecycleInterrupted)
			_ = conn.Close()
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	if fault == FaultProtocol {
		n := &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RunResultDone{RunResultDone: &agentrewire.RunResultDoneNotification{SessionId: params.SessionId, ProviderSessionId: providerSessionID, StopErrorMessage: "e2e remote protocol failure"}}}
		s.appendNotification(params.SessionId, n)
		s.setLifecycle(params.SessionId, wire.SessionLifecycleIdle)
		_ = conn.Notify(n)
		return
	}
	done := &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: &agentrewire.RuntimeEventNotification{SessionId: params.SessionId, Event: &agentrewire.RuntimeEventNotification_Done{Done: &agentrewire.Done{}}}}}
	s.appendNotification(params.SessionId, done)
	if !disconnected {
		if err := conn.Notify(done); err != nil {
			return
		}
	}
	terminal := &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RunResultDone{RunResultDone: &agentrewire.RunResultDoneNotification{SessionId: params.SessionId, ProviderSessionId: providerSessionID, Model: "remote-peer-model"}}}
	s.appendNotification(params.SessionId, terminal)
	s.setLifecycle(params.SessionId, wire.SessionLifecycleIdle)
	if disconnected {
		return
	}
	_ = conn.Notify(terminal)
}

func (s *Server) appendNotification(sessionID int64, notification *agentrewire.RpcNotification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[sessionID]
	if sess == nil {
		return
	}
	sess.latestSeq++
	if event := notification.GetRuntimeEvent(); event != nil {
		event.Seq = sess.latestSeq
	}
	if done := notification.GetRunResultDone(); done != nil {
		done.Seq = sess.latestSeq
	}
	sess.notifications = append(sess.notifications, &agentrewire.JournaledNotification{Seq: sess.latestSeq, Payload: notification})
}

func (s *Server) setLifecycle(sessionID int64, lifecycle string) {
	s.mu.Lock()
	if sess := s.sessions[sessionID]; sess != nil {
		sess.lifecycle = lifecycle
	}
	s.mu.Unlock()
}

func (s *Server) sessionList() *agentrewire.SessionListResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int64, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := &agentrewire.SessionListResponse{}
	for _, id := range ids {
		sess := s.sessions[id]
		result.Sessions = append(result.Sessions, &agentrewire.SessionSummary{
			SessionId: sess.id, ProviderSessionId: sess.providerSessionID,
			BackendType: "claudecode", LifecycleState: sess.lifecycle, LatestSeq: sess.latestSeq,
		})
	}
	return result
}

func (s *Server) attach(sessionID int64) (*agentrewire.SessionAttachResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[sessionID]
	if sess == nil {
		return nil, &protorpc.Error{Code: wire.ErrCodeSessionNotFound, Message: agentruntime.ErrSessionNotFound.Error()}
	}
	if sess.lifecycle == wire.SessionLifecycleInterrupted {
		return nil, &protorpc.Error{Code: wire.ErrCodeNoActiveTurn, Message: agentruntime.ErrNoActiveTurn.Error()}
	}
	return &agentrewire.SessionAttachResponse{
		SessionId: sessionID, BackendType: "claudecode",
		LifecycleState: sess.lifecycle, LatestSeq: sess.latestSeq,
	}, nil
}

func (s *Server) pull(params *agentrewire.SessionPullRequest) *agentrewire.SessionPullResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[params.SessionId]
	if sess == nil {
		return &agentrewire.SessionPullResponse{Cursor: params.Cursor}
	}
	limit := int(params.Limit)
	if limit <= 0 {
		limit = wire.DefaultSessionPullLimit
	}
	if limit > wire.MaxSessionPullLimit {
		limit = wire.MaxSessionPullLimit
	}
	result := &agentrewire.SessionPullResponse{Cursor: params.Cursor}
	if len(sess.notifications) > 0 {
		result.OldestSeq = sess.notifications[0].Seq
	}
	for _, notification := range sess.notifications {
		if notification.Seq <= params.Cursor {
			continue
		}
		if len(result.Notifications) >= limit {
			result.HasMore = true
			break
		}
		result.Notifications = append(result.Notifications, notification)
		result.Cursor = notification.Seq
	}
	return result
}

func (s *Server) record(connectionID int64, method string, value any) {
	raw, _ := json.Marshal(value)
	var params any
	_ = json.Unmarshal(raw, &params)
	s.mu.Lock()
	s.requests = append(s.requests, RecordedRequest{
		ConnectionID: connectionID, Method: method, Params: sanitize(params),
	})
	s.mu.Unlock()
}

func sanitize(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "credential") ||
				strings.Contains(lower, "secret") || strings.Contains(lower, "authorization") ||
				strings.Contains(lower, "fingerprint") {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = sanitize(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitize(item)
		}
		return out
	default:
		return value
	}
}

func (s *Server) authorizeControl(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer "+s.opts.ControlToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) handleFault(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeControl(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Fault Fault `json:"fault"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || (body.Fault != FaultDisconnect && body.Fault != FaultRecoverableDisconnect && body.Fault != FaultProtocol) {
		http.Error(w, "invalid fault", http.StatusBadRequest)
		return
	}
	s.SetNextRunFault(body.Fault)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeControl(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.Snapshot())
}
