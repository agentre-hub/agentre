// Package identity owns protocol-neutral daemon identity derivation.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
)

// DaemonFingerprint returns the canonical TOFU identifier "sha256:<hex>"
// derived from the daemon's instance UUID.
func DaemonFingerprint(uuid string) string {
	h := sha256.Sum256([]byte(uuid))
	return "sha256:" + hex.EncodeToString(h[:])
}
