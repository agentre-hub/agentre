package main

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeServiceManager struct {
	calls        []string
	status       ServiceStatus
	statusByCall map[string]ServiceStatus
	errAt        string
}

func (f *fakeServiceManager) result(call string) (ServiceStatus, error) {
	f.calls = append(f.calls, call)
	if f.errAt == call {
		return ServiceStatus{}, errors.New("manager failed")
	}
	if status, ok := f.statusByCall[call]; ok {
		return status, nil
	}
	return f.status, nil
}

func (f *fakeServiceManager) Install(context.Context) (ServiceStatus, error) {
	return f.result("install")
}
func (f *fakeServiceManager) Start(context.Context) (ServiceStatus, error) {
	return f.result("start")
}
func (f *fakeServiceManager) Stop(context.Context) (ServiceStatus, error) {
	return f.result("stop")
}
func (f *fakeServiceManager) Restart(context.Context) (ServiceStatus, error) {
	return f.result("restart")
}
func (f *fakeServiceManager) Uninstall(context.Context) (ServiceStatus, error) {
	return f.result("uninstall")
}
func (f *fakeServiceManager) Status(context.Context) (ServiceStatus, error) {
	return f.result("status")
}

func executeServiceCommand(t *testing.T, manager ServiceManager, args ...string) (string, error) {
	t.Helper()
	cmd := newServiceCmdWithManager(manager)
	cmd.SilenceUsage = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestGivenWindowsLifecycleIsOutOfScopeWhenReviewingServiceManagersThenNoReadinessWaitIsAdded(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "service_windows.go", nil, 0)
	require.NoError(t, err)

	checked := 0
	for _, declaration := range file.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if !ok || method.Recv == nil || (method.Name.Name != "Start" && method.Name.Name != "Stop" && method.Name.Name != "Restart") {
			continue
		}
		checked++
		ast.Inspect(method.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok {
				assert.NotEqual(t, "waitForState", selector.Sel.Name,
					"Windows lifecycle behavior is an explicit non-goal of this change")
			}
			return true
		})
	}
	assert.Equal(t, 3, checked)
}

func TestGivenServiceCommandWhenInspectingSubcommandsThenLifecycleSurfaceIsStable(t *testing.T) {
	cmd := newServiceCmdWithManager(&fakeServiceManager{})
	got := map[string]bool{}
	for _, child := range cmd.Commands() {
		got[child.Name()] = true
	}
	assert.Equal(t, map[string]bool{
		"install": true, "start": true, "status": true, "restart": true, "stop": true, "uninstall": true,
	}, got)
}

func TestGivenInstallWithStartWhenExecutedThenManagerInstallsBeforeStarting(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{Installed: true, Running: true}}
	out, err := executeServiceCommand(t, manager, "install", "--start")
	require.NoError(t, err)
	assert.Equal(t, []string{"install", "start"}, manager.calls)
	assert.Equal(t, "Daemon running", strings.Split(strings.TrimSpace(out), "\n")[0])
}

func TestGivenRunningManagerButUnreadableLocalDaemonWhenStartingThenCommandFailsWithoutPrintingSuccess(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{
		Installed: true,
		Running:   true,
		Manager:   "launchd LaunchAgent",
		Target:    "gui/501/ai.agentre.agentred",
		Details: []string{
			"Manager: launchd LaunchAgent",
			"Target: gui/501/ai.agentre.agentred",
		},
	}}
	cmd := newServiceCmdWithDeps(serviceCommandDeps{
		managerFactory: func() (ServiceManager, error) { return manager, nil },
		localStatus: func(context.Context) (map[string]any, error) {
			return nil, errors.New("dial local socket: connection refused")
		},
	})
	cmd.SilenceUsage = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"start"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Empty(t, out.String())
	assert.Contains(t, err.Error(), "wait for local daemon status")
	assert.Contains(t, err.Error(), "dial local socket: connection refused")
	assert.Contains(t, err.Error(), "launchctl target gui/501/ai.agentre.agentred")
	assert.Contains(t, err.Error(), "Run manually: launchctl print gui/501/ai.agentre.agentred")
}

func TestGivenCanceledStatusCommandWhenLoadingLocalDaemonThenCancellationIsPropagated(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{Installed: true, Running: true}}
	seenContext := make(chan context.Context, 1)
	cmd := newServiceCmdWithDeps(serviceCommandDeps{
		managerFactory: func() (ServiceManager, error) { return manager, nil },
		localStatus: func(ctx context.Context) (map[string]any, error) {
			seenContext <- ctx
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, errors.New("status loader lost command cancellation")
		},
	})
	cmd.SilenceUsage = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"status"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	require.NoError(t, cmd.Execute())
	seen := <-seenContext
	assert.ErrorIs(t, seen.Err(), context.Canceled)
	assert.Contains(t, out.String(), context.Canceled.Error())
	assert.NotContains(t, out.String(), "lost command cancellation")
}

func TestGivenLocalDaemonNeverBecomesReadyWhenStartingThenCancellationStopsObservation(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{
		Installed: true,
		Running:   true,
		Details:   []string{"State: active"},
	}}
	cmd := newServiceCmdWithDeps(serviceCommandDeps{
		managerFactory: func() (ServiceManager, error) { return manager, nil },
		localStatus: func(context.Context) (map[string]any, error) {
			return nil, errors.New("socket missing")
		},
	})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"start"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	err := cmd.Execute()
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "socket missing")
	assert.Contains(t, err.Error(), "State: active")
}

func TestGivenLocalDaemonFailureStopsServiceWhenStartingThenItReturnsEarlyWithServiceEvidence(t *testing.T) {
	manager := &fakeServiceManager{statusByCall: map[string]ServiceStatus{
		"start":  {Installed: true, Running: true},
		"status": {Installed: true, Details: []string{"State: failed"}},
	}}
	cmd := newServiceCmdWithDeps(serviceCommandDeps{
		managerFactory: func() (ServiceManager, error) { return manager, nil },
		localStatus: func(context.Context) (map[string]any, error) {
			return nil, errors.New("connection reset")
		},
	})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"start"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service stopped before becoming ready")
	assert.Contains(t, err.Error(), "State: failed")
	assert.Contains(t, err.Error(), "connection reset")
}

func TestGivenLifecycleStartActionsWhenLocalDaemonBecomesReadableThenTheyPrintRunning(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantCalls []string
	}{
		{name: "install --start", args: []string{"install", "--start"}, wantCalls: []string{"install", "start", "status"}},
		{name: "start", args: []string{"start"}, wantCalls: []string{"start", "status"}},
		{name: "restart", args: []string{"restart"}, wantCalls: []string{"restart"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &fakeServiceManager{status: ServiceStatus{Installed: true, Running: true}}
			attempts := 0
			cmd := newServiceCmdWithDeps(serviceCommandDeps{
				managerFactory: func() (ServiceManager, error) { return manager, nil },
				localStatus: func(context.Context) (map[string]any, error) {
					attempts++
					if attempts == 1 {
						return nil, errors.New("socket not ready")
					}
					return map[string]any{"pid": float64(42)}, nil
				},
			})
			cmd.SilenceUsage = true
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tt.args)

			require.NoError(t, cmd.Execute())
			assert.Equal(t, tt.wantCalls, manager.calls)
			assert.Equal(t, 2, attempts)
			assert.Equal(t, "Daemon running", strings.Split(strings.TrimSpace(out.String()), "\n")[0])
		})
	}
}

func TestGivenReadableLocalStatusWithoutPIDWhenStartingThenItKeepsWaitingForDaemonIdentity(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{Installed: true, Running: true}}
	attempts := 0
	cmd := newServiceCmdWithDeps(serviceCommandDeps{
		managerFactory: func() (ServiceManager, error) { return manager, nil },
		localStatus: func(context.Context) (map[string]any, error) {
			attempts++
			if attempts == 1 {
				return map[string]any{"version": "starting"}, nil
			}
			return map[string]any{"pid": float64(42)}, nil
		},
	})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"start"})

	require.NoError(t, cmd.Execute())
	assert.Equal(t, 2, attempts, "a decodable status without daemon PID must not satisfy readiness")
	assert.Equal(t, []string{"start", "status"}, manager.calls)
}

func TestGivenRestartPreflightWhenLocalStatusStallsThenItsProbeIsBounded(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{
		Installed: true,
		Running:   true,
		Manager:   "systemd --user",
		Details:   []string{"Manager: systemd --user"},
	}}
	attempts := 0
	cmd := newServiceCmdWithDeps(serviceCommandDeps{
		managerFactory: func() (ServiceManager, error) { return manager, nil },
		localStatus: func(ctx context.Context) (map[string]any, error) {
			attempts++
			if _, bounded := ctx.Deadline(); !bounded {
				return nil, errors.New("pre-restart local status probe has no deadline")
			}
			if attempts < 3 {
				return map[string]any{"pid": float64(41)}, nil
			}
			return map[string]any{"pid": float64(42)}, nil
		},
	})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"restart"})

	require.NoError(t, cmd.Execute())
	assert.Equal(t, 3, attempts, "the bounded preflight must capture the old PID before restart")
	assert.Equal(t, []string{"restart", "status"}, manager.calls)
}

func TestGivenRestartWhenOldDaemonStillAnswersThenItWaitsForANewDaemonPID(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{
		Installed: true,
		Running:   true,
		Manager:   "launchd LaunchAgent",
		Details:   []string{"Manager: launchd LaunchAgent"},
	}}
	attempts := 0
	cmd := newServiceCmdWithDeps(serviceCommandDeps{
		managerFactory: func() (ServiceManager, error) { return manager, nil },
		localStatus: func(context.Context) (map[string]any, error) {
			attempts++
			switch attempts {
			case 1, 2:
				return map[string]any{"pid": float64(41)}, nil
			default:
				return map[string]any{"pid": float64(42)}, nil
			}
		},
	})
	cmd.SilenceUsage = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"restart"})

	require.NoError(t, cmd.Execute())
	assert.Equal(t, 3, attempts, "the pre-restart daemon must not satisfy post-restart readiness")
	assert.Equal(t, []string{"restart", "status"}, manager.calls)
	assert.Equal(t, "Daemon running", strings.Split(strings.TrimSpace(out.String()), "\n")[0])
}

func TestGivenWindowsRestartWhenLocalStatusIsReadableThenNoNewPIDContractIsAdded(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{
		Installed: true,
		Running:   true,
		Manager:   "Windows Task Scheduler",
		Details:   []string{"Manager: Windows Task Scheduler"},
	}}
	attempts := 0
	cmd := newServiceCmdWithDeps(serviceCommandDeps{
		managerFactory: func() (ServiceManager, error) { return manager, nil },
		localStatus: func(context.Context) (map[string]any, error) {
			attempts++
			return map[string]any{"pid": float64(41)}, nil
		},
	})
	cmd.SilenceUsage = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"restart"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)

	require.NoError(t, cmd.Execute())
	assert.Equal(t, 2, attempts, "Windows restart keeps the pre-existing readable-status contract")
}

func TestGivenInstallWithStartAndLingerWarningWhenExecutedThenRepairDetailIsPreserved(t *testing.T) {
	manager := &fakeServiceManager{statusByCall: map[string]ServiceStatus{
		"install": {Installed: true, Details: []string{"Run: loginctl enable-linger alice"}},
		"start":   {Installed: true, Running: true},
	}}
	out, err := executeServiceCommand(t, manager, "install", "--start")
	require.NoError(t, err)
	assert.Contains(t, out, "Run: loginctl enable-linger alice")
}

func TestGivenServiceLifecycleActionsWhenExecutedThenTheyDelegateAndPrintStableState(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		status    ServiceStatus
		wantCall  string
		wantFirst string
	}{
		{name: "installed but stopped", args: []string{"install"}, status: ServiceStatus{Installed: true}, wantCall: "install", wantFirst: "Daemon stopped"},
		{name: "start", args: []string{"start"}, status: ServiceStatus{Installed: true, Running: true}, wantCall: "start", wantFirst: "Daemon running"},
		{name: "status not installed", args: []string{"status"}, status: ServiceStatus{}, wantCall: "status", wantFirst: "Service not installed"},
		{name: "restart", args: []string{"restart"}, status: ServiceStatus{Installed: true, Running: true}, wantCall: "restart", wantFirst: "Daemon running"},
		{name: "stop", args: []string{"stop"}, status: ServiceStatus{Installed: true}, wantCall: "stop", wantFirst: "Daemon stopped"},
		{name: "idempotent uninstall", args: []string{"uninstall"}, status: ServiceStatus{}, wantCall: "uninstall", wantFirst: "Service not installed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &fakeServiceManager{status: tt.status}
			out, err := executeServiceCommand(t, manager, tt.args...)
			require.NoError(t, err)
			assert.Equal(t, []string{tt.wantCall}, manager.calls)
			assert.Equal(t, tt.wantFirst, strings.Split(strings.TrimSpace(out), "\n")[0])
		})
	}
}

func TestGivenRunningServiceWhenStatusIsRequestedThenStableHeaderPrecedesLocalDaemonDetails(t *testing.T) {
	manager := &fakeServiceManager{status: ServiceStatus{Installed: true, Running: true}}
	cmd := newServiceCmdWithDeps(serviceCommandDeps{
		managerFactory: func() (ServiceManager, error) { return manager, nil },
		localStatus: func(context.Context) (map[string]any, error) {
			return map[string]any{
				"pid":              float64(42),
				"version":          "v1.2.3 (abcdef1)",
				"listenURLs":       []any{"ws://127.0.0.1:7456"},
				"pairedPeers":      []any{},
				"activeSessions":   float64(0),
				"llmProviderCount": float64(0),
			}, nil
		},
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"status"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "Daemon running", strings.Split(strings.TrimSpace(out.String()), "\n")[0])
	assert.Contains(t, out.String(), "PID: 42")
	assert.Contains(t, out.String(), "Version: v1.2.3 (abcdef1)")
}

func TestGivenLocalStatusEndpointRejectsTheRequestWhenLoadingThenReadinessReturnsTheHTTPFailure(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status:     "503 Service Unavailable",
			Body:       io.NopCloser(strings.NewReader(`{"pid":42}`)),
		}, nil
	})}

	_, err := loadLocalDaemonStatus(context.Background(), client)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503 Service Unavailable")
}

func TestGivenManagerFailureWhenServiceActionRunsThenCommandReturnsActionableError(t *testing.T) {
	manager := &fakeServiceManager{errAt: "restart"}
	out, err := executeServiceCommand(t, manager, "restart")
	require.Error(t, err)
	assert.Empty(t, out)
	assert.Contains(t, err.Error(), "restart service")
	assert.Contains(t, err.Error(), "manager failed")
}
