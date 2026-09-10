// Package identity owns protocol-neutral daemon identity derivation.
package identity

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// DaemonFingerprint returns the canonical TOFU identifier "sha256:<hex>"
// derived from the daemon's instance UUID.
func DaemonFingerprint(uuid string) devicefp.Carrier {
	h := sha256.Sum256([]byte(uuid))
	return devicefp.Carrier("sha256:" + hex.EncodeToString(h[:]))
}
