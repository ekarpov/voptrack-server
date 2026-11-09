import ast
from contextlib import nullcontext
import json
from pathlib import Path
import unittest

class SetupSession(unittest.TestCase):
    def namespace(self,statuses):
        source=(Path(__file__).resolve().parents[1]/'release/console/drive.py').read_text()
        node=next(n for n in ast.parse(source).body if isinstance(n,ast.FunctionDef) and n.name=='setup_operator_token')
        minted=[];events=[]
        def mint(*args):minted.append(len(minted));return 'synthetic-'+str(len(minted))
        def api(*args):
            status,admin=statuses.pop(0)
            return status,json.dumps({'server_administrator':admin}),{}
        g={'_setup_operator':None,'api':api,'app_token':mint,'owner_pw':'synthetic','json':json,
           'phase':lambda name:nullcontext(),'event':lambda name:events.append(name)}
        exec(compile(ast.Module(body=[node],type_ignores=[]),'<setup-session>','exec'),g)
        return g,minted,events
    def test_validated_setup_authority_is_reused(self):
        g,minted,events=self.namespace([(200,True),(200,True)])
        self.assertEqual(g['setup_operator_token'](),g['setup_operator_token']())
        self.assertEqual(len(minted),1);self.assertEqual(events,['setup-session-reused'])
    def test_withdrawn_or_missing_setup_authority_is_not_reused(self):
        g,minted,events=self.namespace([(200,True),(401,False),(200,True)])
        first=g['setup_operator_token']();self.assertNotEqual(first,g['setup_operator_token']())
        self.assertEqual(len(minted),2)
        g,_,_=self.namespace([(200,False)])
        with self.assertRaises(RuntimeError):g['setup_operator_token']()
