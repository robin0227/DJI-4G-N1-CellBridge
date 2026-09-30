#!/bin/sh
set -eu

DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export PYTHONPATH="$DIR/vendor:$DIR/module-tools"

case "${1:-start}" in
	start)
		python3 -c 'import voice_runtime; voice_runtime.ensure_voice_route(force=True)'
		;;
	status)
		exec "$DIR/module-tools/adb" shell 'pidof mavo-pcm-bridge.armv7 2>/dev/null || pidof mavo-pcm-bridge 2>/dev/null; tail -n 3 /run/mavo-voice-route.log 2>/dev/null'
		;;
	*)
		echo "usage: $0 start|status" >&2
		exit 2
		;;
esac
