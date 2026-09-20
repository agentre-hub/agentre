package backendcred

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// TestBackendcredRegistersNoRuntime pins the leaf property agentred relies on:
// this test binary links only backendcred's own dependencies, so a registered
// Hermes or OpenClaw runtime here means backendcred started importing a runtime
// package whose init() would make agentred advertise it.
func TestBackendcredRegistersNoRuntime(t *testing.T) {
	for _, bt := range []agent_backend_entity.BackendType{
		agent_backend_entity.TypeHermes,
		agent_backend_entity.TypeOpenClaw,
	} {
		assert.Nil(t, agentruntime.RuntimeFor(bt), "backendcred must not register the %q runtime", bt)
	}
}
