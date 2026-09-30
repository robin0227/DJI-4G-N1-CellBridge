#!/bin/sh
# Isolated OpenWrt staging runner. It writes only below its own directory,
# does not install an init service, and does not alter the firewall.
set -eu

DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
RUN="$DIR/run"
DATA="$DIR/data"
LOG="$DIR/logs"
SECRETS="$DIR/cellbridge.secrets.env"

umask 077
mkdir -p "$RUN" "$DATA" "$LOG"

if [ ! -r "$SECRETS" ]; then
	echo "missing $SECRETS" >&2
	exit 1
fi
. "$SECRETS"
: "${SIP_USER:?missing SIP_USER}"
: "${SIP_PASS:?missing SIP_PASS}"
: "${SIP_USER2:?missing SIP_USER2}"
: "${SIP_PASS2:?missing SIP_PASS2}"

export PATH="$DIR/module-tools:/usr/sbin:/usr/bin:/sbin:/bin"
export PYTHONPATH="$DIR/vendor:$DIR/module-tools"
export PYTHONDONTWRITEBYTECODE=1
export CELLBRIDGE_ROUTE_HELPER="$DIR/route-helper.sh"
export CELLBRIDGE_SMS_DRY_RUN=false
export CELLBRIDGE_SMS_POLL_INTERVAL=15s

# The custom OpenWrt image has the correct option.ko and USB ID alias, but it
# does not autoload that module. This is runtime-only; no modules.d file is
# written by the staging runner.
modprobe option

wait_for_devices() {
	i=0
	while [ "$i" -lt 60 ]; do
		if [ -c /dev/ttyUSB2 ] && [ -c /dev/snd/pcmC0D0c ] && [ -c /dev/snd/pcmC0D0p ]; then
			return 0
		fi
		sleep 1
		i=$((i + 1))
	done
	return 1
}

wait_for_devices || {
	echo "QDC507 serial/audio devices did not become ready" >&2
	exit 1
}
command -v arecord >/dev/null
command -v aplay >/dev/null
python3 -c 'import usb.core; assert usb.core.find(idVendor=0x2c7c, idProduct=0x0125) is not None'

# Provision the checksum-pinned modem runtime and start a fresh MaVo session.
# The QDC507 may briefly re-enumerate while UAC is enabled, so wait once more.
# Bound a stuck USB/helper operation so procd can recover instead of hanging.
timeout 90 "$DIR/route-helper.sh" start
wait_for_devices || {
	echo "QDC507 did not re-enumerate after voice route setup" >&2
	exit 1
}

# JSON is valid YAML and safely represents arbitrary generated SIP passwords.
SIP_USER="$SIP_USER" SIP_PASS="$SIP_PASS" SIP_USER2="$SIP_USER2" SIP_PASS2="$SIP_PASS2" \
python3 - "$RUN/config.yaml" "$DATA" <<'PY'
import json
import os
import sys

path, data_dir = sys.argv[1:]
config = {
    "network": {"mode": "tailnet", "transport": "tailnet", "public_fallback": False},
    "server": {"listen": "127.0.0.1:8787"},
    "data": {"dir": data_dir},
    "modem": {"adapter": "at", "tty": "/dev/ttyUSB2", "baud": 115200},
    "voice": {
        "enabled": True,
        "backend": "alsa",
        "codec": "pcmu",
        "sample_rate": 8000,
        "rx_path": "hw:CARD=EG25GQDC507,DEV=0",
        "tx_path": "hw:CARD=EG25GQDC507,DEV=0",
    },
    "sip": {
        "enabled": True,
        "listen": "0.0.0.0:5060",
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
with open(path, "w", encoding="utf-8") as handle:
    json.dump(config, handle, ensure_ascii=False, indent=2)
    handle.write("\n")
PY
chmod 600 "$RUN/config.yaml"

echo "$$" > "$RUN/gateway.pid"
exec "$DIR/cellbridge-gateway" -config "$RUN/config.yaml" -version n1-20260930 -log-level info
