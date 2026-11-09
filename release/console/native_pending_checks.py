"""Crash an observed pending settings command in the disposable producer only."""
import json,os,pathlib,secrets,subprocess,time

def verify(g,dc,admin):
 api=g['api'];work=pathlib.Path(os.environ['VOPTRACK_GATE_WORK'])
 status,body,_=api(admin,'GET','/admin/api/v1/backup-scheduler');before=json.loads(body)
 if status!=200 or before['pending_request_id']:raise RuntimeError('pending-restart fixture not idle')
 identifier=before['settings_scope']+'.'+str(int(time.time()))+'-'+secrets.token_hex(16)
 payload={'expected_revision':before['settings']['revision'],'every_hours':before['settings']['every_hours'],'keep':before['settings']['keep']}
 record=work/'pending-settings-command.json'
 with record.open('x') as f:
  os.chmod(record,0o600);json.dump({'id':identifier,'payload':payload},f);f.flush();os.fsync(f.fileno())
 directory=os.open(work,os.O_RDONLY);os.fsync(directory);os.close(directory)
 container=dc('ps','-q','tesserad').decode().strip()
 def docker(*args):
  p=subprocess.run(['docker',*args],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
  if p.returncode:raise RuntimeError('pending-restart host operation failed')
 try:
  # A narrow CPU quota makes the existing 64 MiB fixture hold acquisition long
  # enough to observe pending before abrupt death. No existing service is touched.
  docker('update','--cpus=0.1','--restart=no',container)
  acquisition=str(int(time.time()))+'-'+secrets.token_hex(16)
  if api(admin,'PUT','/admin/api/v1/backup-operations/'+acquisition,{'kind':'backup'})[0]!=202:raise RuntimeError('pending-restart acquisition not admitted')
  path='/admin/api/v1/backup-settings-operations/'+identifier
  if api(admin,'PUT',path,payload)[0]!=202:raise RuntimeError('pending command not accepted')
  status,body,_=api(admin,'GET',path)
  if status!=200 or json.loads(body)['state']!='pending':raise RuntimeError('fixture failed to observe pending before death')
  observed=json.loads(api(admin,'GET','/admin/api/v1/backup-scheduler')[1])
  if observed['next_attempt_at'] is not None or observed['next_reason']!='settings_pending':raise RuntimeError('pending nullable-next contract failed')
  docker('kill',container)
 finally:
  docker('update','--cpus=0','--restart=unless-stopped',container)
  dc('up','-d','--no-deps','--wait','tesserad')
 for _ in range(200):
  status,body,_=api(admin,'GET',path);op=json.loads(body)
  if status==200 and op['state']=='effective':break
  time.sleep(.03)
 else:raise RuntimeError('pending command lost across process death')
 if any(op.get(k)!=v for k,v in payload.items()) or op['id']!=identifier:raise RuntimeError('restart replaced pending command binding')
 after=json.loads(api(admin,'GET','/admin/api/v1/backup-scheduler')[1])
 if after['settings']['revision']!=op['revision'] or after['settings_scope']!=before['settings_scope'] or after['instance_id']==before['instance_id']:raise RuntimeError('restart did not reconcile accepted policy in same settings epoch')
 status,page,_=g['request'](g['owner'],'GET','/backups')
 if status!=200 or 'data-kit-state="stale"' not in page:raise RuntimeError('effective revision left previous kit current')
 # Preserve both the host copy and the immutable kit revision when preparing anew.
 old=pathlib.Path(g['_approved_kit']);saved=old.with_name('approved-kit-before-live.tar');old.rename(saved);g['_approved_kit']=str(saved)
 from recovery_kit_checks import verify as prepare_current
 prepare_current(g,dc)
 g['say']('pending live request across abrupt restart','same durable binding effective; nullable next observed; earlier kit stale and retained; current export prepared')
