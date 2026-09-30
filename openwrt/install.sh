#!/bin/sh
# Fresh-install staging only. No service restart, UCI, firewall, cron or opkg writes.
set -eu
SOURCE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TARGET=/mnt/mmcblk2p4/cellbridge
BINARY=
SECRETS=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --binary) [ "$#" -ge 2 ]; BINARY=$2; shift 2 ;;
        --secrets) [ "$#" -ge 2 ]; SECRETS=$2; shift 2 ;;
        *) echo "usage: $0 --binary <linux-arm64-binary> --secrets <private-env-file>" >&2; exit 2 ;;
    esac
done
[ "$(id -u)" = 0 ] || { echo 'Root is required on N1' >&2; exit 1; }
[ "$(uname -m)" = aarch64 ] || { echo 'This package targets aarch64 N1 only' >&2; exit 1; }
[ -f "$BINARY" ] && [ -f "$SECRETS" ] || { echo 'Supply binary and private credentials' >&2; exit 1; }
[ ! -e "$TARGET" ] && [ ! -L "$TARGET" ] || { echo 'Existing target: use a reviewed upgrade/rollback procedure' >&2; exit 1; }
[ ! -e /etc/init.d/cellbridge ] || { echo 'Existing service: refusing a fresh install' >&2; exit 1; }
for command in python3 arecord aplay curl timeout sha256sum logger modprobe; do
    command -v "$command" >/dev/null || { echo "Missing user-space dependency: $command" >&2; exit 1; }
done
PYTHONPATH="$SOURCE/vendor:$SOURCE/module-tools" python3 - "$SOURCE" "$BINARY" "$SECRETS" <<'PY'
import os
from pathlib import Path
import re
import shlex
import sqlite3
import sys
import shutil
import usb.core
import voice_runtime

source, binary, secrets = map(Path, sys.argv[1:])
parent = Path('/mnt/mmcblk2p4')
if not os.path.ismount(parent) or parent.is_symlink():
    sys.exit('Data partition must be mounted, not a symlink')
if shutil.disk_usage(parent).free < 512 * 1024 * 1024:
    sys.exit('Need at least 512 MiB free on the data partition')
with binary.open('rb') as stream:
    header = stream.read(20)
if header[:6] != b'\x7fELF\x02\x01' or int.from_bytes(header[18:20], 'little') != 183:
    sys.exit('Not a little-endian AArch64 ELF binary')
if secrets.is_symlink() or (secrets.stat().st_mode & 0o077):
    sys.exit('Credential file must be regular, private (chmod 600), and not a symlink')
values = {}
for line in secrets.read_text().splitlines():
    if not line.strip() or line.lstrip().startswith('#'):
        continue
    if '=' not in line:
        sys.exit('Unexpected credential format; use init-secrets.py')
    key, value = line.split('=', 1)
    parts = shlex.split(value)
    if key not in ('SIP_USER', 'SIP_PASS', 'SIP_USER2', 'SIP_PASS2') or key in values or len(parts) != 1:
        sys.exit('Unexpected credential format; use init-secrets.py')
    if value != parts[0] or not re.fullmatch(r'[A-Za-z0-9_.-]+', value):
        sys.exit('Unsafe shell value; use init-secrets.py')
    values[key] = parts[0]
if set(values) != {'SIP_USER', 'SIP_PASS', 'SIP_USER2', 'SIP_PASS2'}:
    sys.exit('Missing credentials')
for key in ('SIP_PASS', 'SIP_PASS2'):
    if len(values[key]) < 24 or 'REPLACE_' in values[key]:
        sys.exit('Use long generated passwords, not placeholders')
if values['SIP_USER'] == values['SIP_USER2'] or values['SIP_PASS'] == values['SIP_PASS2']:
    sys.exit('Accounts and passwords must be distinct')
ready, reason = voice_runtime.runtime_installed()
if not ready:
    sys.exit('User-provided module runtime required: ' + reason)
if not os.path.exists('/dev/ttyUSB2') or not os.path.exists('/dev/snd/pcmC0D0c'):
    sys.exit('Verify the primary AT and UAC nodes before installation')
if usb.core.find(idVendor=0x2c7c, idProduct=0x0125) is None:
    sys.exit('Expected QDC507 USB identity not found')
PY
umask 077
STAGE=$(mktemp -d /mnt/mmcblk2p4/.cellbridge-new.XXXXXX)
cp -R "$SOURCE/." "$STAGE/"
cp "$BINARY" "$STAGE/cellbridge-gateway"
cp "$SECRETS" "$STAGE/cellbridge.secrets.env"
mkdir -p "$STAGE/run" "$STAGE/data" "$STAGE/logs"
chmod 700 "$STAGE/cellbridge-gateway" "$STAGE/run-staging.sh" "$STAGE/route-helper.sh" "$STAGE/module-tools/adb" "$STAGE/doctor.sh" "$STAGE/cellbridge-maintenance.sh"
chmod 600 "$STAGE/cellbridge.secrets.env"
mv "$STAGE" "$TARGET"
echo "Fresh files installed at $TARGET. Service not installed or started."
echo 'Review docs/INSTALL.md before manually enabling the procd service.'
