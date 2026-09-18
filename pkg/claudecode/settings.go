package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

func prepareSettings(value string, env map[string]string) (string, func(), error) {
	if len(env) == 0 {
		return value, func() {}, nil
	}

	settings, err := readSettings(value)
	if err != nil {
		return "", func() {}, err
	}
	settingsEnv, err := settingsEnvironment(settings)
	if err != nil {
		return "", func() {}, err
	}
	for key, val := range env {
		settingsEnv[key] = val
	}
	settings["env"] = settingsEnv

	data, err := json.Marshal(settings)
	if err != nil {
		return "", func() {}, fmt.Errorf("claudecode: encode merged settings: %w", err)
	}
	f, err := os.CreateTemp("", "agentre-claudecode-settings-*.json")
	if err != nil {
		return "", func() {}, fmt.Errorf("claudecode: create temporary settings: %w", err)
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("claudecode: protect temporary settings: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("claudecode: write temporary settings: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("claudecode: close temporary settings: %w", err)
	}
	return path, cleanup, nil
}

func readSettings(value string) (map[string]any, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return make(map[string]any), nil
	}
	data := []byte(trimmed)
	if !strings.HasPrefix(trimmed, "{") {
		var err error
		data, err = os.ReadFile(trimmed) //nolint:gosec // caller explicitly selected this settings file
		if err != nil {
			return nil, fmt.Errorf("claudecode: read settings file: %w", err)
		}
	}
	settings := make(map[string]any)
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("claudecode: decode settings: %w", err)
	}
	return settings, nil
}

func settingsEnvironment(settings map[string]any) (map[string]any, error) {
	raw, ok := settings["env"]
	if !ok || raw == nil {
		return make(map[string]any), nil
	}
	env, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("claudecode: settings env must be an object")
	}
	return env, nil
}
