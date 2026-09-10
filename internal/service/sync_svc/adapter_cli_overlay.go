package sync_svc

import (
	"context"
	"encoding/json"

	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
)

type cliOverlayPayload struct {
	CLIPath string `json:"cli_path"`
}

// agentBackendCLIAdapter mirrors project_location's split identity: the
// backend sync ID and device fingerprint live in the envelope, never in the
// backend identity payload.
type agentBackendCLIAdapter struct{ baseAdapter }

func (agentBackendCLIAdapter) kind() string { return syncwire.KindAgentBackendCLI }

func (agentBackendCLIAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &agent_backend_entity.CLIOverlay{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindAgentBackendCLI, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	payload, err := json.Marshal(cliOverlayPayload{CLIPath: row.CLIPath})
	if err != nil {
		return nil, err
	}
	return &outbound{
		SyncID: row.SyncID, UpdatedAt: row.Updatetime, ProjectSyncID: row.BackendSyncID,
		AgentredFingerprint: row.AgentredFingerprint, Payload: payload,
	}, nil
}

func (agentBackendCLIAdapter) refs(in *inbound) []ref {
	return []ref{{Kind: syncwire.KindAgentBackend, SyncID: in.ProjectSyncID}}
}

func (agentBackendCLIAdapter) apply(ctx context.Context, in *inbound, _ map[string]int64) error {
	var payload cliOverlayPayload
	if err := json.Unmarshal(in.Payload, &payload); err != nil {
		return err
	}
	row := &agent_backend_entity.CLIOverlay{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindAgentBackendCLI, in.SyncID, row)
	if err != nil {
		return err
	}
	if !found {
		held, findErr := agent_backend_repo.AgentBackend().FindCLIOverlay(ctx, in.ProjectSyncID, in.AgentredFingerprint)
		if findErr != nil {
			return findErr
		}
		if held != nil {
			row, found = held, true
		}
	}
	row.BackendSyncID, row.AgentredFingerprint, row.CLIPath = in.ProjectSyncID, in.AgentredFingerprint, payload.CLIPath
	row.Status, row.SyncID = consts.ACTIVE, in.SyncID
	if found {
		return agent_backend_repo.AgentBackend().UpdateCLIOverlay(ctx, row)
	}
	return agent_backend_repo.AgentBackend().CreateCLIOverlay(ctx, row)
}

func (agentBackendCLIAdapter) remove(ctx context.Context, in *inbound) error {
	row := &agent_backend_entity.CLIOverlay{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindAgentBackendCLI, in.SyncID, row)
	if err != nil || !found {
		return err
	}
	return agent_backend_repo.AgentBackend().DeleteCLIOverlay(ctx, row.ID)
}
