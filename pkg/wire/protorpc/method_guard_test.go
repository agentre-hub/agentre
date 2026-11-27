package protorpc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

type guardCtxKey struct{}

// guardRig serves reg on one end of a pipe and returns the calling end.
func guardRig(t *testing.T, reg *protorpc.Registry) (*protorpc.Conn, context.Context) {
	t.Helper()
	a, b := pipePair()
	client, server := protorpc.NewConn(a, protorpc.NewRegistry()), protorpc.NewConn(b, reg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)
	return client, ctx
}

func callEmpty(ctx context.Context, conn *protorpc.Conn, methodID uint32) error {
	_, err := protorpc.CallMethod(ctx, conn, methodID, &agentrewire.Empty{}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
	return err
}

// Given a registry with a method guard, When the guard refuses a call, Then the
// caller receives the guard's error verbatim and the handler never runs.
func TestMethodGuard_GivenTheGuardRefuses_ThenTheCallerGetsItsErrorAndTheHandlerNeverRuns(t *testing.T) {
	const methodID uint32 = 41
	reg := protorpc.NewRegistry()
	ran := false
	protorpc.RegisterMethod(reg, methodID, func() *agentrewire.Empty { return &agentrewire.Empty{} }, func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) {
		ran = true
		return &agentrewire.Empty{}, nil
	})
	var guarded []uint32
	reg.SetMethodGuard(func(ctx context.Context, id uint32) (context.Context, error) {
		guarded = append(guarded, id)
		return ctx, &protorpc.Error{Code: -32005, Message: "token lacks scope chat"}
	})
	client, ctx := guardRig(t, reg)

	err := callEmpty(ctx, client, methodID)

	var rpcErr *protorpc.Error
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, int32(-32005), rpcErr.Code)
	require.Equal(t, "token lacks scope chat", rpcErr.Message)
	require.False(t, ran)
	require.Equal(t, []uint32{methodID}, guarded)
}

// Given a guard that admits the call, Then the handler runs on the context the
// guard returned, so the guard can hand its verdict to the handler.
func TestMethodGuard_GivenTheGuardAdmits_ThenTheHandlerSeesTheGuardsContext(t *testing.T) {
	const methodID uint32 = 42
	reg := protorpc.NewRegistry()
	seen := make(chan any, 1)
	protorpc.RegisterMethod(reg, methodID, func() *agentrewire.Empty { return &agentrewire.Empty{} }, func(ctx context.Context, _ *agentrewire.Empty) (*agentrewire.Empty, error) {
		seen <- ctx.Value(guardCtxKey{})
		require.NotNil(t, protorpc.ConnFromContext(ctx), "the guard's context still carries the connection")
		return &agentrewire.Empty{}, nil
	})
	reg.SetMethodGuard(func(ctx context.Context, _ uint32) (context.Context, error) {
		return context.WithValue(ctx, guardCtxKey{}, "verdict"), nil
	})
	client, ctx := guardRig(t, reg)

	require.NoError(t, callEmpty(ctx, client, methodID))
	require.Equal(t, "verdict", <-seen)
}

// Given a guard set before cloning, When a method is registered on the clone
// afterwards, Then the clone's methods are guarded too — a connection-level
// registry never escapes its host's guard.
func TestMethodGuard_GivenAClonedRegistry_ThenMethodsRegisteredOnTheCloneAreGuarded(t *testing.T) {
	const methodID uint32 = 43
	base := protorpc.NewRegistry()
	base.SetMethodGuard(func(ctx context.Context, _ uint32) (context.Context, error) {
		return ctx, &protorpc.Error{Code: -32005, Message: "refused"}
	})
	clone := base.Clone()
	protorpc.RegisterMethod(clone, methodID, func() *agentrewire.Empty { return &agentrewire.Empty{} }, func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) {
		return &agentrewire.Empty{}, nil
	})
	client, ctx := guardRig(t, clone)

	err := callEmpty(ctx, client, methodID)

	var rpcErr *protorpc.Error
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, "refused", rpcErr.Message)
}

// Given a guard, When the caller names a method nobody registered, Then the answer
// is still method-not-found: the guard judges calls, it does not replace absence.
func TestMethodGuard_GivenAnUnregisteredMethod_ThenAnswersMethodNotFoundWithoutAskingTheGuard(t *testing.T) {
	reg := protorpc.NewRegistry()
	asked := false
	reg.SetMethodGuard(func(ctx context.Context, _ uint32) (context.Context, error) {
		asked = true
		return ctx, &protorpc.Error{Code: -32005, Message: "refused"}
	})
	client, ctx := guardRig(t, reg)

	err := callEmpty(ctx, client, 44)

	var rpcErr *protorpc.Error
	require.True(t, errors.As(err, &rpcErr))
	require.Equal(t, protorpc.CodeMethodNotFound, rpcErr.Code)
	require.False(t, asked)
}
