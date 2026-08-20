// Package peer owns the desktop's inbound, session-level peer surface.
package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agentre-ai/agentre/internal/daemon/handlers"
	"github.com/agentre-ai/agentre/internal/daemon/rpc"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-ai/agentre/internal/service/chat_svc"
	"github.com/agentre-ai/agentre/internal/service/remote_device_svc"
)

type inboundSessionAdapter interface {
	ListPeerSessions(context.Context) (*wire.SessionListResult, error)
	AttachPeerSession(context.Context, wire.SessionAttachParams, chat_svc.PeerSessionSubscriber) (wire.SessionAttachResult, error)
	PullPeerSession(context.Context, wire.SessionPullParams, chat_svc.PeerSessionSubscriber) (wire.SessionPullResult, error)
	PendingPeerSessionWaiters(context.Context, wire.SessionPendingWaitersParams) (wire.SessionPendingWaitersResult, error)
	// Delete 是桌面端自己那条删除对话的入口(不是为对端另开的第二条删除路径):
	// 对端删掉的就是用户在这台电脑上删掉的那一份。
	Delete(context.Context, *chat_svc.DeleteRequest) (*chat_svc.DeleteResponse, error)
	RunPeerSession(context.Context, wire.RunParams, chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error)
	EnqueuePeerSession(context.Context, wire.SteerParams, chat_svc.PeerSessionSource) (*chat_svc.EnqueueResponse, error)
	AnswerPeerUserQuestion(context.Context, wire.SubmitAnswerParams) (chat_svc.PeerSessionControlResult, error)
	AnswerPeerToolPermission(context.Context, wire.SubmitToolPermissionParams) (chat_svc.PeerSessionControlResult, error)
}

type connPeerSessionSubscriber struct {
	conn *rpc.Conn
}

func (s connPeerSessionSubscriber) Notify(method string, params any) error {
	return s.conn.Notify(method, params)
}

func (s connPeerSessionSubscriber) Done() <-chan struct{} { return s.conn.Done() }

func (s connPeerSessionSubscriber) PeerSessionSubscriberKey() string {
	return fmt.Sprintf("rpc-conn:%p", s.conn)
}

// Inbound keeps the desktop registered through one reconnecting relay link and
// turns relay-initiated virtual channels into private JSON-RPC registries.
// Session methods are registered through RegisterInboundMethods as they are
// added; this type deliberately does not reuse daemon state or handlers.
type Inbound struct {
	link     *rpc.HubLink
	mux      *rpc.Multiplexer
	registry *rpc.Registry
}

// NewInbound constructs the desktop's session-level relay endpoint. The caller
// owns the HubLink configuration and calls Run for the App lifetime.
func NewInbound(link *rpc.HubLink) *Inbound {
	registry := rpc.NewRegistry()
	RegisterInboundMethods(registry)
	return &Inbound{
		link:     link,
		mux:      rpc.NewMultiplexer(link),
		registry: registry,
	}
}

// Run keeps the desktop online until ctx is canceled. HubLink owns reconnect
// and registration while the multiplexer owns only virtual channels, so closing
// the latter cannot accidentally leave an addressable physical connection.
func (p *Inbound) Run(ctx context.Context) error {
	defer p.mux.Close()
	go p.serve(ctx)
	return p.link.Run(ctx)
}

func (p *Inbound) serve(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case channel := <-p.mux.Accept():
			if channel == nil {
				return
			}
			conn := rpc.NewConn(channel, p.registry.Clone())
			go conn.Serve(ctx)
		}
	}
}

// RegisterInboundMethods is the desktop peer's single method-registration
// entry point. Task 3 adds session-list and session-attach handlers here.
func RegisterInboundMethods(registry *rpc.Registry) {
	registry.Register("auth.account", authenticateAccount)
	registry.Register(wire.MethodCapabilities, requireAccount(capabilities))
	registry.Register(wire.MethodSessionList, requireAccount(listSessions))
	registry.Register(wire.MethodSessionAttach, requireAccount(attachSession))
	registry.Register(wire.MethodSessionPull, requireAccount(pullSession))
	registry.Register(wire.MethodSessionPendingWaiters, requireAccount(pendingSessionWaiters))
	registry.Register(wire.MethodSessionDelete, requireAccount(deleteSession))
	registry.Register(wire.MethodSkillsCatalog, requireAccount(skillsCatalog))
	registry.Register(wire.MethodRun, requireAccount(runSession))
	registry.Register(wire.MethodSteer, requireAccount(steerSession))
	registry.Register(wire.MethodSubmitAnswer, requireAccount(submitAnswer))
	registry.Register(wire.MethodSubmitToolPermission, requireAccount(submitToolPermission))
}

// authenticateAccount completes the existing account-handshake vocabulary.
// The relay has already authenticated both WebSocket ends and enforced their
// same-account relationship before it can forward a virtual channel; this
// desktop-only endpoint records that established authorization per channel.
func authenticateAccount(ctx context.Context, raw json.RawMessage) (any, error) {
	var params rpc.AccountParams
	if err := json.Unmarshal(raw, &params); err != nil || params.Credential == "" || params.DeviceFingerprint == "" {
		return nil, rpc.ErrInvalidParams
	}
	conn := rpc.ConnFromContext(ctx)
	if conn == nil {
		return nil, rpc.ErrUnauthorized
	}
	conn.SetAuth(rpc.AuthState{Authenticated: true, DeviceFingerprint: params.DeviceFingerprint})
	return rpc.ConnectResult{OK: true}, nil
}

// skillsCatalog answers "which skill packs live on this machine" for one exec
// target. agentred serves the same method (daemon.registry) with the same
// handler, and that symmetry is the point: an exec target's backend may be
// claimed by this desktop's own fingerprint (R13) instead of an agentred, and a
// browser addressing it by fingerprint must not have to know which kind of
// process answered.
//
// It deliberately does not go through skill_svc even though this process has the
// org database: the wire contract says the caller names the authorizations
// (there is no agent id or exec-target id on the wire, because agentred has no
// row to look one up in). Answering the same question two different ways
// depending on who is asking is exactly how the two ends drift apart.
func skillsCatalog(ctx context.Context, raw json.RawMessage) (any, error) {
	var params wire.SkillCatalogParams
	if err := json.Unmarshal(raw, &params); err != nil || params.BackendType == "" {
		return nil, rpc.ErrInvalidParams
	}
	return handlers.NewSkillsHandlers().Catalog(ctx, params)
}

func requireAccount(next rpc.HandlerFunc) rpc.HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		conn := rpc.ConnFromContext(ctx)
		if conn == nil || !conn.Auth().Authenticated {
			return nil, rpc.ErrUnauthorized
		}
		return next(ctx, raw)
	}
}

// capabilities is intentionally the smallest existing runtime method. It
// proves that an authorized relay peer reaches the desktop-owned registry;
// session behavior is added by the session adapter in later tasks.
func capabilities(_ context.Context, raw json.RawMessage) (any, error) {
	var params wire.CapabilitiesParams
	if err := json.Unmarshal(raw, &params); err != nil || params.BackendType == "" {
		return nil, rpc.ErrInvalidParams
	}
	return wire.CapabilitiesResult{}, nil
}

func listSessions(ctx context.Context, raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var params struct{}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, rpc.ErrInvalidParams
	}
	adapter, ok := chat_svc.Chat().(inboundSessionAdapter)
	if !ok || adapter == nil {
		return nil, rpc.ErrInternal
	}
	result, err := adapter.ListPeerSessions(ctx)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func pullSession(ctx context.Context, raw json.RawMessage) (any, error) {
	var params wire.SessionPullParams
	if err := json.Unmarshal(raw, &params); err != nil || params.SessionID <= 0 || params.Cursor < 0 {
		return nil, rpc.ErrInvalidParams
	}
	conn := rpc.ConnFromContext(ctx)
	if conn == nil {
		return nil, rpc.ErrUnauthorized
	}
	adapter, ok := chat_svc.Chat().(inboundSessionAdapter)
	if !ok || adapter == nil {
		return nil, rpc.ErrInternal
	}
	result, err := adapter.PullPeerSession(ctx, params, connPeerSessionSubscriber{conn: conn})
	if err != nil {
		if errors.Is(err, chat_svc.ErrPeerSessionNotFound) {
			return nil, rpc.ErrSessionNotFound
		}
		return nil, err
	}
	return result, nil
}

// pendingSessionWaiters is the read half of submitAnswer / submitToolPermission,
// and the only data source a browser peer has for drawing an approval or
// question card: it does not receive the desktop's Wails events, and the event
// stream alone never tells it which requests are still blocked. agentred serves
// the same method (daemon.registry), so the browser parses one shape either way.
func pendingSessionWaiters(ctx context.Context, raw json.RawMessage) (any, error) {
	var params wire.SessionPendingWaitersParams
	if err := json.Unmarshal(raw, &params); err != nil || params.SessionID <= 0 {
		return nil, rpc.ErrInvalidParams
	}
	adapter, ok := chat_svc.Chat().(inboundSessionAdapter)
	if !ok || adapter == nil {
		return nil, rpc.ErrInternal
	}
	result, err := adapter.PendingPeerSessionWaiters(ctx, params)
	if err != nil {
		if errors.Is(err, chat_svc.ErrPeerSessionNotFound) {
			return nil, rpc.ErrSessionNotFound
		}
		return nil, err
	}
	return result, nil
}

// deleteSession removes this computer's own copy of a chat session, because the
// desktop is a mirror target on equal terms with agentred: a session deleted
// from the account is deleted wherever it executes.
//
// The blast radius here is not an execution log. On agentred the same method
// drops a session row and its notification journal; on the desktop it drops the
// user's primary copy of the conversation — so this is the desktop's own delete
// (chat_svc.Delete), not a second deletion path invented for peers.
//
// peerFingerprint has exactly one legal value on this end: this computer. Every
// desktop session originates here, and the session list hands out this machine's
// fingerprint on every summary, so a mirror naming any other machine is asking a
// different machine to delete something. Following the bare sessionId anyway
// would destroy this computer's same-numbered conversation — session ids are
// each end's local autoincrement, so collisions are the norm, not the exception.
func deleteSession(ctx context.Context, raw json.RawMessage) (any, error) {
	var params wire.SessionDeleteParams
	if err := json.Unmarshal(raw, &params); err != nil || params.SessionID <= 0 {
		return nil, rpc.ErrInvalidParams
	}
	if err := requireOwnOrigin(params.PeerFingerprint); err != nil {
		return nil, err
	}
	adapter, ok := chat_svc.Chat().(inboundSessionAdapter)
	if !ok || adapter == nil {
		return nil, rpc.ErrInternal
	}
	if _, err := adapter.Delete(ctx, &chat_svc.DeleteRequest{SessionID: params.SessionID}); err != nil {
		return nil, err
	}
	// Deleting an already-deleted session stays a success: the account keeps a
	// pending delete for every machine that was offline and replays it, and an
	// error would make that job replay forever.
	return wire.SessionDeleteResult{Deleted: true}, nil
}

// requireOwnOrigin accepts an omitted origin (= this machine, the wire's own
// convention) and this desktop's own fingerprint; anything else is refused.
// An unreadable local fingerprint refuses a named origin rather than assuming
// it matches — the destructive direction is the one that must fail closed.
func requireOwnOrigin(originPeer string) error {
	if originPeer == "" {
		return nil
	}
	device := remote_device_svc.Default()
	if device == nil {
		return rpc.ErrUnauthorized
	}
	fingerprint, err := device.DeviceFingerprint()
	if err != nil || fingerprint == "" || fingerprint != originPeer {
		return rpc.ErrUnauthorized
	}
	return nil
}

func peerSource(ctx context.Context, requestedName string) (chat_svc.PeerSessionSource, error) {
	conn := rpc.ConnFromContext(ctx)
	if conn == nil || conn.Auth().DeviceFingerprint == "" {
		return chat_svc.PeerSessionSource{}, rpc.ErrUnauthorized
	}
	return chat_svc.PeerSessionSource{Device: conn.Auth().DeviceFingerprint, Name: requestedName}, nil
}

func runSession(ctx context.Context, raw json.RawMessage) (any, error) {
	var params wire.RunParams
	if err := json.Unmarshal(raw, &params); err != nil || params.SessionID <= 0 || params.UserText == "" {
		return nil, rpc.ErrInvalidParams
	}
	source, err := peerSource(ctx, params.SourceDeviceName)
	if err != nil {
		return nil, err
	}
	adapter, ok := chat_svc.Chat().(inboundSessionAdapter)
	if !ok || adapter == nil {
		return nil, rpc.ErrInternal
	}
	result, err := adapter.RunPeerSession(ctx, params, source)
	if err != nil {
		if errors.Is(err, chat_svc.ErrPeerSessionNotFound) {
			return nil, rpc.ErrSessionNotFound
		}
		if errors.Is(err, chat_svc.ErrPeerExecutionUnavailable) {
			result, resultErr := chat_svc.PeerSessionExecutionResult(err)
			if resultErr != nil {
				return nil, resultErr
			}
			data, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				return nil, rpc.ErrInternal
			}
			return nil, &rpc.Error{Code: wire.ErrCodePeerExecutionUnavailable, Message: err.Error(), Data: data}
		}
		return nil, err
	}
	return wire.RunAck{SessionID: result.SessionID}, nil
}

func steerSession(ctx context.Context, raw json.RawMessage) (any, error) {
	var params wire.SteerParams
	if err := json.Unmarshal(raw, &params); err != nil || params.SessionID <= 0 || params.Text == "" {
		return nil, rpc.ErrInvalidParams
	}
	source, err := peerSource(ctx, "")
	if err != nil {
		return nil, err
	}
	adapter, ok := chat_svc.Chat().(inboundSessionAdapter)
	if !ok || adapter == nil {
		return nil, rpc.ErrInternal
	}
	if _, err := adapter.EnqueuePeerSession(ctx, params, source); err != nil {
		return nil, err
	}
	return wire.OK{}, nil
}

func submitAnswer(ctx context.Context, raw json.RawMessage) (any, error) {
	var params wire.SubmitAnswerParams
	if err := json.Unmarshal(raw, &params); err != nil || params.SessionID <= 0 || params.RequestID == "" {
		return nil, rpc.ErrInvalidParams
	}
	adapter, ok := chat_svc.Chat().(inboundSessionAdapter)
	if !ok || adapter == nil {
		return nil, rpc.ErrInternal
	}
	return adapter.AnswerPeerUserQuestion(ctx, params)
}

func submitToolPermission(ctx context.Context, raw json.RawMessage) (any, error) {
	var params wire.SubmitToolPermissionParams
	if err := json.Unmarshal(raw, &params); err != nil || params.SessionID <= 0 || params.RequestID == "" {
		return nil, rpc.ErrInvalidParams
	}
	adapter, ok := chat_svc.Chat().(inboundSessionAdapter)
	if !ok || adapter == nil {
		return nil, rpc.ErrInternal
	}
	return adapter.AnswerPeerToolPermission(ctx, params)
}

func attachSession(ctx context.Context, raw json.RawMessage) (any, error) {
	var params wire.SessionAttachParams
	if err := json.Unmarshal(raw, &params); err != nil || params.SessionID <= 0 {
		return nil, rpc.ErrInvalidParams
	}
	conn := rpc.ConnFromContext(ctx)
	if conn == nil {
		return nil, rpc.ErrUnauthorized
	}
	adapter, ok := chat_svc.Chat().(inboundSessionAdapter)
	if !ok || adapter == nil {
		return nil, rpc.ErrInternal
	}
	result, err := adapter.AttachPeerSession(ctx, params, connPeerSessionSubscriber{conn: conn})
	if err != nil {
		if errors.Is(err, chat_svc.ErrPeerSessionNotFound) {
			return nil, rpc.ErrSessionNotFound
		}
		return nil, err
	}
	return result, nil
}
