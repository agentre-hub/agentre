package desktop

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

// TestDarwinDevBundleIdentityIsDistinctFromProduction pins the macOS identity
// split that keeps `wails dev` from colliding with /Applications/Agentre.app.
// Both bundles used to ship as com.wails.Agentre; Dock, Spaces and fullscreen
// then treated them as one app, so the StartHidden dev window never became
// visible on the current space.
func TestDarwinDevBundleIdentityIsDistinctFromProduction(t *testing.T) {
	prodID := plistString(t, darwinPlist("Info.plist"), "CFBundleIdentifier")
	devID := plistString(t, darwinPlist("Info.dev.plist"), "CFBundleIdentifier")
	devName := plistString(t, darwinPlist("Info.dev.plist"), "CFBundleName")
	devDisplay := plistString(t, darwinPlist("Info.dev.plist"), "CFBundleDisplayName")

	t.Run("Given the darwin Info templates When compared Then they carry the stable and dev channel bundle ids", func(t *testing.T) {
		if prodID != paths.ChannelStable.Identity().BundleID {
			t.Fatalf("production CFBundleIdentifier = %q, want %q", prodID, paths.ChannelStable.Identity().BundleID)
		}
		if devID != paths.ChannelDev.Identity().BundleID {
			t.Fatalf("dev CFBundleIdentifier = %q, want %q", devID, paths.ChannelDev.Identity().BundleID)
		}
	})

	t.Run("Given Info.dev.plist When read Then Dock name is the Dev channel display name", func(t *testing.T) {
		if want := paths.ChannelDev.Identity().DisplayName; devName != want {
			t.Fatalf("dev CFBundleName = %q, want %q for Dock/Cmd+Tab", devName, want)
		}
		if devDisplay != devName {
			t.Fatalf("dev CFBundleDisplayName = %q, want %q so Dock matches the app menu", devDisplay, devName)
		}
	})
}

func darwinPlist(name string) string {
	return filepath.Join("..", "..", "build", "darwin", name)
}

func plistString(t *testing.T, path, key string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is a repo-relative constant joined in darwinPlist
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	re := regexp.MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key>\s*<string>([^<]*)</string>`)
	m := re.FindSubmatch(data)
	if m == nil {
		t.Fatalf("%s: missing %s", path, key)
	}
	return string(m[1])
}
