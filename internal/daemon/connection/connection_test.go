package connection_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/connection"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
)

func TestConnectionPreservesSocketIdentity(t *testing.T) {
	t.Run("given repeated wrapping of one protobuf connection, then auth and map identity remain equal", func(t *testing.T) {
		conn := protorpc.NewConn(nil, protorpc.NewRegistry())
		conn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "fp", AccountID: "account"})
		first, second := connection.Protobuf(conn), connection.Protobuf(conn)
		require.Equal(t, first, second)
		require.Equal(t, connection.AuthState{Authenticated: true, DeviceFingerprint: "fp", AccountID: "account"}, first.Auth())
		require.Len(t, map[connection.Conn]struct{}{first: {}, second: {}}, 1)
	})
}
