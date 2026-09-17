// Package agentredipc owns the local daemon endpoint shared by agentred and
// its CLI. Platform transports live behind build tags so callers cannot drift
// on endpoint naming or dialing behavior.
package agentredipc

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

const unixSocketName = "agentred.sock"

// unixSocketPath preserves the existing local IPC path on Unix platforms.
func unixSocketPath(dataDir string) string {
	return filepath.Join(dataDir, unixSocketName)
}

// windowsPipePath derives an opaque, stable pipe name from the data directory.
// The digest prevents usernames and directory layout from leaking through the
// globally visible named-pipe namespace.
func windowsPipePath(dataDir string) string {
	normalized := strings.ToLower(strings.TrimRight(strings.ReplaceAll(dataDir, "/", `\`), `\`))
	digest := sha256.Sum256([]byte(normalized))
	return `\\.\pipe\agentred-` + hex.EncodeToString(digest[:16])
}

func securityDescriptorForSID(sid string) string {
	return "D:P(A;;GA;;;" + sid + ")"
}
