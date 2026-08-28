package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentre-hub/agentre/e2e/fakepeer"
)

type ready struct {
	URL               string `json:"url"`
	ControlURL        string `json:"controlUrl"`
	DaemonFingerprint string `json:"daemonFingerprint"`
}

func main() {
	var deviceFingerprint, deviceToken, instanceUUID, controlToken, readyPath string
	flag.StringVar(&deviceFingerprint, "device-fingerprint", "", "generated desktop fingerprint")
	flag.StringVar(&deviceToken, "device-token", "", "generated pairing token")
	flag.StringVar(&instanceUUID, "instance-uuid", "", "generated fake daemon instance UUID")
	flag.StringVar(&controlToken, "control-token", "", "generated runner control token")
	flag.StringVar(&readyPath, "ready-file", "", "private path for sanitized readiness JSON")
	flag.Parse()
	deviceFingerprint = firstNonEmpty(deviceFingerprint, os.Getenv("AGENTRE_E2E_DEVICE_FINGERPRINT"))
	deviceToken = firstNonEmpty(deviceToken, os.Getenv("AGENTRE_E2E_REMOTE_DEVICE_TOKEN"))
	instanceUUID = firstNonEmpty(instanceUUID, os.Getenv("AGENTRE_E2E_REMOTE_INSTANCE_UUID"))
	controlToken = firstNonEmpty(controlToken, os.Getenv("AGENTRE_E2E_CONTROL_TOKEN"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server, err := fakepeer.Start(ctx, fakepeer.Options{
		DeviceFingerprint: deviceFingerprint,
		DeviceToken:       deviceToken,
		InstanceUUID:      instanceUUID,
		ControlToken:      controlToken,
	})
	if err != nil {
		fatal(err)
	}
	defer func() { _ = server.Close() }()
	if readyPath == "" {
		fatal(errors.New("fake-peer: ready-file is required"))
	}
	raw, err := json.Marshal(ready{
		URL: server.URL(), ControlURL: server.ControlURL(), DaemonFingerprint: server.DaemonFingerprint(),
	})
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(readyPath, append(raw, '\n'), 0o600); err != nil {
		fatal(fmt.Errorf("write ready file: %w", err))
	}
	<-ctx.Done()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
