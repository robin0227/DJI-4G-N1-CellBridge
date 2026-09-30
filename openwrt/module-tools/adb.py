#!/usr/bin/env python3
"""PyUSB implementation behind the QDC507 adb-compatible shim."""

import os
import shlex
import stat
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import voice_runtime as voice  # noqa: E402


def usage(message=None):
    if message:
        print(message, file=sys.stderr)
    print("usage: adb shell <command...> | adb push <local> <remote>", file=sys.stderr)
    return 2


def main(argv):
    if len(argv) < 2:
        return usage()
    action = argv[1]
    if action == "shell":
        if len(argv) == 2:
            return usage("missing shell command")
        command = argv[2] if len(argv) == 3 else " ".join(shlex.quote(arg) for arg in argv[2:])
        timeout = 86400.0 if "voice-route-watchdog.sh" in command else 60.0
        client = voice.ADBClient()
        try:
            output, status = client.shell_checked(command, timeout)
            sys.stdout.write(output)
            return status
        finally:
            client.close()
    if action == "push":
        if len(argv) != 4:
            return usage("push needs a local and remote path")
        local_path, remote_path = argv[2:]
        with open(local_path, "rb") as handle:
            payload = handle.read()
        permissions = stat.S_IMODE(os.stat(local_path).st_mode)
        client = voice.ADBClient()
        try:
            client.push(payload, remote_path, stat.S_IFREG | permissions)
            return 0
        finally:
            client.close()
    return usage(f"unsupported adb command: {action}")


if __name__ == "__main__":
    try:
        raise SystemExit(main(sys.argv))
    except Exception as error:
        print(f"qdc507 adb error: {error}", file=sys.stderr)
        raise SystemExit(1)
