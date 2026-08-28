package main

import (
	"context"
	"embed"
	"log"

	"github.com/agentre-hub/agentre/internal/app"
	"github.com/agentre-hub/agentre/internal/desktop"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := desktop.Run(context.Background(), productionDesktopOptions()); err != nil {
		log.Fatalf("desktop run: %v", err)
	}
}

func productionDesktopOptions() desktop.Options {
	return desktop.Options{
		Assets:      assets,
		RuntimeMode: app.RuntimeModeInteractive,
	}
}
