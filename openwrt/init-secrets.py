#!/usr/bin/env python3
"""Generate private SIP credentials without printing passwords or overwriting files."""
import argparse
import os
from pathlib import Path
import re
import secrets
import shlex


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, default=Path('cellbridge.secrets.env'))
    parser.add_argument('--user', default='iphone')
    parser.add_argument('--second-user', default='secondary')
    args = parser.parse_args()
    for user in (args.user, args.second_user):
        if not re.fullmatch(r'[A-Za-z0-9_.-]{1,64}', user):
            parser.error('usernames must contain 1-64 letters, digits, dot, dash or underscore')
    if args.user == args.second_user:
        parser.error('usernames must be different')
    values = {'SIP_USER': args.user, 'SIP_PASS': secrets.token_urlsafe(32),
              'SIP_USER2': args.second_user, 'SIP_PASS2': secrets.token_urlsafe(32)}
    os.umask(0o077)
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, 'O_NOFOLLOW', 0)
    fd = os.open(args.output, flags, 0o600)
    with os.fdopen(fd, 'w', encoding='utf-8') as handle:
        for name, value in values.items():
            handle.write(name + '=' + shlex.quote(value) + '\n')
    print('Private credential file created: ' + str(args.output))
    print('Read it locally to configure your phone. Do not upload it or paste it into an Issue.')


if __name__ == '__main__':
    main()
