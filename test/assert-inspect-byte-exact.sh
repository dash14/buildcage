#!/bin/bash
set -euo pipefail

# Builds the same Dockerfile under universal and inspect with a shared
# SOURCE_DATE_EPOCH, and checks the resulting layers are byte-identical:
# path, type, mode, owner, size, symlink targets, content, and mtime.

IMAGE_A="${1:?usage: $0 IMAGE_A IMAGE_B}"
IMAGE_B="${2:?usage: $0 IMAGE_A IMAGE_B}"
FAILURES=0

pass() { echo "  PASS  $1"; }
fail() {
  echo "  FAIL  $1"
  FAILURES=$((FAILURES + 1))
}

echo ""
echo "=== Byte-Exact Layer Comparison ($IMAGE_A vs $IMAGE_B) ==="
echo ""

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# Ordered blob list from a `docker save` tarball, without depending on jq.
layers_of() {
  local image="$1" outdir="$2"
  mkdir -p "$outdir"
  docker save "$image" -o "$outdir/image.tar"
  tar -xf "$outdir/image.tar" -C "$outdir"
  grep -o '"Layers":\[[^]]*\]' "$outdir/manifest.json" \
    | grep -o '"blobs/sha256/[a-f0-9]*"' | tr -d '"'
}

# path/type/mode/owner/size/symlink-target/mtime in one line each, from the
# same tar binary for both images in this run, so the two sides are always
# comparable even though bsdtar and GNU tar format columns differently.
listing_of() {
  tar -tvf "$1" 2>/dev/null | sort
}

# apt/dpkg/ldconfig write real wall-clock content into these regardless of
# SOURCE_DATE_EPOCH, which only pins layer timestamps, not file content.
NONDETERMINISTIC_PATHS='^(var/log/apt/[^ ]*|var/log/dpkg\.log|var/cache/ldconfig/aux-cache) '

# tar -tv has no content hash, only size, so regular files are separately
# extracted and hashed.
content_hashes_of() {
  local layer="$1" dir="$2"
  mkdir -p "$dir"
  tar -xf "$layer" -C "$dir" 2>/dev/null || true
  find "$dir" -type f 2>/dev/null | while IFS= read -r f; do
    printf '%s %s\n' "${f#"$dir"/}" "$(sha256_of "$f")"
  done | grep -vE "$NONDETERMINISTIC_PATHS" | sort
}

DIR_A="$WORKDIR/a"
DIR_B="$WORKDIR/b"

LAYERS_A=()
while IFS= read -r layer; do LAYERS_A+=("$layer"); done < <(layers_of "$IMAGE_A" "$DIR_A")
LAYERS_B=()
while IFS= read -r layer; do LAYERS_B+=("$layer"); done < <(layers_of "$IMAGE_B" "$DIR_B")

if [ "${#LAYERS_A[@]}" -ne "${#LAYERS_B[@]}" ]; then
  fail "layer count differs: $IMAGE_A has ${#LAYERS_A[@]}, $IMAGE_B has ${#LAYERS_B[@]}"
else
  pass "both images have ${#LAYERS_A[@]} layers"
fi

count=${#LAYERS_A[@]}
if [ "${#LAYERS_B[@]}" -lt "$count" ]; then
  count=${#LAYERS_B[@]}
fi

i=0
while [ "$i" -lt "$count" ]; do
  layer_a="$DIR_A/${LAYERS_A[$i]}"
  layer_b="$DIR_B/${LAYERS_B[$i]}"

  if diff -q <(listing_of "$layer_a") <(listing_of "$layer_b") >/dev/null; then
    pass "layer $i: identical path/type/mode/owner/size/mtime listing"
  else
    fail "layer $i: listing differs"
    diff <(listing_of "$layer_a") <(listing_of "$layer_b") || true
  fi

  extract_a="$WORKDIR/extract-a-$i"
  extract_b="$WORKDIR/extract-b-$i"
  if diff -q <(content_hashes_of "$layer_a" "$extract_a") <(content_hashes_of "$layer_b" "$extract_b") >/dev/null; then
    pass "layer $i: identical file content"
  else
    fail "layer $i: file content differs"
    diff <(content_hashes_of "$layer_a" "$extract_a") <(content_hashes_of "$layer_b" "$extract_b") || true
  fi

  i=$((i + 1))
done

echo ""
if [ "$FAILURES" -gt 0 ]; then
  echo "❌ FAILED: $FAILURES assertion(s) failed"
  exit 1
fi
echo "✅ All assertions passed."
echo ""
