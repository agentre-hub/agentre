// Package composition is the dedicated E2E composition root. Production
// entrypoints must never import it.
package composition

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/e2e/fakes"
	"github.com/agentre-hub/agentre/e2e/preflight"
	daemonidentity "github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/agent_backend_svc"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
)

const (
	syncServerURLEnv    = "AGENTRE_E2E_SERVER_URL"
	syncServerUserIDEnv = "AGENTRE_E2E_SERVER_USER_ID"
	syncDeviceIDEnv     = "AGENTRE_E2E_DEVICE_ID"
	syncDeviceFPEnv     = "AGENTRE_E2E_DEVICE_FINGERPRINT"
	syncRefreshTokenEnv = "AGENTRE_E2E_REFRESH_TOKEN" //nolint:gosec // G101: environment-variable name, not a credential

	remotePeerURLEnv            = "AGENTRE_E2E_REMOTE_PEER_URL"
	remoteDaemonFPEnv           = "AGENTRE_E2E_REMOTE_DAEMON_FINGERPRINT"
	remoteInstanceUUIDEnv       = "AGENTRE_E2E_REMOTE_INSTANCE_UUID"
	remoteDeviceTokenEnv        = "AGENTRE_E2E_REMOTE_DEVICE_TOKEN" //nolint:gosec // G101: environment-variable name, not a credential
	remoteDeviceName            = "E2E Remote Agentred"
	remoteBackendName           = "E2E Remote Backend"
	remoteAgentName             = "E2E Remote Agent"
	remoteKeychainAccountPrefix = "agentre-daemon-token-"
)

// LoggedInIdentity is runner-generated and accepted only by the dedicated E2E
// composition after storage preflight succeeds.
type LoggedInIdentity struct {
	ServerURL         string
	ServerUserID      int64
	DeviceID          int64
	DeviceFingerprint string
	RefreshToken      string
}

// RemotePeerIdentity is generated for this run and consumed only by E2E composition.
type RemotePeerIdentity struct {
	URL               string
	DaemonFingerprint string
	InstanceUUID      string
	DeviceToken       string
}

// Config extends validated storage with isolated fake external identities.
type Config struct {
	preflight.Config
	Identity LoggedInIdentity
	Remote   RemotePeerIdentity
}

// FromPreflight reads runner-owned fake identities after path/token preflight.
func FromPreflight(config preflight.Config) (Config, error) {
	userID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(syncServerUserIDEnv)), 10, 64)
	if err != nil || userID <= 0 {
		return Config{}, fmt.Errorf("e2e composition requires a valid fake server user id")
	}
	deviceID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(syncDeviceIDEnv)), 10, 64)
	if err != nil || deviceID <= 0 {
		return Config{}, fmt.Errorf("e2e composition requires a valid fake device id")
	}
	identity := LoggedInIdentity{
		ServerURL:         strings.TrimSpace(os.Getenv(syncServerURLEnv)),
		ServerUserID:      userID,
		DeviceID:          deviceID,
		DeviceFingerprint: strings.TrimSpace(os.Getenv(syncDeviceFPEnv)),
		RefreshToken:      strings.TrimSpace(os.Getenv(syncRefreshTokenEnv)),
	}
	if !isLoopbackHTTP(identity.ServerURL) || identity.DeviceFingerprint == "" || identity.RefreshToken == "" {
		return Config{}, fmt.Errorf("e2e composition requires a loopback fake sync identity")
	}
	remote := RemotePeerIdentity{
		URL:               strings.TrimSpace(os.Getenv(remotePeerURLEnv)),
		DaemonFingerprint: strings.TrimSpace(os.Getenv(remoteDaemonFPEnv)),
		InstanceUUID:      strings.TrimSpace(os.Getenv(remoteInstanceUUIDEnv)),
		DeviceToken:       strings.TrimSpace(os.Getenv(remoteDeviceTokenEnv)),
	}
	if !isLoopbackRPC(remote.URL) || remote.DaemonFingerprint == "" || remote.InstanceUUID == "" ||
		remote.DeviceToken == "" || remote.DaemonFingerprint != daemonidentity.DaemonFingerprint(remote.InstanceUUID) {
		return Config{}, fmt.Errorf("e2e composition requires a loopback fake remote peer identity")
	}
	return Config{Config: config, Identity: identity, Remote: remote}, nil
}

func isLoopbackHTTP(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "http" && u.Path == "" && loopbackHost(u.Hostname()) && u.Port() != ""
}

func isLoopbackRPC(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "ws" && u.Path == "/rpc" && loopbackHost(u.Hostname()) && u.Port() != ""
}

func loopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var installFakes = fakes.Install

// Install replaces only external/runtime boundaries and seeds deterministic
// E2E state after production bootstrap has completed.
func Install(ctx context.Context, validated preflight.Config) error {
	config, err := FromPreflight(validated)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Unsetenv(syncRefreshTokenEnv)
		_ = os.Unsetenv(remoteDeviceTokenEnv)
	}()
	if config.RunRoot == "" || config.DataDir == "" || config.KeychainDir == "" {
		return fmt.Errorf("e2e composition requires validated preflight config")
	}
	identityEnv := map[string]string{
		syncServerURLEnv:    config.Identity.ServerURL,
		syncServerUserIDEnv: strconv.FormatInt(config.Identity.ServerUserID, 10),
		syncDeviceIDEnv:     strconv.FormatInt(config.Identity.DeviceID, 10),
		syncDeviceFPEnv:     config.Identity.DeviceFingerprint,
		syncRefreshTokenEnv: config.Identity.RefreshToken,
	}
	for name, value := range identityEnv {
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set fake login identity: %w", err)
		}
	}
	if err := installFakes(ctx); err != nil {
		return fmt.Errorf("seed local fake runtime: %w", err)
	}
	if err := installRemotePeer(ctx, config.Remote); err != nil {
		return err
	}
	return nil
}

func installRemotePeer(ctx context.Context, identity RemotePeerIdentity) error {
	deviceID, err := ensureRemoteDevice(ctx, identity)
	if err != nil {
		return fmt.Errorf("seed remote device: %w", err)
	}
	if err := keychain.Default().Set(remoteKeychainAccountPrefix+strconv.FormatInt(deviceID, 10), identity.DeviceToken); err != nil {
		return fmt.Errorf("seed remote device token: %w", err)
	}
	backendID, err := ensureRemoteBackend(ctx, identity.DaemonFingerprint)
	if err != nil {
		return fmt.Errorf("seed remote backend: %w", err)
	}
	if err := ensureRemoteAgent(ctx, backendID); err != nil {
		return fmt.Errorf("seed remote agent: %w", err)
	}
	return nil
}

func ensureRemoteDevice(ctx context.Context, identity RemotePeerIdentity) (int64, error) {
	repo := remote_device_repo.PairedAgentred()
	row, err := repo.FindByURL(ctx, identity.URL)
	if err != nil {
		return 0, err
	}
	if row != nil {
		return row.ID, nil
	}
	now := time.Now().UnixMilli()
	row = &paired_agentred_entity.PairedAgentred{
		Name: remoteDeviceName, URL: identity.URL,
		DaemonFingerprint: identity.DaemonFingerprint, InstanceUUID: identity.InstanceUUID,
		TLSMode: "default", PairedAt: now, LastSeenAt: now, Status: consts.ACTIVE,
	}
	if err := row.Check(ctx); err != nil {
		return 0, err
	}
	if err := repo.Create(ctx, row); err != nil {
		return 0, err
	}
	return row.ID, nil
}

func ensureRemoteBackend(ctx context.Context, daemonFingerprint string) (int64, error) {
	existing, err := agent_backend_repo.AgentBackend().FindByName(ctx, remoteBackendName)
	if err != nil {
		return 0, err
	}
	if existing != nil {
		return existing.ID, nil
	}
	response, err := agent_backend_svc.AgentBackend().Create(ctx, &agent_backend_svc.CreateBackendRequest{
		Type: string(agent_backend_entity.TypeClaudeCode), Name: remoteBackendName, DeviceID: daemonFingerprint,
	})
	if err != nil {
		return 0, err
	}
	return response.Item.ID, nil
}

func ensureRemoteAgent(ctx context.Context, backendID int64) error {
	existing, err := agent_repo.Agent().FindByName(ctx, remoteAgentName)
	if err != nil {
		return err
	}
	if existing != nil {
		_, err = agent_svc.Agent().Update(ctx, &agent_svc.UpdateAgentRequest{
			ID: existing.ID, Name: existing.Name,
			ExecTargets: []agent_svc.ExecTargetInputDTO{{AgentBackendID: backendID}},
		})
		return err
	}
	parent, err := agent_repo.Agent().FindSystem(ctx)
	if err != nil {
		return err
	}
	if parent == nil {
		return errors.New("system agent not found")
	}
	_, err = agent_svc.Agent().Create(ctx, &agent_svc.CreateAgentRequest{
		Name: remoteAgentName, ParentAgentID: parent.ID, AgentBackendID: backendID,
	})
	return err
}
