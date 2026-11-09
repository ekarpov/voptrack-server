#!/usr/bin/env python3
"""Prepare digest-pinned GHCR installation bundles; never publish website files."""
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
from qualification import validate_note

ROOT = Path(__file__).resolve().parents[1]
DIGEST = r'sha256:[0-9a-f]{64}'
VERSION = r'v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?'


def run(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def qualified_source():
    if run('git', 'status', '--porcelain'):
        raise ValueError('Commit changes and run full CI before preparing a release')
    revision = run('git', 'rev-parse', 'HEAD')
    validate_note(run('git', 'notes', '--ref=ci', 'show', revision), revision)
    return revision


def inspect_image(reference, platform, local=False):
    if not local:
        run('docker', 'pull', '--platform', platform, reference)
    images = json.loads(run('docker', 'image', 'inspect', '--platform', platform, reference))
    if len(images) != 1 or images[0].get('Os') + '/' + images[0].get('Architecture') != platform:
        raise ValueError('Image platform differs from requested platform')
    return images


def render_compose(source, application, caddy, platform, product=None):
    replacements = {
        '  image: ${VOPTRACK_IMAGE:-voptrack-server:latest}':
            f'  image: {application}\n  platform: {platform}',
        '    image: caddy:2-alpine': f'    image: {caddy}\n    platform: {platform}',
    }
    for old, new in replacements.items():
        if source.count(old) != 1:
            raise ValueError('Compose image layout changed; review distribution packaging')
        source = source.replace(old, new)
    # Fail closed if a new third-party service was added without archive support.
    if len(re.findall(r'^\s+image:', source, re.M)) != 2:
        raise ValueError('Every Compose image must be included in distribution packaging')
    if product:
        variant = {'home': 'household', 'research': 'research'}[product]
        old = 'VOPTRACK_VARIANT: ${VOPTRACK_VARIANT:-standard}'
        if source.count(old) != 2:
            raise ValueError('Compose product layout changed')
        source = source.replace(old, 'VOPTRACK_VARIANT: ' + variant)
    return source


def checksum(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def pack(directory, destination):
    with tarfile.open(destination, 'w:gz') as archive:
        archive.add(directory, arcname=directory.name)


def prepare(args):
    if not re.fullmatch(VERSION, args.version):
        raise ValueError('Use a version such as v0.1.0-rc.1')
    local = getattr(args, 'local', False)
    product = getattr(args, 'product', None)
    if local and (not args.archive or not product):
        raise ValueError('Local preparation needs --archive and --product')
    if not local and not re.fullmatch(r'ghcr\.io/[a-z0-9._/-]+@' + DIGEST, args.image):
        raise ValueError('Use an immutable ghcr.io image@sha256:digest reference')
    revision = qualified_source()
    output = args.output.resolve()
    if output.is_relative_to(ROOT):
        raise ValueError('Place release outputs outside the checkout')
    output.mkdir(parents=True, exist_ok=False)
    app = inspect_image(args.image, args.platform, local=True) if local else inspect_image(args.image, args.platform)
    labels = app[0].get('Config', {}).get('Labels') or {}
    if (labels.get('org.opencontainers.image.revision') != revision or
            labels.get('org.opencontainers.image.version') != args.version):
        raise ValueError('Image revision/version labels do not match qualified source')
    if product and labels.get('app.voptrack.product') != product:
        raise ValueError('Image product label does not match requested product')
    application = app[0]['Id'] if local else args.image
    # Resolve the proxy once, then freeze its digest and exact runtime for both paths.
    proxy = inspect_image('caddy:2-alpine', args.platform)
    caddy = next((d for d in proxy[0].get('RepoDigests', [])
                  if re.fullmatch(r'(?:docker\.io/library/)?caddy@' + DIGEST, d)), None)
    if not caddy:
        raise ValueError('Caddy registry digest is missing')
    source = (ROOT / 'release/compose.yaml').read_text()
    prefix = product + '-' if product else ''
    name = f'voptrack-{prefix}{args.version}-{args.platform.replace("/", "-")}'
    bundle = output / name
    bundle.mkdir()
    (bundle / 'compose.yaml').write_text(render_compose(source, application, caddy, args.platform, product))
    for origin, target in [('docs/installation.md', 'INSTALL.md'),
                           ('docs/install-agent.md', 'INSTALL-AGENT.md'),
                           ('docs/install-source.md', 'INSTALL-SOURCE.md'),
                           ('docs/install-source-agent.md', 'INSTALL-SOURCE-AGENT.md'),
                           ('docs/install/host.md', 'HOST.md'),
                           ('docs/install/local-home.md', 'LOCAL-HOME.md'),
                           ('release/distribution/load.py', 'load.py'),
                           ('release/recovery-set/image_identity.py', 'image_identity.py'),
                           ('LICENSE', 'LICENSE'),
                           ('THIRD-PARTY.md', 'THIRD-PARTY.md')]:
        shutil.copyfile(ROOT / origin, bundle / target)
        if target.endswith('.md'):
            text=(bundle/target).read_text()
            for old, new in [('install/host.md', 'HOST.md'), ('install/local-home.md', 'LOCAL-HOME.md'),
                             ('install-source-agent.md', 'INSTALL-SOURCE-AGENT.md'),
                             ('install-source.md', 'INSTALL-SOURCE.md'), ('installation.md', 'INSTALL.md'),
                             ('install-agent.md', 'INSTALL-AGENT.md'), ('local-home.md', 'LOCAL-HOME.md')]:
                text=text.replace(']('+old, ']('+new)
            text=text.replace('](../release/README.md', f'](https://github.com/ekarpov/voptrack-server/blob/{revision}/release/README.md')
            (bundle/target).write_text(text)
    # Copy the exact image's notices without starting its server or any service.
    notice_container = run('docker', 'create', '--entrypoint', '/bin/true', app[0]['Id'])
    try:
        run('docker', 'cp', notice_container + ':/usr/share/voptrack/licenses', str(bundle / 'licenses'))
    finally:
        run('docker', 'rm', '-v', notice_container)
    with tarfile.open(bundle / 'licenses.tar.gz', 'w:gz') as archive:
        archive.add(bundle / 'licenses', arcname='licenses')
    shutil.rmtree(bundle / 'licenses')
    (bundle / '.env.example').write_text(
        f'COMPOSE_PROJECT_NAME=voptrack-{product or "home"}\n'
        f'VOPTRACK_VARIANT={"research" if product == "research" else "household"}\nVOPTRACK_DOMAIN=server.example.org\n'
        'VOPTRACK_NAME=Our Home\nVOPTRACK_TIMEZONE=UTC\n'
        '# Set yes only after arranging encrypted storage beneath Docker volumes.\n'
        'VOPTRACK_STORAGE_ENCRYPTED=\n')
    images = []
    for role, reference, inspection in [('voptrack', application, app), ('caddy', caddy, proxy)]:
        filename = f'{role}-image.json'
        (bundle / filename).write_text(json.dumps(inspection, indent=2) + '\n')
        images.append({'role': role, 'reference': reference, 'inspection': filename})
    source_url = f'https://github.com/ekarpov/voptrack-server/tree/{revision}'
    manifest = {'version': args.version, 'revision': revision, 'platform': args.platform,
                'source_url': source_url,
                'agent_url': f'https://raw.githubusercontent.com/ekarpov/voptrack-server/{revision}/docs/install-agent.md',
                'operations_url': f'https://github.com/ekarpov/voptrack-server/blob/{revision}/release/README.md',
                'tessera': re.search(r'github.com/ekarpov/tessera (\S+)', (ROOT / 'go.mod').read_text())[1],
                'images': images, 'archive': False, 'product': product, 'delivery': 'prepared-offline' if local else 'registry'}
    (bundle / 'release.json').write_text(json.dumps(manifest, indent=2) + '\n')
    (bundle / 'qualification.txt').write_text(run('git', 'notes', '--ref=ci', 'show', revision) + '\n')
    online = output / f'{name}-install.tar.gz'
    # The small bundle is sufficient for registry installation.
    write_checksums(bundle)
    if not local:
        pack(bundle, online)
    if args.archive:
        manifest['archive'] = True
        (bundle / 'release.json').write_text(json.dumps(manifest, indent=2) + '\n')
        for entry, inspection in zip(images, (app, proxy)):
            raw = output / f'{entry["role"]}.tar'
            run('docker', 'image', 'save', '--output', str(raw), inspection[0]['Id'])
            with raw.open('rb') as src, gzip.open(bundle / f'{entry["role"]}.tar.gz', 'wb') as dst:
                shutil.copyfileobj(src, dst)
            raw.unlink()
        write_checksums(bundle)
        pack(bundle, output / f'{name}-offline.tar.gz')
    assets = sorted(output.glob('*.tar.gz'))
    (output / 'SHA256SUMS').write_text(''.join(f'{checksum(p)}  {p.name}\n' for p in assets))
    print(f'Prepared {len(assets)} download(s) in {output}; nothing uploaded.')


def write_checksums(directory):
    paths = sorted(p for p in directory.iterdir() if p.name != 'SHA256SUMS')
    (directory / 'SHA256SUMS').write_text(''.join(f'{checksum(p)}  {p.name}\n' for p in paths))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--platform', choices=['linux/amd64', 'linux/arm64'], required=True)
    parser.add_argument('--output', type=Path, required=True, help='new directory outside checkout')
    parser.add_argument('--product', choices=['home', 'research'])
    parser.add_argument('--local', action='store_true', help='prepare unpublished offline bundle from a local image')
    parser.add_argument('--archive', action='store_true', help='also export all Compose images')
    args = parser.parse_args()
    try:
        prepare(args)
    except (ValueError, OSError, IndexError, subprocess.CalledProcessError) as error:
        print(f'distribution: {error}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
