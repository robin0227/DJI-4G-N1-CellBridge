#!/bin/sh
# Fresh-install staging only for OpenWrt/iStoreOS ARM devices (e.g. NanoPi R76S, N1, R4S/R5S/R6S).
# No service restart, UCI, firewall, cron or opkg writes.
set -eu
SOURCE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
TARGET=
BINARY=
SECRETS=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --binary) [ "$#" -ge 2 ]; BINARY=$2; shift 2 ;;
        --secrets) [ "$#" -ge 2 ]; SECRETS=$2; shift 2 ;;
        --target) [ "$#" -ge 2 ]; TARGET=$2; shift 2 ;;
        *) echo "usage: $0 --binary <linux-arm64-or-arm-binary> --secrets <private-env-file> [--target <install-dir>]" >&2; exit 2 ;;
    esac
done
[ "$(id -u)" = 0 ] || { echo 'Root is required on OpenWrt' >&2; exit 1; }
ARCH=$(uname -m)
case "$ARCH" in
    aarch64|arm64|armv7l|armv7*) ;;
    *) echo "Unsupported architecture: $ARCH (expected Linux ARM64/aarch64 or ARMv7)" >&2; exit 1 ;;
esac
[ -f "$BINARY" ] && [ -f "$SECRETS" ] || { echo 'Supply binary and private credentials' >&2; exit 1; }
if [ -z "$TARGET" ]; then
    for parent in /mnt/data /mnt/mmcblk2p4; do
        if [ -d "$parent" ] && [ ! -L "$parent" ]; then
            TARGET="$parent/cellbridge"
            break
        fi
    done
fi
if [ -z "$TARGET" ]; then
    echo 'No default data partition (/mnt/data or /mnt/mmcblk2p4) found; pass --target <dir>/cellbridge' >&2
    exit 1
fi
[ ! -e "$TARGET" ] && [ ! -L "$TARGET" ] || { echo "Existing target $TARGET: use a reviewed upgrade/rollback procedure" >&2; exit 1; }
[ ! -e /etc/init.d/cellbridge ] || { echo 'Existing service: refusing a fresh install' >&2; exit 1; }
for command in python3 arecord aplay curl timeout sha256sum logger; do
    command -v "$command" >/dev/null || { echo "Missing user-space dependency: $command" >&2; exit 1; }
done
PYTHONPATH="$SOURCE/vendor:$SOURCE/module-tools" python3 - "$SOURCE" "$BINARY" "$SECRETS" "$TARGET" "$ARCH" <<'PY'
import os
from pathlib import Path
import re
import shlex
import shutil
import sqlite3
import sys
import usb.core
import voice_runtime

source, binary, secrets, target = map(Path, sys.argv[1:5])
arch = sys.argv[5]
parent = target.parent
if not parent.is_dir() or parent.is_symlink():
    sys.exit(f'Target parent directory must exist and not be a symlink: {parent}')
if str(parent).startswith('/mnt/') and not os.path.ismount(parent):
    sys.exit(f'Data partition under /mnt must be mounted: {parent}')
if shutil.disk_usage(parent).free < 512 * 1024 * 1024:
    sys.exit(f'Need at least 512 MiB free on {parent}')
with binary.open('rb') as stream:
    header = stream.read(20)
if arch in ('aarch64', 'arm64'):
    if header[:6] != b'\x7fELF\x02\x01' or int.from_bytes(header[18:20], 'little') != 183:
        sys.exit('Not a little-endian AArch64 ELF binary')
else:
    if header[:6] != b'\x7fELF\x01\x01' or int.from_bytes(header[18:20], 'little') != 40:
        sys.exit('Not a little-endian 32-bit ARM ELF binary')
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

usb_found = usb.core.find(idVendor=0x2c7c, idProduct=0x0125) or usb.core.find(idVendor=0x2ca3, idProduct=0x4006)
if usb_found is None:
    sys.exit('Expected QDC507 USB identity (2c7c:0125 or 2ca3:4006) not found')

tty_ok = False
for dev in Path('/sys/bus/usb/devices').glob('*'):
    vid_f, pid_f = dev / 'idVendor', dev / 'idProduct'
    if not (vid_f.is_file() and pid_f.is_file()):
        continue
    vid = vid_f.read_text(errors='ignore').strip()
    pid = pid_f.read_text(errors='ignore').strip()
    if (vid, pid) not in (('2c7c', '0125'), ('2ca3', '4006')):
        continue
    for intf in dev.parent.glob(dev.name + ':*'):
        ifn = intf / 'bInterfaceNumber'
        if ifn.is_file() and ifn.read_text(errors='ignore').strip() == '02':
            for tty in intf.glob('ttyUSB*'):
                if os.path.exists('/dev/' + tty.name):
                    tty_ok = True
                    break
if not tty_ok and os.path.exists('/dev/ttyUSB2'):
    tty_ok = True

alsa_ok = False
for card_dir in Path('/sys/class/sound').glob('card*'):
    id_file = card_dir / 'id'
    if id_file.is_file() and id_file.read_text(errors='ignore').strip() == 'EG25GQDC507':
        num = card_dir.name[4:]
        if os.path.exists(f'/dev/snd/pcmC{num}D0c') and os.path.exists(f'/dev/snd/pcmC{num}D0p'):
            alsa_ok = True
            break

if not tty_ok or not alsa_ok:
    sys.exit('Verify the QDC507 AT serial (/dev/ttyUSB*) and UAC ALSA (EG25GQDC507) nodes before installation')
PY
umask 077
PARENT=$(dirname -- "$TARGET")
STAGE=$(mktemp -d "$PARENT/.cellbridge-new.XXXXXX")
cp -R "$SOURCE/." "$STAGE/"
cp "$BINARY" "$STAGE/cellbridge-gateway"
cp "$SECRETS" "$STAGE/cellbridge.secrets.env"
mkdir -p "$STAGE/run" "$STAGE/data" "$STAGE/logs"
python3 - "$STAGE/openwrt-init" "$TARGET/run-staging.sh" <<'PY'
from pathlib import Path
import sys
init_path, prog_path = Path(sys.argv[1]), sys.argv[2]
text = init_path.read_text(encoding='utf-8')
lines = []
replaced = False
for line in text.splitlines():
    if line.startswith('PROG='):
        lines.append(f'PROG={prog_path}')
        replaced = True
    else:
        lines.append(line)
if replaced:
    init_path.write_text('\n'.join(lines) + '\n', encoding='utf-8')
PY
chmod 700 "$STAGE/cellbridge-gateway" "$STAGE/run-staging.sh" "$STAGE/route-helper.sh" "$STAGE/module-tools/adb" "$STAGE/doctor.sh" "$STAGE/cellbridge-maintenance.sh"
chmod 755 "$STAGE/openwrt-init"
chmod 600 "$STAGE/cellbridge.secrets.env"
mv "$STAGE" "$TARGET"
echo "Fresh files installed at $TARGET. Service not installed or started."
echo 'Review docs/INSTALL.md before manually enabling the procd service.'
