#!/bin/sh
# Build on a development machine, never on the router root partition.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST="$ROOT/dist"
mkdir -p "$DIST"
cd "$ROOT/gateway"
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' \
    -o "$DIST/cellbridge-gateway-linux-arm64" ./cmd/cellbridge-gateway
cd "$ROOT"
python3 tools/audit-release.py
python3 tools/collect-licenses.py --output "$DIST/licenses"
python3 - "$DIST" <<'PY'
import hashlib
from pathlib import Path
import sys
directory = Path(sys.argv[1])
binary = directory / 'cellbridge-gateway-linux-arm64'
digest = hashlib.sha256(binary.read_bytes()).hexdigest()
(directory / 'SHA256SUMS').write_text(digest + '  ' + binary.name + '\n')
print('ARM64 gateway SHA256: ' + digest)
PY
tar -czf "$DIST/cellbridge-n1-openwrt.tar.gz" \
    --exclude='__pycache__' --exclude='*.pyc' --exclude='*.env' \
    --exclude='voice-runtime' --exclude='openwrt/run' --exclude='openwrt/data' --exclude='openwrt/logs' \
    -C "$ROOT" openwrt LICENSE NOTICE docs README.md SECURITY.md \
    -C "$DIST" cellbridge-gateway-linux-arm64 SHA256SUMS licenses
echo "Build ready: $DIST/cellbridge-n1-openwrt.tar.gz"
