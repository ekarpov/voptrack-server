"""Exercise cleanup ownership with a fake Docker CLI; never touches the daemon."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT=Path(__file__).with_name('verify-postgres.sh')


class PostgresOwnership(unittest.TestCase):
    def probe(self, foreign=False, planted=False):
        with tempfile.TemporaryDirectory(prefix='voptrack-pg-ownership-') as temp:
            root=Path(temp);binary=root/'bin';binary.mkdir()
            code=SCRIPT.read_text()
            if planted:
                code=code.replace('docker run -d --name', 'docker rm -fv voptrack-gate-postgres >/dev/null 2>&1 || true\ndocker run -d --name',1)
            script=root/'verify.sh';script.write_text(code)
            mock=binary/'docker'
            mock.write_text("""#!/usr/bin/env python3
import json,os,pathlib,sys
args=sys.argv[1:];action=args[0];state=pathlib.Path(os.environ['PG_MOCK_STATE'])
s=json.loads(state.read_text()) if state.exists() else {}
if action=='run':
    s={'name':args[args.index('--name')+1],'owner':args[args.index('--label')+1].split('=',1)[1]}
    state.write_text(json.dumps(s))
    item={'action':action,'name':s['name']}
elif action=='inspect':
    print('foreign' if os.environ.get('PG_MOCK_FOREIGN')=='yes' else s.get('owner',''))
    item={'action':action,'name':args[-1]}
elif action=='rm':item={'action':action,'name':args[-1]}
else:sys.exit(1)
with open(os.environ['PG_MOCK_LOG'],'a') as out:out.write(json.dumps(item)+'\\n')
sys.exit(1 if action=='run' else 0)
""")
            mock.chmod(0o755)
            pkill=binary/'pkill';pkill.write_text('#!/bin/sh\nexit 0\n');pkill.chmod(0o755)
            env=dict(os.environ,PATH=str(binary)+os.pathsep+os.environ['PATH'],
                     PG_MOCK_STATE=str(root/'state.json'),PG_MOCK_LOG=str(root/'calls.jsonl'),
                     PG_MOCK_FOREIGN='yes' if foreign else 'no')
            result=subprocess.run(['bash',str(script)],env=env,capture_output=True,text=True,timeout=15)
            self.assertNotEqual(result.returncode,0,'mock intentionally refuses startup')
            return [json.loads(line) for line in (root/'calls.jsonl').read_text().splitlines()]

    def assert_owned_only(self,calls):
        started=[c['name'] for c in calls if c['action']=='run']
        self.assertEqual(len(started),1)
        self.assertTrue(started[0].startswith('voptrack-gate-postgres-'))
        self.assertTrue(all(c['name']==started[0] for c in calls if c['action']=='rm'))

    def test_failed_start_cleans_only_own_unique_container(self):
        calls=self.probe()
        self.assert_owned_only(calls)
        self.assertEqual(len([c for c in calls if c['action']=='rm']),1)

    def test_foreign_label_is_preserved(self):
        calls=self.probe(foreign=True)
        self.assert_owned_only(calls)
        self.assertFalse(any(c['action']=='rm' for c in calls))

    def test_legacy_fixed_name_defect_is_detected(self):
        calls=self.probe(planted=True)
        with self.assertRaises(AssertionError):self.assert_owned_only(calls)


if __name__=='__main__':unittest.main()
