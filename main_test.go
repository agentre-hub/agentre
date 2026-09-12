package main

import (
	"testing"

	"github.com/agentre-hub/agentre/internal/app"
)

func TestProductionDesktopOptionsAreExplicitlyInteractive(t *testing.T) {
	opts := productionDesktopOptions()
	if opts.RuntimeMode != app.RuntimeModeInteractive {
		t.Fatalf("RuntimeMode = %q, want %q", opts.RuntimeMode, app.RuntimeModeInteractive)
	}
	if opts.Assets == nil {
		t.Fatal("Assets must be provided to the shared desktop runner")
	}
}
