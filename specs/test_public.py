"""Publication checker behavior, using disposable repositories and synthetic markers."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

CHECK = Path(__file__).with_name('check-public.py')
SPEC = importlib.util.spec_from_file_location('public_check', CHECK)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class PublicationCheck(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='voptrack-public-proof-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git('init', '-q')
        self.git('config', 'user.name', 'Synthetic release check')
        self.git('config', 'user.email', 'synthetic@example.invalid')
        (self.root/'README.md').write_text('Public installation guide.\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'Initial public source')

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, stderr=subprocess.PIPE)

    def run_check(self, *args):
        p = subprocess.run(['python3', str(CHECK), '--root', str(self.root), '--json', *args],
                           capture_output=True, text=True)
        return p, json.loads(p.stdout) if p.stdout else None

    def test_planted_defect_then_restore(self):
        self.assertEqual(self.run_check()[0].returncode, 0)
        p = self.root/'README.md'
        intact = p.read_text()
        p.write_text(intact+'Internal Codex handoff at /home/synthetic/private on 10.23.45.67 with tessera_dev.\n')
        result, report = self.run_check()
        self.assertEqual(result.returncode, 1)
        self.assertEqual({f['category'] for f in report['findings']},
                         {'internal-tool','internal-document','local-path','host-address','key-name'})
        self.assertNotIn('/home/synthetic', result.stdout)
        self.assertNotIn('10.23.45.67', result.stdout)
        p.write_text(intact)
        self.assertEqual(self.run_check()[0].returncode, 0)

    def test_commit_messages_and_ref_are_scanned(self):
        self.git('commit', '--allow-empty', '-qm', 'Internal Codex handoff')
        p, report = self.run_check('--ref', 'HEAD')
        self.assertEqual(p.returncode, 1)
        self.assertTrue(any(f['path'].startswith('commit:') for f in report['findings']))
        (self.root/'README.md').write_text('Codex\n')
        self.assertFalse(any(f['path']=='README.md' for f in self.run_check('--ref','HEAD')[1]['findings']))
        self.assertTrue(any(f['path']=='README.md' for f in self.run_check()[1]['findings']))

    def test_document_name_and_addresses(self):
        (self.root/'handoff.md').write_text('Internal coordination.\n')
        self.git('add', '.')
        self.assertEqual(self.run_check()[0].returncode, 1)
        self.assertFalse(MODULE.categories('127.0.0.1 192.0.2.3 ::1 2001:db8::7 https://example.org http://example.org'))
        self.assertIn('local-path', MODULE.categories(r'C:\work\private'))
        self.assertIn('host-address', MODULE.categories('10.2.3.4 fd12:3456::9'))

    def test_missing_input_fails_closed(self):
        (self.root/'README.md').unlink()
        self.assertEqual(self.run_check()[0].returncode, 2)
        self.assertEqual(self.run_check('--ref','missing-ref')[0].returncode, 2)

    def test_symlink_fails_closed(self):
        (self.root/'README.md').unlink()
        (self.root/'README.md').symlink_to('/dev/null')
        self.assertEqual(self.run_check()[0].returncode, 2)


    def test_committed_symlink_fails_closed(self):
        # Its target contains no lexical marker; file mode must cause refusal.
        (self.root/'public-link').symlink_to('/etc/example-private')
        self.git('add', '.')
        self.git('commit', '-qm', 'Add a public link')
        self.assertEqual(self.run_check()[0].returncode, 2)
        self.assertEqual(self.run_check('--ref','HEAD')[0].returncode, 2)

    def test_committed_gitlink_fails_closed(self):
        commit=self.git('rev-parse','HEAD').decode().strip()
        self.git('update-index','--add','--cacheinfo','160000,'+commit+',dependency')
        self.git('commit','-qm','Add a dependency link')
        self.assertEqual(self.run_check('--ref','HEAD')[0].returncode, 2)


if __name__ == '__main__':
    unittest.main()
