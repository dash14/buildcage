#!/bin/bash
set -euo pipefail

# Checks that each CA bundle candidate appears in at most one image layer:
# it should only ever be introduced once, by whichever RUN step installs it.
# Appearing in more than one layer means an unrelated step is still
# rewriting the file, bloating the image without changing its content.
#
# CANDIDATES mirrors the system CA store paths inspect looks for.

IMAGE="${1:-buildcage-test}"
FAILURES=0

CANDIDATES=(
  "etc/ssl/certs/ca-certificates.crt"
  "etc/pki/tls/certs/ca-bundle.crt"
  "etc/ssl/ca-bundle.pem"
  "etc/pki/tls/cacert.pem"
  "etc/ssl/cert.pem"
)

pass() { echo "  PASS  $1"; }
fail() {
  echo "  FAIL  $1"
  FAILURES=$((FAILURES + 1))
}

echo ""
echo "=== No Per-Layer CA Bundle Duplication ($IMAGE) ==="
echo ""

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

docker save "$IMAGE" -o "$WORKDIR/image.tar"
tar -xf "$WORKDIR/image.tar" -C "$WORKDIR"

# Pull the ordered blob list out of manifest.json without jq or mapfile,
# since neither is guaranteed to be available (macOS ships bash 3.2).
LAYERS=()
while IFS= read -r layer; do
  LAYERS+=("$layer")
done < <(grep -o '"Layers":\[[^]]*\]' "$WORKDIR/manifest.json" \
  | grep -o '"blobs/sha256/[a-f0-9]*"' | tr -d '"')

if [ "${#LAYERS[@]}" -eq 0 ]; then
  fail "could not read the layer list from manifest.json"
fi

for candidate in "${CANDIDATES[@]}"; do
  count=0
  for layer in "${LAYERS[@]}"; do
    # grep -c, not -q: -q can exit before tar/sed finish writing, which
    # under pipefail can drop the match via their resulting SIGPIPE.
    entries=$(tar -tf "$WORKDIR/$layer" 2>/dev/null | sed 's#^\./##' | grep -cx "$candidate" || true)
    if [ "${entries:-0}" -gt 0 ]; then
      count=$((count + 1))
    fi
  done
  if [ "$count" -gt 1 ]; then
    fail "$candidate appears in $count layers (expected at most 1)"
  elif [ "$count" -eq 1 ]; then
    pass "$candidate appears in exactly 1 layer"
  fi
done

echo ""
if [ "$FAILURES" -gt 0 ]; then
  echo "❌ FAILED: $FAILURES assertion(s) failed"
  exit 1
fi
echo "✅ All assertions passed."
echo ""
