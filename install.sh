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

# Resolve the version so the asset name can carry it, matching what the
# Homebrew formula and the release job both expect.
if [ "$VERSION" = "latest" ]; then
  RESOLVED="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/$REPO/releases/latest" 2>/dev/null | sed 's|.*/tag/||')"
  [ -n "$RESOLVED" ] || err "could not resolve the latest release. Build from source:
  go install github.com/$REPO/cmd/$BIN@latest"
else
  RESOLVED="$VERSION"
fi
BARE="${RESOLVED#v}"
BASE="https://github.com/$REPO/releases/download/$RESOLVED"

ASSET="${BIN}_${BARE}_${OS}_${ARCH}.tar.gz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

info "downloading $ASSET"
curl -fsSL "$BASE/$ASSET" -o "$TMP/$ASSET" \
  || err "download failed. If no release exists yet, build from source:
  go install github.com/$REPO/cmd/$BIN@latest"

# Checksum verification is not optional. Skipping it on a machine with no
# sha256 tool would silently downgrade every install on that machine.
curl -fsSL "$BASE/checksums.txt" -o "$TMP/checksums.txt" \
  || err "could not fetch checksums.txt; refusing to install unverified"

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')"
else
  err "no sha256sum or shasum found; refusing to install unverified"
fi

EXPECTED="$(awk -v a="$ASSET" '$2 == a || $2 == "*"a {print $1}' "$TMP/checksums.txt" | head -n1)"
[ -n "$EXPECTED" ] || err "no checksum listed for $ASSET; refusing to install"
[ "$ACTUAL" = "$EXPECTED" ] || err "checksum mismatch for $ASSET
  expected $EXPECTED
  actual   $ACTUAL"

tar -xzf "$TMP/$ASSET" -C "$TMP" || err "could not unpack $ASSET"
[ -f "$TMP/$BIN" ] || err "$ASSET did not contain a $BIN binary"

mkdir -p "$INSTALL_DIR"
chmod +x "$TMP/$BIN"
mv "$TMP/$BIN" "$INSTALL_DIR/$BIN"

# The tarball also carries LICENSE, README.md and AGENTS.md. Homebrew installs
# them into doc; put them somewhere findable here too rather than making this
# the one channel that drops them.
DOC_DIR="${DK_DOC_DIR:-$HOME/.local/share/doc/$BIN}"
if mkdir -p "$DOC_DIR" 2>/dev/null; then
  for f in LICENSE README.md AGENTS.md; do
    [ -f "$TMP/$f" ] && cp "$TMP/$f" "$DOC_DIR/" 2>/dev/null || true
  done
fi

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
