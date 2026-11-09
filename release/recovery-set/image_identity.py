"""Verify runtime identity across Docker stores that rewrite image metadata.

Only ordered content-addressed filesystem layers, platform and the full runtime
configuration may establish equivalence. Tags, version strings and a load message
alone are insufficient. Receipts contain hashes rather than configuration values.
"""
import hashlib
import json
import pathlib
import re
import sys

DIGEST = re.compile(r'^sha256:[0-9a-f]{64}$')


def verify(source, loaded):
    if len(source) != 1 or len(loaded) != 1:
        raise ValueError('image identity mismatch')
    source, loaded = source[0], loaded[0]
    if not all(DIGEST.fullmatch(v.get('Id', '')) for v in (source, loaded)):
        raise ValueError('image identity mismatch')
    layers = source.get('RootFS', {})
    if (layers.get('Type') != 'layers' or not layers.get('Layers') or
            not all(DIGEST.fullmatch(v) for v in layers['Layers']) or
            not isinstance(source.get('Config'), dict) or
            not source.get('Os') or not source.get('Architecture')):
        raise ValueError('image identity mismatch')
    for field in ('Os', 'Architecture', 'RootFS', 'Config'):
        if source.get(field) != loaded.get(field):
            raise ValueError('image identity mismatch')
    configuration = json.dumps(source['Config'], sort_keys=True).encode()
    return {'source_image': source['Id'], 'recovery_image': loaded['Id'],
            'os': source['Os'], 'architecture': source['Architecture'],
            'filesystem_layers': layers['Layers'],
            'runtime_configuration_sha256': hashlib.sha256(configuration).hexdigest(),
            'runtime_contents_equal': True}


def main():
    try:
        source, loaded = (json.loads(pathlib.Path(p).read_text()) for p in sys.argv[1:])
        receipt = verify(source, loaded)
    except (OSError, ValueError, TypeError, KeyError):
        print('Image runtime identity verification failed.', file=sys.stderr)
        return 1
    print(json.dumps(receipt, sort_keys=True))
    return 0


if __name__ == '__main__':
    sys.exit(main())
