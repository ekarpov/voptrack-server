"""Actual-image live-policy overlap probes; public operations only.
No throughput promise; each admitted update has a retained exact identity.
"""
import hashlib,html,json,os,secrets,subprocess,time

def verify(g,dc,admin,phase):
 api,fail=(g[n] for n in ('api','fail'))
 def observe():
  status,raw,_=api(admin,'GET','/admin/api/v1/backup-scheduler')
  if status!=200:fail('overlap scheduler observation unavailable')
  return json.loads(raw)
 def update(hours,keep,overlap=None):
  before=observe();identifier=before['settings_scope']+'.'+str(int(time.time()))+'-'+secrets.token_hex(16)
  payload={'expected_revision':before['settings']['revision'],'every_hours':hours,'keep':keep}
  path='/admin/api/v1/backup-settings-operations/'+identifier
  status,raw,_=api(admin,'PUT',path,payload)
  if status not in (200,202):fail('overlap live admission refused')
  if overlap:overlap()
  for _ in range(600):
   status,raw,_=api(admin,'GET',path);op=json.loads(raw)
   if status==200 and op['state']=='effective':break
   if status==200 and op['state']=='failed':fail('overlap admitted update failed')
   time.sleep(.02)
  else:fail('overlap live application stalled')
  after=observe()
  if after['settings']['every_hours']!=hours or after['settings']['keep']!=keep:fail('overlap effective policy differs')
  if api(admin,'PUT',path,payload)[0]!=200:fail('overlap same-ID effective replay failed')
  return op
 def wait_operation(identifier):
  for _ in range(2000):
   status,raw,_=api(admin,'GET','/admin/api/v1/backup-operations/'+identifier)
   operation=json.loads(raw) if status==200 else {}
   if operation.get('state')=='complete':return operation
   if operation.get('state') in ('failed','cancelled','expired'):fail('overlap acquisition failed')
   time.sleep(.01)
  fail('overlap acquisition stalled')
 def create(kind):
  identifier=str(int(time.time()))+'-'+secrets.token_hex(16)
  if api(admin,'PUT','/admin/api/v1/backup-operations/'+identifier,{'kind':kind})[0]!=202:fail('overlap acquisition admission failed')
  return identifier
 # Larger producer keep prevents this independent matrix from disturbing the
 # earlier keep=2 pin/rotation assertions. Return to the product policy below.
 update(12,8)
 from producer_stage_checks import stage_producer
 with stage_producer(g,dc,admin) as stage:
  for kind in ('backup','evidence'):
   def acquisition_window(kind=kind):
    stage.arm()
    identifier=create(kind)
    stage.paused()
    status,raw,_=api(admin,'GET','/admin/api/v1/backup-operations/'+identifier)
    if status!=200 or json.loads(raw)['state']!='running':fail(kind+' finished before live overlap observation')
    def still_running():
     status,raw,_=api(admin,'GET','/admin/api/v1/backup-operations/'+identifier)
     if status!=200 or json.loads(raw)['state']!='running':fail(kind+' did not overlap admitted live update and device progress')
    def admitted_window():
     still_running()
     def progress():
      g['_online_progress_until'](still_running)
     try:phase('admitted live policy at '+kind+' publication boundary',progress)
     finally:stage.release()
    update(13,9,admitted_window)
    result=wait_operation(identifier)
    update(12,8)
    return result
   acquisition_window()
  def scheduled_window():
   update(0,8) # Test-only public disable/enable; Console still refuses zero.
   stage.arm()
   update(12,8)
   stage.paused()
   for _ in range(600):
    active=observe().get('active_scheduled_operation')
    if active:break
    time.sleep(.01)
   else:fail('scheduled overlap not observed active')
   def still_active():
    if observe().get('active_scheduled_operation')!=active:fail('scheduled operation did not overlap admitted live update and device progress')
   def admitted_window():
    still_active()
    def progress():
     g['_online_progress_until'](still_active)
    try:phase('admitted live policy at scheduled publication boundary',progress)
    finally:stage.release()
   update(13,9,admitted_window)
   # Scheduler publishes process history independently of manual operations.
   for _ in range(2000):
    snapshot=observe()
    if not snapshot.get('active_scheduled_operation') and snapshot.get('last_scheduled_attempt'):break
    time.sleep(.01)
   else:fail('scheduled overlap did not settle')
   update(12,8)
  scheduled_window()
 from collector_overlap_checks import verify as collection_overlap
 collection_overlap(g,dc,admin,phase,update)
 def pinned_window():
  import re
  status,page,_=g['request'](g['owner'],'GET','/backups')
  links=re.findall(r'href="(/backups/bundle/[^" ]+)"',page)
  if status!=200 or not links:fail('overlap bundle missing')
  work=__import__('pathlib').Path(os.environ['VOPTRACK_GATE_WORK']);project=os.environ['VOPTRACK_GATE_PROJECT']
  binary=work/'inode-lockprobe'
  subprocess.run(['go','build','-o',str(binary),'./release/native-settings/lockprobe'],check=True,env=dict(os.environ,CGO_ENABLED='0'))
  binary.chmod(0o755)  # Public test executable in a private fixture directory.
  selected=html.unescape(links[0]);identifier=selected.rsplit('/',1)[-1]
  def pin_held():
   result=subprocess.run(['docker','run','--rm','--network','none','--read-only','--cap-drop','ALL','--user','10001:10001','-v',project+'_bundles:/bundles:ro','-v',str(binary)+':/probe:ro','--entrypoint','/probe',os.environ['VOPTRACK_IMAGE'],'/bundles/'+identifier+'/backup.tar'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
   if result.returncode!=0:fail('Console inode pin absent during admitted live policy window')
  with g['owner'].open(g['base']+selected) as response:
   first=response.read(65536)
   if not first:fail('overlap transfer empty')
   if int(response.headers.get('Content-Length','0')) < 8<<20:fail('pinned overlap fixture too small for unread transfer')
   pin_held()
   def admitted_window():
    pin_held()
    def progress():
     g['_online_progress_until'](pin_held)
    phase('admitted live policy inside held download pin',progress)
   update(13,9,admitted_window)
   # Keep the response pinned while collecting under the effective new policy.
   dc('run','--rm','--no-deps','-T','collector','-once')
   raw=first+response.read()
  import io,tarfile
  with tarfile.open(fileobj=io.BytesIO(raw)) as bundle:
   manifest=json.load(bundle.extractfile('manifest.json'))
   for artifact in manifest['operation']['artifacts']:
    contents=bundle.extractfile(artifact['id']).read()
    if len(contents)!=artifact['size'] or hashlib.sha256(contents).hexdigest()!=artifact['sha256']:fail('live update changed pinned bundle bytes')
  update(12,2)
 pinned_window()
 g['say']('live overlap matrix','scheduled/manual/evidence acquisitions, collection and pinned immutable transfer; same-ID settlement; device progress checked in every phase')
