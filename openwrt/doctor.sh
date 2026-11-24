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
PYTHONPATH="$DIR/vendor:$DIR/module-tools" python3 -B -c 'import sqlite3, usb.core; print("sqlite3/PyUSB: import OK")' || true

# USB & ALSA auto-detection check
for d in /sys/bus/usb/devices/*; do
    [ -r "$d/idVendor" ] && [ -r "$d/idProduct" ] || continue
    vid=$(cat "$d/idVendor" 2>/dev/null || true)
    pid=$(cat "$d/idProduct" 2>/dev/null || true)
    if [ "$vid:$pid" = "2c7c:0125" ] || [ "$vid:$pid" = "2ca3:4006" ]; then
        echo "modem usb=$d id=$vid:$pid power=$(cat "$d/power/control" 2>/dev/null || echo unknown) autosuspend_ms=$(cat "$d/power/autosuspend_delay_ms" 2>/dev/null || echo unknown)"
    fi
done
for c in /sys/class/sound/card*; do
    [ -r "$c/id" ] || continue
    cid=$(cat "$c/id" 2>/dev/null || true)
    if [ "$cid" = "EG25GQDC507" ]; then
        echo "alsa card=$(basename "$c") id=$cid"
    fi
done

if [ -s "$DIR/run/gateway.pid" ]; then
    PID=$(tr -dc '0-9' < "$DIR/run/gateway.pid")
    if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
        NICE=$(awk '{print $19}' "/proc/$PID/stat" 2>/dev/null || echo unknown)
        RSS=$(awk '/^VmRSS:/ {print $2, $3}' "/proc/$PID/status" 2>/dev/null || echo unknown)
        echo "gateway pid=$PID nice=$NICE rss=$RSS"
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
if [ -x "$DIR/route-helper.sh" ]; then
    ROUTE_PID=$("$DIR/route-helper.sh" status 2>/dev/null | head -n1 | tr -dc '0-9' || true)
    if [ -n "$ROUTE_PID" ]; then
        echo "module voice route: active (mavo pid=$ROUTE_PID)"
    else
        echo "module voice route: NOT detected"
    fi
fi
if command -v logread >/dev/null; then
    echo "USB serial loss restarts in system log: $(logread 2>/dev/null | grep -c 'modem serial port lost')"
    LAST_TEMP=$(logread 2>/dev/null | grep 'modem temperature' | tail -n1 | sed -E 's/.*temp_c=([^ ]+).*/\1/' || true)
    [ -n "$LAST_TEMP" ] && echo "latest modem temperature: ${LAST_TEMP} C"
fi
echo 'Health OK is not a proof of current USB voice or lock-screen push readiness.'
