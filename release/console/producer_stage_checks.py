"""Test-only deterministic acquisition publication-boundary overlap.
Parks one native stage-directory fsync after capture returns; all other threads run.
"""
from contextlib import contextmanager
import json,os,pathlib,secrets,subprocess,time

@contextmanager
def stage_producer(g,dc,admin):
 work=pathlib.Path(os.environ['VOPTRACK_GATE_WORK'])
 source=pathlib.Path(__file__).resolve().parents[1]/'native-settings'/'persistfault'/'trace.c'
 control=work/'producer-stage-control';override=work/'compose.override.yaml'
 if control.exists() or override.exists():raise RuntimeError('producer stage fixture path occupied')
 control.mkdir(mode=0o755);mode=control/'mode';mode.write_text('off\n');mode.chmod(0o644);binary=control/'trace'
 api=g['api'];armed=None;expected=0
 def observe():
  status,raw,_=api(admin,'GET','/admin/api/v1/backup-scheduler')
  if status!=200:raise RuntimeError('stage producer scheduler observation unavailable')
  return json.loads(raw)
 def idle():
  for _ in range(600):
   o=observe()
   if o['available'] and not o['pending_request_id'] and not o['active_scheduled_operation']:return o
   time.sleep(.05)
  raise RuntimeError('stage producer did not settle')
 def policy(hours,keep):
  before=idle();identifier=before['settings_scope']+'.'+str(int(time.time()))+'-'+secrets.token_hex(16)
  path='/admin/api/v1/backup-settings-operations/'+identifier
  payload={'expected_revision':before['settings']['revision'],'every_hours':hours,'keep':keep}
  if api(admin,'PUT',path,payload)[0] not in (200,202):raise RuntimeError('stage fixture policy admission refused')
  for _ in range(600):
   status,raw,_=api(admin,'GET',path)
   if status==200 and json.loads(raw)['state']=='effective':return
   time.sleep(.05)
  raise RuntimeError('stage fixture policy did not become effective')
 def restart():
  # Intentional test fixture maintenance lies outside measured overlap windows.
  # Drain current device calls; resume only after producer/public health returns.
  pause,resume=g.get('_online_pause'),g.get('_online_resume')
  if pause is None or resume is None:raise RuntimeError('stage producer requires explicit device maintenance barriers')
  pause()
  dc('up','-d','--no-deps','--force-recreate','--wait','tesserad')
  idle()
  resume()
 def logs():return dc('logs','--no-color','tesserad')
 class Stage:
  def arm(self):
   nonlocal armed,expected
   if armed is not None:raise RuntimeError('stage fixture already armed')
   history=logs()
   if b'persistfault stage timeout' in history:raise RuntimeError('previous acquisition stage pause expired')
   expected=history.count(b'persistfault stage paused')+1
   armed=secrets.token_hex(16);mode.write_text('pause-stage '+armed+'\n')
  def paused(self):
   if armed is None:raise RuntimeError('stage fixture not armed')
   for _ in range(600):
    history=logs()
    if b'persistfault stage timeout' in history:raise RuntimeError('acquisition stage pause expired')
    if history.count(b'persistfault stage released')>=expected:raise RuntimeError('acquisition stage already released')
    if history.count(b'persistfault stage paused')==expected:return True
    time.sleep(.025)
   raise RuntimeError('native acquisition did not reach stage publication boundary')
  def release(self):
   nonlocal armed
   if armed is None:return
   mode.write_text('release '+armed+'\n')
   for _ in range(200):
    history=logs()
    if b'persistfault stage timeout' in history:raise RuntimeError('acquisition stage pause expired instead of releasing')
    if history.count(b'persistfault stage released')==expected:
     armed=None;return
    time.sleep(.025)
   raise RuntimeError('acquisition stage release not observed')
 original=None
 try:
  subprocess.run(['cc','-static','-O2','-Wall','-Wextra','-Werror',str(source),'-o',str(binary)],check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
  binary.chmod(0o755)  # Container-executable test tracer, independent of host umask.
  original=idle()['settings'].copy();policy(0,original['keep'])
  override.write_text(json.dumps({'services':{'tesserad':{'entrypoint':['/stage-trace','/stage-mode','/var/lib/voptrack-backups/.acquisitions','/usr/local/bin/tesserad'],'command':['-config','/var/lib/voptrack-config/server.json'],'volumes':[str(binary)+':/stage-trace:ro',str(mode)+':/stage-mode:ro']}}}))
  restart()
  yield Stage()
 finally:
  if armed is not None:mode.write_text('release '+armed+'\n')
  else:mode.write_text('off\n')
  if override.exists():override.unlink()
  restart()
  if original is not None:policy(original['every_hours'],original['keep'])
  mode.unlink()
  if binary.exists():binary.unlink()
  control.rmdir()
