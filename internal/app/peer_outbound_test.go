package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/service/peer_svc"
)

// stubPeerSvc 是绑定层测试用的 PeerSvc 假实现：记录调用、返回可配置结果。
type stubPeerSvc struct {
	lastListFingerprint string
	lastAttach          peer_svc.AttachRequest
	lastSteer           peer_svc.SteerRequest
	lastAnswer          peer_svc.SubmitAnswerRequest
	lastPermission      peer_svc.SubmitToolPermissionRequest
	lastDetachSession   int64
	runFreshResult      wire.RunAck
	listResult          *wire.SessionListResult
	attachResult        *wire.SessionAttachResult
	controlResult       *wire.PeerSessionControlResult
	err                 error
}

func (s *stubPeerSvc) ListSessions(_ context.Context, fingerprint string) (*wire.SessionListResult, error) {
	s.lastListFingerprint = fingerprint
	return s.listResult, s.err
}
func (s *stubPeerSvc) RunFresh(_ context.Context, _ peer_svc.RunFreshRequest) (wire.RunAck, error) {
	return s.runFreshResult, s.err
}
func (s *stubPeerSvc) Attach(_ context.Context, req peer_svc.AttachRequest) (*wire.SessionAttachResult, error) {
	s.lastAttach = req
	return s.attachResult, s.err
}
func (s *stubPeerSvc) Pull(_ context.Context, _ peer_svc.PullRequest) (*wire.SessionPullResult, error) {
	return &wire.SessionPullResult{Cursor: 1}, s.err
}
func (s *stubPeerSvc) Steer(_ context.Context, req peer_svc.SteerRequest) error {
	s.lastSteer = req
	return s.err
}
func (s *stubPeerSvc) SubmitAnswer(_ context.Context, req peer_svc.SubmitAnswerRequest) (*wire.PeerSessionControlResult, error) {
	s.lastAnswer = req
	return s.controlResult, s.err
}
func (s *stubPeerSvc) SubmitToolPermission(_ context.Context, req peer_svc.SubmitToolPermissionRequest) (*wire.PeerSessionControlResult, error) {
	s.lastPermission = req
	return s.controlResult, s.err
}
func (s *stubPeerSvc) Detach(_ context.Context, _ string, sessionID int64) error {
	s.lastDetachSession = sessionID
	return s.err
}
func (s *stubPeerSvc) Close() error { return nil }

// Given a wired peer service, when the binding is called, then it is a thin
// passthrough to peer_svc (parse → svc.X → return), and the desktop App-not-
// running sentinel surfaces unchanged.
func TestAppPeerBindings_GivenWiredService_WhenCalled_ThenPassThrough(t *testing.T) {
	stub := &stubPeerSvc{
		listResult:    &wire.SessionListResult{Sessions: []wire.SessionSummary{{SessionID: 7}}},
		attachResult:  &wire.SessionAttachResult{SessionID: 7, LatestSeq: 12},
		controlResult: &wire.PeerSessionControlResult{AlreadyHandled: true},
	}
	previous := peerSvcAccessor
	peerSvcAccessor = func() peer_svc.PeerSvc { return stub }
	t.Cleanup(func() { peerSvcAccessor = previous })

	a := &App{}
	ctx := context.Background()
	a.ctx = ctx

	list, err := a.PeerListSessions("sha256:peer-desktop")
	require.NoError(t, err)
	assert.Equal(t, "sha256:peer-desktop", stub.lastListFingerprint)
	require.Len(t, list.Sessions, 1)

	att, err := a.PeerAttach(peer_svc.AttachRequest{Fingerprint: "sha256:peer-desktop", SessionID: 7})
	require.NoError(t, err)
	assert.Equal(t, int64(12), att.LatestSeq)

	require.NoError(t, a.PeerSteer(peer_svc.SteerRequest{Fingerprint: "sha256:peer-desktop", SessionID: 7, Text: "接着干"}))
	assert.Equal(t, "接着干", stub.lastSteer.Text)

	res, err := a.PeerSubmitAnswer(peer_svc.SubmitAnswerRequest{Fingerprint: "sha256:peer-desktop", SessionID: 7, RequestID: "req-1"})
	require.NoError(t, err)
	assert.True(t, res.AlreadyHandled, "alreadyHandled must reach the binding caller")

	require.NoError(t, a.PeerDetach("sha256:peer-desktop", 7))
	assert.Equal(t, int64(7), stub.lastDetachSession)
}

// Given the desktop App on the target is not running, when the binding calls
// peer_svc, then the distinct sentinel is what the frontend sees (R2).
func TestAppPeerBindings_GivenDesktopAppNotRunning_ThenSentinelSurfaces(t *testing.T) {
	stub := &stubPeerSvc{err: errPeerServiceUnavailable}
	previous := peerSvcAccessor
	peerSvcAccessor = func() peer_svc.PeerSvc { return stub }
	t.Cleanup(func() { peerSvcAccessor = previous })

	a := &App{ctx: context.Background()}
	_, err := a.PeerListSessions("sha256:peer-desktop")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errPeerServiceUnavailable))
}

// Given the peer service is not wired (nil), when the binding is called, then
// it fails open with the sentinel instead of panicking.
func TestAppPeerBindings_GivenNoService_WhenCalled_ThenFailOpen(t *testing.T) {
	previous := peerSvcAccessor
	peerSvcAccessor = func() peer_svc.PeerSvc { return nil }
	t.Cleanup(func() { peerSvcAccessor = previous })

	a := &App{ctx: context.Background()}
	_, err := a.PeerListSessions("sha256:peer-desktop")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errPeerServiceUnavailable))
}
