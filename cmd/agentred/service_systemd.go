package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const systemdServiceName = "agentred.service"

type systemdServiceManager struct {
	config   serviceManagerConfig
	unitPath string
}

func newSystemdServiceManager(config serviceManagerConfig) ServiceManager {
	return &systemdServiceManager{
		config:   config,
		unitPath: filepath.Join(config.HomeDir, ".config", "systemd", "user", systemdServiceName),
	}
}

func (m *systemdServiceManager) Install(ctx context.Context) (ServiceStatus, error) {
	if err := writeServiceFile(m.unitPath, []byte(m.unit())); err != nil {
		return ServiceStatus{}, err
	}
	if err := m.run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return ServiceStatus{}, err
	}
	if err := m.run(ctx, "systemctl", "--user", "enable", systemdServiceName); err != nil {
		return ServiceStatus{}, err
	}
	status := ServiceStatus{Installed: true}
	output, err := m.config.Runner.Run(ctx, "loginctl", "enable-linger", m.config.UserName)
	if err != nil {
		status.Details = append(status.Details,
			fmt.Sprintf("Linger setup failed: %v", serviceCommandError("loginctl", []string{"enable-linger", m.config.UserName}, output, err)),
			fmt.Sprintf("Run: loginctl enable-linger %s", m.config.UserName),
		)
	}
	return status, nil
}

func (m *systemdServiceManager) Start(ctx context.Context) (ServiceStatus, error) {
	if err := requireInstalled(m.unitPath); err != nil {
		return ServiceStatus{}, err
	}
	if err := m.run(ctx, "systemctl", "--user", "start", systemdServiceName); err != nil {
		return ServiceStatus{}, err
	}
	return m.waitForState(ctx, true)
}

func (m *systemdServiceManager) Stop(ctx context.Context) (ServiceStatus, error) {
	if err := requireInstalled(m.unitPath); err != nil {
		return ServiceStatus{}, err
	}
	if err := m.run(ctx, "systemctl", "--user", "stop", systemdServiceName); err != nil {
		return ServiceStatus{}, err
	}
	return m.waitForState(ctx, false)
}

func (m *systemdServiceManager) Restart(ctx context.Context) (ServiceStatus, error) {
	if err := requireInstalled(m.unitPath); err != nil {
		return ServiceStatus{}, err
	}
	if err := m.run(ctx, "systemctl", "--user", "restart", systemdServiceName); err != nil {
		return ServiceStatus{}, err
	}
	return m.waitForState(ctx, true)
}

func (m *systemdServiceManager) Uninstall(ctx context.Context) (ServiceStatus, error) {
	installed, err := serviceFileExists(m.unitPath)
	if err != nil {
		return ServiceStatus{}, err
	}
	if !installed {
		return ServiceStatus{}, nil
	}
	if err := m.run(ctx, "systemctl", "--user", "disable", "--now", systemdServiceName); err != nil {
		return ServiceStatus{}, err
	}
	if err := os.Remove(m.unitPath); err != nil && !os.IsNotExist(err) {
		return ServiceStatus{}, fmt.Errorf("remove systemd user unit: %w", err)
	}
	if err := m.run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return ServiceStatus{}, err
	}
	return ServiceStatus{}, nil
}

func (m *systemdServiceManager) Status(ctx context.Context) (ServiceStatus, error) {
	installed, err := serviceFileExists(m.unitPath)
	if err != nil {
		return ServiceStatus{}, err
	}
	if !installed {
		return ServiceStatus{}, nil
	}
	args := []string{"--user", "is-active", systemdServiceName}
	output, err := m.config.Runner.Run(ctx, "systemctl", args...)
	state := strings.TrimSpace(string(output))
	if err != nil && !isSystemdInactiveState(state) {
		return ServiceStatus{}, serviceCommandError("systemctl", args, output, err)
	}
	return ServiceStatus{
		Installed: true,
		Running:   state == "active",
		Manager:   "systemd --user",
		State:     state,
		Details:   []string{"Manager: systemd --user", "Unit: " + m.unitPath, "State: " + state},
	}, nil
}

func (m *systemdServiceManager) waitForState(ctx context.Context, running bool) (ServiceStatus, error) {
	waitCtx, cancel := context.WithTimeout(ctx, serviceReadyTimeout)
	defer cancel()
	for {
		status, err := m.Status(waitCtx)
		if err != nil {
			return ServiceStatus{}, err
		}
		state := status.State
		if running && state == "failed" {
			return ServiceStatus{}, fmt.Errorf("wait for systemd unit %s to become active: state failed; Run manually: systemctl --user status %s", systemdServiceName, systemdServiceName)
		}
		if status.Running == running && state != "activating" && state != "deactivating" {
			return status, nil
		}
		select {
		case <-waitCtx.Done():
			want := "inactive"
			if running {
				want = "active"
			}
			return ServiceStatus{}, fmt.Errorf("wait for systemd unit %s to become %s: %w (last state: %s); Run manually: systemctl --user status %s", systemdServiceName, want, waitCtx.Err(), state, systemdServiceName)
		case <-time.After(serviceReadyPollInterval):
		}
	}
}

func isSystemdInactiveState(state string) bool {
	switch state {
	case "inactive", "failed", "activating", "deactivating":
		return true
	default:
		return false
	}
}

func (m *systemdServiceManager) run(ctx context.Context, name string, args ...string) error {
	output, err := m.config.Runner.Run(ctx, name, args...)
	if err != nil {
		return serviceCommandError(name, args, output, err)
	}
	return nil
}

func (m *systemdServiceManager) unit() string {
	return `[Unit]
Description=Agentre agentred daemon
After=network-online.target

[Service]
Type=simple
ExecStart=` + systemdQuote(m.config.BinaryPath) + ` run
Environment=` + systemdQuote("AGENTRED_DATA_DIR="+m.config.DataDir) + `
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
}

func systemdQuote(value string) string {
	return strconv.Quote(strings.ReplaceAll(value, "%", "%%"))
}
