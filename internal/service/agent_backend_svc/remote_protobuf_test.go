package agent_backend_svc

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type serviceProtoPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func serviceProtoPair() (*serviceProtoPipe, *serviceProtoPipe) {
	a, b, done, once := make(chan []byte, 4), make(chan []byte, 4), make(chan struct{}), &sync.Once{}
	return &serviceProtoPipe{a, b, done, once}, &serviceProtoPipe{b, a, done, once}
}
func (p *serviceProtoPipe) ReadFrame() ([]byte, error) {
	select {
	case value := <-p.in:
		return value, nil
	case <-p.done:
		return nil, io.EOF
	}
}
func (p *serviceProtoPipe) WriteFrame(value []byte) error {
	select {
	case p.out <- append([]byte(nil), value...):
		return nil
	case <-p.done:
		return io.EOF
	}
}
func (p *serviceProtoPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *serviceProtoPipe) Done() <-chan struct{} { return p.done }

func TestRemoteCLIAndSkillsUseTypedProtobufMethods(t *testing.T) {
	registry := protorpc.NewRegistry()
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_RESOLVE_PATH), func() *agentrewire.CLIResolvePathRequest { return &agentrewire.CLIResolvePathRequest{} }, func(_ context.Context, request *agentrewire.CLIResolvePathRequest) (*agentrewire.CLIResolvePathResponse, error) {
		require.Equal(t, "claudecode", request.Type)
		return &agentrewire.CLIResolvePathResponse{Path: "/remote/claude", Found: true}, nil
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_PROBE), func() *agentrewire.CLIProbeRequest { return &agentrewire.CLIProbeRequest{} }, func(_ context.Context, request *agentrewire.CLIProbeRequest) (*agentrewire.CLIProbeResponse, error) {
		require.Equal(t, "codex", request.BackendType)
		return &agentrewire.CLIProbeResponse{Text: "pong"}, nil
	})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SKILLS_LIST), func() *agentrewire.SkillsListRequest { return &agentrewire.SkillsListRequest{} }, func(_ context.Context, request *agentrewire.SkillsListRequest) (*agentrewire.SkillsListResponse, error) {
		require.Equal(t, "claudecode", request.BackendType)
		return &agentrewire.SkillsListResponse{Packs: []*agentrewire.InstalledSkillPack{{Id: "pack", Skills: []string{"skill"}, Installed: true}}}, nil
	})
	clientTransport, serverTransport := serviceProtoPair()
	clientConn, serverConn := protorpc.NewConn(clientTransport, protorpc.NewRegistry()), protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go clientConn.Serve(ctx)
	go serverConn.Serve(ctx)

	resolved, err := resolveRemoteCLIPath(ctx, clientConn, "claudecode")
	require.NoError(t, err)
	require.Equal(t, "/remote/claude", resolved.Path)
	probed, err := probeRemoteCLI(ctx, clientConn, handlers.CLIProbeParams{BackendType: "codex"})
	require.NoError(t, err)
	require.Equal(t, "pong", probed.Text)
	packs, err := listRemoteSkills(ctx, clientConn, "claudecode")
	require.NoError(t, err)
	require.Equal(t, "pack", packs[0].ID)
	require.Equal(t, []string{"skill"}, packs[0].Skills)
}
