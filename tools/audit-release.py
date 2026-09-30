#!/usr/bin/env python3
"""Fail closed on common secrets/private artifacts; report names, never matched values."""
import argparse
from pathlib import Path
import re
import shlex

ROOT = Path(__file__).resolve().parents[1]
PATTERNS = (
    re.compile(rb'gh[pousr]_[A-Za-z0-9]{20,}'),
    re.compile(rb'github_pat_[A-Za-z0-9_]{20,}'),
    re.compile(rb'tskey-(?:auth|api)-[A-Za-z0-9_-]{20,}'),
    re.compile(rb'-----BEGIN (?:OPENSSH |RSA |EC |DSA )?PRIVATE KEY-----'),
    re.compile(rb'AKIA[0-9A-Z]{16}'),
)
PRIVATE_SUFFIXES = ('.sqlite', '.sqlite-wal', '.sqlite-shm', '.db', '.pem', '.key', '.ko', '.armv7', '.dylib', '.log')


def private_values(paths):
    values = []
    for path in paths:
        for line in path.read_text().splitlines():
            if not line.strip() or line.lstrip().startswith('#') or '=' not in line:
                continue
            key, value = line.split('=', 1)
            if not re.search(r'PASS|TOKEN|SECRET|AUTH', key, re.I):
                continue
            parsed = shlex.split(value)
            if len(parsed) == 1 and len(parsed[0]) >= 8 and not parsed[0].startswith('REPLACE_'):
                values.append(parsed[0].encode())
    return values


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--private-secrets', type=Path, action='append', default=[],
                        help='Optional local comparison file outside the public tree; never printed')
    args = parser.parse_args()
    known = private_values(args.private_secrets)
    issues, count = [], 0
    for path in ROOT.rglob('*'):
        relative = path.relative_to(ROOT)
        if any(part in ('.git', 'dist', '__pycache__') for part in relative.parts):
            continue
        if path.is_symlink():
            issues.append((str(relative), 'symlink not allowed in publication tree'))
            continue
        if not path.is_file():
            continue
        count += 1
        if (path.name.endswith('.env') or path.name in ('identity.secret', 'authorized_keys')
                or path.name.endswith(PRIVATE_SUFFIXES)
                or any(part in ('voice-runtime', 'logs', 'run', 'data') for part in relative.parts)):
            issues.append((str(relative), 'private state or excluded runtime artifact'))
        data = path.read_bytes()
        if any(pattern.search(data) for pattern in PATTERNS) or any(value in data for value in known):
            issues.append((str(relative), 'credential pattern or private comparison match'))
        if b'\x00' in data:
            issues.append((str(relative), 'unexpected binary file; review before release'))
    for name, reason in issues:
        print(name + ': ' + reason)
    if issues:
        raise SystemExit('Publication audit FAILED; matched values were not printed')
    print('Publication audit passed for %d text files; excluded private/runtime artifacts absent' % count)


if __name__ == '__main__':
    main()
