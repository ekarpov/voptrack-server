"""Completion cannot omit a mode, substitute an image or promote external proof."""
import json,pathlib,shutil,tempfile,unittest
from unittest.mock import patch
from complete import aggregate
from test_report import ReportFixture
class CompletionTest(ReportFixture):
 def setUp(self):
  super().setUp()
  tmp=tempfile.TemporaryDirectory();self.addCleanup(tmp.cleanup);self.matrix=pathlib.Path(tmp.name)
  for label,mode in [('private','private'),('app-only','off'),('public','public')]:
   target=self.matrix/label;shutil.copytree(self.root,target)
   for path in ['source-household/result.json','off-machine/guided/success-household/verification-inputs/home-assurance-receipt.json']:
    p=target/path;value=json.loads(p.read_text());value.update(home_mode=mode,mode_authority_and_routing_preserved=True);p.write_text(json.dumps(value))
   if mode=='off':
    p=target/'off-machine/guided/success-household/correctness.json';value=json.loads(p.read_text());value.update(saved_console_state=False,app_only_authority_absent=True);p.write_text(json.dumps(value))
  (self.matrix/'capacity').mkdir()
  self.capacity={'passed':True,'synthetic_transport_only':True,'queue_exhaustion_refused':True,'outage_preserved_pending':True,'restart_drained':True,'native_authenticity_qualified':False,'remote_deletion':False}
  self.capacity.update(large_bundles=3,archive_bytes_each=256<<20,evidence_bytes_each=16<<20,queue_budget=550<<20,worker_memory_limit_bytes=384<<20,worker_cpus=2)
  # The full byte-binding verifier has separate real-file adversarial tests.
  binding=patch('complete.verify_capacity_binding',return_value={'bound_operations':7,'large_bundles':3,'large_bytes':(816<<20)+1024,'native_authenticity_qualified':False});binding.start();self.addCleanup(binding.stop)
  self.receipt={'image':self.image,'mode':'capacity','passed':True,'aws_qualified':False,'separate_host_qualified':False,'local_synthetic_only':True,'physical_independence_attested':False}
  self.write_capacity()
 def write_capacity(self):
  (self.matrix/'capacity/capacity-result.json').write_text(json.dumps(self.capacity))
  (self.matrix/'capacity/result.json').write_text(json.dumps(self.receipt))
 def test_all_modes_are_required_without_external_promotion(self):
  result=aggregate(self.matrix,self.image)
  self.assertEqual(set(result['home_modes']),{'private','app-only','public'})
  self.assertEqual(result['aws_iam'],'deferred-pre-release-unverified')
  p=self.matrix/'app-only/source-household/result.json';p.unlink()
  with self.assertRaises(FileNotFoundError):aggregate(self.matrix,self.image)
 def test_capacity_cannot_substitute_image_or_claim_native_or_provider_proof(self):
  for key,value in [('image','other'),('aws_qualified',True),('separate_host_qualified',True)]:
   before=self.receipt[key];self.receipt[key]=value;self.write_capacity()
   with self.assertRaises(ValueError):aggregate(self.matrix,self.image)
   self.receipt[key]=before
  self.capacity['native_authenticity_qualified']=True;self.write_capacity()
  with self.assertRaises(ValueError):aggregate(self.matrix,self.image)
 def test_omitted_limits_and_false_resource_envelope_refused(self):
  for key in ['large_bundles','archive_bytes_each','evidence_bytes_each','queue_budget','worker_memory_limit_bytes','worker_cpus']:
   before=self.capacity.pop(key);self.write_capacity()
   with self.assertRaises(ValueError):aggregate(self.matrix,self.image)
   self.capacity[key]=before
  self.capacity['worker_memory_limit_bytes']=1;self.write_capacity()
  with self.assertRaises(ValueError):aggregate(self.matrix,self.image)
 def test_run_mapping_cannot_escape_root(self):
  for value in ['../foreign','/foreign','..','private/subdir']:
   with self.assertRaises(ValueError):aggregate(self.matrix,self.image,dict(private=value,**{'app-only':'app-only','public':'public','capacity':'capacity'}))
if __name__=='__main__':unittest.main()
