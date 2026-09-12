//go:build windows

package agentredipc

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Endpoint returns the current data directory's opaque named-pipe path.
func Endpoint(dataDir string) string {
	return WindowsPipePath(dataDir)
}

// Listen creates a named pipe whose protected DACL grants full access only to
// the Windows identity running agentred.
func Listen(dataDir string) (net.Listener, error) {
	securityDescriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(Endpoint(dataDir), &winio.PipeConfig{
		SecurityDescriptor: securityDescriptor,
	})
}

// DialContext returns an HTTP transport dialer for the local named pipe.
func DialContext(dataDir string) func(context.Context, string, string) (net.Conn, error) {
	path := Endpoint(dataDir)
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return winio.DialPipeContext(ctx, path)
	}
}

func currentUserSecurityDescriptor() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return securityDescriptorForSID(user.User.Sid.String()), nil
}
