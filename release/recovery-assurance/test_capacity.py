"""Retained transport proof cannot substitute queue, object or ack authority."""
import hashlib,io,json,pathlib,tarfile,tempfile,unittest
from capacity import verify_capacity_binding
class CapacityBindingTest(unittest.TestCase):
 def setUp(self):
  tmp=tempfile.TemporaryDirectory();self.addCleanup(tmp.cleanup);self.root=pathlib.Path(tmp.name)
  self.config=dict(Version=1,LocalFixture=True,IndependentDestination=False,Budget=550<<20,Deployment='a'*32,Prefix='test/capacity/',Region='us-east-1',Bucket='synthetic-backups',Endpoint='https://127.0.0.1:30691',Outbox='/queue',ExpectedBucketOwner='000000000000')
  self.save('capacity/config.json',self.config)
  self.namespace=self.config['Prefix']+self.sha(self.config['Deployment'].encode())+'/'
  self.save('capacity/state/binding.json',self.sha(json.dumps([self.config[k] for k in ('Region','Bucket','Prefix','Deployment','Endpoint','Outbox','ExpectedBucketOwner')],separators=(',',':')).encode()))
  self.listing=[];self.records=[];self.payloads=[]
  for index in range(7):
   identifier='123-'+format(index+1,'032x');opaque=self.sha(identifier.encode());kind='backup' if index<5 else 'evidence';queued='2026-10-21T00:00:00Z'
   manifest={'version':1,'operation':{'id':identifier,'kind':kind,'pair':{'deployment':self.config['Deployment']}}}
   raw=json.dumps(manifest).encode();stream=io.BytesIO()
   with tarfile.open(fileobj=stream,mode='w') as archive:
    member=tarfile.TarInfo('manifest.json');member.size=len(raw);archive.addfile(member,io.BytesIO(raw))
   raw=stream.getvalue();sha=self.sha(raw);key=self.namespace+'payloads/'+sha;self.put(key,raw);self.payloads.append(self.object_path(key))
   obligation={'Version':1,'LocalPublished':True,'Manifest':manifest,'Queued':queued,'SHA256':sha,'Size':len(raw)}
   path='capacity/queue/'+identifier+'/obligation.json';self.save(path,obligation);self.records.append(self.root/path)
   settings=json.dumps({'deployment':self.config['Deployment']}).encode();settings_sha=self.sha(settings);settings_key=self.namespace+'settings/'+settings_sha;self.put(settings_key,settings)
   descriptor={'Version':1,'ID':opaque,'Deployment':self.sha(self.config['Deployment'].encode()),'Kind':kind,'Published':queued,'Payload':{'Key':key,'SHA256':sha,'Size':len(raw)},'Settings':{'Key':settings_key,'SHA256':settings_sha,'Size':len(settings)}}
   self.put(self.namespace+'commits/'+opaque+'.json',json.dumps(descriptor).encode())
   self.save('capacity/state/'+opaque+'.json',{'Version':1,'Phase':'verified','Descriptor':descriptor,'Verified':queued})
   self.listing.append({'ID':opaque,'Kind':kind,'Published':queued,'SettingsAvailable':True})
  self.save('capacity/capacity-read-only-list.log',self.listing)
 def sha(self,raw):return hashlib.sha256(raw).hexdigest()
 def object_path(self,key):return self.root/'objects'/self.sha(key.encode())
 def put(self,key,raw):
  p=self.object_path(key);p.parent.mkdir(exist_ok=True);p.write_bytes(raw)
 def save(self,path,value):
  p=self.root/path;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(value))
 def test_exact_small_transport_bytes_do_not_establish_large_capacity(self):
  result=verify_capacity_binding(self.root)
  self.assertEqual(result['bound_operations'],7);self.assertEqual(result['large_bundles'],0);self.assertFalse(result['native_authenticity_qualified'])
 def test_changed_payload_or_post_ack_manifest_refused(self):
  path=self.payloads[0];before=path.read_bytes();path.write_bytes(b'corrupt')
  with self.assertRaises((ValueError,tarfile.TarError)):verify_capacity_binding(self.root)
  path.write_bytes(before)
  path=self.records[0];value=json.loads(path.read_text());value['Manifest']['version']=0;path.write_text(json.dumps(value))
  with self.assertRaisesRegex(ValueError,'payload/outbox manifest'):verify_capacity_binding(self.root)
 def test_rebound_destination_or_unverified_ack_refused(self):
  path=self.root/'capacity/config.json';before=path.read_bytes();value=json.loads(before);value['Bucket']='another-bucket';path.write_text(json.dumps(value))
  with self.assertRaises(ValueError):verify_capacity_binding(self.root)
  path.write_bytes(before)
  path=next(p for p in (self.root/'capacity/state').glob('*.json') if p.name!='binding.json')
  value=json.loads(path.read_text());value['Phase']='committing';path.write_text(json.dumps(value))
  with self.assertRaises(ValueError):verify_capacity_binding(self.root)
 def test_missing_reader_or_indirect_payload_refused(self):
  path=self.root/'capacity/capacity-read-only-list.log';before=path.read_bytes();path.unlink()
  with self.assertRaises(FileNotFoundError):verify_capacity_binding(self.root)
  path.write_bytes(before)
  path=self.payloads[0];other=self.root/'other';other.write_bytes(path.read_bytes());path.unlink();path.symlink_to(other)
  with self.assertRaisesRegex(ValueError,'indirect'):verify_capacity_binding(self.root)
if __name__=='__main__':unittest.main()
