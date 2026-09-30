import importlib.util
from pathlib import Path
import sqlite3
import tempfile
import time
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('storage', Path(__file__).with_name('storage-maintenance.py'))
storage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(storage)


class StorageMaintenanceTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='cellbridge-maintenance-test-')
        self.base = Path(self.temporary.name)
        for name in ('data', 'run', 'logs'):
            (self.base / name).mkdir()
        self.db = sqlite3.connect(self.base / 'data/cellbridge.sqlite')
        self.db.execute('PRAGMA journal_mode=WAL')
        for table in ('messages', 'calls', 'sync_log', 'idempotency_records', 'sms_segments'):
            self.db.execute('CREATE TABLE ' + table + '(id INTEGER PRIMARY KEY, value TEXT)')
            self.db.execute('INSERT INTO ' + table + ' VALUES (1, ?)', ('history-' + table,))
        self.db.commit()
        self.log = self.base / 'logs/launcher.log'
        self.log.write_bytes(b'old' * storage.LOG_LIMIT + b'recent')

    def tearDown(self):
        self.db.close()
        self.temporary.cleanup()

    def test_preserves_all_database_content_and_limits_log(self):
        before = list(self.db.iterdump())
        original = self.log.read_bytes()
        report = storage.maintain(self.base, apply=True)
        self.assertEqual(list(self.db.iterdump()), before)
        self.assertEqual(self.log.read_bytes(), original[-storage.LOG_KEEP:])
        self.assertEqual(report['database_rows_deleted'], 0)
        self.assertEqual(self.db.execute('PRAGMA quick_check').fetchone()[0], 'ok')
        self.assertTrue((self.base / 'run/storage-status.json').is_file())

    def test_check_has_no_maintenance_side_effects(self):
        original = self.log.read_bytes()
        report = storage.maintain(self.base)
        self.assertEqual(self.log.read_bytes(), original)
        self.assertNotIn('checkpoint', report)
        self.assertFalse((self.base / 'run/storage-status.json').exists())

    def test_does_not_replace_active_log(self):
        before = self.log.stat()
        with patch.object(storage, 'in_use', return_value=True):
            report = storage.maintain(self.base, apply=True)
        self.assertEqual(self.log.stat().st_ino, before.st_ino)
        self.assertEqual(self.log.stat().st_size, before.st_size)
        self.assertTrue(any('in use' in warning for warning in report['warnings']))

    def test_checkpoint_does_not_wait_for_writer(self):
        self.db.execute('BEGIN IMMEDIATE')
        self.db.execute("INSERT INTO messages VALUES (2, 'ongoing-write')")
        started = time.monotonic()
        storage.maintain(self.base, apply=True)
        self.assertLess(time.monotonic() - started, 1)
        self.db.commit()
        self.assertEqual(self.db.execute('SELECT count(*) FROM messages').fetchone()[0], 2)


if __name__ == '__main__':
    unittest.main()
