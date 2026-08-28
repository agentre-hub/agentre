package chat_svc

import (
	"context"
	"errors"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// BorrowDeviceClient leases a device's daemon client from the shared
// connection pool. The release closure must be called when the caller is
// done; failure to release will keep the lease alive until app shutdown.
//
// Used by terminal_svc to talk to a remote daemon — same pool/lease
// machinery chat_svc uses for runtime.* RPCs, so a single daemon
// connection is shared between chat and terminal traffic.
func BorrowDeviceClient(ctx context.Context, deviceID int64) (client.ProtobufConnection, func(), error) {
	rds := remote_device_svc.Default()
	if rds == nil {
		return nil, nil, errors.New("remote_device_svc not initialized")
	}
	pool := rds.Pool()
	if pool == nil {
		return nil, nil, errors.New("conn pool not initialized")
	}
	lease, err := pool.Borrow(ctx, deviceID)
	if err != nil {
		return nil, nil, err
	}
	return lease.Client(), lease.Release, nil
}
