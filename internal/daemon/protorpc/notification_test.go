package protorpc_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestNotificationSubscriptionsAreOrderedIsolatedAndCloneSafe(t *testing.T) {
	registry := protorpc.NewRegistry()
	var mu sync.Mutex
	var calls []string
	appendCall := func(value string) { mu.Lock(); calls = append(calls, value); mu.Unlock() }
	unsubscribeFirst := registry.SubscribeNotification(func(context.Context, *agentrewire.RpcNotification) error {
		appendCall("first")
		return errors.New("ignored handler failure")
	})
	registry.SubscribeNotification(func(context.Context, *agentrewire.RpcNotification) error {
		appendCall("second")
		return nil
	})
	clone := registry.Clone()
	unsubscribeFirst()
	unsubscribeFirst()
	registry.SubscribeNotification(func(context.Context, *agentrewire.RpcNotification) error {
		appendCall("original-only")
		return nil
	})

	dispatchNotification(t, registry, &agentrewire.RpcNotification{})
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(calls) == 2 }, time.Second, time.Millisecond)
	require.Equal(t, []string{"second", "original-only"}, calls)
	mu.Lock()
	calls = nil
	mu.Unlock()
	dispatchNotification(t, clone, &agentrewire.RpcNotification{})
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(calls) == 2 }, time.Second, time.Millisecond)
	require.Equal(t, []string{"first", "second"}, calls, "clone must keep its ordered snapshot and isolate handler errors")
}

func TestNotificationSubscriptionsPermitConcurrentSubscribeAndUnsubscribe(t *testing.T) {
	registry := protorpc.NewRegistry()
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unsubscribe := registry.SubscribeNotification(func(context.Context, *agentrewire.RpcNotification) error { return nil })
			unsubscribe()
			unsubscribe()
		}()
	}
	wg.Wait()
	dispatchNotification(t, registry, &agentrewire.RpcNotification{})
}

func dispatchNotification(t *testing.T, registry *protorpc.Registry, notification *agentrewire.RpcNotification) {
	t.Helper()
	a, b := pipePair()
	client := protorpc.NewConn(a, registry)
	server := protorpc.NewConn(b, protorpc.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	go client.Serve(ctx)
	require.NoError(t, server.Notify(notification))
	t.Cleanup(cancel)
}
