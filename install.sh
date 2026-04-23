#!/usr/bin/env sh
# gosymdb installer.
# Installs the gosymdb binary and — if Claude Code is available —
# registers it as a first-class MCP tool via cli-bridge.
#
#   curl -sSL https://raw.githubusercontent.com/walkindude/gosymdb/master/install.sh | sh
#
# Env vars:
#   GOSYMDB_VERSION   Version to install. Default: latest GitHub release.
#   GOSYMDB_PREFIX    Install prefix. Default: $HOME/.local
#   GOSYMDB_SKIP_CLI_BRIDGE=1   Don't attempt to set up cli-bridge.

set -eu

REPO="walkindude/gosymdb"
CLI_BRIDGE_REPO="walkindude/cli-bridge"
VERSION="${GOSYMDB_VERSION:-latest}"
PREFIX="${GOSYMDB_PREFIX:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"

say() { printf '%s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

detect_platform() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "unsupported arch: $arch" ;;
  esac
  case "$os" in
    linux|darwin) : ;;
    *) die "unsupported OS: $os (use install.ps1 on Windows)" ;;
  esac
  PLATFORM_OS="$os"
  PLATFORM_ARCH="$arch"
}

resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    return
  fi
  if ! command -v curl >/dev/null 2>&1; then
    die "curl is required"
  fi
  say "Resolving latest gosymdb release..."
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$VERSION" ] || die "could not resolve latest release (is the repo public with a release?)"
}

download_and_install() {
  url="https://github.com/$REPO/releases/download/$VERSION/gosymdb_${VERSION#v}_${PLATFORM_OS}_${PLATFORM_ARCH}.tar.gz"
  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' EXIT
  say "Downloading $url"
  if ! curl -fsSL "$url" -o "$tmpdir/gosymdb.tar.gz"; then
    die "download failed. Try: go install github.com/walkindude/gosymdb@latest"
  fi
  say "Verifying checksum..."
  checksum_url="https://github.com/$REPO/releases/download/$VERSION/checksums.txt"
  if curl -fsSL "$checksum_url" -o "$tmpdir/checksums.txt" 2>/dev/null; then
    asset_name="gosymdb_${VERSION#v}_${PLATFORM_OS}_${PLATFORM_ARCH}.tar.gz"
    expected=$(grep "  $asset_name\$" "$tmpdir/checksums.txt" | awk '{print $1}')
    if [ -n "$expected" ]; then
      actual=$(shasum -a 256 "$tmpdir/gosymdb.tar.gz" 2>/dev/null | awk '{print $1}' || sha256sum "$tmpdir/gosymdb.tar.gz" | awk '{print $1}')
      if [ "$expected" != "$actual" ]; then
        die "checksum mismatch: expected $expected, got $actual"
      fi
      say "Checksum OK"
    else
      say "warn: no checksum entry for $asset_name — proceeding without verification"
    fi
  else
    say "warn: could not fetch checksums.txt — proceeding without verification"
  fi

  tar -xzf "$tmpdir/gosymdb.tar.gz" -C "$tmpdir"
  mkdir -p "$BIN_DIR"
  mv "$tmpdir/gosymdb" "$BIN_DIR/gosymdb"
  chmod +x "$BIN_DIR/gosymdb"
  say "Installed gosymdb to $BIN_DIR/gosymdb"

  case ":$PATH:" in
    *":$BIN_DIR:"*) : ;;
    *) say ""; say "NOTE: $BIN_DIR is not on your PATH. Add this line to your shell profile:"; say "  export PATH=\"$BIN_DIR:\$PATH\"" ;;
  esac
}

setup_cli_bridge() {
  if [ "${GOSYMDB_SKIP_CLI_BRIDGE:-0}" = 1 ]; then
    return
  fi
  if ! command -v claude >/dev/null 2>&1; then
    say ""
    say "Claude Code CLI not found. To enable gosymdb as an MCP tool later:"
    say "  1. Install Claude Code: https://claude.com/claude-code"
    say "  2. In a session, run: /plugin marketplace add $CLI_BRIDGE_REPO"
    say "  3. Then: /plugin install cli-bridge@cli-bridge"
    say "  4. Then: /cli-bridge:register gosymdb"
    return
  fi
  say ""
  say "Generating cli-bridge spec for gosymdb..."
  spec_dir="${XDG_CONFIG_HOME:-$HOME/.config}/cli-bridge/specs/gosymdb"
  mkdir -p "$spec_dir"
  binver=$("$BIN_DIR/gosymdb" version 2>/dev/null | awk '{print $2}')
  [ -n "$binver" ] || binver="dev"
  spec_path="$spec_dir/$binver.json"
  if "$BIN_DIR/gosymdb" cli-bridge-manifest > "$spec_path.tmp" 2>/dev/null; then
    mv "$spec_path.tmp" "$spec_path"
    say "Wrote cli-bridge spec: $spec_path"
  else
    rm -f "$spec_path.tmp"
    say "warn: gosymdb cli-bridge-manifest unavailable (older build?). Skipping spec write."
    return
  fi

  say ""
  say "Next steps (in a Claude Code session):"
  say "  /plugin marketplace add $CLI_BRIDGE_REPO"
  say "  /plugin install cli-bridge@cli-bridge"
  say ""
  say "Then restart Claude Code. gosymdb_* tools will appear in the MCP tool list."
}

main() {
  detect_platform
  resolve_version
  download_and_install
  setup_cli_bridge
  say ""
  say "Done. Try: gosymdb --help"
}

main
