#!/bin/sh
# The worker reports to OpenWrt's bounded system log.
exec /usr/bin/python3 -B /mnt/mmcblk2p4/cellbridge/storage-maintenance.py "$@"
