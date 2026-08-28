package terminal_svc_test

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeBackend struct{ name string }

func (f *fakeBackend) Open(_ context.Context, _ pty.Spec) (pty.Handle, error) {
	return nil, nil
}

func TestBackendSelector_Pick_LocalOnEmptyDeviceID(t *testing.T) {
	local := &fakeBackend{name: "local"}
	sel := terminal_svc.NewBackendSelector(local, func(string) (terminal_svc.PTYBackend, error) {
		t.Fatal("remote factory must not be called for local")
		return nil, nil
	})
	got, err := sel.Pick("")
	require.NoError(t, err)
	assert.Equal(t, local, got)
}

func TestBackendSelector_Pick_RemoteOnNonEmptyDeviceID(t *testing.T) {
	remote := &fakeBackend{name: "remote"}
	var gotID string
	sel := terminal_svc.NewBackendSelector(&fakeBackend{name: "local"}, func(id string) (terminal_svc.PTYBackend, error) {
		gotID = id
		return remote, nil
	})
	got, err := sel.Pick("42")
	require.NoError(t, err)
	assert.Equal(t, remote, got)
	assert.Equal(t, "42", gotID)
}
