import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from tessera_pin import parse_pin
from timing import RetryBudget, event

class Helpers(unittest.TestCase):
    def setUp(self):
        silence=patch.dict(os.environ,{"VOPTRACK_CI_METRICS_FILE":""})
        silence.start();self.addCleanup(silence.stop)

    def test_pin_is_exact_not_an_indirect_module_or_tool(self):
        self.assertEqual(parse_pin('tool github.com/ekarpov/tessera/cmd/tesserad\nrequire (\n github.com/ekarpov/tessera v1.2.3-rc.4\n)'), 'v1.2.3-rc.4')
        for text in ('', 'github.com/ekarpov/tessera/cmd/tesserad v1.0.0',
                     'github.com/ekarpov/tessera v1.0.0\ngithub.com/ekarpov/tessera v2.0.0',
                     '  github.com/ekarpov/tessera v1.0.0\n  github.com/ekarpov/tessera v2.0.0'):
            with self.assertRaises(ValueError):parse_pin(text)

    def test_retry_honors_server_wait_and_is_bounded(self):
        now=[0.0];waits=[]
        def sleep(v):waits.append(v);now[0]+=v
        budget=RetryBudget(clock=lambda:now[0], sleep=sleep, wall=lambda:0)
        self.assertTrue(budget.wait({'Retry-After':'60'},0))
        self.assertTrue(budget.wait({'Retry-After':'60'},1))
        self.assertFalse(budget.wait({'Retry-After':'60'},2))
        self.assertEqual(waits,[60.05,60.05])
        self.assertFalse(RetryBudget(sleep=sleep).wait({'Retry-After':'3600'},0))
        self.assertTrue(RetryBudget(clock=lambda:0,sleep=sleep,wall=lambda:0).wait({'Retry-After':'Thu, 01 Jan 1970 00:00:02 GMT'},0))

    def test_metrics_reject_sensitive_or_arbitrary_fields(self):
        with tempfile.TemporaryDirectory() as d, patch.dict(os.environ,{'VOPTRACK_CI_METRICS_FILE':str(Path(d)/'metrics')}):
            event('auth-backoff',1,attempt=1)
            self.assertEqual(json.loads((Path(d)/'metrics').read_text())['seconds'],1)
            for kw in ({'url':'https://example/?code=secret'},{'status':'payload'}):
                with self.assertRaises(ValueError):event('request',1,**kw)
            with self.assertRaises(ValueError):event('user@example',1)
