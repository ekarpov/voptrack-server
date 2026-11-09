import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import runner
import smoke_image
from qualification import validate_note, gate_names
from tessera_pin import expected_pin

SHA='a'*40
IMAGE='sha256:'+'b'*64

class RunnerTests(unittest.TestCase):
    def test_manifest_preserves_all_23_gates_and_recovery_chain(self):
        gates=runner.load_gates()
        self.assertEqual(len(gates),23)
        by={g['name']:g for g in gates}
        self.assertEqual(by['guided-recovery-check']['depends'],['recovery-set-check'])
        self.assertEqual(by['off-machine-backups-check']['depends'],['guided-recovery-check'])
        self.assertEqual(by['evaluation']['stage'],'scenario')
        chosen,full=runner.select(gates,['console-check'])
        self.assertFalse(full)
        self.assertIn('image',chosen)
        self.assertNotIn('research-check',chosen)
        self.assertFalse(runner.select(gates,[g['name'] for g in gates])[1])
        with self.assertRaises(ValueError):runner.select(gates,['unknown'])

    def test_worker_bundles_and_projects_are_disjoint_and_budgeted(self):
        runner.validate_slots({})
        a,pa,na=runner.resources({},0,'one','console-check')
        b,pb,nb=runner.resources({},1,'one','research-check')
        self.assertFalse(set(pa)&set(pb));self.assertFalse(na.overlaps(nb))
        self.assertNotEqual(a['VOPTRACK_GATE_PROJECT'],b['VOPTRACK_GATE_PROJECT'])
        self.assertEqual(a['VOPTRACK_CONSOLE_BIND'],'127.0.0.1')
        self.assertEqual(a['VOPTRACK_PUBLISH_PREFIX'],'127.0.0.1:')
        with self.assertRaises(ValueError):runner.validate_slots({'VOPTRACK_RESEARCH_DEV_NET':'10.117.50'})
        base={'VOPTRACK_RESEARCH_DEV_CONSOLE_PORT':'9446','VOPTRACK_CI_AUX_PORT':'9555'}
        runner.validate_slots(base)
        self.assertEqual(runner.resources(base,1,'one','probe')[1][2],9555)
        with patch.object(os,'cpu_count',return_value=1):
            with self.assertRaises(ValueError):runner.check_budget(2)

    def test_release_refuses_partial_missing_wrong_pin_and_skipped_records(self):
        rows='\n'.join(f'{n} PASS 1s' for n in gate_names())
        note=f'ci: pass\nmode: full\ncommit: {SHA}\ntessera: {expected_pin()}\nimage: {IMAGE}\n'+rows+'\n'
        validate_note(note,SHA)
        for bad in (note.replace('mode: full','mode: partial'),note.replace('ci: pass','ci: partial pass'),
                    note.replace(expected_pin(),'v0.0.0'),note.replace(' PASS ',' SKIP ',1),
                    '\n'.join(note.splitlines()[:-1]),note.replace(SHA,'c'*40)):
            with self.assertRaises(ValueError):validate_note(bad,SHA)

    def fake_run(self,steps='',image_fail=False,exclusive=False,conflict=False):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);(root/'ci').mkdir();(root/'.gitignore').write_text('.ci/\n')
            gates=[
                {'name':'check','command':'true','stage':'preflight','depends':[]},
                {'name':'image','command':'true','stage':'preflight','depends':['check']},
                {'name':'alpha','command':'sleep 0.2','stage':'runtime','depends':['image']},
                {'name':'beta','command':'sleep 0.2','stage':'runtime','depends':['image']},
            ]
            gates[2]['exclusive']=exclusive
            if conflict:
                import shlex
                guard=shlex.quote(str(root/'overlap-guard'))
                command='mkdir '+guard+' || exit 17; trap '+shlex.quote('rmdir '+guard)+' EXIT; sleep 0.3'
                gates[2]['command']=command;gates[3]['command']=command
            manifest=root/'ci/gates.json';manifest.write_text(json.dumps({'version':1,'gates':gates}))
            for args in (['init','-q'],['add','.'],['-c','user.name=test','-c','user.email=test@example.invalid','commit','-qm','fixture']):
                subprocess.run(['git','-C',d,*args],check=True,stdout=subprocess.DEVNULL)
            env={**os.environ,'CI_STEPS':steps,'VOPTRACK_IMAGE':'candidate'}
            def image(*a):
                if image_fail:raise ValueError('bootstrap not executable')
                return {'image':IMAGE,'tessera':expected_pin()}
            with patch.object(runner,'ROOT',root),patch.object(runner,'MANIFEST',manifest),patch.object(runner,'verify_image',side_effect=image),patch.object(runner,'check_budget'),patch.dict(os.environ,env,clear=True),patch('sys.argv',['runner']),contextlib.redirect_stdout(io.StringIO()):
                code=runner.main()
            result=json.loads(next((root/'.ci').glob('*/runs/*/result.json')).read_text())
            note=subprocess.run(['git','-C',d,'notes','--ref=ci','show'],text=True,capture_output=True)
            return code,result,note

    def test_actual_subprocess_scheduler_runs_two_workers(self):
        code,result,note=self.fake_run()
        self.assertEqual(code,0);self.assertTrue(result['qualified'])
        self.assertEqual(result['maximum_concurrent_gates'],2)
        self.assertEqual(note.returncode,0)

    def test_exclusive_gate_prevents_actual_resource_contention(self):
        code,result,_=self.fake_run(conflict=True)
        self.assertNotEqual(code,0);self.assertFalse(result['qualified'])
        code,result,_=self.fake_run(exclusive=True,conflict=True)
        self.assertEqual(code,0);self.assertTrue(result['qualified'])
        self.assertEqual(result['maximum_concurrent_gates'],1)

    def test_partial_success_cannot_publish_full_note(self):
        code,result,note=self.fake_run('alpha')
        self.assertEqual(code,0);self.assertFalse(result['qualified'])
        self.assertEqual(result['mode'],'partial');self.assertNotEqual(note.returncode,0)

    def test_shared_image_failure_skips_dependent_work(self):
        code,result,note=self.fake_run(image_fail=True)
        self.assertEqual(code,1);self.assertFalse(result['qualified'])
        self.assertEqual([g['status'] for g in result['gates']],['PASS','FAIL','SKIP','SKIP'])
        self.assertIn('ci: fail',note.stdout)

class ImageSmoke(unittest.TestCase):
    def test_wrong_version_bad_mode_and_unexpected_bootstrap_are_refused(self):
        def fake(version=expected_pin(),executable=True,refuses=True,logo=True):
            def run(*args):
                if args[1:3]==('image','inspect'):return subprocess.CompletedProcess(args,0,IMAGE+'\n','')
                if args[-1]=='-version':return subprocess.CompletedProcess(args,0,version+'\n','')
                if args[-2:] == ('-c','test -x /usr/local/bin/voptrack-init'):return subprocess.CompletedProcess(args,0 if executable else 1,'','')
                if args[-2:] == ('-c','test -r /usr/local/share/voptrack/identity-logo.png'):return subprocess.CompletedProcess(args,0 if logo else 1,'','')
                return subprocess.CompletedProcess(args,1 if refuses else 0,'','VOPTRACK_STORAGE_ENCRYPTED is not yes' if refuses else '')
            return run
        self.assertEqual(smoke_image.verify('tag',run=fake())['image'],IMAGE)
        for run in (fake(version='v0.0.0'),fake(executable=False),fake(refuses=False),fake(logo=False)):
            with self.assertRaises(ValueError):smoke_image.verify('tag',run=run)
