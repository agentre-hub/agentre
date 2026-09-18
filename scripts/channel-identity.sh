#!/usr/bin/env bash
# Applies a build channel's identity (stable|beta|nightly|dev) to build inputs and
# outputs. Shared by `make build` and CI so the rewrite exists exactly once. The
# identity values come from paths.Channel.Identity via go run; nothing here repeats them.
#
# usage: scripts/channel-identity.sh <channel> get <display-name|bundle-id|data-dir-name|keychain-service|release-asset-prefix|linux-command>
#        scripts/channel-identity.sh <channel> wails-json <path/to/wails.json>
#            set info.productName (Windows NSIS product name / install dir / uninstall key)
#        scripts/channel-identity.sh <channel> dev-plist <path/to/Info.dev.plist>
#            set CFBundleName / CFBundleDisplayName / CFBundleIdentifier of the wails dev bundle
#        scripts/channel-identity.sh <channel> macos-bundle <path/to/X.app> [codesign-identity]
#            rename the bundle to "<DisplayName>.app" beside it, set CFBundleIdentifier /
#            CFBundleName / CFBundleDisplayName, re-sign (ad-hoc when no identity is given)
#            and print the final bundle path
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
	sed -n 's/^# \{0,1\}\(usage: .*\)$/\1/p; s/^#        \(scripts\/.*\)$/       \1/p' "${BASH_SOURCE[0]}" >&2
	exit 2
}

identity() {
	(cd "$repo_root" && go run ./internal/desktop/channelidentity "$@")
}

[ $# -ge 2 ] || usage
channel="$1"
action="$2"
shift 2

case "$action" in
get | wails-json | dev-plist)
	[ $# -eq 1 ] || usage
	target="$1"
	if [ "$action" != get ]; then
		# go run changes directory; hand it an absolute path.
		case "$target" in /*) ;; *) target="$PWD/$target" ;; esac
	fi
	identity "$channel" "$action" "$target"
	;;
macos-bundle)
	[ $# -ge 1 ] && [ $# -le 2 ] || usage
	app="${1%/}"
	sign_identity="${2:--}"
	display_name="$(identity "$channel" get display-name)"
	bundle_id="$(identity "$channel" get bundle-id)"
	if [ ! -f "$app/Contents/Info.plist" ]; then
		echo "channel-identity: $app is not an app bundle (no Contents/Info.plist)" >&2
		exit 1
	fi
	dest="$(dirname "$app")/$display_name.app"
	if [ "$app" != "$dest" ]; then
		rm -rf "$dest"
		mv "$app" "$dest"
	fi
	plist="$dest/Contents/Info.plist"
	/usr/libexec/PlistBuddy -c "Delete :CFBundleDisplayName" "$plist" >/dev/null 2>&1 || true
	/usr/libexec/PlistBuddy \
		-c "Set :CFBundleIdentifier $bundle_id" \
		-c "Set :CFBundleName $display_name" \
		-c "Add :CFBundleDisplayName string $display_name" \
		"$plist"
	codesign --force --deep --sign "$sign_identity" "$dest" >&2
	echo "$dest"
	;;
*)
	usage
	;;
esac
