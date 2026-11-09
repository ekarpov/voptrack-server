"""Local synthetic TLS S3 + public collector-contract fixture, actual immutable image.
No AWS requests, credentials or provider/physical-independence acceptance.
Every invocation requires a fresh persistent private root.
"""
import base64,re,copy,datetime,hashlib,http.server,io,json,os,pathlib,secrets,shutil,socket,ssl,subprocess,sys,tarfile,threading,time,urllib.parse

import sys as _sys
from pathlib import Path as _Path
_sys.path.insert(0, str(_Path(__file__).resolve().parents[2] / 'ci'))
from tessera_pin import expected_pin
PIN = expected_pin()
REPO=pathlib.Path(__file__).resolve().parents[2]
IMAGE=sys.argv[1];INPUTS=pathlib.Path(sys.argv[2]).resolve();WORK=pathlib.Path(sys.argv[3]).resolve()
MODE=os.environ.get('VOPTRACK_OFF_MACHINE_MODE','full');PORT=int(os.environ.get('VOPTRACK_OFF_MACHINE_PORT','30691'))
ORIGIN='https://127.0.0.1:'+str(PORT);PROJECT='voptrack-off-machine-'+secrets.token_hex(5)
def check(v,label):
 if not v:raise RuntimeError(label)
def run(args,**kw):return subprocess.run(args,check=True,**kw)
def docker(*args,ok=True):
 p=subprocess.run(['docker',*args],capture_output=True)
 if ok:check(p.returncode==0,'private qualification container refused; diagnostics retained')
 return p
def fingerprint(root):return {str(p.relative_to(root)):(p.stat().st_mode,p.stat().st_size,p.stat().st_mtime_ns,hashlib.sha256(p.read_bytes()).hexdigest()) for p in root.rglob('*') if p.is_file()}
def write(path,value):path.write_text(json.dumps(value,indent=2)+'\n');path.chmod(0o600)
def private(path):path.mkdir(parents=True,mode=0o700);path.chmod(0o700)
check(IMAGE.startswith('sha256:'),'immutable actual image required');check(not WORK.exists(),'qualification root must be fresh')
with socket.socket() as probe:probe.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);probe.bind(('127.0.0.1',PORT))
original=fingerprint(INPUTS);private(WORK)
run([sys.executable,str(REPO/'release/recovery-set/inventory.py'),str(WORK/'before.json')])
version=docker('run','--rm','--network','none','--tmpfs','/var/lib/tessera','--entrypoint','tesserad',IMAGE,'-version').stdout.decode().strip()
check(version==PIN,'actual image pin mismatch')
run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','2','-subj','/CN=localhost','-addext','subjectAltName=DNS:localhost,IP:127.0.0.1','-keyout',str(WORK/'tls.key'),'-out',str(WORK/'tls.crt')],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
class Fixture:
 def __init__(self):
  self.lock=threading.RLock();self.objects={};self.events=[];self.fault='';self.operations=[];self.artifacts={}
  private(WORK/'objects')
 def response(self,h,code,raw=b'',kind='application/octet-stream'):
  h.send_response(code);h.send_header('Content-Type',kind);h.send_header('Content-Length',str(len(raw)));h.end_headers()
  try:h.wfile.write(raw)
  except (OSError,ssl.SSLError):pass
 def error(self,h,code,label):self.response(h,code,('<Error><Code>'+label+'</Code></Error>').encode(),'application/xml')
 def handle(self,h):
  with self.lock:
   parsed=urllib.parse.urlparse(h.path);path=urllib.parse.unquote(parsed.path);query=urllib.parse.parse_qs(parsed.query)
   length=int(h.headers.get('Content-Length','0'));body=h.rfile.read(length) if length else b''
   if path.startswith('/admin/'):
    if h.headers.get('Authorization')!='Bearer synthetic-retrieval-only':self.response(h,403);return
    if path=='/admin/api/v1/backup-operations' and h.command=='GET':self.response(h,200,json.dumps({'results':self.operations,'next':''}).encode(),'application/json');return
    if '/leases/' in path and h.command in ('PUT','DELETE'):
     self.response(h,200,json.dumps({'id':path.rsplit('/',1)[1],'expires_at':(datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(minutes=5)).isoformat().replace('+00:00','Z')}).encode(),'application/json');return
    if '/artifacts/' in path and h.command=='GET':
     raw=self.artifacts.get(path.rsplit('/',1)[1])
     if raw is not None:self.response(h,200,raw);return
    self.response(h,403);return
   if self.fault=='unavailable':self.error(h,503,'ServiceUnavailable');return
   auth=h.headers.get('Authorization','');identity='writer' if 'Credential=synthetic-writer/' in auth else 'reader' if 'Credential=synthetic-reader/' in auth else ''
   if not identity or h.headers.get('X-Amz-Expected-Bucket-Owner')!='000000000000':self.error(h,403,'AccessDenied');return
   prefix='/synthetic-backups/'
   if path in (prefix,prefix.rstrip('/')) and h.command=='GET' and query.get('list-type')==['2']:
    requested=query.get('prefix',[''])[0]
    if not requested.startswith('test/'):self.error(h,403,'AccessDenied');return
    keys=sorted(k for k in self.objects if k.startswith(requested))
    raw=('<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsTruncated>false</IsTruncated>'+''.join('<Contents><Key>'+k+'</Key><Size>'+str(len(self.objects[k]))+'</Size></Contents>' for k in keys)+'</ListBucketResult>').encode()
    self.response(h,200,raw,'application/xml');return
   key=path[len(prefix):] if path.startswith(prefix) else ''
   if not key.startswith('test/'):self.error(h,403,'AccessDenied');return
   category='commit' if '/commits/' in key else 'payload' if '/payloads/' in key else 'settings'
   self.events.append({'method':h.command,'category':category,'identity':identity,'conditional':h.headers.get('If-None-Match')=='*'})
   if h.command=='PUT':
    if identity!='writer' or h.headers.get('If-None-Match')!='*':self.error(h,403,'AccessDenied');return
    if key in self.objects:self.error(h,412,'PreconditionFailed');return
    self.objects[key]=body
    path=WORK/'objects'/hashlib.sha256(key.encode()).hexdigest();path.write_bytes(body);path.chmod(0o600)
    if self.fault=='lost-response':
     h.close_connection=True;h.connection.shutdown(socket.SHUT_RDWR);h.connection.close();return
    self.response(h,200);return
   if h.command=='GET':
    if key not in self.objects:self.error(h,404,'NoSuchKey');return
    raw=self.objects[key]
    if self.fault=='corrupt-read' and category=='payload':raw=bytes([raw[0]^1])+raw[1:]
    self.response(h,200,raw);return
   if h.command=='HEAD':
    # Deliberately plausible HEAD/ETag cannot establish copy integrity.
    h.send_response(200);h.send_header('ETag','"synthetic-not-content-proof"');h.end_headers();return
   self.error(h,403,'AccessDenied')
fixture=Fixture()
class Handler(http.server.BaseHTTPRequestHandler):
 protocol_version='HTTP/1.1'
 def log_message(self,*args):pass
 def do_GET(self):fixture.handle(self)
 def do_PUT(self):fixture.handle(self)
 def do_DELETE(self):fixture.handle(self)
 def do_HEAD(self):fixture.handle(self)
server=http.server.ThreadingHTTPServer(('127.0.0.1',PORT),Handler);server.daemon_threads=True
tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(str(WORK/'tls.crt'),str(WORK/'tls.key'));server.socket=tls.wrap_socket(server.socket,server_side=True)
thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
def container(bindings,entry,args,log,image=None):
 mounts=[]
 for host,target,ro in bindings:mounts+=['-v',str(host)+':'+target+(':ro' if ro else '')]
 limits=['--memory','384m','--memory-swap','384m','--cpus','2'] if MODE=='capacity' else []
 p=docker('run',*limits,'--rm','--label','voptrack.off-machine='+PROJECT,'--network','host','--user',f'{os.getuid()}:{os.getgid()}','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--tmpfs',f'/tmp:uid={os.getuid()},mode=0700','--tmpfs',f'/var/lib/tessera:uid={os.getuid()},mode=0700',*mounts,'--entrypoint',entry,(image or IMAGE),*args,ok=False)
 log.write_bytes(p.stdout+p.stderr);log.chmod(0o600);return p
class Case:
 def __init__(self,name,variant='household',prefix=None):
  self.name=name;self.dir=WORK/name;private(self.dir);self.variant=variant;self.source=INPUTS/variant
  self.deployment=json.loads((self.source/'trust/backup-lineage.json').read_text())['deployment']
  for name in ['queue','state','status','published']:private(self.dir/name)
  self.settings=self.source/'inputs/settings.json'
  selection=json.loads((self.source/'inputs/selection.json').read_text());self.settings=self.source/'inputs'/selection['settings']['file']
  self.real_publication=MODE=='assurance' and self.name.startswith('success-')
  self.expected_records=2 if self.real_publication else 4
  if self.real_publication:
   selected=json.loads((self.source/'publication/selection.json').read_text())
   for role in ('backup','evidence'):
    identifier=selected[role]
    check(re.fullmatch(r'[0-9]+-[a-f0-9]{32}',identifier),'unsafe real publication identifier')
    shutil.copytree(self.source/'publication/.replication-outbox'/identifier,self.dir/'queue'/identifier)
    shutil.copytree(self.source/'publication'/identifier,self.dir/'published'/identifier)
    obligation=json.loads((self.dir/'queue'/identifier/'obligation.json').read_text())
    check(obligation['LocalPublished'] and obligation['Manifest']['operation']['kind']==role,'native publication obligation mismatch')
    check(hashlib.sha256((self.dir/'queue'/identifier/'backup.tar').read_bytes()).hexdigest()==selection[role]['sha256'],'native publication differs from approved selection')
   self.records=[json.loads(p.read_text()) for p in (self.dir/'queue').glob('*/obligation.json')]
   write(self.dir/'native-publication.json',{'real_collector_outbox':True,'selected_native_operations':2,'source_services_unavailable':True})
  else:
   fixture.operations=[];fixture.artifacts={}
   accepted=datetime.datetime.now(datetime.timezone.utc)-datetime.timedelta(minutes=1)
   for index,role in enumerate(['backup','evidence','backup','evidence']):
    with tarfile.open(self.source/'inputs'/selection[role]['file']) as src:
     manifest=json.load(src.extractfile('manifest.json'));op=copy.deepcopy(manifest['operation'])
     for a in op['artifacts']:fixture.artifacts[a['id']]=src.extractfile(a['id']).read()
    op['id']=str(int(time.time()))+'-'+secrets.token_hex(16);op['accepted_at']=(accepted+datetime.timedelta(seconds=index)).isoformat().replace('+00:00','Z');op['expires_at']=(accepted+datetime.timedelta(hours=24)).isoformat().replace('+00:00','Z')
    fixture.operations.append(op)
   write(self.dir/'collector.json',{'ID':'a'*32,'Token':'synthetic-retrieval-only','Deployment':self.deployment,'Origin':ORIGIN,'expires_at':(accepted+datetime.timedelta(hours=2)).isoformat().replace('+00:00','Z')})
   p=container([(self.dir/'published','/published',False),(self.dir/'queue','/queue',False),(self.dir/'collector.json','/collector.json',True),(WORK/'tls.crt','/ca.crt',True)],'voptrack-collector',['-once','-origin',ORIGIN,'-ca','/ca.crt','-credential','/collector.json','-bundles','/published','-outbox','/queue','-keep','1'],self.dir/'collector.log')
   check(p.returncode==0,'collector outbox handoff failed')
   self.records=[json.loads(p.read_text()) for p in (self.dir/'queue').glob('*/obligation.json')]
   check(len(self.records)==4,'local rotation lost durable replication obligations')
   check(len(list((self.dir/'published').glob('*/backup.tar')))==2,'local usable retention was changed')
   for r in self.records:check((self.dir/'queue'/r['Manifest']['operation']['id']/'backup.tar').exists(),'pending captured payload missing')
  write(self.dir/'writer.json',{'AccessKeyID':'synthetic-writer','SecretAccessKey':'synthetic-private-fixture-only','SessionToken':''})
  write(self.dir/'reader.json',{'AccessKeyID':'synthetic-reader','SecretAccessKey':'synthetic-read-only-fixture','SessionToken':''})
  self.config={'Version':1,'ExpectedBucketOwner':'000000000000','Label':'Synthetic '+variant,'Region':'us-east-1','Bucket':'synthetic-backups','Prefix':prefix or 'test/'+self.name+'/','Deployment':self.deployment,'CredentialFile':'/authorization/s3.json','Endpoint':ORIGIN,'CAFile':'/ca.crt','Outbox':'/queue','State':'/state','StatusFile':'/status/off-machine.json','SettingsFile':'/settings.json','Budget':5368709120,'LocalFixture':True,'IndependentDestination':False}
  write(self.dir/'config.json',self.config)
 def invoke(self,mode='copy',reader=False,extra=None,bindings=None,label='copy',image=None):
  base=[(self.dir/'config.json','/config.json',True),(self.dir/('reader.json' if reader else 'writer.json'),'/authorization/s3.json',True),(WORK/'tls.crt','/ca.crt',True)]
  if mode=='copy':base +=[(self.dir/'queue','/queue',False),(self.dir/'state','/state',False),(self.dir/'status','/status',False),(self.settings,'/settings.json',True)]
  if (self.dir/'stop-phase').exists():base.append((self.dir/'stop-phase','/stop-at-phase',True))
  base+=bindings or []
  return container(base,'voptrack-replicator',['-config','/config.json','-mode',mode]+(['-once'] if mode=='copy' else [])+(extra or []),self.dir/(label+'.log'),image=image)
 def pending(self):return [p for p in (self.dir/'queue').glob('*/backup.tar')]
 def copy(self,label='copy',reader=False,image=None):return self.invoke(reader=reader,label=label,image=image)
def faults():
 c=Case('readback')
 fixture.fault='corrupt-read';p=c.copy('corrupt-read')
 check(p.returncode!=0,'corrupt readback was accepted')
 check(len(c.pending())==4,'failed transfer released pending bytes')
 check(not any(k.startswith(c.config['Prefix']) and '/commits/' in k for k in fixture.objects),'premature completion descriptor')
 fixture.fault='';check(c.copy('intact-readback-replay').returncode==0,'intact readback replay failed')
 check(not c.pending(),'verified obligations were not released')
 print('Actual-image readback failure and intact retry: PASS',flush=True)
def conflict():
 c=Case('conflict');r=c.records[0]
 key=c.config['Prefix']+hashlib.sha256(c.deployment.encode()).hexdigest()+'/payloads/'+r['SHA256'];fixture.objects[key]=b'immutable conflicting bytes'
 p=c.copy('conflict')
 check(p.returncode!=0,'conflicting object was accepted')
 check(fixture.objects[key]==b'immutable conflicting bytes','existing conflicting object was overwritten')
 check(bool(c.pending()),'conflict discarded pending data')
 print('Actual-image immutable conflict refusal: PASS',flush=True)
def positive(variant):
 c=Case('success-'+variant,variant)
 check(c.copy('reader-cannot-create',reader=True).returncode!=0,'read-only identity created missing remote objects')
 check(len(c.pending())==c.expected_records,'read-only refusal lost pending obligations')
 fixture.fault='lost-response';start=time.monotonic()
 check(c.copy('lost-response-copy').returncode==0,'lost successful upload response could not reconcile')
 fixture.fault='';elapsed=time.monotonic()-start
 check(not c.pending(),'remote verification did not release local payloads')
 # Preserve removal evidence; only these task-owned runtime copies are removed.
 check(c.dir.resolve().is_relative_to(WORK),'task source escape')
 hashes={str(p.relative_to(c.dir/'published')):hashlib.sha256(p.read_bytes()).hexdigest() for p in (c.dir/'published').glob('*/backup.tar')}
 if MODE=='assurance':(c.dir/'published').rename(c.dir/'published-preserved')
 else:shutil.rmtree(c.dir/'published')
 write(c.dir/'source-removal.json',{'removed_task_runtime_copies':MODE!='assurance','source_runtime_unmounted':True,'outbox_payloads_absent':True,'published_sha256':hashes,'original_fixture_removed':False})
 p=c.invoke('list',reader=True,label='read-only-list');check(p.returncode==0,'read-only committed-object listing failed')
 descriptors=json.loads(p.stdout);check(len(descriptors)==c.expected_records,'remote descriptor discovery incomplete')
 backup=next(d['ID'] for d in descriptors if d['Kind']=='backup');evidence=next(d['ID'] for d in descriptors if d['Kind']=='evidence')
 fetched=WORK/'fetched';fetched.mkdir(mode=0o700,exist_ok=True)
 kit=c.source/'inputs'/json.loads((c.source/'inputs/selection.json').read_text())['kit']['file']
 p=c.invoke('fetch',reader=True,extra=['-backup',backup,'-evidence',evidence,'-target','/fetched/'+variant,'-kit','/independent-kit.tar'],bindings=[(fetched,'/fetched',False),(kit,'/independent-kit.tar',True)],label='read-only-fetch')
 check(p.returncode==0,'remote-only recovery staging failed')
 retained=fetched/variant
 for part in ['trust','key']:shutil.copytree(c.source/part,retained/part)
 for name in ['drill-private.json','source-removed.json']:shutil.copyfile(c.source/name,retained/name)
 if (c.source/'research-assurance.json').exists():shutil.copyfile(c.source/'research-assurance.json',retained/'research-assurance.json')
 write(c.dir/'copy-result.json',{'variant':variant,'copy_seconds':elapsed,'fixture_objects_read_back':True,'local_runtime_payloads_removed':MODE!='assurance','real_collector_outbox':c.real_publication,'separate_read_only_fetch':True,'aws_qualified':False,'independent_host':False})
 print('Actual-image '+variant+' destination readback and read-only source-loss fetch: PASS',flush=True)
def capacity():
 # Transport-only modeled opaque bytes. Never label these native/recoverable.
 c=Case('capacity');budget=550<<20
 c.config['Budget']=budget;write(c.dir/'config.json',c.config)
 original_operations=copy.deepcopy(fixture.operations)
 template=next(op for op in original_operations if op['kind']=='backup')
 fixture.operations=[]
 for index in range(3):
  op=copy.deepcopy(template);op['id']=str(int(time.time()))+'-'+secrets.token_hex(16)
  op['accepted_at']=(datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(seconds=index)).isoformat().replace('+00:00','Z')
  archive=next(a for a in op['artifacts'] if a['kind']=='archive')
  archive['id']=secrets.token_hex(16)
  raw=bytes([index+1])*(256<<20)
  archive.update(size=len(raw),sha256=hashlib.sha256(raw).hexdigest())
  fixture.artifacts[archive['id']]=raw
  evidence=next(a for a in op['artifacts'] if a['kind']=='evidence');evidence['id']=secrets.token_hex(16)
  raw=bytes([index+4])*(16<<20)
  evidence.update(size=len(raw),sha256=hashlib.sha256(raw).hexdigest());fixture.artifacts[evidence['id']]=raw
  op['pair']['artifacts']=[copy.deepcopy(a) for a in op['artifacts'] if a['kind']!='metadata']
  meta=next(a for a in op['artifacts'] if a['kind']=='metadata');meta['id']=secrets.token_hex(16)
  raw=json.dumps({'payload':base64.b64encode(json.dumps(op['pair']).encode()).decode(),'mac':'0'*64}).encode()
  meta.update(size=len(raw),sha256=hashlib.sha256(raw).hexdigest());fixture.artifacts[meta['id']]=raw
  fixture.operations.append(op)
 bindings=[(c.dir/'published','/published',False),(c.dir/'queue','/queue',False),(c.dir/'collector.json','/collector.json',True),(WORK/'tls.crt','/ca.crt',True)]
 def collect(label):
  return container(bindings,'voptrack-collector',['-once','-origin',ORIGIN,'-ca','/ca.crt','-credential','/collector.json','-bundles','/published','-outbox','/queue','-outbox-budget',str(budget),'-keep','1'],c.dir/(label+'.log'))
 previous={str(p.relative_to(c.dir/'published')):hashlib.sha256(p.read_bytes()).hexdigest() for p in (c.dir/'published').glob('*/backup.tar')}
 check(collect('queue-full').returncode!=0,'capacity limit did not refuse capture')
 pending=c.pending();large=[p for p in pending if p.stat().st_size>272<<20]
 check(len(large)==2 and sum(p.stat().st_size for p in pending)<=budget,'queue limit evicted or exceeded obligations')
 # Existing evidence download must survive failed backup capture; normal keep=1
 # may rotate the previous backup only after a new usable backup is published.
 check(any('/'+name in str(p) and hashlib.sha256(p.read_bytes()).hexdigest()==sha for name,sha in previous.items() for p in (c.dir/'published').glob('*/backup.tar')),'queue full removed every previous usable download')
 before={p.parent.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in pending}
 fixture.fault='unavailable';start=time.monotonic()
 check(c.copy('destination-outage').returncode!=0,'outage reported success')
 check(before=={p.parent.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in c.pending()},'outage changed pending bytes')
 outage_seconds=time.monotonic()-start;fixture.fault='';start=time.monotonic()
 check(c.copy('restart-drain').returncode==0 and not c.pending(),'restart failed to drain exact backlog')
 drain_seconds=time.monotonic()-start
 write(c.dir/'capacity-operations.json',fixture.operations)
 collected=collect('capture-after-drain')
 check(collected.returncode==0,'freed budget did not allow refused capture')
 check(len(c.pending())==1,'retry duplicated or lost obligations')
 check(c.copy('final-drain').returncode==0 and not c.pending(),'final backlog did not drain')
 p=c.invoke('list',reader=True,label='capacity-read-only-list')
 check(p.returncode==0 and len(json.loads(p.stdout))==7,'backlog committed descriptors missing or duplicated')
 descriptors=json.loads(p.stdout)
 commits=[json.loads(raw) for key,raw in fixture.objects.items() if key.startswith(c.config['Prefix']) and '/commits/' in key]
 obligations=[json.loads(p.read_text()) for p in (c.dir/'queue').glob('*/obligation.json')]
 check(len(commits)==7 and all(any(d['ID']==hashlib.sha256(o['Manifest']['operation']['id'].encode()).hexdigest() and d['Kind']==o['Manifest']['operation']['kind'] and d['Payload']['SHA256']==o['SHA256'] and d['Payload']['Size']==o['Size'] for d in commits) for o in obligations),'committed backlog differs from captured bytes')
 check(sum(d['Payload']['Size'] for d in commits if d['Payload']['Size']>272<<20)>816<<20,'near-limit objects not committed')
 sys.path.insert(0,str(REPO/'release/recovery-assurance'))
 from capacity import verify_capacity_binding
 check(verify_capacity_binding(WORK)['large_bundles']==3,'capacity retained provenance incomplete')
 write(WORK/'capacity-result.json',{'passed':True,'native_authenticity_qualified':False,'synthetic_transport_only':True,'large_bundles':3,'archive_bytes_each':256<<20,'evidence_bytes_each':16<<20,'queue_budget':budget,'outage_seconds':outage_seconds,'first_drain_seconds':drain_seconds,'worker_memory_limit_bytes':384<<20,'worker_cpus':2,'queue_exhaustion_refused':True,'outage_preserved_pending':True,'restart_drained':True,'remote_deletion':False})
 print('Actual-image near-limit transport, queue exhaustion, outage and restart drain: PASS',flush=True)
def local_replay():
 c=Case('publication-ack')
 before={str(p.relative_to(c.dir)):hashlib.sha256(p.read_bytes()).hexdigest() for base in ['published','queue'] for p in (c.dir/base).glob('*/backup.tar')}
 for p in (c.dir/'queue').glob('*/obligation.json'):
  r=json.loads(p.read_text());r['LocalPublished']=False;write(p,r)
 fixture.operations=[]
 p=container([(c.dir/'published','/published',False),(c.dir/'queue','/queue',False),(c.dir/'collector.json','/collector.json',True),(WORK/'tls.crt','/ca.crt',True)],'voptrack-collector',['-once','-origin',ORIGIN,'-ca','/ca.crt','-credential','/collector.json','-bundles','/published','-outbox','/queue','-keep','1'],c.dir/'publication-ack-restart.log')
 check(p.returncode==0,'published-before-ack restart refused exact existing target')
 check(all(json.loads(p.read_text())['LocalPublished'] for p in (c.dir/'queue').glob('*/obligation.json')),'local replay acknowledgement not durable')
 check(all((c.dir/k).exists() and hashlib.sha256((c.dir/k).read_bytes()).hexdigest()==v for k,v in before.items()),'local replay altered existing exact bytes')
 print('Actual-image publication-before-ack reconciliation without catalog: PASS',flush=True)
def binding():
 c=Case('binding')
 check(c.copy('initial-copy').returncode==0,'binding baseline copy failed')
 c.config['ExpectedBucketOwner']='111111111111';write(c.dir/'config.json',c.config)
 check(c.copy('owner-change').returncode!=0,'bucket owner changed without state refusal')
 c.config['ExpectedBucketOwner']='000000000000'
 # Preserve both old directories. A fresh state/outbox may not inherit public
 # success belonging to a different configured destination.
 (c.dir/'state').rename(c.dir/'previous-state');private(c.dir/'state')
 (c.dir/'queue').rename(c.dir/'previous-queue');private(c.dir/'queue')
 c.config['Prefix']='test/rebound/';c.config['Label']='Different synthetic destination';write(c.dir/'config.json',c.config)
 check(c.copy('fresh-destination').returncode==0,'fresh unprotected destination status failed')
 status=json.loads((c.dir/'status/off-machine.json').read_text())
 check(status['State']=='unprotected' and status['LastBackup'].startswith('0001-'),'public success rebound to different destination')
 print('Actual-image destination account binding and public-status separation: PASS',flush=True)
def binding_loss():
 for mode in ['missing','symlink']:
  c=Case('binding-'+mode);preserved=c.dir/'payload-preserved';private(preserved)
  for p in c.pending():shutil.copyfile(p,preserved/p.parent.name)
  check(c.copy('initial-copy').returncode==0,'binding-loss baseline copy failed')
  for p in preserved.iterdir():shutil.copyfile(p,c.dir/'queue'/p.name/'backup.tar')
  binding=c.dir/'state/binding.json';binding.rename(c.dir/'state/binding-preserved.json')
  if mode=='symlink':binding.symlink_to('binding-preserved.json')
  if mode=='missing':c.config['ExpectedBucketOwner']='111111111111';write(c.dir/'config.json',c.config)
  before={p.parent.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in c.pending()}
  check(c.copy('binding-refused').returncode!=0,'missing or indirect binding admitted existing acknowledgements')
  check(len(c.pending())==4 and all(hashlib.sha256((c.dir/'queue'/k/'backup.tar').read_bytes()).hexdigest()==v for k,v in before.items()),'binding-loss refusal changed pending bytes')
  if binding.is_symlink():binding.rename(c.dir/'state/binding-link-preserved')
  (c.dir/'state/binding-preserved.json').rename(binding)
  c.config['ExpectedBucketOwner']='000000000000';write(c.dir/'config.json',c.config)
  check(c.copy('binding-restored-retry').returncode==0 and not c.pending(),'exact original binding could not resume acknowledgement')
 print('Actual-image lost/indirect binding refusal, pending preservation and original-binding retry: PASS',flush=True)
def outbox_barrier():
 c=Case('outbox-barrier')
 before=fingerprint(c.dir/'queue')
 (c.dir/'queue').chmod(0o300)
 try:
  # Only existing public bundles and outbox are mounted. No cloud, native
  # retrieval, kit, key or administrator credential is available to this helper.
  p=container([(c.dir/'published','/published',False),(c.dir/'queue','/queue',False)],'voptrack-collector',['-mode','queue','-bundles','/published','-outbox','/queue'],c.dir/'barrier-refused.log')
  check(p.returncode!=0,'outbox observation bypassed failed parent sync')
 finally:(c.dir/'queue').chmod(0o700)
 check(before==fingerprint(c.dir/'queue'),'barrier refusal modified durable obligations')
 p=container([(c.dir/'published','/published',False),(c.dir/'queue','/queue',False)],'voptrack-collector',['-mode','queue','-bundles','/published','-outbox','/queue'],c.dir/'barrier-intact-retry.log')
 check(p.returncode==0,'outbox could not resume after directory barrier restored')
 check(len(c.pending())==4,'outbox barrier retry lost pending bytes')
 print('Actual-image outbox directory barrier failure/refusal and intact retry: PASS',flush=True)
def interruptions():
 intact=os.environ.get('VOPTRACK_OFF_MACHINE_INTACT_IMAGE')
 check(intact and intact.startswith('sha256:'),'intact restart image required')
 check(docker('run','--rm','--network','none','--tmpfs','/var/lib/tessera','--entrypoint','tesserad',intact,'-version').stdout.decode().strip()==PIN,'intact restart pin mismatch')
 for phase in ['pending','uploading','verifying','committing','verified']:
  c=Case('interrupt-'+phase);(c.dir/'stop-phase').write_text(phase)
  check(c.copy('interrupted-copy').returncode==75,'worker did not stop after intended durable stage')
  check(len(c.pending())==4,'durable interruption prematurely released queue bytes')
  before={k:hashlib.sha256(v).hexdigest() for k,v in fixture.objects.items() if k.startswith(c.config['Prefix'])}
  (c.dir/'stop-phase').rename(c.dir/'stop-phase-preserved')
  check(c.copy('intact-restart',image=intact).returncode==0,'intact transfer restart failed')
  check(not c.pending(),'intact restart did not reconcile acknowledgement')
  check(all(hashlib.sha256(fixture.objects[k]).hexdigest()==v for k,v in before.items()),'restart overwrote prior remote bytes')
  p=c.invoke('list',reader=True,label='reconciled-list',image=intact)
  check(p.returncode==0 and len(json.loads(p.stdout))==4,'restart duplicated or omitted remote commits')
 print('Actual-image worker interruption and intact restart at five durable boundaries: PASS',flush=True)
try:
 if MODE=='capacity':capacity()
 elif MODE=='readback':faults()
 elif MODE=='conflict':conflict()
 elif MODE=='handoff':Case('handoff');print('Actual-image capture before local rotation: PASS',flush=True)
 elif MODE=='binding':binding()
 elif MODE=='local-replay':local_replay()
 elif MODE=='binding-loss':binding_loss()
 elif MODE=='outbox-barrier':outbox_barrier()
 elif MODE=='interruptions':interruptions()
 else:
  local_replay();faults();conflict();binding();binding_loss();outbox_barrier();positive('household');positive('research')
  env=dict(os.environ,VOPTRACK_IMAGE=IMAGE,VOPTRACK_GUIDED_INPUTS=str(WORK/'fetched'),VOPTRACK_GUIDED_DIR=str(WORK/'guided'))
  with (WORK/'guided.log').open('w') as log:p=subprocess.run(['make','guided-recovery-check'],cwd=REPO,env=env,stdout=log,stderr=subprocess.STDOUT)
  check(p.returncode==0,'destination-fetched guided recovery failed; private evidence retained')
  check(not any(e['method']=='HEAD' for e in fixture.events),'HEAD used as integrity proof')
  print('Fetched Home/Research inputs pass protected guided recovery and live correctness: PASS',flush=True)
finally:
 server.shutdown();server.server_close();thread.join()
 write(WORK/'events.json',fixture.events)
 ids=docker('ps','-aq','--filter','label=voptrack.off-machine='+PROJECT).stdout.decode().split()
 if ids:docker('rm','-fv',*ids,ok=False)
 run([sys.executable,str(REPO/'release/recovery-set/inventory.py'),str(WORK/'after.json')])
 check(original==fingerprint(INPUTS),'retained qualification source fixture was modified')
write(WORK/'result.json',{'passed':True,'mode':MODE,'image':IMAGE,'local_synthetic_only':True,'aws_qualified':False,'separate_host_qualified':False,'physical_independence_attested':False})
