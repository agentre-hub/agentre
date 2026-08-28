package server_svc

import (
	"context"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/relaytransport"
	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

// ServerSvc 桌面端接入 Hub 的服务接口。
type ServerSvc interface {
	GetState(ctx context.Context) (*server_state_entity.ServerState, error)
	StartLogin(ctx context.Context, serverURL string) (*StartLoginResult, error)
	PollLoginToken(ctx context.Context, deviceCode string) (bool, error)
	CancelLogin(ctx context.Context) error
	ListDevices(ctx context.Context) ([]Device, error)
	Logout(ctx context.Context) error
	Refresh(ctx context.Context) error
	// RefreshWithBackoff 是开机热身用的刷新：服务端够不着时保留登录态、标记离线
	// 并退避重试；只有服务端明确拒绝凭据才清掉本地登录。会一直跑到成功/被拒/登出/
	// ctx 结束，调用方自己决定是否放进 goroutine。
	RefreshWithBackoff(ctx context.Context)
	// Offline 报告服务端此刻够不着（退避重试进行中）。
	Offline() bool
	ClearLogin(ctx context.Context) error
	// CheckURL validates that the given Server URL is reachable + healthy without
	// affecting the singleton service's state. Returns the hub-reported version.
	CheckURL(ctx context.Context, serverURL string) (string, error)
	SetEmitter(emit func(any))
	// AccessToken returns the current hub access token (empty when not logged in).
	// Consumed by the relay dial to authenticate /v1/relay/client.
	AccessToken() string
	// NewInboundHubLink creates the desktop's one relay-registration transport.
	// The peer package layers its session-level adapter on top of this link.
	NewInboundHubLink(ctx context.Context) (*relaytransport.HubLink, error)
	// DialDaemonRelay connects to the given daemon through the account relay,
	// presenting peerFingerprint (the desktop's own device fingerprint) to
	// auth.account — the same identity the LAN path presents (R5/R6).
	DialDaemonRelay(ctx context.Context, daemonFingerprint, peerFingerprint string) (client.ProtobufConnection, error)
	// DialDesktopRelay connects to a desktop App through the account relay. A
	// registered desktop with no live App maps to ErrDesktopAppNotRunning,
	// distinct from the existing agentred offline result.
	DialDesktopRelay(ctx context.Context, desktopFingerprint, peerFingerprint string) (client.ProtobufConnection, error)
	// SyncPush 上行一批本地改动；超窗口设备返回 syncwire.ErrResyncRequired（R6a）。
	SyncPush(ctx context.Context, items []syncwire.PushItem) ([]syncwire.PushResult, error)
	// SyncPull 按版本游标增量下行；cursor = 0 拉全量快照。
	SyncPull(ctx context.Context, cursor int64, limit int) (*syncwire.PullPage, error)
	// ReportLocalPaths 上报本机路径整份快照（R16）。
	ReportLocalPaths(ctx context.Context, items []syncwire.LocalPathReportItem) error
	// PutAvatar 把本机持有的头像正文按内容哈希推给对端（R16a）。
	PutAvatar(ctx context.Context, contentHash, contentType, content string) error
	// GetAvatar 取一份尚未持有的头像正文（R16a）。
	GetAvatar(ctx context.Context, contentHash string) (content, contentType string, err error)
}

var defaultSvc ServerSvc

// Hub 返回默认实现单例。
func Server() ServerSvc { return defaultSvc }

// SetDefault 由 bootstrap 注入实现。
func SetDefault(s ServerSvc) { defaultSvc = s }

// service 是 ServerSvc 的具体实现。
type service struct {
	mu            sync.Mutex
	client        *serverClient
	loginInFlight bool
	offline       bool      // 服务端够不着；由 markOffline/markOnline 翻转
	emitState     func(any) // 由 bootstrap 注入的 Wails 事件发射器；测试时可为 nil
	// sleepFn 是退避等待，单测注入假等待用；返回 false 表示 ctx 结束、别再试了。
	sleepFn func(ctx context.Context, d time.Duration) bool
}

// New 构造一个 service。client + emit 由 bootstrap 装配。
func New(client *serverClient, emit func(any)) ServerSvc {
	return &service{client: client, emitState: emit, sleepFn: waitOrDone}
}

// Refresh exposes the package-private refresh() for callers like bootstrap.HubBoot.
func (s *service) Refresh(ctx context.Context) error { return s.refresh(ctx) }

// SetEmitter swaps the Wails event emitter at runtime. Called from app.go.startup
// once the wails context exists; safe to call before any concurrent reader.
func (s *service) SetEmitter(emit func(any)) {
	s.mu.Lock()
	s.emitState = emit
	s.mu.Unlock()
}

// emit safely invokes the registered Wails event emitter, if any.
// Reads s.emitState under the same mutex SetEmitter uses to write it.
func (s *service) emit(payload any) {
	s.mu.Lock()
	fn := s.emitState
	s.mu.Unlock()
	if fn != nil {
		fn(payload)
	}
}

// getClient returns the current serverClient under the mutex.
// client field is protected by s.mu (shared with loginInFlight + emitState).
func (s *service) getClient() *serverClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

// setClient atomically replaces the serverClient under the mutex.
func (s *service) setClient(c *serverClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
}

// GetState, ListDevices, and Logout are implemented in state.go, devices.go, and logout.go.
