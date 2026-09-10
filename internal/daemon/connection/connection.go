package connection

import (
	"context"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
)

type AuthState struct {
	Authenticated     bool
	DeviceFingerprint string
	DeviceName        string
	AccountID         string
}

type Conn interface {
	Auth() AuthState
	Done() <-chan struct{}
	Close() error
}

type protobufConn struct{ conn *protorpc.Conn }

func Protobuf(conn *protorpc.Conn) Conn {
	if conn == nil {
		return nil
	}
	return protobufConn{conn: conn}
}
func (c protobufConn) Auth() AuthState {
	auth := c.conn.Auth()
	return AuthState{Authenticated: auth.Authenticated, DeviceFingerprint: auth.DeviceFingerprint, DeviceName: auth.DeviceName, AccountID: auth.AccountID}
}
func (c protobufConn) Done() <-chan struct{} { return c.conn.Done() }
func (c protobufConn) Close() error          { return c.conn.Close() }

func Normalize(conn any) Conn {
	switch value := conn.(type) {
	case Conn:
		return value
	case *protorpc.Conn:
		if value != nil {
			return Protobuf(value)
		}
	}
	return nil
}

func FromContext(ctx context.Context) Conn {
	if conn := protorpc.ConnFromContext(ctx); conn != nil {
		return Protobuf(conn)
	}
	return nil
}
