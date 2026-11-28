package agent_backend_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Given 这台桌面端真的连了一次 Hermes / OpenClaw 端点(测试连接,本机发起或被控制台问到),
// Then 进程级的连接记录里留下这个端点上次连接成没成,设备情况据此报「上次连接是否成功」。
// 每个用例用自己独有的端点地址:记录是进程级的一份。
func TestConnectionTests_RecordTheLastConnectionOutcome(t *testing.T) {
	last := func(e devicefacts.Engine) string {
		status, _ := devicefacts.DefaultConnections().Last(e)
		return status
	}
	newPort := func() *LocalBackendCredentials {
		memory := keychain.NewMemory()
		return &LocalBackendCredentials{svc: &agentBackendSvc{
			now: func() int64 { return 1 }, secrets: memory,
			hermes: backendcred.NewHermesCredentials(func() backendcred.Store { return memory }),
			probes: map[string]context.CancelFunc{},
		}}
	}
	stubHermes := func(t *testing.T, err error) {
		orig := hermesProbe
		hermesProbe = func(context.Context, hermes.ProbeRequest) (string, error) { return "", err }
		t.Cleanup(func() { hermesProbe = orig })
	}

	t.Run("the backend Test of a Hermes backend records a failure", func(t *testing.T) {
		stubHermes(t, errors.New("gateway refused"))
		_, err := hermesProber{}.Run(context.Background(), &agent_backend_entity.AgentBackend{
			Type: string(agent_backend_entity.TypeHermes), HermesURL: "http://10.9.0.1:9119",
		}, ProbeDeps{})
		require.Error(t, err)
		assert.Equal(t, syncwire.ConnectionFailed, last(devicefacts.Engine{Runtime: "hermes", HermesURL: "http://10.9.0.1:9119/"}))
	})

	t.Run("a console-asked Hermes test that connects records a success", func(t *testing.T) {
		stubHermes(t, nil)
		resp, err := newPort().TestConnection(context.Background(), &agentrewire.BackendConnectionTestRequest{
			BackendType: string(agent_backend_entity.TypeHermes), HermesUrl: "http://10.9.0.2:9119",
		})
		require.NoError(t, err)
		require.True(t, resp.GetOk())
		assert.Equal(t, syncwire.ConnectionSucceeded, last(devicefacts.Engine{Runtime: "hermes", HermesURL: "http://10.9.0.2:9119"}))
	})

	t.Run("an OpenClaw test records the gateway's outcome", func(t *testing.T) {
		port := newPort()
		port.svc.openClawProbe = func(context.Context, openclawgateway.Config, openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error) {
			return nil, errors.New("connection refused")
		}
		_, err := port.TestConnection(context.Background(), &agentrewire.BackendConnectionTestRequest{
			BackendType: string(agent_backend_entity.TypeOpenClaw), OpenclawGatewayUrl: "wss://gw-record.local:18789",
		})
		require.NoError(t, err)
		assert.Equal(t, syncwire.ConnectionFailed, last(devicefacts.Engine{Runtime: "openclaw", OpenClawGatewayURL: "wss://gw-record.local:18789"}))

		port.svc.openClawProbe = func(context.Context, openclawgateway.Config, openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error) {
			return &openclawgateway.ProbeResult{}, nil
		}
		_, err = port.TestConnection(context.Background(), &agentrewire.BackendConnectionTestRequest{
			BackendType: string(agent_backend_entity.TypeOpenClaw), OpenclawGatewayUrl: "wss://gw-record.local:18789",
		})
		require.NoError(t, err)
		assert.Equal(t, syncwire.ConnectionSucceeded, last(devicefacts.Engine{Runtime: "openclaw", OpenClawGatewayURL: "wss://gw-record.local:18789"}))
	})
}
