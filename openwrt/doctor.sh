#!/bin/sh
# Read-only host diagnostics. Never dumps SIP headers, secrets, modem IDs or logs.
set -u
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
echo "architecture=$(uname -m) kernel=$(uname -r)"
echo "install_dir=$DIR"
df -h "$DIR"
for command in python3 arecord aplay curl timeout; do
    command -v "$command" >/dev/null && echo "$command: present" || echo "$command: MISSING"
done
PYTHONPATH="$DIR/vendor:$DIR/module-tools" python3 -c 'import sqlite3, usb.core; print("sqlite3/PyUSB: import OK")' || true
if [ -s "$DIR/run/gateway.pid" ]; then
    PID=$(tr -dc '0-9' < "$DIR/run/gateway.pid")
    if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
        echo "gateway pid=$PID"
        for descriptor in "/proc/$PID/fd/"*; do
            link=$(readlink "$descriptor" 2>/dev/null || true)
            case "$link" in
                /dev/ttyUSB*deleted*) echo 'WARNING: gateway holds a removed USB serial descriptor' ;;
                /dev/ttyUSB*) echo "serial descriptor=$link" ;;
            esac
        done
    else
        echo 'gateway PID is absent or stale'
    fi
else
    echo 'gateway PID file missing'
fi
curl -fsS --max-time 3 http://127.0.0.1:8787/api/v1/health || true
echo
if command -v tailscale >/dev/null; then
    echo 'Local Tailscale IPv4:'
    tailscale ip -4 || true
fi
echo 'Health OK is not a proof of current USB voice or lock-screen push readiness.'
