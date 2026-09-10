package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/protobufadapter"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/daemon/remotefs"
	"github.com/agentre-hub/agentre/internal/daemon/workspacefs"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestProtobufPeripheralMethodsPreserveNativeBinaryPayloads(t *testing.T) {
	registry := protorpc.NewRegistry()
	wantBody := []byte{0, 255, 1, 254}
	protobufadapter.RegisterPeripheralMethods(registry, protobufadapter.PeripheralDeps{
		MCPProxy: func(_ context.Context, request *agentrewire.MCPProxyRequest) (*agentrewire.MCPProxyResponse, error) {
			require.Equal(t, wantBody, request.Body)
			return &agentrewire.MCPProxyResponse{Status: 201, Body: append([]byte(nil), request.Body...)}, nil
		},
		Skills:      handlers.NewSkillsHandlers(),
		RemoteFS:    remotefs.NewHandlers(remotefs.Options{}),
		WorkspaceFS: workspacefs.NewHandlers(workspacefs.Options{}),
	})
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	server.SetAuth(protorpc.AuthState{Authenticated: true})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)

	response, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY),
		&agentrewire.MCPProxyRequest{Body: wantBody}, func() *agentrewire.MCPProxyResponse { return &agentrewire.MCPProxyResponse{} })
	require.NoError(t, err)
	require.Equal(t, int32(201), response.Status)
	require.Equal(t, wantBody, response.Body)
}

func TestProtobufPeripheralMethodsRequireAuthentication(t *testing.T) {
	registry := protorpc.NewRegistry()
	protobufadapter.RegisterPeripheralMethods(registry, protobufadapter.PeripheralDeps{
		Skills: handlers.NewSkillsHandlers(), RemoteFS: remotefs.NewHandlers(remotefs.Options{}), WorkspaceFS: workspacefs.NewHandlers(workspacefs.Options{}),
	})
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_REMOTE_FS_LIST_DIR),
		&agentrewire.RemoteFsListDirRequest{}, func() *agentrewire.RemoteFsListDirResponse { return &agentrewire.RemoteFsListDirResponse{} })
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, int32(-32001), rpcErr.Code)
}

func TestProtobufWorkspaceReadFileReturnsImageAsNativeBytes(t *testing.T) {
	root := t.TempDir()
	want := []byte{0x89, 'P', 'N', 'G', 0, 255}
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.png"), want, 0o600))
	registry := protorpc.NewRegistry()
	protobufadapter.RegisterPeripheralMethods(registry, protobufadapter.PeripheralDeps{WorkspaceFS: workspacefs.NewHandlers(workspacefs.Options{})})
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	server.SetAuth(protorpc.AuthState{Authenticated: true})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)

	response, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE),
		&agentrewire.WorkspaceFsReadFileRequest{Root: root, RelPath: "sample.png"}, func() *agentrewire.WorkspaceFsReadFileResponse { return &agentrewire.WorkspaceFsReadFileResponse{} })
	require.NoError(t, err)
	require.Equal(t, "image/png", response.ContentType)
	require.Equal(t, want, response.Content)
}

func TestProtobufWorkspaceErrorsKeepStableTypedCodes(t *testing.T) {
	registry := protorpc.NewRegistry()
	protobufadapter.RegisterPeripheralMethods(registry, protobufadapter.PeripheralDeps{WorkspaceFS: workspacefs.NewHandlers(workspacefs.Options{})})
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	server.SetAuth(protorpc.AuthState{Authenticated: true})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE),
		&agentrewire.WorkspaceFsReadFileRequest{}, func() *agentrewire.WorkspaceFsReadFileResponse { return &agentrewire.WorkspaceFsReadFileResponse{} })
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, int32(-32042), rpcErr.Code)
}

func TestDaemonProtobufRegistryIncludesStaticPeripheralMethods(t *testing.T) {
	daemon, err := New(Options{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { closeDB(daemon.db) })
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, daemon.protobufRegistry.Clone())
	server.SetAuth(protorpc.AuthState{Authenticated: true})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_READ_FILE),
		&agentrewire.WorkspaceFsReadFileRequest{}, func() *agentrewire.WorkspaceFsReadFileResponse { return &agentrewire.WorkspaceFsReadFileResponse{} })
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, int32(-32042), rpcErr.Code, "registered workspace method must preserve the no-cwd domain error")

	skills, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_SKILLS_CATALOG),
		&agentrewire.SkillCatalogRequest{BackendType: "nonesuch"}, func() *agentrewire.SkillCatalogResponse { return &agentrewire.SkillCatalogResponse{} })
	require.NoError(t, err)
	require.Equal(t, "unsupported", skills.Discovery)
}
