package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const launchdServiceLabel = "ai.agentre.agentred"

type launchdServiceManager struct {
	config    serviceManagerConfig
	plistPath string
	domain    string
	target    string
}

func newLaunchdServiceManager(config serviceManagerConfig) ServiceManager {
	domain := "gui/" + strconv.Itoa(config.UID)
	return &launchdServiceManager{
		config:    config,
		plistPath: filepath.Join(config.HomeDir, "Library", "LaunchAgents", launchdServiceLabel+".plist"),
		domain:    domain,
		target:    domain + "/" + launchdServiceLabel,
	}
}

func (m *launchdServiceManager) Install(ctx context.Context) (ServiceStatus, error) {
	if err := writeServiceFile(m.plistPath, []byte(m.plist())); err != nil {
		return ServiceStatus{}, err
	}
	if err := m.bootoutAndWait(ctx); err != nil {
		return ServiceStatus{}, err
	}
	return ServiceStatus{Installed: true}, nil
}

func (m *launchdServiceManager) Start(ctx context.Context) (ServiceStatus, error) {
	return m.start(ctx, false)
}

func (m *launchdServiceManager) Stop(ctx context.Context) (ServiceStatus, error) {
	if err := requireInstalled(m.plistPath); err != nil {
		return ServiceStatus{}, err
	}
	if err := m.bootoutAndWait(ctx); err != nil {
		return ServiceStatus{}, err
	}
	return ServiceStatus{
		Installed: true,
		Manager:   "launchd LaunchAgent",
		Target:    m.target,
		Details: []string{
			"Manager: launchd LaunchAgent",
			"Plist: " + m.plistPath,
			"Target: " + m.target,
			"Loaded: false",
			"Running: false",
		},
	}, nil
}

func (m *launchdServiceManager) Restart(ctx context.Context) (ServiceStatus, error) {
	return m.start(ctx, true)
}

func (m *launchdServiceManager) start(ctx context.Context, restart bool) (ServiceStatus, error) {
	if err := requireInstalled(m.plistPath); err != nil {
		return ServiceStatus{}, err
	}
	_, loaded, err := m.inspect(ctx)
	if err != nil {
		return ServiceStatus{}, err
	}
	if loaded {
		args := []string{"kickstart", m.target}
		if restart {
			args = []string{"kickstart", "-k", m.target}
		}
		if err := m.run(ctx, "launchctl", args...); err != nil {
			return ServiceStatus{}, err
		}
	} else if err := m.run(ctx, "launchctl", "bootstrap", m.domain, m.plistPath); err != nil {
		return ServiceStatus{}, err
	}
	return m.waitForRunning(ctx)
}

func (m *launchdServiceManager) Uninstall(ctx context.Context) (ServiceStatus, error) {
	installed, err := serviceFileExists(m.plistPath)
	if err != nil {
		return ServiceStatus{}, err
	}
	if !installed {
		return ServiceStatus{}, nil
	}
	if err := m.bootoutAndWait(ctx); err != nil {
		return ServiceStatus{}, err
	}
	if err := os.Remove(m.plistPath); err != nil && !os.IsNotExist(err) {
		return ServiceStatus{}, fmt.Errorf("remove LaunchAgent plist: %w", err)
	}
	return ServiceStatus{}, nil
}

func (m *launchdServiceManager) Status(ctx context.Context) (ServiceStatus, error) {
	status, _, err := m.inspect(ctx)
	return status, err
}

func (m *launchdServiceManager) inspect(ctx context.Context) (ServiceStatus, bool, error) {
	status, loaded, _, err := m.inspectOutput(ctx)
	return status, loaded, err
}

func (m *launchdServiceManager) inspectOutput(ctx context.Context) (ServiceStatus, bool, []byte, error) {
	installed, err := serviceFileExists(m.plistPath)
	if err != nil {
		return ServiceStatus{}, false, nil, err
	}
	if !installed {
		return ServiceStatus{}, false, nil, nil
	}
	args := []string{"print", m.target}
	output, err := m.config.Runner.Run(ctx, "launchctl", args...)
	if err != nil && !isLaunchdMissingService(output) {
		return ServiceStatus{}, false, output, serviceCommandError("launchctl", args, output, err)
	}
	loaded := err == nil
	running := loaded && strings.Contains(string(output), "state = running")
	return ServiceStatus{
		Installed: true,
		Running:   running,
		Manager:   "launchd LaunchAgent",
		Target:    m.target,
		Details: []string{
			"Manager: launchd LaunchAgent",
			"Plist: " + m.plistPath,
			"Target: " + m.target,
			fmt.Sprintf("Loaded: %t", loaded),
			fmt.Sprintf("Running: %t", running),
		},
	}, loaded, output, nil
}

func isLaunchdMissingService(output []byte) bool {
	detail := strings.ToLower(string(output))
	return strings.Contains(detail, "could not find service") ||
		strings.Contains(detail, "service not found") ||
		strings.Contains(detail, "no such process") ||
		strings.Contains(detail, "service cannot load in requested session")
}

func launchdTerminalFailure(output []byte) bool {
	detail := strings.ToLower(string(output))
	if strings.Contains(detail, "state = exited") || strings.Contains(detail, "state = crashed") {
		return true
	}
	if strings.Contains(detail, "state = running") || strings.Contains(detail, "state = xpcproxy") {
		return false
	}
	for _, line := range strings.Split(detail, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "last exit code =")
		if !ok {
			continue
		}
		switch strings.TrimSpace(value) {
		case "0", "(never exited)":
			continue
		default:
			return true
		}
	}
	return false
}

func (m *launchdServiceManager) waitForRunning(ctx context.Context) (ServiceStatus, error) {
	waitCtx, cancel := context.WithTimeout(ctx, serviceReadyTimeout)
	defer cancel()
	for {
		status, loaded, output, err := m.inspectOutput(waitCtx)
		if err != nil {
			return ServiceStatus{}, err
		}
		if status.Running {
			return status, nil
		}
		if !loaded {
			return ServiceStatus{}, fmt.Errorf("wait for launchd target %s to run: job unloaded; Run manually: launchctl print %s", m.target, m.target)
		}
		if launchdTerminalFailure(output) {
			return ServiceStatus{}, fmt.Errorf("wait for launchd target %s to run: terminal failure (%s); Run manually: launchctl print %s", m.target, strings.TrimSpace(string(output)), m.target)
		}
		select {
		case <-waitCtx.Done():
			return ServiceStatus{}, fmt.Errorf("wait for launchd target %s to run: %w; Run manually: launchctl print %s", m.target, waitCtx.Err(), m.target)
		case <-time.After(serviceReadyPollInterval):
		}
	}
}

func (m *launchdServiceManager) bootoutAndWait(ctx context.Context) error {
	output, err := m.config.Runner.Run(ctx, "launchctl", "bootout", m.target)
	if err != nil {
		if isLaunchdMissingService(output) {
			return nil
		}
		return serviceCommandError("launchctl", []string{"bootout", m.target}, output, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, serviceReadyTimeout)
	defer cancel()
	for {
		_, loaded, inspectErr := m.inspect(waitCtx)
		if inspectErr != nil {
			return inspectErr
		}
		if !loaded {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("wait for launchd target %s to unload: %w; Run manually: launchctl print %s", m.target, waitCtx.Err(), m.target)
		case <-time.After(serviceReadyPollInterval):
		}
	}
}

func (m *launchdServiceManager) run(ctx context.Context, name string, args ...string) error {
	output, err := m.config.Runner.Run(ctx, name, args...)
	if err != nil {
		return serviceCommandError(name, args, output, err)
	}
	return nil
}

func (m *launchdServiceManager) plist() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + xmlText(launchdServiceLabel) + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlText(m.config.BinaryPath) + `</string>
    <string>run</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>AGENTRED_DATA_DIR</key>
    <string>` + xmlText(m.config.DataDir) + `</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
</dict>
</plist>
`
}

func xmlText(value string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	return escaped.String()
}
