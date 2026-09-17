package desktop

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

const channelLDFlagPrefix = "-X github.com/agentre-hub/agentre/internal/pkg/paths.buildChannel="

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

// makeDryRun runs `make -n <args>` at the repo root: recipes are expanded and printed
// but never executed, so nothing is built and no tracked file is touched.
func makeDryRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Makefile recipes are exercised through a POSIX shell")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	cmd := exec.Command("make", append([]string{"-n", "-C", repoRoot(t)}, args...)...) //nolint:gosec // G204: fixed binary, test-controlled args
	cmd.Env = withoutEnv(os.Environ(), "CHANNEL")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func withoutEnv(env []string, key string) []string {
	kept := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			kept = append(kept, kv)
		}
	}
	return kept
}

func linesContaining(out, needle string) []string {
	var hits []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			hits = append(hits, line)
		}
	}
	return hits
}

func TestMakefileChannelSelection(t *testing.T) {
	t.Run("Given no CHANNEL When make build Then wails build and the bundled agrctl are marked dev", func(t *testing.T) {
		out, err := makeDryRun(t, "build")
		if err != nil {
			t.Fatalf("make -n build: %v\n%s", err, out)
		}
		assertChannelFlagOn(t, out, "build", paths.ChannelDev)
		assertChannelFlagOn(t, out, "cmd/agrctl", paths.ChannelDev)
	})

	for _, c := range paths.AllChannels() {
		t.Run("Given CHANNEL="+string(c)+" When make build Then the flag carries "+string(c), func(t *testing.T) {
			out, err := makeDryRun(t, "build", "CHANNEL="+string(c))
			if err != nil {
				t.Fatalf("make -n build: %v\n%s", err, out)
			}
			assertChannelFlagOn(t, out, "build", c)
			assertChannelFlagOn(t, out, "cmd/agrctl", c)
		})
	}

	t.Run("Given CHANNEL=nightly When make dev Then wails dev is marked nightly", func(t *testing.T) {
		out, err := makeDryRun(t, "dev", "CHANNEL=nightly")
		if err != nil {
			t.Fatalf("make -n dev: %v\n%s", err, out)
		}
		assertChannelFlagOn(t, out, "dev", paths.ChannelNightly)
	})

	for _, c := range paths.AllChannels() {
		t.Run("Given CHANNEL="+string(c)+" When make dev Then Info.dev.plist carries the "+string(c)+" bundle identity for the session and is restored", func(t *testing.T) {
			out, err := makeDryRun(t, "dev", "CHANNEL="+string(c))
			if err != nil {
				t.Fatalf("make -n dev: %v\n%s", err, out)
			}
			rewrite := "channel-identity.sh " + string(c) + " dev-plist build/darwin/Info.dev.plist"
			at := strings.Index(out, rewrite)
			if at < 0 {
				t.Fatalf("make dev does not apply the %s identity to Info.dev.plist:\n%s", c, out)
			}
			if wails := strings.Index(out, "\" dev"); wails < at {
				t.Fatalf("wails dev runs before Info.dev.plist is rewritten:\n%s", out)
			}
			if !strings.Contains(out, "trap") || !strings.Contains(out, "build/darwin/Info.dev.plist") {
				t.Fatalf("make dev does not restore Info.dev.plist:\n%s", out)
			}
		})
	}

	for _, goal := range []string{"dev", "build", "run", "install"} {
		for _, bad := range []string{"bogus", "Beta", "beta dev"} {
			t.Run("Given CHANNEL="+bad+" When make "+goal+" Then it fails before building and lists the allowed values", func(t *testing.T) {
				out, err := makeDryRun(t, goal, "CHANNEL="+bad)
				if err == nil {
					t.Fatalf("make -n %s CHANNEL=%q succeeded, want failure\n%s", goal, bad, out)
				}
				if strings.Contains(out, "wails\" build") || strings.Contains(out, "wails\" dev") {
					t.Fatalf("make %s CHANNEL=%q reached wails before failing:\n%s", goal, bad, out)
				}
				for _, c := range paths.AllChannels() {
					if !strings.Contains(out, string(c)) {
						t.Fatalf("failure output does not list allowed value %q:\n%s", c, out)
					}
				}
			})
		}
	}

	t.Run("Given the branch-local beta targets When listed Then they are gone", func(t *testing.T) {
		for _, goal := range []string{"build-beta", "install-beta"} {
			if out, err := makeDryRun(t, goal); err == nil {
				t.Fatalf("make -n %s still exists:\n%s", goal, out)
			}
		}
	})
}

func TestMakefileUsesChannelBundleOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("bundle naming only applies to macOS hosts")
	}
	for _, tc := range []struct {
		goal    string
		channel paths.Channel
		want    string
	}{
		{"run", paths.ChannelBeta, `open "build/bin/Agentre Beta.app"`},
		{"install", paths.ChannelDev, "/Agentre Dev.app"},
		{"install", paths.ChannelStable, "/Agentre.app"},
	} {
		t.Run("Given CHANNEL="+string(tc.channel)+" When make "+tc.goal+" Then it uses "+tc.want, func(t *testing.T) {
			out, err := makeDryRun(t, tc.goal, "CHANNEL="+string(tc.channel))
			if err != nil {
				t.Fatalf("make -n %s: %v\n%s", tc.goal, err, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("make -n %s CHANNEL=%s output lacks %q:\n%s", tc.goal, tc.channel, tc.want, out)
			}
			if !strings.Contains(out, "channel-identity.sh "+string(tc.channel)+" macos-bundle") {
				t.Fatalf("build does not apply the %s identity to the bundle:\n%s", tc.channel, out)
			}
		})
	}
	t.Run("Given no CHANNEL When make install Then it installs Agentre Dev.app, never Agentre.app", func(t *testing.T) {
		out, err := makeDryRun(t, "install")
		if err != nil {
			t.Fatalf("make -n install: %v\n%s", err, out)
		}
		installs := linesContaining(out, "ditto")
		if len(installs) == 0 {
			t.Fatalf("no ditto install line:\n%s", out)
		}
		for _, line := range installs {
			if !strings.Contains(line, "/Agentre Dev.app") || strings.Contains(line, "/Agentre.app") {
				t.Fatalf("default install must copy Agentre Dev.app only: %s", line)
			}
		}
	})
}

func assertChannelFlagOn(t *testing.T, out, marker string, c paths.Channel) {
	t.Helper()
	var target []string
	switch marker {
	case "build", "dev":
		for _, line := range linesContaining(out, "wails") {
			if strings.Contains(line, "\" "+marker) {
				target = append(target, line)
			}
		}
	default:
		target = linesContaining(out, marker)
	}
	if len(target) == 0 {
		t.Fatalf("no %s command in make output:\n%s", marker, out)
	}
	for _, line := range target {
		if !strings.Contains(line, channelLDFlagPrefix+string(c)) {
			t.Fatalf("%s command lacks %s%s:\n%s", marker, channelLDFlagPrefix, c, line)
		}
	}
}

func channelIdentityScript(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the channel identity script runs under bash")
	}
	cmd := exec.Command("bash", append([]string{filepath.Join(repoRoot(t), "scripts", "channel-identity.sh")}, args...)...) //nolint:gosec // G204: repo script, test-controlled args
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	return o.String(), e.String(), err
}

func TestChannelIdentityScriptReadsIdentityFromGo(t *testing.T) {
	for _, c := range paths.AllChannels() {
		id := c.Identity()
		for field, want := range map[string]string{
			"display-name":         id.DisplayName,
			"bundle-id":            id.BundleID,
			"data-dir-name":        id.DataDirName,
			"keychain-service":     id.KeychainService,
			"release-asset-prefix": id.ReleaseAssetPrefix,
			"linux-command":        id.LinuxCommand,
		} {
			t.Run("Given "+string(c)+" When get "+field+" Then it matches paths.Identity", func(t *testing.T) {
				out, stderr, err := channelIdentityScript(t, string(c), "get", field)
				if err != nil {
					t.Fatalf("get %s: %v\n%s", field, err, stderr)
				}
				if got := strings.TrimSuffix(out, "\n"); got != want {
					t.Fatalf("get %s = %q, want %q", field, got, want)
				}
			})
		}
	}

	t.Run("Given an invalid channel When the script runs Then it fails listing the allowed values", func(t *testing.T) {
		_, stderr, err := channelIdentityScript(t, "bogus", "get", "display-name")
		if err == nil {
			t.Fatal("invalid channel accepted")
		}
		for _, c := range paths.AllChannels() {
			if !strings.Contains(stderr, string(c)) {
				t.Fatalf("stderr does not list %q: %s", c, stderr)
			}
		}
	})

	t.Run("Given an unknown field When get Then it fails", func(t *testing.T) {
		if _, _, err := channelIdentityScript(t, "beta", "get", "shoe-size"); err == nil {
			t.Fatal("unknown field accepted")
		}
	})
}

func TestChannelIdentityScriptRewritesWailsProductName(t *testing.T) {
	original, err := os.ReadFile(filepath.Join(repoRoot(t), "wails.json"))
	if err != nil {
		t.Fatalf("read wails.json: %v", err)
	}
	for _, c := range paths.AllChannels() {
		t.Run("Given "+string(c)+" When wails-json Then only info.productName changes", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wails.json")
			if err := os.WriteFile(path, original, 0o600); err != nil { //nolint:gosec // G703: temp path
				t.Fatal(err)
			}
			if _, stderr, err := channelIdentityScript(t, string(c), "wails-json", path); err != nil {
				t.Fatalf("wails-json: %v\n%s", err, stderr)
			}
			rewritten, err := os.ReadFile(path) //nolint:gosec // G304: temp path
			if err != nil {
				t.Fatal(err)
			}
			var before, after map[string]any
			if err := json.Unmarshal(original, &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(rewritten, &after); err != nil {
				t.Fatalf("rewritten wails.json is not JSON: %v\n%s", err, rewritten)
			}
			if got := after["info"].(map[string]any)["productName"]; got != c.Identity().DisplayName {
				t.Fatalf("productName = %v, want %q", got, c.Identity().DisplayName)
			}
			before["info"].(map[string]any)["productName"] = c.Identity().DisplayName
			b, _ := json.Marshal(before)
			a, _ := json.Marshal(after)
			if !bytes.Equal(a, b) {
				t.Fatalf("wails-json changed more than productName:\n%s", rewritten)
			}
			// Executable name stays Agentre(.exe): only the product identity is per channel.
			if after["outputfilename"] != "Agentre" || after["name"] != "Agentre" {
				t.Fatalf("name/outputfilename changed: %s", rewritten)
			}
		})
	}
}

func TestChannelIdentityScriptRewritesDevPlist(t *testing.T) {
	original, err := os.ReadFile(darwinPlist("Info.dev.plist"))
	if err != nil {
		t.Fatalf("read Info.dev.plist: %v", err)
	}
	devID := paths.ChannelDev.Identity()
	for _, c := range paths.AllChannels() {
		id := c.Identity()
		t.Run("Given "+string(c)+" When dev-plist Then only the bundle name, display name and id change", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Info.dev.plist")
			if err := os.WriteFile(path, original, 0o600); err != nil { //nolint:gosec // G703: temp path
				t.Fatal(err)
			}
			if _, stderr, err := channelIdentityScript(t, string(c), "dev-plist", path); err != nil {
				t.Fatalf("dev-plist: %v\n%s", err, stderr)
			}
			for key, want := range map[string]string{
				"CFBundleName":        id.DisplayName,
				"CFBundleDisplayName": id.DisplayName,
				"CFBundleIdentifier":  id.BundleID,
			} {
				if got := plistString(t, path, key); got != want {
					t.Fatalf("%s = %q, want %q", key, got, want)
				}
			}
			rewritten, err := os.ReadFile(path) //nolint:gosec // G304: temp path
			if err != nil {
				t.Fatal(err)
			}
			back := strings.NewReplacer(
				"<string>"+id.BundleID+"</string>", "<string>"+devID.BundleID+"</string>",
				"<string>"+id.DisplayName+"</string>", "<string>"+devID.DisplayName+"</string>",
			).Replace(string(rewritten))
			if back != string(original) {
				t.Fatalf("dev-plist changed more than the bundle identity:\n%s", rewritten)
			}
		})
	}
}

func TestChannelIdentityScriptRewritesMacOSBundle(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("PlistBuddy and codesign are macOS tools")
	}
	for _, c := range []paths.Channel{paths.ChannelBeta, paths.ChannelStable, paths.ChannelDev} {
		id := c.Identity()
		t.Run("Given a wails-built Agentre.app When macos-bundle "+string(c)+" Then name, bundle id and signature follow the channel", func(t *testing.T) {
			dir := t.TempDir()
			app := fakeWailsBundle(t, dir)
			out, stderr, err := channelIdentityScript(t, string(c), "macos-bundle", app)
			if err != nil {
				t.Fatalf("macos-bundle: %v\n%s", err, stderr)
			}
			want := filepath.Join(dir, id.DisplayName+".app")
			if got := strings.TrimSuffix(out, "\n"); got != want {
				t.Fatalf("printed bundle path = %q, want %q", got, want)
			}
			if c != paths.ChannelStable {
				if _, err := os.Stat(app); !os.IsNotExist(err) {
					t.Fatalf("original %s still exists (err=%v)", app, err)
				}
			}
			plist := filepath.Join(want, "Contents", "Info.plist")
			for key, v := range map[string]string{
				"CFBundleIdentifier":  id.BundleID,
				"CFBundleName":        id.DisplayName,
				"CFBundleDisplayName": id.DisplayName,
			} {
				got, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :"+key, plist).Output() //nolint:gosec // G204: fixed tool
				if err != nil {
					t.Fatalf("read %s: %v", key, err)
				}
				if strings.TrimSpace(string(got)) != v {
					t.Fatalf("%s = %q, want %q", key, strings.TrimSpace(string(got)), v)
				}
			}
			if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", want).CombinedOutput(); err != nil { //nolint:gosec // G204: fixed tool
				t.Fatalf("bundle signature invalid after rewrite: %v\n%s", err, out)
			}
		})
	}

	t.Run("Given a stale channel bundle When macos-bundle runs again Then it is replaced", func(t *testing.T) {
		dir := t.TempDir()
		stale := filepath.Join(dir, "Agentre Beta.app", "Contents", "stale")
		if err := os.MkdirAll(stale, 0o750); err != nil {
			t.Fatal(err)
		}
		app := fakeWailsBundle(t, dir)
		if _, stderr, err := channelIdentityScript(t, "beta", "macos-bundle", app); err != nil {
			t.Fatalf("macos-bundle: %v\n%s", err, stderr)
		}
		if _, err := os.Stat(stale); !os.IsNotExist(err) {
			t.Fatalf("stale content survived (err=%v)", err)
		}
	})

	t.Run("Given a missing bundle When macos-bundle Then it fails", func(t *testing.T) {
		if _, _, err := channelIdentityScript(t, "beta", "macos-bundle", filepath.Join(t.TempDir(), "Agentre.app")); err == nil {
			t.Fatal("missing bundle accepted")
		}
	})
}

// fakeWailsBundle lays out the minimum of what `wails build` produces: an Info.plist
// rendered from build/darwin/Info.plist and a main executable.
func fakeWailsBundle(t *testing.T, dir string) string {
	t.Helper()
	app := filepath.Join(dir, "Agentre.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o750); err != nil {
		t.Fatal(err)
	}
	tmpl, err := os.ReadFile(darwinPlist("Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	rendered := strings.NewReplacer(
		"{{.Info.ProductName}}", "Agentre",
		"{{.OutputFilename}}", "Agentre",
		"{{.Info.ProductVersion}}", "0.1.0",
		"{{.Info.Comments}}", "",
		"{{.Info.Copyright}}", "",
	).Replace(string(tmpl))
	// Drop the template-only conditional blocks (file associations / protocols).
	for _, block := range []string{"{{if .Info.FileAssociations}}", "{{if .Info.Protocols}}"} {
		if i := strings.Index(rendered, block); i >= 0 {
			if j := strings.Index(rendered[i:], "{{end}}\n        </array>\n        {{end}}"); j >= 0 {
				rendered = rendered[:i] + rendered[i+j+len("{{end}}\n        </array>\n        {{end}}"):]
			}
		}
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(rendered), 0o600); err != nil { //nolint:gosec // G703: temp path
		t.Fatal(err)
	}
	exe := filepath.Join(app, "Contents", "MacOS", "Agentre")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return app
}
