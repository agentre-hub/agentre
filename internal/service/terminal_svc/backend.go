package terminal_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
)

//go:generate mockgen -source=backend.go -destination=mocks/mock_backend.go -package=mocks

type PTYBackend interface {
	Open(ctx context.Context, spec pty.Spec) (pty.Handle, error)
}

type RemoteBackendFactory func(deviceID string) (PTYBackend, error)

type BackendSelector struct {
	local        PTYBackend
	remoteFactor RemoteBackendFactory
}

func NewBackendSelector(local PTYBackend, remote RemoteBackendFactory) *BackendSelector {
	return &BackendSelector{local: local, remoteFactor: remote}
}

func (s *BackendSelector) Pick(deviceID string) (PTYBackend, error) {
	if deviceID == "" {
		return s.local, nil
	}
	return s.remoteFactor(deviceID)
}
