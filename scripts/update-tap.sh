#!/usr/bin/env bash
# Copy Formula/dk.rb into the tap, rewriting only version, urls and sha256s.
#
# The formula in THIS repo is the source of truth. Everything else in it,
# notably `def install`, ships to users verbatim, and a test here asserts it
# only installs paths `make dist` actually stages. Editing the tap's copy
# directly is pointless: the next release overwrites it.
#
# Run after `make dist`, so dist/checksums.txt exists.
set -euo pipefail

TAG="${1:?usage: update-tap.sh vX.Y.Z}"
VERSION="${TAG#v}"
REPO="${REPO:-mcavage/dk-cli}"
TAP_REPO="${TAP_REPO:-mcavage/homebrew-tap}"
SRC="Formula/dk.rb"

[ -f "$SRC" ] || { echo "missing $SRC" >&2; exit 1; }
[ -f dist/checksums.txt ] || { echo "missing dist/checksums.txt; run make dist first" >&2; exit 1; }

sha_for() {
  awk -v a="dk_${VERSION}_$1.tar.gz" '$2 == a || $2 == "*"a {print $1}' dist/checksums.txt
}

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

cp "$SRC" "$WORK/dk.rb"

# Pin the version, then point every URL and checksum at this release. Done with
# per-platform substitution rather than a blanket sed so a mismatch between the
# formula's platforms and the built assets fails loudly instead of leaving a
# stale URL behind.
sed -i.bak -E "s|^(  version \")[^\"]+(\")|\\1${VERSION}\\2|" "$WORK/dk.rb"

for platform in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
  sha="$(sha_for "$platform")"
  [ -n "$sha" ] || { echo "no checksum for $platform in dist/checksums.txt" >&2; exit 1; }
  url="https://github.com/${REPO}/releases/download/${TAG}/dk_${VERSION}_${platform}.tar.gz"

  # Replace the URL line for this platform, then the sha256 line that follows it.
  perl -0pi -e "s{url \"[^\"]*${platform}\\.tar\\.gz\"\\n(\\s*)sha256 \"[^\"]*\"}{url \"${url}\"\\n\${1}sha256 \"${sha}\"}s" "$WORK/dk.rb"
done
rm -f "$WORK/dk.rb.bak"

# Fail rather than publish a formula still carrying a placeholder.
if grep -q '0000000000000000' "$WORK/dk.rb"; then
  echo "formula still contains placeholder checksums; substitution failed" >&2
  exit 1
fi
if grep -q 'v0\.0\.0' "$WORK/dk.rb"; then
  echo "formula still contains the placeholder version" >&2
  exit 1
fi
grep -c "download/${TAG}/" "$WORK/dk.rb" | grep -qx 4 || {
  echo "expected 4 release URLs pinned to ${TAG}" >&2; exit 1; }

if [ -z "${TAP_TOKEN:-}" ]; then
  echo "TAP_TOKEN unset; printing the formula instead of pushing it" >&2
  cat "$WORK/dk.rb"
  exit 0
fi

git clone --depth 1 "https://x-access-token:${TAP_TOKEN}@github.com/${TAP_REPO}.git" "$WORK/tap"
mkdir -p "$WORK/tap/Formula"
cp "$WORK/dk.rb" "$WORK/tap/Formula/dk.rb"

cd "$WORK/tap"
if git diff --quiet; then
  echo "tap formula already current for ${TAG}"
  exit 0
fi

git config user.name "dk release"
git config user.email "noreply@github.com"
git add Formula/dk.rb
git commit -m "dk ${TAG}"

# A PR, not a push to main. The tap protects main behind required status
# checks, so a direct push is rejected outright, and it should be: the tap's
# CI is what proves a formula actually installs on a real macOS runner before
# users get it. Bypassing that would make the checks decorative.
BRANCH="dk-${TAG}"
git branch -m "$BRANCH"
git push -u origin "$BRANCH" --force

if command -v gh >/dev/null 2>&1; then
  GH_TOKEN="$TAP_TOKEN" gh pr create --repo "$TAP_REPO" \
    --head "$BRANCH" --base main \
    --title "dk ${TAG}" \
    --body "Automated formula bump for [dk ${TAG}](https://github.com/${REPO}/releases/tag/${TAG}).

Checksums come from the published release assets. Source of truth is
\`Formula/dk.rb\` in ${REPO}; edits made directly here are overwritten by the
next release." 2>&1 || echo "branch pushed; open a PR manually"
else
  echo "branch $BRANCH pushed; open a PR against $TAP_REPO to merge it"
fi
