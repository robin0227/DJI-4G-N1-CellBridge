#!/bin/sh
# Build on a development machine, never on the router root partition.
# Defaults to Linux ARM64 (aarch64). For 32-bit ARMv7 OpenWrt devices:
#   GOARCH=arm GOARM=7 sh tools/build.sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST="$ROOT/dist"
TARGET_ARCH="${GOARCH:-arm64}"
TARGET_GOARM="${GOARM:-}"
BIN_NAME="cellbridge-gateway-linux-${TARGET_ARCH}"
TAR_NAME="cellbridge-openwrt-${TARGET_ARCH}.tar.gz"
mkdir -p "$DIST"
cd "$ROOT/gateway"
if [ "$TARGET_ARCH" = "arm" ] && [ -n "$TARGET_GOARM" ]; then
    GOOS=linux GOARCH="$TARGET_ARCH" GOARM="$TARGET_GOARM" CGO_ENABLED=0 \
        go build -buildvcs=false -trimpath -ldflags='-s -w' \
        -o "$DIST/$BIN_NAME" ./cmd/cellbridge-gateway
else
    GOOS=linux GOARCH="$TARGET_ARCH" CGO_ENABLED=0 \
        go build -buildvcs=false -trimpath -ldflags='-s -w' \
        -o "$DIST/$BIN_NAME" ./cmd/cellbridge-gateway
fi
cd "$ROOT"
python3 tools/audit-release.py
python3 tools/collect-licenses.py --output "$DIST/licenses"
python3 - "$DIST" "$BIN_NAME" <<'PY'
import hashlib
from pathlib import Path
import sys
directory = Path(sys.argv[1])
binary = directory / sys.argv[2]
digest = hashlib.sha256(binary.read_bytes()).hexdigest()
(directory / 'SHA256SUMS').write_text(digest + '  ' + binary.name + '\n')
print(f'{binary.name} SHA256: {digest}')
PY
tar -czf "$DIST/$TAR_NAME" \
    --exclude='__pycache__' --exclude='*.pyc' --exclude='*.env' \
    --exclude='voice-runtime' --exclude='openwrt/run' --exclude='openwrt/data' --exclude='openwrt/logs' \
    -C "$ROOT" openwrt LICENSE NOTICE docs README.md SECURITY.md CHANGELOG.md \
    -C "$DIST" "$BIN_NAME" SHA256SUMS licenses
echo "Build ready: $DIST/$TAR_NAME"
