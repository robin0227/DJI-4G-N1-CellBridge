#!/bin/sh
# Isolated OpenWrt/iStoreOS ARM runner for CellBridge (NanoPi R76S, N1, and other ARM devices).
# Writes runtime config to tmpfs (/tmp/cellbridge), keeps persistent data under $DIR/data,
# does not modify firewall or UCI.
set -eu

DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
RUN="$DIR/run"
DATA="$DIR/data"
LOG="$DIR/logs"
TMP_RUN="/tmp/cellbridge"
SECRETS="$DIR/cellbridge.secrets.env"
ENV_OVERRIDE="$DIR/cellbridge.env"

umask 077
mkdir -p "$RUN" "$DATA" "$LOG" "$TMP_RUN"
chmod 700 "$TMP_RUN"

if [ ! -r "$SECRETS" ]; then
	echo "missing $SECRETS" >&2
	exit 1
fi
. "$SECRETS"
: "${SIP_USER:?missing SIP_USER}"
: "${SIP_PASS:?missing SIP_PASS}"
: "${SIP_USER2:?missing SIP_USER2}"
: "${SIP_PASS2:?missing SIP_PASS2}"

# Optional local overrides (chmod 600, non-symlink):
# CELLBRIDGE_SMS_POLL_INTERVAL, CELLBRIDGE_SMS_DRY_RUN, CELLBRIDGE_LOG_LEVEL,
# CELLBRIDGE_SIP_LISTEN, CELLBRIDGE_MODEM_TTY, CELLBRIDGE_ALSA_CARD,
# CB_LINPHONE_PUSH_KEY, CB_LINPHONE_PUSH_URL, CB_LINPHONE_FROM
if [ -f "$ENV_OVERRIDE" ] && [ ! -L "$ENV_OVERRIDE" ]; then
	. "$ENV_OVERRIDE"
fi

export PATH="$DIR/module-tools:/usr/sbin:/usr/bin:/sbin:/bin"
export PYTHONPATH="$DIR/vendor:$DIR/module-tools"
export PYTHONDONTWRITEBYTECODE=1
export CELLBRIDGE_ROUTE_HELPER="$DIR/route-helper.sh"
export CELLBRIDGE_SMS_DRY_RUN="${CELLBRIDGE_SMS_DRY_RUN:-false}"
export CELLBRIDGE_SMS_POLL_INTERVAL="${CELLBRIDGE_SMS_POLL_INTERVAL:-15s}"

ALSA_CARD_ID="${CELLBRIDGE_ALSA_CARD:-EG25GQDC507}"
SIP_LISTEN="${CELLBRIDGE_SIP_LISTEN:-0.0.0.0:5060}"
LOG_LEVEL="${CELLBRIDGE_LOG_LEVEL:-info}"

# Load option driver if not already built-in or loaded.
if [ ! -d /sys/bus/usb-serial/drivers/option1 ]; then
	modprobe option 2>/dev/null || true
fi

find_qdc507_usb() {
	for d in /sys/bus/usb/devices/*; do
		[ -r "$d/idVendor" ] && [ -r "$d/idProduct" ] || continue
		vid=$(cat "$d/idVendor" 2>/dev/null || true)
		pid=$(cat "$d/idProduct" 2>/dev/null || true)
		if [ "$vid:$pid" = "2c7c:0125" ] || [ "$vid:$pid" = "2ca3:4006" ]; then
			echo "$d"
			return 0
		fi
	done
	return 1
}

find_qdc507_tty() {
	if [ -n "${CELLBRIDGE_MODEM_TTY:-}" ]; then
		[ -c "$CELLBRIDGE_MODEM_TTY" ] && { echo "$CELLBRIDGE_MODEM_TTY"; return 0; }
		return 1
	fi
	usb_dev=$(find_qdc507_usb 2>/dev/null || true)
	if [ -n "$usb_dev" ]; then
		for intf in "$usb_dev":*; do
			[ -r "$intf/bInterfaceNumber" ] || continue
			if [ "$(cat "$intf/bInterfaceNumber" 2>/dev/null || true)" = "02" ]; then
				for tty_dir in "$intf"/ttyUSB*; do
					[ -d "$tty_dir" ] || continue
					tty_name=$(basename "$tty_dir")
					if [ -c "/dev/$tty_name" ]; then
						echo "/dev/$tty_name"
						return 0
					fi
				done
			fi
		done
	fi
	if [ -c /dev/ttyUSB2 ]; then
		echo "/dev/ttyUSB2"
		return 0
	fi
	return 1
}

find_alsa_card_num() {
	for card_dir in /sys/class/sound/card*; do
		[ -r "$card_dir/id" ] || continue
		if [ "$(cat "$card_dir/id" 2>/dev/null || true)" = "$ALSA_CARD_ID" ]; then
			basename "$card_dir" | sed 's/^card//'
			return 0
		fi
	done
	return 1
}

disable_usb_autosuspend() {
	usb_dev=$(find_qdc507_usb 2>/dev/null || true)
	if [ -n "$usb_dev" ] && [ -w "$usb_dev/power/control" ]; then
		echo on > "$usb_dev/power/control" 2>/dev/null || true
	fi
	if [ -n "$usb_dev" ] && [ -w "$usb_dev/power/autosuspend_delay_ms" ]; then
		echo -1 > "$usb_dev/power/autosuspend_delay_ms" 2>/dev/null || true
	fi
}

wait_for_devices() {
	i=0
	while [ "$i" -lt 60 ]; do
		MODEM_TTY=$(find_qdc507_tty 2>/dev/null || true)
		CARD_NUM=$(find_alsa_card_num 2>/dev/null || true)
		if [ -n "$MODEM_TTY" ] && [ -c "$MODEM_TTY" ] && \
		   [ -n "$CARD_NUM" ] && \
		   [ -c "/dev/snd/pcmC${CARD_NUM}D0c" ] && \
		   [ -c "/dev/snd/pcmC${CARD_NUM}D0p" ]; then
			return 0
		fi
		sleep 1
		i=$((i + 1))
	done
	return 1
}

MODEM_TTY=""
CARD_NUM=""
wait_for_devices || {
	echo "QDC507 serial/audio devices did not become ready" >&2
	exit 1
}
disable_usb_autosuspend
command -v arecord >/dev/null
command -v aplay >/dev/null
python3 -B -c 'import usb.core; assert (usb.core.find(idVendor=0x2c7c, idProduct=0x0125) or usb.core.find(idVendor=0x2ca3, idProduct=0x4006)) is not None'

# Provision/rotate the checksum-pinned modem runtime and start a fresh MaVo session.
# Bound a stuck USB/helper operation so procd can recover instead of hanging.
timeout 90 "$DIR/route-helper.sh" start
wait_for_devices || {
	echo "QDC507 did not re-enumerate after voice route setup" >&2
	exit 1
}
disable_usb_autosuspend

CONFIG_PATH="$TMP_RUN/config.yaml"

# JSON is valid YAML and safely represents arbitrary generated SIP passwords.
SIP_USER="$SIP_USER" SIP_PASS="$SIP_PASS" SIP_USER2="$SIP_USER2" SIP_PASS2="$SIP_PASS2" \
python3 -B - "$CONFIG_PATH" "$DATA" "$DIR/yak-push-token" "$MODEM_TTY" "$ALSA_CARD_ID" "$SIP_LISTEN" <<'PY'
import json
import os
from pathlib import Path
import re
import sys

path, data_dir, token_file, modem_tty, alsa_card, sip_listen = sys.argv[1:]
push_token = ""
token_path = Path(token_file)
if token_path.exists():
    if token_path.is_symlink() or token_path.stat().st_mode & 0o077:
        raise SystemExit("YakPhone token file must be private and not a symlink")
    push_token = token_path.read_text(encoding="ascii").strip()
    if not re.fullmatch(r"[0-9a-fA-F]{64}", push_token):
        raise SystemExit("YakPhone token must be 64 hexadecimal characters")
alsa_device = f"hw:CARD={alsa_card},DEV=0"
config = {
    "network": {"mode": "tailnet", "transport": "tailnet", "public_fallback": False},
    "server": {"listen": "127.0.0.1:8787"},
    "data": {"dir": data_dir},
    "modem": {"adapter": "at", "tty": modem_tty, "baud": 115200},
    "voice": {
        "enabled": True,
        "backend": "alsa",
        "codec": "pcmu",
        "sample_rate": 8000,
        "rx_path": alsa_device,
        "tx_path": alsa_device,
    },
    "sip": {
        "enabled": True,
        "listen": sip_listen,
        "realm": "cellbridge",
        "users": [
            {"username": os.environ["SIP_USER"], "password": os.environ["SIP_PASS"]},
            {"username": os.environ["SIP_USER2"], "password": os.environ["SIP_PASS2"]},
        ],
    },
    "recording": {
        "enabled": False,
        "retention_days": 0,
        "minimum_free_bytes": 536870912,
        "auto_mode": "off",
    },
    "security": {"pairing_local_only": True, "admin_local_only": True, "redact_logs": True},
}
if push_token:
    config["sip"]["push_token"] = push_token
tmp_path = path + ".tmp"
with open(tmp_path, "w", encoding="utf-8") as handle:
    json.dump(config, handle, ensure_ascii=False, indent=2)
    handle.write("\n")
os.chmod(tmp_path, 0o600)
os.replace(tmp_path, path)
PY
chmod 600 "$CONFIG_PATH"

echo "$$" > "$RUN/gateway.pid"
VERSION=$(tr -dc 'A-Za-z0-9._-' < "$DIR/VERSION" 2>/dev/null || true)
exec "$DIR/cellbridge-gateway" -config "$CONFIG_PATH" -version "${VERSION:-v1.2.0}" -log-level "$LOG_LEVEL"
