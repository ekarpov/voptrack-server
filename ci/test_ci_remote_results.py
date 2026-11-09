"""Execute the remote launcher with fake transport and real local result files."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

class RemoteResults(unittest.TestCase):
    def run_fixture(self,mode,steps=()):
        root=Path(tempfile.mkdtemp());self.addCleanup(shutil.rmtree,root)
        (root/'ci').mkdir();(root/'deploy').mkdir();bin=root/'bin';bin.mkdir()
        for name in ('remote.sh', 'remote-paths.sh'):
            shutil.copy(Path(__file__).with_name(name),root/'ci'/name)
        (root/'ci/select-image.sh').write_text('export VOPTRACK_IMAGE=fixture\n')
        (root/'ci/runner.py').write_text('pass\n')
        (root/'deploy/modproxy.sh').write_text('#!/bin/bash\nmkdir -p "$1"\n');(root/'deploy/modproxy.sh').chmod(0o755)
        sha='a'*40;dest=root/'.ci'/sha;(dest/'runs'/'old').mkdir(parents=True)
        (dest/'summary.txt').write_text('prior summary\n')
        tools={
          'git':'''#!/usr/bin/env python3
import os,sys
from pathlib import Path
a=sys.argv[1:]
if a[0]=='rev-parse':print('a'*40)
elif a[0]=='bundle':Path(a[2]).write_text('bundle')
elif a[0]=='notes':Path(os.environ['NOTE_MARKER']).write_text('published')
''',
          'ssh':'#!/bin/sh\nexit 0\n',
          'scp':'''#!/usr/bin/env python3
import sys
from pathlib import Path
if any('runner.log' in a for a in sys.argv[1:-1]):Path(sys.argv[-1]).write_text('runner output')
''',
          'rsync':'''#!/usr/bin/env python3
import os,sys
from pathlib import Path
if any(a.endswith('/evidence/') for a in sys.argv):
 p=Path(sys.argv[-1]);p.mkdir(parents=True,exist_ok=True)
 if os.environ['RESULT_MODE']!='none':
  (p/'runs'/'current').mkdir(parents=True)
  if os.environ['RESULT_MODE']=='full':(p/'summary.txt').write_text('current summary\\n')
'''}
        for name,text in tools.items():(bin/name).write_text(text);(bin/name).chmod(0o755)
        env=dict(os.environ,PATH=str(bin)+':'+os.environ['PATH'],VOPTRACK_CI_HOST='fixture',VOPTRACK_CI_KEY='unused',VOPTRACK_CI_ROOT='/var/tmp/synthetic-ci',RESULT_MODE=mode,NOTE_MARKER=str(root/'note'))
        p=subprocess.run(['bash','ci/remote.sh',*steps],cwd=root,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        return p,root,dest
    def test_early_failure_cannot_republish_prior_local_full_receipt(self):
        p,root,dest=self.run_fixture('none')
        self.assertNotEqual(p.returncode,0,p.stderr)
        self.assertFalse((root/'note').exists())
        self.assertEqual((dest/'summary.txt').read_text(),'prior summary\n')
    def test_current_full_retrieval_publishes_and_partial_does_not(self):
        p,root,dest=self.run_fixture('full');self.assertEqual(p.returncode,0,p.stderr)
        self.assertTrue((root/'note').exists());self.assertEqual((dest/'summary.txt').read_text(),'current summary\n')
        p,root,dest=self.run_fixture('partial',('--steps','kit-check'))
        self.assertEqual(p.returncode,0,p.stderr);self.assertFalse((root/'note').exists())
        self.assertTrue((dest/'runs'/'current').is_dir())
