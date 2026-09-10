package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/paths"
	"github.com/agentre-hub/agentre/internal/pkg/procattr"
)

// ServiceManager is the platform-neutral lifecycle contract shared by the CLI
// and each user-level service implementation.
type ServiceManager interface {
	Install(context.Context) (ServiceStatus, error)
	Start(context.Context) (ServiceStatus, error)
	Stop(context.Context) (ServiceStatus, error)
	Restart(context.Context) (ServiceStatus, error)
	Uninstall(context.Context) (ServiceStatus, error)
	Status(context.Context) (ServiceStatus, error)
}

// ServiceStatus is intentionally platform-neutral so onboarding can consume
// the same installed/running states on every supported operating system.
type ServiceStatus struct {
	Installed bool
	Running   bool
	Details   []string
}

type serviceCommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execServiceCommandRunner struct{}

func (execServiceCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // command names and arguments are fixed service-manager inputs.
	procattr.ApplyNoConsoleWindow(cmd)
	return cmd.CombinedOutput()
}

type serviceManagerConfig struct {
	BinaryPath string
	DataDir    string
	HomeDir    string
	UserName   string
	UID        int
	Runner     serviceCommandRunner
}

func newPlatformServiceManager() (ServiceManager, error) {
	binaryPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve agentred executable: %w", err)
	}
	binaryPath, err = filepath.Abs(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute agentred executable: %w", err)
	}
	dataDir, err := paths.AgentredDataDir()
	if err != nil {
		return nil, err
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute agentred data directory: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("resolve current user: %w", err)
	}
	config := serviceManagerConfig{
		BinaryPath: binaryPath,
		DataDir:    dataDir,
		HomeDir:    homeDir,
		UserName:   currentUser.Username,
		Runner:     execServiceCommandRunner{},
	}
	return newOSServiceManager(config, currentUser)
}

func writeServiceFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create service directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write service definition: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install service definition: %w", err)
	}
	return nil
}

func serviceFileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func serviceCommandError(name string, args []string, output []byte, err error) error {
	command := strings.Join(append([]string{name}, args...), " ")
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return fmt.Errorf("run %s: %w: %s; Run manually: %s", command, err, detail, command)
	}
	return fmt.Errorf("run %s: %w; Run manually: %s", command, err, command)
}

func requireInstalled(path string) error {
	installed, err := serviceFileExists(path)
	if err != nil {
		return err
	}
	if !installed {
		return fmt.Errorf("service is not installed; run agentred service install")
	}
	return nil
}
