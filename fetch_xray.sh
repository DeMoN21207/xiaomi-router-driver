#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
VERSION=v26.11.9
SHA256=64ed6858982a7f5eb85d88b811089063bd4f17561f4c8f44e0f1dc8e6f6430be
DEST="$ROOT_DIR/.tools/xray/$VERSION/linux"
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT
curl --fail --location --retry 2 --output "$TEMP_DIR/xray.zip" \
  "https://github.com/XTLS/Xray-core/releases/download/$VERSION/Xray-linux-arm64-v8a.zip"
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "$TEMP_DIR/xray.zip" | awk '{print $1}')"
else
  ACTUAL="$(shasum -a 256 "$TEMP_DIR/xray.zip" | awk '{print $1}')"
fi
[[ "$ACTUAL" == "$SHA256" ]] || { echo 'Xray checksum mismatch' >&2; exit 1; }
unzip -q "$TEMP_DIR/xray.zip" xray LICENSE -d "$TEMP_DIR"
mkdir -p "$DEST"
cp "$TEMP_DIR/xray" "$TEMP_DIR/LICENSE" "$DEST/"
chmod 755 "$DEST/xray"
chmod 644 "$DEST/LICENSE"
echo "Verified Xray $VERSION: $DEST/xray"
