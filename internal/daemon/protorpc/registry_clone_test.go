package protorpc

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// cloneCopiedFields names every Registry field Clone is expected to carry into the
// copy, and cloneSkippedFields names the ones it deliberately leaves at their zero
// value. Together they must account for the whole struct.
//
// Every LAN and relay connection gets its registry through Clone, so a field that
// Clone forgets is not a partial copy — it is nil on every connection in production
// while staying perfectly healthy in any test that builds a Registry directly. The
// typed-handler fields this registry used to carry were exactly that, unnoticed
// only because nothing ever read them.
var (
	cloneCopiedFields = map[string]string{
		"methods":            "handlers registered before the clone must still dispatch on it",
		"nextNotificationID": "IDs must keep increasing, or an unsubscribe removes the wrong subscriber",
		"notifications":      "subscribers registered before the clone must still be delivered to",
	}
	cloneSkippedFields = map[string]string{
		"methodMu":       "each Registry owns its own mutex; copying one would alias the lock",
		"notificationMu": "each Registry owns its own mutex; copying one would alias the lock",
	}
)

// Given a new field is added to Registry, When Clone is not taught to carry it,
// Then this guard fails and names the field — because no behavioral test can notice
// state that nothing reads yet.
func TestRegistryClone_GivenEveryRegistryField_WhenCloned_ThenItIsCopiedOrDeliberatelySkipped(t *testing.T) {
	registryType := reflect.TypeOf(Registry{})
	for i := range registryType.NumField() {
		name := registryType.Field(i).Name
		_, copied := cloneCopiedFields[name]
		_, skipped := cloneSkippedFields[name]
		require.Truef(t, copied || skipped,
			"Registry.%s is accounted for in neither cloneCopiedFields nor cloneSkippedFields: "+
				"teach Clone to carry it (and cover it below), or record here why it is skipped", name)
	}
	require.Equal(t, registryType.NumField(), len(cloneCopiedFields)+len(cloneSkippedFields),
		"a field was removed from Registry but is still listed here")
}

// Given a registry with methods and notification subscribers, When it is cloned,
// Then the clone carries both, and later mutations of either side stay isolated.
func TestRegistryClone_GivenRegisteredState_WhenCloned_ThenTheCloneCarriesItAndStaysIsolated(t *testing.T) {
	methodID := uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY)
	origin := NewRegistry()
	RegisterMethod(
		origin,
		methodID,
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) {
			return &agentrewire.Empty{}, nil
		},
	)
	var mu sync.Mutex
	var calls []string
	origin.SubscribeNotification(func(context.Context, *agentrewire.RpcNotification) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, "before-clone")
		return nil
	})

	clone := origin.Clone()

	// methods: registered before the clone, so the clone must dispatch it.
	_, err := clone.dispatchMethod(context.Background(), methodID, nil)
	require.NoError(t, err, "clone lost the method registered before it was taken")

	// notifications: the pre-clone subscriber is carried over.
	clone.dispatchNotification(context.Background(), &agentrewire.RpcNotification{})
	mu.Lock()
	require.Equal(t, []string{"before-clone"}, calls)
	calls = nil
	mu.Unlock()

	// nextNotificationID continues from the origin's, so the clone's own
	// subscriptions cannot collide with the IDs it inherited.
	require.Equal(t, origin.nextNotificationID, clone.nextNotificationID)
	unsubscribe := clone.SubscribeNotification(func(context.Context, *agentrewire.RpcNotification) error { return nil })
	unsubscribe()
	clone.dispatchNotification(context.Background(), &agentrewire.RpcNotification{})
	mu.Lock()
	require.Equal(t, []string{"before-clone"}, calls, "unsubscribing the clone's own handler removed an inherited one")
	mu.Unlock()

	// Isolation: registrations made after the clone do not leak into it.
	RegisterMethod(
		origin,
		uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING),
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) {
			return &agentrewire.Empty{}, nil
		},
	)
	_, err = clone.dispatchMethod(context.Background(), uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING), nil)
	require.Error(t, err, "a method registered on the origin after cloning leaked into the clone")
}
