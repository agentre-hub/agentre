package app

import (
	"context"
	"strconv"
	"sync"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/pkg/pty/local"
	"github.com/agentre-hub/agentre/internal/pkg/pty/remote"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
)

// ptyBackendAdapter bridges pty.Backend → terminal_svc.PTYBackend (same
// method set in different packages; explicit wrapper required by Go's
// nominal typing).
type ptyBackendAdapter struct{ be pty.Backend }

func (a ptyBackendAdapter) Open(ctx context.Context, spec pty.Spec) (pty.Handle, error) {
	return a.be.Open(ctx, spec)
}

func resolveLocalCommandScope(
	ctx context.Context,
	req *chat_svc.ResolveLocalCommandScopeRequest,
) (*chat_svc.LocalCommandScope, error) {
	chat := chat_svc.Chat()
	if chat == nil {
		return nil, terminal_svc.ErrCommandScopeResolverNotInitialized
	}
	scope, err := chat.ResolveLocalCommandScope(ctx, req)
	if err != nil {
		return nil, err
	}
	if scope == nil {
		return nil, terminal_svc.ErrCommandScopeUnavailable
	}
	return scope, nil
}

type terminalDaemonClientBorrow func(
	ctx context.Context,
	deviceID int64,
) (remote.DaemonClient, func(), error)

// terminalRemoteWiring is the composition-root owner of the remote terminal
// adapter cache. Backend selection stays cheap; each returned backend borrows
// only if terminal_svc reaches PTYBackend.Open.
type terminalRemoteWiring struct {
	borrow   terminalDaemonClientBorrow
	adapters *terminalClientAdapterCache
}

func newTerminalRemoteWiring(borrow terminalDaemonClientBorrow) *terminalRemoteWiring {
	return &terminalRemoteWiring{
		borrow:   borrow,
		adapters: newTerminalClientAdapterCache(),
	}
}

func (w *terminalRemoteWiring) Backend(deviceIDStr string) (terminal_svc.PTYBackend, error) {
	deviceID, err := strconv.ParseInt(deviceIDStr, 10, 64)
	if err != nil {
		return nil, err
	}
	return &lazyTerminalRemoteBackend{
		deviceID: deviceID,
		borrow:   w.borrow,
		adapters: w.adapters,
	}, nil
}

type lazyTerminalRemoteBackend struct {
	deviceID int64
	borrow   terminalDaemonClientBorrow
	adapters *terminalClientAdapterCache
}

func (b *lazyTerminalRemoteBackend) Open(ctx context.Context, spec pty.Spec) (pty.Handle, error) {
	client, release, err := b.borrow(ctx, b.deviceID)
	if err != nil {
		return nil, err
	}
	adapter := b.adapters.ForClient(b.deviceID, client)
	return remote.NewBackendWithLease(adapter, release).Open(ctx, spec)
}

// terminalClientAdapterCache keeps only the current connection generation for
// each device. Live handles retain old adapters directly, so replacement does
// not disrupt them and an old close cannot evict the replacement entry.
type terminalClientAdapterCache struct {
	mu      sync.Mutex
	entries map[int64]*terminalClientAdapterEntry
}

type terminalClientAdapterEntry struct {
	closed  <-chan struct{}
	adapter *remote.ClientAdapter
}

func newTerminalClientAdapterCache() *terminalClientAdapterCache {
	return &terminalClientAdapterCache{entries: map[int64]*terminalClientAdapterEntry{}}
}

func (c *terminalClientAdapterCache) ForClient(
	deviceID int64,
	client remote.DaemonClient,
) *remote.ClientAdapter {
	// Active daemon clients expose one stable Closed channel per connection;
	// channel identity is therefore the pool generation identity without
	// depending on its concrete client wrapper.
	closed := client.Closed()
	c.mu.Lock()
	if current := c.entries[deviceID]; current != nil && closed != nil && current.closed == closed {
		adapter := current.adapter
		c.mu.Unlock()
		return adapter
	}
	entry := &terminalClientAdapterEntry{
		closed:  closed,
		adapter: remote.NewClientAdapter(client),
	}
	c.entries[deviceID] = entry
	c.mu.Unlock()
	if closed != nil {
		go c.evictOnClose(deviceID, entry)
	}
	return entry.adapter
}

func (c *terminalClientAdapterCache) evictOnClose(deviceID int64, entry *terminalClientAdapterEntry) {
	<-entry.closed
	c.mu.Lock()
	if c.entries[deviceID] == entry {
		delete(c.entries, deviceID)
	}
	c.mu.Unlock()
}

func borrowTerminalDaemonClient(
	ctx context.Context,
	deviceID int64,
) (remote.DaemonClient, func(), error) {
	client, release, err := chat_svc.BorrowDeviceClient(ctx, deviceID)
	if err != nil {
		return nil, nil, err
	}
	return client, release, nil
}

func newTerminalService(appCtx context.Context) *terminal_svc.Service {
	localBE := local.NewBackend()
	remoteWiring := newTerminalRemoteWiring(borrowTerminalDaemonClient)
	selector := terminal_svc.NewBackendSelector(
		ptyBackendAdapter{be: localBE}, remoteWiring.Backend,
	)
	emitter := terminal_svc.EmitterFunc(func(_ context.Context, name string, payload any) {
		wailsruntime.EventsEmit(appCtx, name, payload)
	})
	service := terminal_svc.NewService(selector, emitter)
	service.SetCommandScopeResolver(func(
		ctx context.Context,
		req terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		scope, err := resolveLocalCommandScope(
			ctx,
			&chat_svc.ResolveLocalCommandScopeRequest{SessionID: req.SessionID},
		)
		if err != nil {
			return nil, err
		}
		return &terminal_svc.CommandScope{DeviceID: scope.DeviceID, Cwd: scope.Cwd}, nil
	})
	return service
}
