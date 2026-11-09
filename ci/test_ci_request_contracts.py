"""HTTP-level compatibility for the real decorated fixture request functions."""
import ast
import http.server
import json
import os
from pathlib import Path
import threading
import unittest
from unittest.mock import patch
import urllib.error,urllib.parse,urllib.request
from timing import retry_auth

ROOT=Path(__file__).resolve().parents[1]
class RequestContracts(unittest.TestCase):
    def setUp(self):
        self.seen=[];seen=self.seen
        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def do_POST(self):
                seen.append((self.path,self.headers,self.rfile.read(int(self.headers.get('Content-Length','0')))))
                limited=self.path=='/oauth/authorize' and len(seen)==1
                self.send_response(429 if limited else 200)
                if limited:self.send_header('Retry-After','0')
                self.end_headers();self.wfile.write(b'fixture')
            do_GET=do_POST
        server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
        threading.Thread(target=server.serve_forever,daemon=True).start()
        self.addCleanup(server.server_close);self.addCleanup(server.shutdown)
        self.base='http://127.0.0.1:'+str(server.server_port)
        silence=patch.dict(os.environ,{'VOPTRACK_CI_METRICS_FILE':''});silence.start();self.addCleanup(silence.stop)
    def load(self,path):
        node=next(n for n in ast.parse((ROOT/path).read_text()).body if isinstance(n,ast.FunctionDef) and n.name=='request')
        g={'urllib':urllib,'json':json,'base':self.base,'public':self.base,'retry_auth':retry_auth}
        exec(compile(ast.Module(body=[node],type_ignores=[]),str(path),'exec'),g)
        return g['request']
    def test_home_bearer_authority_is_forwarded_through_wrapper(self):
        request=self.load('release/home-app/drive.py')
        status,_,_=request(urllib.request.build_opener(),'GET','/v1/datasets',token='fixture-authority',headers={'X-Fixture':'yes'})
        self.assertEqual(status,200);self.assertEqual(self.seen[0][1]['Authorization'],'Bearer fixture-authority')
        self.assertEqual(self.seen[0][1]['X-Fixture'],'yes')
    def test_seed_multiselects_survive_bounded_auth_retry(self):
        for path in ('release/household/seed_dev.py','release/research/seed_dev.py'):
            self.seen.clear();request=self.load(path)
            status,_,_=request(urllib.request.build_opener(),'POST','/oauth/authorize',form={'track':['one','two'],'decision':'allow'})
            self.assertEqual(status,200);self.assertEqual(len(self.seen),2)
            for _,_,body in self.seen:self.assertEqual(urllib.parse.parse_qs(body.decode())['track'],['one','two'])
