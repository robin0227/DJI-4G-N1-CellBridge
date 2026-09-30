#!/usr/bin/env python3
"""Non-destructive online storage maintenance for this N1 installation."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import tempfile
import time

BASE = Path('/mnt/mmcblk2p4/cellbridge')
LOG_LIMIT = 256 * 1024
LOG_KEEP = 128 * 1024
MIN_FREE = 512 * 1024 * 1024
DB_WARNING = 64 * 1024 * 1024


def atomic_write(path, data):
    fd, temporary = tempfile.mkstemp(prefix='.' + path.name + '.', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def in_use(path):
    """Do not rotate a log whose inode is still open by any process."""
    expected = path.stat()
    for descriptor in Path('/proc').glob('[0-9]*/fd/*'):
        try:
            current = descriptor.stat()
            if (current.st_dev, current.st_ino) == (expected.st_dev, expected.st_ino):
                return True
        except FileNotFoundError:
            continue
        except PermissionError:
            return True
    return False


def maintain(base, apply=False):
    database = base / 'data/cellbridge.sqlite'
    for path in (base, base / 'data', base / 'logs', base / 'run', database):
        if path.is_symlink() or not path.exists():
            raise RuntimeError('missing or symlinked maintenance target: ' + str(path))
    report = {'time': time.strftime('%Y-%m-%d %H:%M:%S %z'),
              'mode': 'apply' if apply else 'check', 'warnings': [],
              'database_rows_deleted': 0}
    uri = database.as_uri() + ('?mode=rw' if apply else '?mode=ro')
    connection = sqlite3.connect(uri, uri=True, timeout=0, isolation_level=None)
    try:
        # PASSIVE does not wait for readers or writers and leaves busy frames
        # for a later run. Never remove WAL/SHM manually, delete rows, VACUUM,
        # or use blocking FULL/RESTART/TRUNCATE checkpoints on this live DB.
        report['journal_mode'] = connection.execute('PRAGMA journal_mode').fetchone()[0]
        if apply and report['journal_mode'] == 'wal':
            report['checkpoint'] = list(connection.execute('PRAGMA wal_checkpoint(PASSIVE)').fetchone())
            busy, frames, completed = report['checkpoint']
            if busy or completed < frames:
                report['warnings'].append('WAL busy; pending frames left for later maintenance')
        report['database_pages'] = connection.execute('PRAGMA page_count').fetchone()[0]
        report['reusable_pages'] = connection.execute('PRAGMA freelist_count').fetchone()[0]
    finally:
        connection.close()

    logfile = base / 'logs/launcher.log'
    report['launcher_log_trimmed'] = False
    if logfile.is_symlink():
        raise RuntimeError('refusing symlinked launcher log')
    if logfile.is_file() and logfile.stat().st_size > LOG_LIMIT:
        if in_use(logfile):
            report['warnings'].append('launcher log above 256 KiB but in use; left intact')
        elif apply:
            # Only historical diagnostic lines are discarded. The recent
            # 128 KiB is kept; no growing rotated archive is created.
            with logfile.open('rb') as stream:
                stream.seek(-LOG_KEEP, os.SEEK_END)
                recent = stream.read(LOG_KEEP)
            atomic_write(logfile, recent)
            report['launcher_log_trimmed'] = True

    report['database_files_bytes'] = {
        path.name: path.stat().st_size for path in
        (database, Path(str(database) + '-wal'), Path(str(database) + '-shm'))
        if path.exists()
    }
    report['free_bytes'] = shutil.disk_usage(base).free
    report['launcher_log_bytes'] = logfile.stat().st_size if logfile.exists() else 0
    if report['free_bytes'] < MIN_FREE:
        report['warnings'].append('data disk free space below 512 MiB')
    if sum(report['database_files_bytes'].values()) > DB_WARNING:
        report['warnings'].append('database including WAL exceeds 64 MiB; inspect growth before deleting history')
    if apply:
        # Fixed filename replaces the previous report, so reports cannot grow.
        atomic_write(base / 'run/storage-status.json',
                     (json.dumps(report, ensure_ascii=False, indent=2) + '\n').encode())
    return report


def log(message, warning=False):
    subprocess.run(['/usr/bin/logger', '-p', 'daemon.warning' if warning else 'daemon.info',
                    '-t', 'cellbridge-maintenance', message], timeout=5, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='inspect only; do not checkpoint or trim logs')
    args = parser.parse_args()
    os.umask(0o077)
    try:
        if not os.path.ismount(BASE.parent):
            raise RuntimeError('data partition is not mounted')
        # flock releases automatically on exit/crash; no stale lock recovery.
        with (BASE / 'run/storage-maintenance.lock').open('a') as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                return 0
            report = maintain(BASE, apply=not args.check)
        print(json.dumps(report, ensure_ascii=False))
        if not args.check:
            warnings = report['warnings']
            log('completed free_bytes=' + str(report['free_bytes']) +
                ' rows_deleted=0 warnings=' + ('; '.join(warnings) or 'none'), bool(warnings))
        return 0
    except Exception as error:
        message = 'maintenance failed: ' + str(error)
        print(message)
        try:
            log(message, True)
        except Exception:
            pass
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
