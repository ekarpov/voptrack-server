"""Test-only ptrace fault injection into unchanged native settings persistence."""
import json,os,pathlib,secrets,subprocess,time

def verify(g,dc,admin):
 api=g['api'];work=pathlib.Path(os.environ['VOPTRACK_GATE_WORK'])
 source=pathlib.Path(__file__).resolve().parents[1]/'native-settings'/'persistfault'
 override=work/'compose.override.yaml';control=work/'native-persistfault-control'
 if override.exists() or control.exists():raise RuntimeError('persistence fixture path already occupied')
 control.mkdir(mode=0o755);mode=control/'mode';mode.write_text('off\n');mode.chmod(0o644)
 binary=control/'trace'
 subprocess.run(['cc','-static','-O2','-Wall','-Wextra','-Werror',str(source/'trace.c'),'-o',str(binary)],check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 binary.chmod(0o755)  # Container-executable test tracer, independent of host umask.
 def observe():
  st,raw,_=api(admin,'GET','/admin/api/v1/backup-scheduler')
  if st!=200:raise RuntimeError('native persistence scheduler observation unavailable')
  return json.loads(raw)
 def idle():
  for _ in range(400):
   o=observe()
   if o['available'] and not o['pending_request_id'] and not o['active_scheduled_operation']:return o
   time.sleep(.05)
  raise RuntimeError('native persistence fixture did not become idle')
 def binding(o,every,keep):
  return o['settings_scope']+'.'+str(int(time.time()))+'-'+secrets.token_hex(16),{'expected_revision':o['settings']['revision'],'every_hours':every,'keep':keep}
 def effective(identifier,payload):
  path='/admin/api/v1/backup-settings-operations/'+identifier
  for _ in range(400):
   st,raw,_=api(admin,'GET',path)
   if st==200 and json.loads(raw)['state']=='effective':
    op=json.loads(raw)
    if op['id']!=identifier or any(op[k]!=v for k,v in payload.items()):raise RuntimeError('native persistence recovery changed request binding')
    return op
   time.sleep(.05)
  raise RuntimeError('native persistence same-ID request never effective')
 original=idle()['settings'].copy()
 try:
  # No scheduler/acquisition traffic may compete for shared atomicJSON temp names.
  before=idle();identifier,payload=binding(before,0,before['settings']['keep'])
  if api(admin,'PUT','/admin/api/v1/backup-settings-operations/'+identifier,payload)[0] not in (200,202):raise RuntimeError('fault fixture scheduling disable refused')
  effective(identifier,payload);idle()
  override.write_text(json.dumps({'services':{'tesserad':{'entrypoint':['/fault-trace','/fault-mode','/var/lib/voptrack-backups/.acquisitions','/usr/local/bin/tesserad'],'command':['-config','/var/lib/voptrack-config/server.json'],'volumes':[str(binary)+':/fault-trace:ro',str(mode)+':/fault-mode:ro']}}}))
  for stage,ordinal in (('temp',1),('temp',2),('directory',1),('directory',2)):
   mode.write_text('off\n');dc('up','-d','--no-deps','--force-recreate','--wait','tesserad');before=idle()
   identifier,payload=binding(before,0,before['settings']['keep']+1)
   path='/admin/api/v1/backup-settings-operations/'+identifier
   mode.write_text(('fail-dir' if stage=='directory' else 'fail')+' '+str(ordinal)+' '+secrets.token_hex(8)+'\n')
   st,raw,_=api(admin,'PUT',path,payload)
   if ordinal==1:
    if st!=503 or json.loads(raw).get('error',{}).get('details',{}).get('reason')!='settings_unavailable':raise RuntimeError('native admission persistence failure did not report uncertainty')
   elif st!=202:raise RuntimeError('native effective-write fixture admission not acknowledged pending')
   for _ in range(200):
    failed=observe()
    if failed['reason']=='settings_unavailable':break
    time.sleep(.05)
   else:raise RuntimeError('native persistence error did not degrade settings observation')
   if failed.get('settings') is not None or failed.get('settings_export') is not None:raise RuntimeError('native persistence uncertainty exposed authoritative policy')
   if api(admin,'GET',path)[0]!=503:raise RuntimeError('native persistence lookup invented a resolved outcome')
   logs=dc('logs','--no-color','tesserad')
   marker=('persistfault EIO journal-'+stage+' sync ordinal='+str(ordinal)).encode()
   if logs.count(marker)!=1:raise RuntimeError('native persistence scenario did not observe exactly one targeted injection')
   mode.write_text('off\n');dc('up','-d','--no-deps','--force-recreate','--wait','tesserad');after=idle()
   if after['settings_scope']!=before['settings_scope']:raise RuntimeError('native persistence restart changed epoch')
   if ordinal==1 and stage=='temp':
    if after['settings']!=before['settings'] or api(admin,'GET',path)[0]!=404:raise RuntimeError('failed pre-rename admission altered durable acknowledged policy')
    if api(admin,'PUT',path,payload)[0] not in (200,202):raise RuntimeError('same uncertain request identity failed reconciliation')
   elif stage=='directory':
    # Rename already happened before directory fsync failed. A 503 must not be
    # converted into a fabricated failed outcome: observe/replay the exact ID.
    status,raw,_=api(admin,'GET',path)
    if status not in (200,404):raise RuntimeError('directory persistence recovery unresolved public readback')
    if status==200:
     recovered=json.loads(raw)
     if recovered['id']!=identifier or any(recovered[k]!=v for k,v in payload.items()):raise RuntimeError('directory persistence recovery replaced binding')
    if api(admin,'PUT',path,payload)[0] not in (200,202):raise RuntimeError('directory persistence same-ID replay refused')
   op=effective(identifier,payload)
   if api(admin,'PUT',path,payload)[0]!=200:raise RuntimeError('native persistence same-ID effective replay changed outcome')
   if observe()['settings']['revision']!=op['revision']:raise RuntimeError('native persistence recovered effective acknowledgement differs')
  g['say']('native settings persistence failures','targeted admission/effective temp-file and post-rename directory fsync EIO; uncertainty and null policy; unchanged epoch; exact same-ID/payload restart reconciliation')
 finally:
  mode.write_text('off\n')
  if override.exists():override.unlink()
  dc('up','-d','--no-deps','--force-recreate','--wait','tesserad')
  # Restore policy through public commands, never edit native private journals.
  current=idle();identifier,payload=binding(current,original['every_hours'],original['keep'])
  if api(admin,'PUT','/admin/api/v1/backup-settings-operations/'+identifier,payload)[0] not in (200,202):raise RuntimeError('native persistence fixture policy restore failed')
  effective(identifier,payload);mode.unlink();binary.unlink();control.rmdir()
