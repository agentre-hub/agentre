#!/bin/sh
set -eu

release_base_url=${AGENTRED_RELEASE_BASE_URL:-https://github.com/agentre-hub/agentre/releases/latest/download}
system_install_dir=${AGENTRED_SYSTEM_INSTALL_DIR:-/usr/local/bin}

die() {
  printf 'agentred installer: %s\n' "$*" >&2
  exit 1
}

download() {
  url=$1
  output=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$output"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$output" "$url"
  else
    die "curl or wget is required"
  fi
}

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "unsupported operating system: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/agentred-install.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
checksums="$work_dir/SHA256SUMS"
download "$release_base_url/SHA256SUMS" "$checksums"

asset=$(awk -v pattern="^agentred-.*-${os}-${arch}\\.tar\\.gz$" '
  {
    name=$2
    sub(/^\*/, "", name)
    if (name ~ pattern) {
      print name
      exit
    }
  }
' "$checksums")
[ -n "$asset" ] || die "SHA256SUMS does not contain an agentred archive for ${os}/${arch}"
archive="$work_dir/$asset"
download "$release_base_url/$asset" "$archive"

expected=$(awk -v wanted="$asset" '
  {
    name=$2
    sub(/^\*/, "", name)
    if (name == wanted) {
      print tolower($1)
      exit
    }
  }
' "$checksums")
[ -n "$expected" ] || die "SHA256 checksum is missing for $asset"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$archive" | awk '{print tolower($1)}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$archive" | awk '{print tolower($1)}')
else
  die "sha256sum or shasum is required"
fi
[ "$actual" = "$expected" ] || die "SHA256 checksum mismatch for $asset"

fallback=false
if [ -n "${AGENTRED_INSTALL_DIR:-}" ]; then
  install_dir=$AGENTRED_INSTALL_DIR
elif { [ -d "$system_install_dir" ] && [ -w "$system_install_dir" ]; } || \
     { [ ! -e "$system_install_dir" ] && [ -w "$(dirname "$system_install_dir")" ]; }; then
  install_dir=$system_install_dir
else
  install_dir=${HOME:?HOME is required for user-level installation}/.local/bin
  fallback=true
fi
mkdir -p "$install_dir"
[ -d "$install_dir" ] && [ -w "$install_dir" ] || die "install directory is not writable: $install_dir"

extract_dir="$work_dir/extracted"
mkdir -p "$extract_dir"
tar -xzf "$archive" -C "$extract_dir"
[ -f "$extract_dir/agentred" ] || die "archive does not contain agentred"
cp "$extract_dir/agentred" "$install_dir/agentred.tmp"
chmod 755 "$install_dir/agentred.tmp"
mv "$install_dir/agentred.tmp" "$install_dir/agentred"

identity=$($install_dir/agentred --version) || die "installed binary failed --version confirmation"
case "$identity" in
  "agentred "*) ;;
  *) die "installed binary returned an unexpected version: $identity" ;;
esac
printf 'Installed agentred to %s\n' "$install_dir/agentred"
printf '%s\n' "$identity"
if [ "$fallback" = true ]; then
  printf 'Add %s to PATH, for example: export PATH="%s:$PATH"\n' "$install_dir" "$install_dir"
fi
