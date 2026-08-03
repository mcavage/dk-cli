#!/bin/sh
# Install dk, an agent-first DigiKey CLI.
#
#   curl -fsSL https://raw.githubusercontent.com/mcavage/dk-cli/main/install.sh | sh
#
# Installs into ~/.local/bin by default, which needs no root. Set DK_INSTALL_DIR
# to override and DK_VERSION to pin a release.
#
# This script verifies the SHA256 checksum before installing. A download that
# cannot be verified is not installed, because "curl | sh" already asks for
# enough trust without also skipping integrity checks.

set -eu

REPO="mcavage/dk-cli"
BIN="dk"
INSTALL_DIR="${DK_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${DK_VERSION:-latest}"

err() { printf '%s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*" >&2; }

command -v curl >/dev/null 2>&1 || err "curl is required"

case "$(uname -s)" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  *) err "unsupported OS: $(uname -s). Build from source: go install github.com/$REPO/cmd/$BIN@latest" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) err "unsupported architecture: $(uname -m)" ;;
esac

if [ "$VERSION" = "latest" ]; then
  BASE="https://github.com/$REPO/releases/latest/download"
else
  BASE="https://github.com/$REPO/releases/download/$VERSION"
fi

ASSET="${BIN}_${OS}_${ARCH}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

info "downloading $ASSET"
curl -fsSL "$BASE/$ASSET" -o "$TMP/$BIN" \
  || err "download failed. If no release exists yet, build from source:
  go install github.com/$REPO/cmd/$BIN@latest"

# Checksum verification is not optional. Skipping it on a machine with no
# sha256 tool would silently downgrade every install on that machine.
curl -fsSL "$BASE/checksums.txt" -o "$TMP/checksums.txt" \
  || err "could not fetch checksums.txt; refusing to install unverified"

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "$TMP/$BIN" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "$TMP/$BIN" | awk '{print $1}')"
else
  err "no sha256sum or shasum found; refusing to install unverified"
fi

EXPECTED="$(awk -v a="$ASSET" '$2 == a || $2 == "*"a {print $1}' "$TMP/checksums.txt" | head -n1)"
[ -n "$EXPECTED" ] || err "no checksum listed for $ASSET; refusing to install"
[ "$ACTUAL" = "$EXPECTED" ] || err "checksum mismatch for $ASSET
  expected $EXPECTED
  actual   $ACTUAL"

mkdir -p "$INSTALL_DIR"
chmod +x "$TMP/$BIN"
mv "$TMP/$BIN" "$INSTALL_DIR/$BIN"

info "installed $INSTALL_DIR/$BIN"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) info ""
     info "$INSTALL_DIR is not on your PATH. Add it:"
     info "  export PATH=\"\$PATH:$INSTALL_DIR\"" ;;
esac

# Warn about shadowing rather than letting it be mysterious later.
EXISTING="$(command -v "$BIN" 2>/dev/null || true)"
if [ -n "$EXISTING" ] && [ "$EXISTING" != "$INSTALL_DIR/$BIN" ]; then
  info ""
  info "warning: another '$BIN' is earlier on your PATH: $EXISTING"
  info "run '$INSTALL_DIR/$BIN version' to check which one you get"
fi

info ""
info "next: dk doctor"
