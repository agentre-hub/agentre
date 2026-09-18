// Command channelidentity exposes paths.Channel.Identity to build tooling, so
// scripts/channel-identity.sh (local make and CI) never keeps its own copy of the
// channel identity table.
//
//	go run ./internal/desktop/channelidentity <channel> get <field>
//	go run ./internal/desktop/channelidentity <channel> wails-json <path>
//	go run ./internal/desktop/channelidentity <channel> dev-plist <path>
package main

import (
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

const usage = "usage: channelidentity <stable|beta|nightly|dev> get <display-name|bundle-id|data-dir-name|keychain-service|release-asset-prefix|linux-command>\n" +
	"       channelidentity <stable|beta|nightly|dev> wails-json <path/to/wails.json>\n" +
	"       channelidentity <stable|beta|nightly|dev> dev-plist <path/to/Info.dev.plist>"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "channelidentity:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) != 3 {
		return fmt.Errorf("%s", usage)
	}
	// Build tooling always names the channel; the binary's "empty means dev" rule
	// covers an unmarked build, not a script invocation.
	if args[0] == "" {
		return fmt.Errorf("empty channel\n%s", usage)
	}
	channel, err := paths.ParseChannel(args[0])
	if err != nil {
		return err
	}
	id := channel.Identity()
	switch args[1] {
	case "get":
		value, err := field(id, args[2])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, value)
		return err
	case "wails-json":
		return rewriteProductName(args[2], id.DisplayName)
	case "dev-plist":
		return rewriteDevPlist(args[2], id)
	default:
		return fmt.Errorf("unknown action %q\n%s", args[1], usage)
	}
}

func field(id paths.Identity, name string) (string, error) {
	switch name {
	case "display-name":
		return id.DisplayName, nil
	case "bundle-id":
		return id.BundleID, nil
	case "data-dir-name":
		return id.DataDirName, nil
	case "keychain-service":
		return id.KeychainService, nil
	case "release-asset-prefix":
		return id.ReleaseAssetPrefix, nil
	case "linux-command":
		return id.LinuxCommand, nil
	default:
		return "", fmt.Errorf("unknown field %q\n%s", name, usage)
	}
}

// productNamePattern matches the single "productName" member; rewriting in place keeps
// the file's formatting so a caller's restore/diff stays byte-exact everywhere else.
var productNamePattern = regexp.MustCompile(`("productName"\s*:\s*)"(?:[^"\\]|\\.)*"`)

func rewriteProductName(path, displayName string) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: build tooling reads the path it was given
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if n := len(productNamePattern.FindAllIndex(data, -1)); n != 1 {
		return fmt.Errorf("%s: want exactly one productName, found %d", path, n)
	}
	out := productNamePattern.ReplaceAll(data, []byte(`${1}"`+displayName+`"`))
	info, err := os.Stat(path) //nolint:gosec // G703: build tooling rewrites the path it was given
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, info.Mode().Perm()); err != nil { //nolint:gosec // G703: same caller-supplied path
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// rewriteDevPlist sets the bundle identity members of the Info.dev.plist template in
// place: the name shown in Dock / Cmd+Tab and the CFBundleIdentifier that keys macOS
// per-app state (including the WKWebView data store), so a `wails dev` session of one
// channel never shares it with another. The file is a Go template rather than a plist,
// so it is rewritten textually and every other byte is kept.
func rewriteDevPlist(path string, id paths.Identity) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: build tooling reads the path it was given
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	for _, member := range []struct{ key, value string }{
		{"CFBundleName", id.DisplayName},
		{"CFBundleDisplayName", id.DisplayName},
		{"CFBundleIdentifier", id.BundleID},
	} {
		re := regexp.MustCompile(`(<key>` + regexp.QuoteMeta(member.key) + `</key>\s*<string>)[^<]*(</string>)`)
		if n := len(re.FindAllIndex(data, -1)); n != 1 {
			return fmt.Errorf("%s: want exactly one %s string, found %d", path, member.key, n)
		}
		data = re.ReplaceAll(data, []byte(`${1}`+member.value+`${2}`))
	}
	info, err := os.Stat(path) //nolint:gosec // G703: build tooling rewrites the path it was given
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, info.Mode().Perm()); err != nil { //nolint:gosec // G703: same caller-supplied path
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
