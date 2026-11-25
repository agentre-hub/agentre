// Package ctlaccount is the account side of agrctl's conversation commands: a
// one-shot account peer that reaches the machine holding a conversation
// through the account server's relay, the same dispatch flow the web console
// uses — dispatch-target picks the device, machine:<fingerprint> opens the
// channel, auth.account, runtime.run, save to the account, pin the overrides.
//
// Two hosts drive it: agrctl (a logged-in account or a personal token) and
// agentred's ctl proxy (a console-owned session's agrctl, with agentred's own
// device credential). It never reaches a service, the cago runtime or Wails
// (internal/guard keeps agrctl slim); the credential, the account server's
// HTTP surface, the relay link and the resource catalog are injected. Result
// types carry the desktop /ctl/v1 JSON shapes so a host can encode them as is.
package ctlaccount

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
	"github.com/agentre-hub/agentre/internal/pkg/relaypeer"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// Server is the account server's HTTP surface with the caller's bearer: a
// non-2xx answer is a *ctlclient.ServerError.
type Server interface {
	GetRaw(ctx context.Context, pathAndQuery string) ([]byte, error)
	PostRaw(ctx context.Context, path string, body []byte) ([]byte, error)
}

// Peer opens authenticated channels to the account's machines (a
// *relaypeer.Peer, or a host wrapper that keeps calls to itself in-process).
type Peer interface {
	Open(ctx context.Context, target string) (*relaypeer.Conn, error)
	Close()
}

// Options configures a Client.
type Options struct {
	Server  Server
	Peer    Peer
	Catalog Catalog
	// ServerURL is the account server's base URL, only for messages.
	ServerURL string
	// DeviceName is the source the turns this client starts are filed under.
	DeviceName string
	// CredentialRejected is what a relay credential refusal reads as (agrctl:
	// "log in again" or "the personal token was rejected").
	CredentialRejected error
	// Warn reports a partial failure that does not fail the command; nil = drop.
	Warn func(msg string)
	// PollInterval is how often wait / tail recheck the session; 0 = 1s.
	PollInterval time.Duration
	// ReconnectDelay is the pause before redialing after a drop; 0 = 1s.
	ReconnectDelay time.Duration
}

// Client is one account peer. Close it to end its relay link.
type Client struct {
	opts Options
	srv  Server
	cat  Catalog
	peer Peer
}

// New builds a Client.
func New(opts Options) *Client {
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	if opts.ReconnectDelay <= 0 {
		opts.ReconnectDelay = time.Second
	}
	if opts.CredentialRejected == nil {
		opts.CredentialRejected = errors.New("the account rejected the credential")
	}
	return &Client{opts: opts, srv: opts.Server, cat: opts.Catalog, peer: opts.Peer}
}

// Close ends the relay link and every channel on it.
func (a *Client) Close() { a.peer.Close() }

func (a *Client) warnf(format string, args ...any) {
	if a.opts.Warn != nil {
		a.opts.Warn(fmt.Sprintf(format, args...))
	}
}

// MaxReconnects 是连续重连的上限：执行者一直回不来时最终按执行失败退出。
const MaxReconnects = 60

// 会话状态（与桌面执行者 /ctl/v1/wait 的 status 同值）。
const (
	StatusIdle    = "idle"
	StatusRunning = "running"
	StatusWaiting = "waiting"
	StatusFailed  = "failed"
)

// Lifecycle 值与执行端 daemon_sessions.lifecycle_state 同值。
const (
	lifecycleRunning = "running"
	lifecycleFailed  = "failed"
)

var (
	// ErrSessionNotVisible 不区分「不存在」与「看不到」。
	ErrSessionNotVisible = errors.New("session not found or not accessible")
	// ErrSessionBusy 是向运行中的会话发新的一轮（没带 --steer）。
	ErrSessionBusy = errors.New("session is running; use --steer to interject, or agrctl wait first")
	// ErrCursorGone 是 tail 的游标已经早于执行端还留着的帧。
	ErrCursorGone = errors.New("cursor is no longer valid; read the history with agrctl messages")
)

// UsageError 是调用方给错了东西（agrctl 退出码 2，HTTP 422）。
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Usagef 造一个 UsageError。
func Usagef(format string, a ...any) error { return &UsageError{Msg: fmt.Sprintf(format, a...)} }

// AmbiguousError 是定位命中了多条资源；Err 是给人看的那句（可以是宿主自己的错误类型）。
type AmbiguousError struct{ Err error }

func (e *AmbiguousError) Error() string { return e.Err.Error() }
func (e *AmbiguousError) Unwrap() error { return e.Err }

// WaitingError 是 tail --until-idle 遇到会话在等输入（agrctl 退出码 4）。
type WaitingError struct{ Session string }

func (e *WaitingError) Error() string { return "session " + e.Session + " is waiting for input" }

// ReachErr 把 relay / 执行端的失败说成给人看的那句话，并指明是哪台设备。
func (a *Client) ReachErr(err error, device, session string) error {
	what := "device " + device
	if device == "" {
		what = "the device"
		if session != "" {
			what = "the device holding session " + session
		}
	}
	switch {
	case errors.Is(err, relaypeer.ErrPeerVersionMismatch):
		return fmt.Errorf("%s needs to be upgraded to the release this agrctl speaks: %v", what, err)
	case errors.Is(err, relaypeer.ErrTargetOffline):
		return fmt.Errorf("%s is offline", what)
	case errors.Is(err, relaypeer.ErrTargetNotFound), errors.Is(err, relaypeer.ErrTargetForbidden):
		return fmt.Errorf("%s cannot be reached through the account", what)
	case errors.Is(err, relaypeer.ErrForwardFailed):
		return fmt.Errorf("the account relay could not reach %s; try again", what)
	case errors.Is(err, relaypeer.ErrCredentialRejected):
		return a.opts.CredentialRejected
	case errors.Is(err, relaypeer.ErrRelayUnavailable):
		return fmt.Errorf("cannot reach the account relay at %s: %v", a.opts.ServerURL, err)
	case errors.Is(err, protorpc.ErrConnClosed):
		return fmt.Errorf("lost the connection to %s", what)
	case isSessionRunning(err):
		return ErrSessionBusy
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) {
		return errors.New(rpcErr.Message)
	}
	return err
}

// transient 报告一次失败值不值得重连再试：执行端或 server 明确的拒绝不重试。
func transient(err error) bool {
	var rpcErr *rpcerror.Error
	var se *ctlclient.ServerError
	switch {
	case errors.As(err, &rpcErr), errors.As(err, &se),
		errors.Is(err, ErrSessionNotVisible),
		errors.Is(err, relaypeer.ErrPeerVersionMismatch), errors.Is(err, relaypeer.ErrCredentialRejected),
		errors.Is(err, relaypeer.ErrTargetInvalid), errors.Is(err, relaypeer.ErrTargetForbidden),
		errors.Is(err, ctlclient.ErrLoginExpired), errors.Is(err, ctlclient.ErrTokenRejected), errors.Is(err, ctlclient.ErrNotLoggedIn),
		errors.Is(err, context.Canceled):
		return false
	}
	return true
}

func rpcCode(err error) int32 {
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) {
		return rpcErr.Code
	}
	return 0
}

func isSessionRunning(err error) bool { return rpcCode(err) == rpcerror.CodeRuntimeSessionRunning }

// ServerErr 把账号 server 的拒绝原样透出。
func ServerErr(err error) error {
	var se *ctlclient.ServerError
	if errors.As(err, &se) {
		return errors.New(se.Message)
	}
	return err
}
