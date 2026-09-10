// Package identity owns protocol-neutral daemon identity derivation.
package identity

import (
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// DaemonFingerprint returns the canonical TOFU identifier "sha256:<hex>"
// derived from the daemon's instance UUID.
//
// 格式不在这里拼:它是这个值空间的规则,只有 pkg/wire/devicefp 一份实现——桌面端也从
// 那里派生(见 internal/pkg/deviceidentity)。两端各自拼一遍字符串,漂移就只是时间问题,
// 而漂移的症状是同一台机器被当成两台。
func DaemonFingerprint(uuid string) devicefp.Carrier {
	return devicefp.FromSeed(uuid)
}
