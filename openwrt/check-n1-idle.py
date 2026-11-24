#!/usr/bin/env python3
"""Read-only call safety check on the spare AT interface; never prints numbers."""
import glob
import os
from pathlib import Path
import select
import sys
import termios
import time


def find_qdc507_card_glob():
    for card_dir in Path('/sys/class/sound').glob('card*'):
        id_file = card_dir / 'id'
        if id_file.is_file() and id_file.read_text(errors='ignore').strip() == 'EG25GQDC507':
            return f'/proc/asound/{card_dir.name}/pcm*/sub*/status'
    return '/proc/asound/card*/pcm*/sub*/status'


def find_spare_at_tty():
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
            if ifn.is_file() and ifn.read_text(errors='ignore').strip() == '03':
                for tty in intf.glob('ttyUSB*'):
                    cand = '/dev/' + tty.name
                    if os.path.exists(cand):
                        return cand
    return '/dev/ttyUSB3'


for path in glob.glob(find_qdc507_card_glob()):
    with open(path) as handle:
        status = handle.read()
    if any(state in status for state in ('RUNNING', 'PREPARED', 'DRAINING')):
        sys.exit('ABORT: host audio is active')

fd = os.open(find_spare_at_tty(), os.O_RDWR | os.O_NOCTTY | os.O_NONBLOCK)
try:
    attrs = termios.tcgetattr(fd)
    attrs[0] = 0
    attrs[1] = 0
    attrs[2] = termios.CLOCAL | termios.CREAD | termios.CS8
    attrs[3] = 0
    attrs[4] = attrs[5] = termios.B115200
    attrs[6][termios.VMIN] = 0
    attrs[6][termios.VTIME] = 0
    termios.tcsetattr(fd, termios.TCSANOW, attrs)
    termios.tcflush(fd, termios.TCIFLUSH)
    os.write(fd, b'AT+CLCC\r')
    result = b''
    deadline = time.monotonic() + 4
    while time.monotonic() < deadline:
        if select.select([fd], [], [], 0.2)[0]:
            result += os.read(fd, 8192)
            if b'\r\nOK\r\n' in result or b'ERROR' in result:
                break
    if b'\r\nOK\r\n' not in result:
        sys.exit('ABORT: could not confirm modem idle')
    voice = data = 0
    for line in result.decode(errors='replace').splitlines():
        if line.strip() == 'RING':
            sys.exit('ABORT: incoming call')
        if not line.startswith('+CLCC:'):
            continue
        fields = [part.strip() for part in line.split(':', 1)[1].split(',')]
        if len(fields) < 5:
            sys.exit('ABORT: malformed modem status')
        if fields[3] == '0' and fields[2] in ('0', '1', '2', '3', '4', '5'):
            voice += 1
        elif fields[3] != '0':
            data += 1
    if voice:
        sys.exit('ABORT: cellular voice call exists')
    print('IDLE: no voice call; data contexts=%d' % data)
finally:
    os.close(fd)
