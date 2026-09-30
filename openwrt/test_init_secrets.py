from pathlib import Path
import shlex
import stat
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('init-secrets.py')


class SecretGenerationTests(unittest.TestCase):
    def test_private_unique_passwords_not_printed(self):
        with tempfile.TemporaryDirectory(prefix='cellbridge-secrets-test-') as temporary:
            target = Path(temporary) / 'private.env'
            result = subprocess.run([sys.executable, str(SCRIPT), '--output', str(target)],
                                    capture_output=True, text=True, check=True)
            values = dict(line.split('=', 1) for line in target.read_text().splitlines())
            self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
            self.assertNotEqual(values['SIP_PASS'], values['SIP_PASS2'])
            for key in ('SIP_PASS', 'SIP_PASS2'):
                self.assertGreaterEqual(len(shlex.split(values[key])[0]), 32)
                self.assertNotIn(values[key], result.stdout + result.stderr)

    def test_existing_file_is_not_overwritten(self):
        with tempfile.TemporaryDirectory(prefix='cellbridge-secrets-test-') as temporary:
            target = Path(temporary) / 'private.env'
            target.write_text('untouched')
            result = subprocess.run([sys.executable, str(SCRIPT), '--output', str(target)],
                                    capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(target.read_text(), 'untouched')

    def test_unsafe_username_rejected_before_file_creation(self):
        with tempfile.TemporaryDirectory(prefix='cellbridge-secrets-test-') as temporary:
            target = Path(temporary) / 'private.env'
            result = subprocess.run([sys.executable, str(SCRIPT), '--output', str(target),
                                     '--user', 'unsafe;command'], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(target.exists())


if __name__ == '__main__':
    unittest.main()
