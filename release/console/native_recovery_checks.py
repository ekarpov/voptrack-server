"""Supported settings-bootstrap refusals; never reads or edits private Tessera state."""
import json,os,pathlib,secrets,subprocess,time

def verify(g,dc,admin):
 api=g['api'];work=os.environ['VOPTRACK_GATE_WORK'];project=os.environ['VOPTRACK_GATE_PROJECT']
 def snapshot():
  status,raw,_=api(admin,'GET','/admin/api/v1/backup-scheduler')
  if status!=200:raise RuntimeError('bootstrap observation unavailable')
  return json.loads(raw)
 before=snapshot();scope=before['settings_scope'];payload={'expected_revision':before['settings']['revision'],'every_hours':12,'keep':2}
 foreign=scope.split('.')[0]+'.'+('f'*32 if scope.split('.')[1]!='f'*32 else 'e'*32)+'.'+str(int(time.time()))+'-'+secrets.token_hex(16)
 for method,body in [('GET',None),('PUT',payload)]:
  status,raw,_=api(admin,method,'/admin/api/v1/backup-settings-operations/'+foreign,body)
  if status!=409 or json.loads(raw)['error']['details']['reason']!='request_scope_mismatch':raise RuntimeError('old settings epoch not refused')
 recent=scope+'.'+str(int(time.time()))+'-'+secrets.token_hex(16)
 expired=scope+'.'+str(int(time.time())-600)+'-'+secrets.token_hex(16)
 if api(admin,'GET','/admin/api/v1/backup-settings-operations/'+recent)[0]!=404 or api(admin,'GET','/admin/api/v1/backup-settings-operations/'+expired)[0]!=410:raise RuntimeError('unknown/expired native identity contract')
 # Compose run has no --tmpfs option: a disposable override supplies /run.
 override=pathlib.Path(work)/'compose.override.yaml'
 if override.exists():raise RuntimeError('unexpected existing recovery fixture override')
 override.write_text('services:\n  tesserad:\n    tmpfs:\n      - /run:uid=10001,mode=0700\n')
 # All existing producer processes are stopped for this explicit recovery probe.
 dc('stop','tesserad')
 empty=project+'-bootstrap-empty';name=project+'-bootstrap-refusal'
 try:
  export=json.dumps(before['settings_export']).encode()
  for trusted,fresh in [(before['settings_export']['deployment'],False),('f'*32,False),(before['settings_export']['deployment'],True)]:
   args=['docker','compose','-p',project,'run','--rm','--no-deps','-T','--name',name,'-e','VOPTRACK_TRUSTED_BACKUP_DEPLOYMENT='+trusted]
   if fresh:args+=['-v',empty+':/var/lib/voptrack-backups']
   args+=['--entrypoint','sh','tesserad','-c','umask 077; cat > /run/backup-settings-export.json; exec voptrack-deploy bootstrap-settings']
   try:
    result=subprocess.run(args,cwd=work,input=export,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=20)
    if result.returncode==0:raise RuntimeError('bootstrap adopted/overwrote unsupported state')
    if b'invalid backup settings bootstrap' in result.stdout+result.stderr:raise RuntimeError('public settings export regressed at standalone decoder')
   except subprocess.TimeoutExpired:
    raise RuntimeError('bootstrap unexpectedly started instead of refusing')
   finally:subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 finally:
  if override.exists():override.unlink()
  subprocess.run(['docker','volume','rm',empty],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  dc('up','-d','--no-deps','--wait','tesserad')
 after=snapshot()
 if after['settings']!=before['settings'] or after['settings_scope']!=scope:raise RuntimeError('refused bootstrap changed acknowledged policy')
 g['say']('settings recovery boundaries','public export passes decoder; existing journal, wrong trusted identity and fresh producer refused; prior policy unchanged; old-scope and unknown/expired IDs refused')
