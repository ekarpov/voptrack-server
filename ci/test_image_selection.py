"""Exercise launcher image selection with real Compose parsing and no containers."""
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
LAUNCHERS = [
    'release/verify.sh', 'release/household/verify.sh', 'release/console/verify.sh',
    'release/kit/verify.sh', 'release/research/verify.sh', 'release/recovery/verify.sh',
    'release/home-app/verify.sh', 'release/private-console/verify.sh',
    'release/household/dev.sh', 'release/research/dev.sh',
    'scenarios/single-user/scenario.sh', 'scenarios/multi-user/scenario.sh',
    'scenarios/household/scenario.sh', 'scenarios/evaluation/scenario.sh',
]
POISON = 'voptrack.invalid/unpublished:missing'


class ImageSelection(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='voptrack-image-selection-')
        self.addCleanup(self.temp.cleanup)
        self.tree = Path(self.temp.name) / 'tree'
        files = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT).decode().split('\0')
        for rel in set(filter(None, files)) | {'ci/select-image.sh','ci/phase.sh','ci/timing.py'}:
            src, dst = ROOT / rel, self.tree / rel
            if src.is_file():
                dst.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(src, dst)
        for p in [self.tree/'release/compose.yaml', *self.tree.glob('scenarios/*/compose.yaml')]:
            text = p.read_text()
            self.assertIn('${VOPTRACK_IMAGE:-', text)
            p.write_text(re.sub(r'\$\{VOPTRACK_IMAGE:-[^}]+\}', '${VOPTRACK_IMAGE:-'+POISON+'}', text))
        self.real_docker = shutil.which('docker')
        self.assertIsNotNone(self.real_docker, 'Docker Compose CLI required (no daemon needed)')
        self.bin = Path(self.temp.name) / 'bin'
        self.bin.mkdir()
        mock = self.bin / 'docker'
        mock.write_text("""#!/usr/bin/env python3
import json, os, subprocess, sys
args=sys.argv[1:]
if not args or args[0]!='compose': sys.exit(97)
options=[]; i=1
while i<len(args):
    if args[i] in ('-f','-p','--project-name','--project-directory','--env-file'):
        options.extend(args[i:i+2]); i+=2
    else: break
p=subprocess.run([os.environ['REAL_DOCKER'],'compose',*options,'config','--format','json'],capture_output=True,text=True)
if p.returncode: sys.exit(98)
services=json.loads(p.stdout)['services']
images=[s['image'] for name,s in services.items() if name!='caddy']
with open(os.environ['IMAGE_SELECTION_LOG'],'a') as f:
    f.write(json.dumps({'selected':os.environ.get('VOPTRACK_IMAGE'),'images':images})+'\\n')
sys.exit(97)
""")
        mock.chmod(0o755)

    def probe(self, launcher, selected=None):
        log = Path(self.temp.name) / 'calls.jsonl'
        log.unlink(missing_ok=True)
        env = dict(os.environ, PATH=str(self.bin)+os.pathsep+os.environ['PATH'],
                   REAL_DOCKER=self.real_docker, IMAGE_SELECTION_LOG=str(log))
        for key in list(env):
            if key.startswith('VOPTRACK_') or key in ('KEEP', 'COMPOSE_FILE', 'COMPOSE_PROFILES'):
                del env[key]
        if selected: env['VOPTRACK_IMAGE'] = selected
        args=['bash',str(self.tree/launcher)]
        if launcher.endswith(('dev.sh','scenario.sh')): args.append('down')
        p=subprocess.run(args,cwd=self.tree,env=env,capture_output=True,text=True,timeout=20)
        self.assertNotEqual(p.returncode,0,'mock must stop before mutations')
        self.assertTrue(log.exists(), f'{launcher}: did not reach Compose')
        return [json.loads(line) for line in log.read_text().splitlines()]

    def test_poisoned_defaults(self):
        for selected in (None,'local-build:candidate'):
            for launcher in LAUNCHERS:
                with self.subTest(launcher=launcher,selected=selected):
                    want=selected or 'voptrack-server:latest'
                    for call in self.probe(launcher,selected):
                        self.assertEqual(call['selected'],want)
                        self.assertTrue(call['images'])
                        self.assertEqual(set(call['images']),{want})

    def test_missing_export_is_detected(self):
        p=self.tree/'release/household/verify.sh'
        p.write_text(p.read_text().replace('. "$root/ci/select-image.sh"\n',''))
        calls=self.probe('release/household/verify.sh')
        self.assertIsNone(calls[0]['selected'])
        self.assertEqual(set(calls[0]['images']),{POISON})


if __name__ == '__main__':
    unittest.main()
