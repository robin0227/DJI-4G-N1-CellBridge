#!/usr/bin/env python3
"""Collect Go module and toolchain notices into an artifact, with no private paths."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    environment = dict(os.environ, GOOS='linux', GOARCH='arm64', CGO_ENABLED='0')
    text = subprocess.check_output(['go', 'list', '-deps', '-json', './cmd/cellbridge-gateway'],
                                   cwd=ROOT / 'gateway', text=True, env=environment)
    decoder = json.JSONDecoder()
    modules, offset = {}, 0
    while offset < len(text):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        if offset >= len(text):
            break
        package, offset = decoder.raw_decode(text, offset)
        module = package.get('Module')
        if not module or module.get('Main'):
            continue
        modules[module['Path']] = module
    index, missing = [], []
    for module in sorted(modules.values(), key=lambda item: item['Path']):
        location = module.get('Dir')
        name, version = module['Path'], module.get('Version', '')
        if not location:
            missing.append(name)
            continue
        notices = [path for path in Path(location).iterdir()
                   if path.is_file() and path.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING', 'NOTICE', 'AUTHORS', 'PATENTS'))]
        if not any(path.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING')) for path in notices):
            missing.append(name)
            continue
        target = output / 'go' / (name.replace('/', '__') + '@' + version)
        target.mkdir(parents=True, exist_ok=True)
        for path in notices:
            shutil.copyfile(path, target / path.name)
        index.append({'module': name, 'version': version, 'notices': sorted(path.name for path in notices)})
    if missing:
        raise SystemExit('Review missing module license files before release: ' + ', '.join(missing))
    goroot = Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
    shutil.copyfile(goroot / 'LICENSE', output / 'Go-toolchain-LICENSE')
    for path in (ROOT / 'licenses').iterdir():
        if path.is_file():
            shutil.copyfile(path, output / path.name)
    (output / 'modules.json').write_text(json.dumps(index, indent=2) + '\n')
    print('Collected notices for %d Go modules and the toolchain' % len(index))


if __name__ == '__main__':
    main()
