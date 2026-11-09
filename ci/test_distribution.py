"""Distribution contracts, with deliberately broken files/images and fake Docker.

These tests create no containers and make no registry/publication requests.
"""
import argparse
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import distribution
from tessera_pin import expected_pin

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'release/recovery-set'))
spec = importlib.util.spec_from_file_location('archive_loader', ROOT / 'release/distribution/load.py')
loader = importlib.util.module_from_spec(spec)
spec.loader.exec_module(loader)
REVISION = 'a' * 40
APP = 'ghcr.io/ekarpov/voptrack-server@sha256:' + 'b' * 64
PROXY = 'caddy@sha256:' + 'c' * 64


def image(char, arch='amd64'):
    return [{'Id': 'sha256:' + char * 64, 'Os': 'linux', 'Architecture': arch,
             'RootFS': {'Type': 'layers', 'Layers': ['sha256:' + 'd' * 64]},
             'Config': {'User': '10001', 'Env': ['TZ=UTC'], 'Labels': {
                 'org.opencontainers.image.revision': REVISION,
                 'org.opencontainers.image.version': 'v0.1.0-rc.1'}},
             'RepoDigests': [PROXY]}]


class DistributionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.output = Path(self.temp.name) / 'new'

    def prepare(self, archive=True, application=None, product=None, local=False):
        args = argparse.Namespace(version='v0.1.0-rc.1', platform='linux/amd64',
                                  image=APP, output=self.output, archive=archive, product=product, local=local)

        def docker(*args):
            if args[:2] == ('git', 'notes'):
                return 'Synthetic test qualification; not runtime evidence'
            if args[:3] == ('docker', 'image', 'save'):
                Path(args[4]).write_bytes(b'synthetic image archive')
                return ''
            if args[:2] == ('docker', 'create'):
                return 'synthetic-notice-container'
            if args[:2] == ('docker', 'cp'):
                target=Path(args[-1]); target.mkdir(); (target/'LICENSE').write_text('synthetic dependency notice')
                return ''
            if args[:2] == ('docker', 'rm'):
                return ''
            raise AssertionError(args)

        with patch.object(distribution, 'qualified_source', return_value=REVISION), \
                patch.object(distribution, 'inspect_image', side_effect=[application or image('e'), image('f')]), \
                patch.object(distribution, 'run', side_effect=docker):
            distribution.prepare(args)
        prefix = product + '-' if product else ''
        return self.output / f'voptrack-{prefix}v0.1.0-rc.1-linux-amd64'

    def test_matched_bundles_contain_all_images_and_pinned_source(self):
        bundle = self.prepare()
        loader.verify_files(bundle)
        text = (bundle / 'compose.yaml').read_text()
        self.assertIn('image: ' + APP, text)
        self.assertIn('image: ' + PROXY, text)
        self.assertNotIn('image: ${VOPTRACK_IMAGE', text)
        manifest = json.loads((bundle / 'release.json').read_text())
        self.assertEqual(manifest['revision'], REVISION)
        self.assertEqual(manifest['tessera'], expected_pin())
        self.assertIn('VOPTRACK_STORAGE_ENCRYPTED=\n', (bundle / '.env.example').read_text())
        downloads = sorted(self.output.glob('*.tar.gz'))
        self.assertEqual(len(downloads), 2)
        for download in downloads:
            with tarfile.open(download) as archive:
                names = archive.getnames()
            self.assertTrue(any(n.endswith('/INSTALL-AGENT.md') for n in names))
            self.assertEqual(any(n.endswith('/caddy.tar.gz') for n in names), '-offline.' in download.name)
            self.assertIn(distribution.checksum(download), (self.output / 'SHA256SUMS').read_text())

    def test_product_bundle_pins_variant_and_includes_exact_image_notices(self):
        app=image('e'); app[0]['Config']['Labels']['app.voptrack.product']='research'
        bundle=self.prepare(application=app, product='research')
        self.assertIn('VOPTRACK_VARIANT: research', (bundle/'compose.yaml').read_text())
        self.assertNotIn('VOPTRACK_VARIANT: ${', (bundle/'compose.yaml').read_text())
        with tarfile.open(bundle/'licenses.tar.gz') as archive:
            self.assertEqual(archive.extractfile('licenses/LICENSE').read(), b'synthetic dependency notice')

    def test_local_preparation_has_only_offline_download_and_loaded_id(self):
        app=image('e'); app[0]['Config']['Labels']['app.voptrack.product']='home'
        bundle=self.prepare(application=app, product='home', local=True)
        self.assertEqual(len(list(self.output.glob('*.tar.gz'))), 1)
        self.assertTrue(next(self.output.glob('*.tar.gz')).name.endswith('-offline.tar.gz'))
        manifest=json.loads((bundle/'release.json').read_text())
        self.assertEqual(manifest['delivery'], 'prepared-offline')
        self.assertEqual(manifest['images'][0]['reference'], app[0]['Id'])
        self.assertIn('VOPTRACK_VARIANT: household', (bundle/'compose.yaml').read_text())

    def test_wrong_product_refuses_packaging(self):
        app=image('e'); app[0]['Config']['Labels']['app.voptrack.product']='home'
        with self.assertRaisesRegex(ValueError,'product label'):
            self.prepare(application=app, product='research')

    def test_no_archive_bundle_refuses_offline_load(self):
        bundle = self.prepare(archive=False)
        with self.assertRaisesRegex(ValueError, 'complete offline'):
            loader.verify_files(bundle)

    def test_corruption_is_refused_before_any_docker_call(self):
        bundle = self.prepare()
        (bundle / 'voptrack.tar.gz').write_bytes(b'broken')
        with patch.object(loader.subprocess, 'check_output') as docker:
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                loader.load(bundle)
            docker.assert_not_called()

    def test_checksum_path_traversal_refused(self):
        bundle = self.prepare()
        (bundle / 'SHA256SUMS').write_text('0' * 64 + '  ../outside\n')
        with self.assertRaisesRegex(ValueError, 'checksum list'):
            loader.verify_files(bundle)

    def docker_loader(self, mismatch=False, architecture='linux/x86_64'):
        images = {'voptrack': image('e'), 'caddy': image('f')}
        loaded = {}

        def docker(args, **kwargs):
            if args[1] == 'info':
                return architecture
            if args[1:3] == ['image', 'load']:
                role = Path(args[-1]).name.split('.')[0]
                record = copy.deepcopy(images[role])
                record[0]['Id'] = 'sha256:' + ('1' if role == 'voptrack' else '2') * 64
                if mismatch and role == 'caddy':
                    record[0]['Config']['User'] = 'root'
                loaded[record[0]['Id']] = record
                return 'Loaded image ID: ' + record[0]['Id'] + '\n'
            if args[1:3] == ['image', 'inspect']:
                return json.dumps(loaded[args[-1]])
            raise AssertionError(args)
        return docker

    def test_rewritten_ids_are_verified_and_pulls_disabled_for_both_images(self):
        bundle = self.prepare()
        with patch.object(loader.subprocess, 'check_output', side_effect=self.docker_loader()):
            loader.load(bundle)
        compose = (bundle / 'compose.offline.yaml').read_text()
        self.assertEqual(compose.count('pull_policy: never'), 2)
        self.assertIn('image: sha256:' + '1' * 64, compose)
        self.assertIn('image: sha256:' + '2' * 64, compose)
        self.assertNotIn(APP, compose)
        receipt = json.loads((bundle / 'image-equivalence.json').read_text())
        self.assertTrue(all(r['runtime_contents_equal'] for r in receipt))
        with patch.object(loader.subprocess, 'check_output', side_effect=self.docker_loader()):
            with self.assertRaises(FileExistsError):
                loader.load(bundle)

    def test_altered_proxy_runtime_refuses_activation(self):
        bundle = self.prepare()
        with patch.object(loader.subprocess, 'check_output', side_effect=self.docker_loader(mismatch=True)):
            with self.assertRaisesRegex(ValueError, 'identity mismatch'):
                loader.load(bundle)
        self.assertFalse((bundle / 'compose.offline.yaml').exists())

    def test_wrong_host_architecture_refused_before_load(self):
        bundle = self.prepare()
        with patch.object(loader.subprocess, 'check_output', return_value='linux/aarch64') as docker:
            with self.assertRaisesRegex(ValueError, 'architecture'):
                loader.load(bundle)
            self.assertEqual(docker.call_count, 1)

    def test_wrong_image_source_refuses_packaging(self):
        wrong = image('e')
        wrong[0]['Config']['Labels']['org.opencontainers.image.revision'] = '0' * 40
        with self.assertRaisesRegex(ValueError, 'revision/version'):
            self.prepare(application=wrong)
        self.assertEqual(list(self.output.glob('*.tar.gz')), [])

    def test_unexpected_compose_image_refused(self):
        source = (ROOT / 'release/compose.yaml').read_text()
        source += '\n  third:\n    image: unexpected:latest\n'
        with self.assertRaisesRegex(ValueError, 'Every Compose image'):
            distribution.render_compose(source, APP, PROXY, 'linux/amd64')

    def test_dirty_source_and_missing_ci_are_refused(self):
        with patch.object(distribution, 'run', return_value='modified'):
            with self.assertRaisesRegex(ValueError, 'Commit changes'):
                distribution.qualified_source()
        with patch.object(distribution, 'run', side_effect=['', REVISION, 'ci: fail']):
            with self.assertRaisesRegex(ValueError, 'passing full CI'):
                distribution.qualified_source()

    def test_inspection_explicitly_selects_the_requested_platform(self):
        with patch.object(distribution, 'run', side_effect=['', json.dumps(image('e'))]) as docker:
            distribution.inspect_image(APP, 'linux/amd64')
        self.assertEqual(docker.call_args.args,
                         ('docker', 'image', 'inspect', '--platform', 'linux/amd64', APP))
        with patch.object(distribution, 'run', side_effect=['', json.dumps(image('e', arch='arm64'))]):
            with self.assertRaisesRegex(ValueError, 'platform'):
                distribution.inspect_image(APP, 'linux/amd64')

    def test_mutable_reference_refused_before_git_or_docker(self):
        args = argparse.Namespace(version='v0.1.0', image='ghcr.io/ekarpov/voptrack-server:latest')
        with patch.object(distribution, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'immutable'):
                distribution.prepare(args)
            run.assert_not_called()

    def test_missing_archive_refused_before_docker(self):
        bundle = self.prepare()
        (bundle / 'caddy.tar.gz').unlink()
        with patch.object(loader.subprocess, 'check_output') as docker:
            with self.assertRaisesRegex(ValueError, 'Missing'):
                loader.load(bundle)
            docker.assert_not_called()
    def test_compose_parser_accepts_generated_online_and_offline_files(self):
        bundle = self.prepare()
        with patch.object(loader.subprocess, 'check_output', side_effect=self.docker_loader()):
            loader.load(bundle)
        for name in ['compose.yaml', 'compose.offline.yaml']:
            result = subprocess.run(['docker', 'compose', '-f', str(bundle / name), 'config', '--format', 'json'],
                                    capture_output=True, text=True, check=True,
                                    env={**os.environ, 'VOPTRACK_IMAGE': 'must-not-be-selected:latest'})
            config = json.loads(result.stdout)
            self.assertEqual({s['platform'] for s in config['services'].values()}, {'linux/amd64'})
            self.assertEqual(len({s['image'] for s in config['services'].values()}), 2)
            self.assertTrue(all(s['image'] != 'must-not-be-selected:latest'
                                for s in config['services'].values()))
            if 'offline' in name:
                self.assertTrue(all(s['pull_policy'] == 'never' for s in config['services'].values()))


if __name__ == '__main__':
    unittest.main()
