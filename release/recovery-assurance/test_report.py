"""Receipts must never turn missing local proof into verified external custody."""
import hashlib,io,json,pathlib,tarfile,tempfile,unittest
from report import build_report
class ReportFixture(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup);self.root=pathlib.Path(self.tmp.name);self.image='sha256:'+'a'*64
  self.save('off-machine/result.json',dict(passed=True,image=self.image,mode='assurance',local_synthetic_only=True,aws_qualified=False,separate_host_qualified=False))
  self.save('off-machine/guided/result.json',dict(passed=True,image=self.image,independent_host=False))
  for variant in ('household','research'):
   self.save('source-'+variant+'/result.json',dict(variant=variant,image=self.image,entry=('protected-home-setup' if variant=='household' else 'operator-and-study-wizard'),native_artifacts=True,real_collector_outbox=True,host_approved_committed_kit=True,source_services_unavailable=True,source_volumes_preserved=8))
   self.save('off-machine/success-'+variant+'/copy-result.json',dict(variant=variant,real_collector_outbox=True,fixture_objects_read_back=True,separate_read_only_fetch=True,copy_seconds=2))
   self.save('off-machine/guided/success-'+variant+'/correctness.json',dict(variant=variant,actual_guided_target_verified=True,source_services_unavailable=True,record_isolation=True,later_erasure_exclusion=True,producer_epoch_settings=True,saved_console_state=True))
   self.provenance(variant)
  self.save('off-machine/guided/success-household/verification-inputs/home-assurance-receipt.json',dict(fixed_choices_restored=True,configuration_matches=True,password_retirement_preserved=True))
  self.save('off-machine/guided/success-research/verification-inputs/research-assurance-receipt.json',dict(study_document_restored=True,consent_restored=True,participant_records_restored=True,participant_isolation=True))
 def provenance(self,variant):
  case='off-machine/success-'+variant
  self.save(case+'/native-publication.json',dict(real_collector_outbox=True,selected_native_operations=2,source_services_unavailable=True))
  chosen={};selection={}
  for role,index in [('backup','1'),('evidence','2')]:
   identifier='123-'+index*32;chosen[role]=identifier
   manifest={'version':1,'operation':{'id':identifier,'kind':role}}
   raw=json.dumps(manifest).encode();stream=io.BytesIO()
   with tarfile.open(fileobj=stream,mode='w') as archive:
    header=tarfile.TarInfo('manifest.json');header.size=len(raw);archive.addfile(header,io.BytesIO(raw))
   raw=stream.getvalue();sha=hashlib.sha256(raw).hexdigest()
   selection[role]={'file':role+'.tar','sha256':sha}
   for directory in ['retained/'+variant+'/publication/.replication-outbox/'+identifier,'retained/'+variant+'/publication/'+identifier,case+'/published-preserved/'+identifier]:
    self.bytes(directory+'/backup.tar',raw)
   obligation=dict(Version=1,LocalPublished=True,SHA256=sha,Size=len(raw),Manifest=manifest)
   for directory in ['retained/'+variant+'/publication/.replication-outbox/'+identifier,case+'/queue/'+identifier]:
    self.save(directory+'/obligation.json',obligation)
   for directory in ['retained/'+variant+'/inputs','off-machine/fetched/'+variant+'/inputs','off-machine/guided/success-'+variant+'/inputs']:
    self.bytes(directory+'/'+role+'.tar',raw)
  self.save('retained/'+variant+'/publication/selection.json',chosen)
  for directory in ['retained/'+variant+'/inputs','off-machine/fetched/'+variant+'/inputs','off-machine/guided/success-'+variant+'/inputs']:
   self.save(directory+'/selection.json',selection)
 def bytes(self,path,value):
  p=self.root/path;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(value)
 def save(self,path,value):
  p=self.root/path;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(value))

class ReportTest(ReportFixture):
 def test_local_success_cannot_promote_external_acceptance(self):
  result=build_report(self.root,self.image)
  for product in result['products'].values():
   self.assertEqual(product['local_integration'],'verified');self.assertTrue(product['aws_iam'].startswith('pending'));self.assertTrue(product['separate_host_recovery'].startswith('pending'));self.assertEqual(product['physical_custody'],'unverified')
 def test_missing_essential_proof_refused(self):
  for path in list(self.root.rglob('*.json')):
   original=path.read_bytes();path.unlink()
   with self.assertRaises((FileNotFoundError,ValueError)):build_report(self.root,self.image)
   path.write_bytes(original)
 def test_external_claim_wrong_image_and_synthetic_outbox_refused(self):
  for path,key,value in [('off-machine/result.json','aws_qualified',True),('off-machine/guided/result.json','image','other'),('off-machine/success-research/copy-result.json','real_collector_outbox',False),('off-machine/guided/success-household/correctness.json','later_erasure_exclusion',False)]:
   p=self.root/path;before=p.read_text();changed=json.loads(before);changed[key]=value;p.write_text(json.dumps(changed))
   with self.assertRaises(ValueError):build_report(self.root,self.image)
   p.write_text(before)

 def test_home_mode_cannot_be_substituted_or_hide_console_authority(self):
  for mode in ('private','off'):
   with self.assertRaises(ValueError):build_report(self.root,self.image,mode)
   source=self.root/'source-household/result.json';value=json.loads(source.read_text());value['home_mode']=mode;self.save(str(source.relative_to(self.root)),value)
   home=self.root/'off-machine/guided/success-household/verification-inputs/home-assurance-receipt.json'
   value=json.loads(home.read_text());value['home_mode']=mode;value['mode_authority_and_routing_preserved']=True;self.save(str(home.relative_to(self.root)),value)
   correctness=self.root/'off-machine/guided/success-household/correctness.json';value=json.loads(correctness.read_text())
   if mode=='off':
    with self.assertRaises(ValueError):build_report(self.root,self.image,mode)
    value['saved_console_state']=False;self.save(str(correctness.relative_to(self.root)),value)
    with self.assertRaises(ValueError):build_report(self.root,self.image,mode)
    value['app_only_authority_absent']=True;self.save(str(correctness.relative_to(self.root)),value)
   result=build_report(self.root,self.image,mode)
   self.assertEqual(result['products']['household']['home_mode'],mode)
   with self.assertRaises(ValueError):build_report(self.root,self.image,'public')

class ProvenanceRefusalTest(ReportFixture):
 def test_acknowledged_queue_payload_is_not_required_but_source_bytes_are(self):
  # Successful acknowledgement may release its copied pending payload. The
  # immutable source publication and fetched bytes must still bind exactly.
  self.assertTrue(build_report(self.root,self.image)['products']['household']['source_outbox_fetch_binding']=='verified')
  path=self.root/'retained/household/publication/.replication-outbox'/('123-'+'1'*32)/'backup.tar'
  path.write_bytes((path.parents[1]/('123-'+'2'*32)/'backup.tar').read_bytes())
  with self.assertRaises(ValueError):build_report(self.root,self.image)
 def test_swapped_fetched_bytes_and_obligation_manifest_refused(self):
  p=self.root/'off-machine/fetched/research/inputs/backup.tar';before=p.read_bytes();p.write_bytes((p.parent/'evidence.tar').read_bytes())
  with self.assertRaises(ValueError):build_report(self.root,self.image)
  p.write_bytes(before)
  p=self.root/'off-machine/success-research/queue'/('123-'+'1'*32)/'obligation.json'
  value=json.loads(p.read_text());value['Manifest']['operation']['kind']='evidence';p.write_text(json.dumps(value))
  with self.assertRaises(ValueError):build_report(self.root,self.image)
 def test_wrong_product_entry_and_symlink_proof_refused(self):
  p=self.root/'source-household/result.json';before=p.read_text();value=json.loads(before);value['entry']='operator-and-study-wizard';p.write_text(json.dumps(value))
  with self.assertRaises(ValueError):build_report(self.root,self.image)
  p.write_text(before)
  before=p.read_bytes();copy=self.root/'source-receipt-copy';copy.write_bytes(before);p.unlink();p.symlink_to(copy)
  with self.assertRaisesRegex(ValueError,'indirect'):build_report(self.root,self.image)
  p.unlink();p.write_bytes(before)
  p=self.root/'off-machine/success-household/native-publication.json';before=p.read_bytes();p.unlink()
  other=self.root/'proof-copy';other.write_bytes(before);p.symlink_to(other)
  with self.assertRaises(ValueError):build_report(self.root,self.image)

if __name__=='__main__':unittest.main()
