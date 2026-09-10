package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/service/update_svc"
)

type updateCommandProbe struct {
	deps            updateCommandDeps
	resolvedChannel string
	applied         int
	manager         *fakeServiceManager
}

func newUpdateProbe(release *update_svc.AgentredRelease, manager *fakeServiceManager) *updateCommandProbe {
	probe := &updateCommandProbe{manager: manager}
	probe.deps = updateCommandDeps{
		resolve: func(_ context.Context, channel string) (*update_svc.AgentredRelease, error) {
			probe.resolvedChannel = channel
			return release, nil
		},
		apply: func(context.Context, *update_svc.AgentredRelease) error {
			probe.applied++
			return nil
		},
		activeTurns:    func(context.Context) (int64, error) { return 0, nil },
		managerFactory: func() (ServiceManager, error) { return manager, nil },
	}
	return probe
}

func executeUpdateCommand(t *testing.T, deps updateCommandDeps, args ...string) (string, error) {
	t.Helper()
	cmd := newUpdateCmdWithDeps(deps)
	cmd.SilenceUsage = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func upgradableRelease() *update_svc.AgentredRelease {
	return &update_svc.AgentredRelease{
		Channel:        update_svc.ChannelStable,
		CurrentVersion: "0.1.0",
		LatestVersion:  "0.2.0",
		HasUpdate:      true,
		AssetName:      "agentred-0.2.0-linux-amd64.tar.gz",
	}
}

func TestUpdateCommandIsRegistered(t *testing.T) {
	root := newRootCmd()
	update, _, err := root.Find([]string{"update"})
	require.NoError(t, err)
	require.Equal(t, "update", update.Name())
	for _, flag := range []string{"check", "channel", "force"} {
		assert.NotNil(t, update.Flags().Lookup(flag), "missing --%s", flag)
	}
}

func TestUpdateCheckReportsVersionsWithoutTouchingTheBinary(t *testing.T) {
	probe := newUpdateProbe(upgradableRelease(), &fakeServiceManager{})
	out, err := executeUpdateCommand(t, probe.deps, "--check")
	require.NoError(t, err)
	assert.Contains(t, out, "0.1.0")
	assert.Contains(t, out, "0.2.0")
	assert.Contains(t, strings.ToLower(out), "update available: yes")
	assert.Zero(t, probe.applied, "--check 不替换")
	assert.Empty(t, probe.manager.calls, "--check 不碰服务")
}

func TestUpdateCheckReportsNoUpdate(t *testing.T) {
	release := upgradableRelease()
	release.LatestVersion, release.HasUpdate = "0.1.0", false
	probe := newUpdateProbe(release, &fakeServiceManager{})
	out, err := executeUpdateCommand(t, probe.deps, "--check")
	require.NoError(t, err)
	assert.Contains(t, strings.ToLower(out), "update available: no")
	assert.Zero(t, probe.applied)
}

func TestUpdateRefusesWhileTurnsAreRunning(t *testing.T) {
	probe := newUpdateProbe(upgradableRelease(), &fakeServiceManager{})
	probe.deps.activeTurns = func(context.Context) (int64, error) { return 3, nil }

	_, err := executeUpdateCommand(t, probe.deps)
	require.Error(t, err)
	var active *update_svc.ActiveTurnsError
	require.ErrorAs(t, err, &active)
	assert.Equal(t, int64(3), active.Count)
	assert.Contains(t, err.Error(), "3")
	assert.Contains(t, err.Error(), "--force")
	assert.Zero(t, probe.applied, "有轮次在跑时不下载、不替换")
	assert.Empty(t, probe.manager.calls)
}

func TestUpdateForceCrossesTheActiveTurnGate(t *testing.T) {
	probe := newUpdateProbe(upgradableRelease(), &fakeServiceManager{})
	probe.deps.activeTurns = func(context.Context) (int64, error) { return 3, nil }

	_, err := executeUpdateCommand(t, probe.deps, "--force")
	require.NoError(t, err)
	assert.Equal(t, 1, probe.applied)
}

func TestUpdateCheckIgnoresActiveTurns(t *testing.T) {
	probe := newUpdateProbe(upgradableRelease(), &fakeServiceManager{})
	probe.deps.activeTurns = func(context.Context) (int64, error) { return 3, nil }

	out, err := executeUpdateCommand(t, probe.deps, "--check")
	require.NoError(t, err)
	assert.Contains(t, strings.ToLower(out), "update available: yes")
}

func TestUpdateRestartsTheRegisteredServiceAfterASuccessfulSwap(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{Installed: true, Running: true}}
	probe := newUpdateProbe(upgradableRelease(), manager)

	out, err := executeUpdateCommand(t, probe.deps)
	require.NoError(t, err)
	assert.Equal(t, 1, probe.applied)
	assert.Contains(t, manager.calls, "restart")
	assert.Contains(t, out, "0.2.0")
	assert.Contains(t, strings.ToLower(out), "restarted")
}

func TestUpdateTellsTheUserToRestartWhenNoServiceIsRegistered(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{}}
	probe := newUpdateProbe(upgradableRelease(), manager)

	out, err := executeUpdateCommand(t, probe.deps)
	require.NoError(t, err)
	assert.Equal(t, 1, probe.applied)
	assert.NotContains(t, manager.calls, "restart")
	assert.Contains(t, strings.ToLower(out), "restart")
	assert.Contains(t, out, "agentred service restart")
}

// 决策 6a：容器里的替换写在可写层，重建容器就回退。这句代价必须出现在命令输出里。
func TestUpdateStatesTheContainerCaveat(t *testing.T) {
	probe := newUpdateProbe(upgradableRelease(), &fakeServiceManager{status: ServiceStatus{Installed: true}})
	out, err := executeUpdateCommand(t, probe.deps)
	require.NoError(t, err)
	assert.Contains(t, strings.ToLower(out), "container")
}

func TestUpdateSkipsTheSwapWhenAlreadyLatest(t *testing.T) {
	release := upgradableRelease()
	release.LatestVersion, release.HasUpdate = "0.1.0", false
	manager := &fakeServiceManager{status: ServiceStatus{Installed: true}}
	probe := newUpdateProbe(release, manager)

	out, err := executeUpdateCommand(t, probe.deps)
	require.NoError(t, err)
	assert.Zero(t, probe.applied)
	assert.Empty(t, manager.calls)
	assert.Contains(t, strings.ToLower(out), "up to date")
}

func TestUpdateKeepsTheServiceUntouchedWhenTheSwapFails(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{Installed: true}}
	probe := newUpdateProbe(upgradableRelease(), manager)
	probe.deps.apply = func(context.Context, *update_svc.AgentredRelease) error {
		return &update_svc.ChecksumMismatchError{AssetName: "agentred-0.2.0-linux-amd64.tar.gz"}
	}

	_, err := executeUpdateCommand(t, probe.deps)
	var mismatch *update_svc.ChecksumMismatchError
	require.ErrorAs(t, err, &mismatch)
	assert.NotContains(t, manager.calls, "restart")
}

func TestUpdateRejectsAnUnknownChannelAsUsage(t *testing.T) {
	probe := newUpdateProbe(upgradableRelease(), &fakeServiceManager{})
	_, err := executeUpdateCommand(t, probe.deps, "--channel", "edge", "--check")
	require.Error(t, err)
	var usage *usageError
	assert.ErrorAs(t, err, &usage)
	assert.Zero(t, probe.applied)
}

func TestUpdatePassesTheRequestedChannelThrough(t *testing.T) {
	probe := newUpdateProbe(upgradableRelease(), &fakeServiceManager{})
	_, err := executeUpdateCommand(t, probe.deps, "--channel", "nightly", "--check")
	require.NoError(t, err)
	assert.Equal(t, update_svc.ChannelNightly, probe.resolvedChannel)
}

func TestUpdateSurfacesAnUnreadableActiveTurnCount(t *testing.T) {
	probe := newUpdateProbe(upgradableRelease(), &fakeServiceManager{})
	probe.deps.activeTurns = func(context.Context) (int64, error) { return 0, errors.New("malformed status") }

	_, err := executeUpdateCommand(t, probe.deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "malformed status")
	assert.Zero(t, probe.applied, "数不清有几条轮次时不该替换")
}

// scripts/install.sh 收尾时靠 `agentred service status` 的这句话判断有没有注册服务。
// 那句话是两边共有的契约：改了打印文案而不改脚本，收尾会静静地退化成永远不重启。
func TestInstallScriptDetectsTheServiceWithTheStringTheCLIPrints(t *testing.T) {
	script, err := os.ReadFile("../../scripts/install.sh")
	require.NoError(t, err)

	var printed bytes.Buffer
	printServiceStatusContext(context.Background(), &printed, ServiceStatus{})
	marker := strings.TrimSpace(printed.String())
	require.NotEmpty(t, marker)

	assert.True(t, strings.Contains(string(script), marker),
		"install.sh 必须按 CLI 实际打印的这句话（%q）判断服务是否已注册", marker)
	assert.True(t, strings.Contains(string(script), "service restart"),
		"install.sh 收尾要么重启已注册的服务，要么打印重启命令")
}
