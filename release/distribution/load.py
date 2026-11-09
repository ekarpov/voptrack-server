#!/usr/bin/env python3
"""Verify an offline bundle, load its images and write a no-pull Compose file."""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

from image_identity import verify


def verify_files(directory):
    entries = (directory / 'SHA256SUMS').read_text().splitlines()
    names = set()
    for line in entries:
        match = re.fullmatch(r'([0-9a-f]{64})  ([A-Za-z0-9._-]+)', line)
        if not match or match[2] in names:
            raise ValueError('Invalid checksum list')
        digest, name = match.groups()
        names.add(name)
        path = directory / name
        if path.is_symlink() or not path.is_file():
            raise ValueError('Missing or unsafe bundle file')
        actual = hashlib.sha256()
        with path.open('rb') as stream:
            for block in iter(lambda: stream.read(1024 * 1024), b''):
                actual.update(block)
        if actual.hexdigest() != digest:
            raise ValueError('Bundle checksum mismatch')
    required = {'compose.yaml', 'release.json', 'voptrack-image.json', 'caddy-image.json',
                'voptrack.tar.gz', 'caddy.tar.gz', 'load.py', 'image_identity.py'}
    if not required <= names:
        raise ValueError('This is not a complete offline bundle')


def load(directory):
    verify_files(directory)
    target = directory / 'compose.offline.yaml'
    if target.exists() or target.is_symlink():
        raise FileExistsError('compose.offline.yaml already exists; preserve the installation')
    manifest = json.loads((directory / 'release.json').read_text())
    if manifest['platform'] not in ('linux/amd64', 'linux/arm64'):
        raise ValueError('Unsupported platform')
    machine = subprocess.check_output(['docker', 'info', '--format', '{{.OSType}}/{{.Architecture}}'], text=True).strip()
    machine = {'linux/x86_64': 'linux/amd64', 'linux/aarch64': 'linux/arm64'}.get(machine, machine)
    if machine != manifest['platform']:
        raise ValueError('Download the bundle matching this Docker host architecture')
    if [entry['role'] for entry in manifest['images']] != ['voptrack', 'caddy']:
        raise ValueError('Unexpected image inventory')
    compose = (directory / 'compose.yaml').read_text()
    receipts = []
    for entry in manifest['images']:
        role = entry['role']
        if entry['inspection'] != f'{role}-image.json':
            raise ValueError('Unexpected inspection file')
        source = json.loads((directory / entry['inspection']).read_text())
        # Save-by-ID archives may be reidentified by a different Docker store.
        # Only inspect the identity Docker actually reported, never an old tag.
        result = subprocess.check_output(['docker', 'image', 'load', '--input', str(directory / f'{role}.tar.gz')], text=True)
        ids = re.findall(r'^Loaded image ID: (sha256:[0-9a-f]{64})$', result, re.M)
        if len(ids) != 1:
            raise ValueError('Expected one loaded image ID; do not guess a local image')
        loaded = json.loads(subprocess.check_output(['docker', 'image', 'inspect', ids[0]], text=True))
        receipts.append({'role': role, **verify(source, loaded)})
        if loaded[0]['Os'] + '/' + loaded[0]['Architecture'] != manifest['platform']:
            raise ValueError('Loaded image platform mismatch')
        old = 'image: ' + entry['reference']
        if compose.count(old) != 1:
            raise ValueError('Compose reference mismatch')
        indent = '  ' if role == 'voptrack' else '    '
        compose = compose.replace(old, f'image: {ids[0]}\n{indent}pull_policy: never')
    # Do not clobber an operator-maintained installation file.
    with target.open('x') as stream:
        stream.write(compose)
    (directory / 'image-equivalence.json').write_text(json.dumps(receipts, indent=2) + '\n')
    print('Verified both images. Use docker compose -f compose.offline.yaml for every lifecycle command.')


if __name__ == '__main__':
    try:
        load(Path(__file__).resolve().parent)
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f'Archive installation refused: {error}', file=sys.stderr)
        sys.exit(1)
