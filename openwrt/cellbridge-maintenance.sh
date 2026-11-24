#!/bin/sh
# The worker reports to OpenWrt's bounded system log.
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec /usr/bin/python3 -B "$DIR/storage-maintenance.py" "$@"
