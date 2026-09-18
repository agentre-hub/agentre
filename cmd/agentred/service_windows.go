package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

const (
	windowsServiceTaskName     = "AgentreAgentred"
	windowsServiceLauncherName = "agentred-service.ps1"
)

type windowsServiceManager struct {
	config       serviceManagerConfig
	launcherPath string
}

func newOSServiceManager(config serviceManagerConfig, _ *user.User) (ServiceManager, error) {
	return newWindowsServiceManager(config), nil
}

func newWindowsServiceManager(config serviceManagerConfig) ServiceManager {
	return &windowsServiceManager{
		config:       config,
		launcherPath: filepath.Join(config.DataDir, windowsServiceLauncherName),
	}
}

func (m *windowsServiceManager) Install(ctx context.Context) (ServiceStatus, error) {
	if err := m.writeLauncher(); err != nil {
		return ServiceStatus{}, err
	}
	args := windowsInstallArgs(m.config, m.launcherPath)
	if err := m.run(ctx, "powershell.exe", args...); err != nil {
		_ = os.Remove(m.launcherPath)
		return ServiceStatus{}, err
	}
	return ServiceStatus{Installed: true}, nil
}

func (m *windowsServiceManager) Start(ctx context.Context) (ServiceStatus, error) {
	if err := m.run(ctx, "schtasks.exe", "/Run", "/TN", windowsServiceTaskName); err != nil {
		return ServiceStatus{}, err
	}
	return m.Status(ctx)
}

func (m *windowsServiceManager) Stop(ctx context.Context) (ServiceStatus, error) {
	status, err := m.Status(ctx)
	if err != nil {
		return ServiceStatus{}, err
	}
	if !status.Installed {
		return ServiceStatus{}, fmt.Errorf("service is not installed; run agentred service install")
	}
	if !status.Running {
		return status, nil
	}
	if err := m.run(ctx, "schtasks.exe", "/End", "/TN", windowsServiceTaskName); err != nil {
		return ServiceStatus{}, err
	}
	return m.Status(ctx)
}

func (m *windowsServiceManager) Restart(ctx context.Context) (ServiceStatus, error) {
	status, err := m.Status(ctx)
	if err != nil {
		return ServiceStatus{}, err
	}
	if !status.Installed {
		return ServiceStatus{}, fmt.Errorf("service is not installed; run agentred service install")
	}
	if status.Running {
		if err := m.run(ctx, "schtasks.exe", "/End", "/TN", windowsServiceTaskName); err != nil {
			return ServiceStatus{}, err
		}
	}
	if err := m.run(ctx, "schtasks.exe", "/Run", "/TN", windowsServiceTaskName); err != nil {
		return ServiceStatus{}, err
	}
	return m.Status(ctx)
}

func (m *windowsServiceManager) Uninstall(ctx context.Context) (ServiceStatus, error) {
	status, err := m.Status(ctx)
	if err != nil {
		return ServiceStatus{}, err
	}
	if !status.Installed {
		return ServiceStatus{}, nil
	}
	if status.Running {
		if err := m.run(ctx, "schtasks.exe", "/End", "/TN", windowsServiceTaskName); err != nil {
			return ServiceStatus{}, err
		}
	}
	if err := m.run(ctx, "schtasks.exe", "/Delete", "/TN", windowsServiceTaskName, "/F"); err != nil {
		return ServiceStatus{}, err
	}
	if err := os.Remove(m.launcherPath); err != nil && !os.IsNotExist(err) {
		return ServiceStatus{}, fmt.Errorf("remove scheduled-task launcher: %w", err)
	}
	return ServiceStatus{}, nil
}

func (m *windowsServiceManager) Status(ctx context.Context) (ServiceStatus, error) {
	args := windowsStatusArgs()
	output, err := m.config.Runner.Run(ctx, "powershell.exe", args...)
	if err != nil {
		if isWindowsTaskMissing(err) {
			return ServiceStatus{}, nil
		}
		return ServiceStatus{}, serviceCommandError("powershell.exe", args, output, err)
	}
	state := strings.TrimSpace(string(output))
	return ServiceStatus{
		Installed: true,
		Running:   strings.EqualFold(state, "Running"),
		Manager:   "Windows Task Scheduler",
		State:     state,
		Details: []string{
			"Manager: Windows Task Scheduler",
			"Task: " + windowsServiceTaskName,
			"State: " + state,
		},
	}, nil
}

func (m *windowsServiceManager) run(ctx context.Context, name string, args ...string) error {
	output, err := m.config.Runner.Run(ctx, name, args...)
	if err != nil {
		return serviceCommandError(name, args, output, err)
	}
	return nil
}

func (m *windowsServiceManager) writeLauncher() error {
	if err := os.MkdirAll(m.config.DataDir, 0o700); err != nil {
		return fmt.Errorf("create scheduled-task launcher directory: %w", err)
	}
	body := "$ErrorActionPreference = 'Stop'\n" +
		"$env:AGENTRED_DATA_DIR = '" + windowsPowerShellLiteral(m.config.DataDir) + "'\n" +
		"& '" + windowsPowerShellLiteral(m.config.BinaryPath) + "' run\n" +
		"exit $LASTEXITCODE\n"
	if err := os.WriteFile(m.launcherPath, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write scheduled-task launcher: %w", err)
	}
	return nil
}

func windowsInstallArgs(config serviceManagerConfig, launcherPath string) []string {
	actionArgs := `-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "` + launcherPath + `"`
	script := "$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument '" +
		windowsPowerShellLiteral(actionArgs) + "'; " +
		"$trigger = New-ScheduledTaskTrigger -AtLogOn -User '" + windowsPowerShellLiteral(config.UserName) + "'; " +
		"$principal = New-ScheduledTaskPrincipal -UserId '" + windowsPowerShellLiteral(config.UserName) +
		"' -LogonType Interactive -RunLevel Limited; " +
		"$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries " +
		"-ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1); " +
		"Register-ScheduledTask -TaskName '" + windowsServiceTaskName +
		"' -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null"
	return []string{"-NoProfile", "-NonInteractive", "-Command", script}
}

func windowsStatusArgs() []string {
	return []string{
		"-NoProfile", "-NonInteractive", "-Command",
		"$task = Get-ScheduledTask -ErrorAction Stop | Where-Object { $_.TaskName -eq '" + windowsServiceTaskName +
			"' -and $_.TaskPath -eq '\\' } | Select-Object -First 1; " +
			"if ($null -eq $task) { exit 3 }; [Console]::Out.Write($task.State.ToString())",
	}
}

func windowsPowerShellLiteral(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func isWindowsTaskMissing(err error) bool {
	type exitCoder interface {
		ExitCode() int
	}
	var exitErr exitCoder
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 3
}
