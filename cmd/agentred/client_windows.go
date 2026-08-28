//go:build windows

package main

import (
	"context"
	"net"

	"github.com/agentre-hub/agentre/internal/pkg/agentredipc"
)

func localDialContext(dataDir string) func(context.Context, string, string) (net.Conn, error) {
	return agentredipc.DialContext(dataDir)
}
