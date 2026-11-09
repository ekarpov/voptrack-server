"""Bootstrap an existing alpha.35 producer through the supported alpha.37 flag.
The operator export is a synthetic trusted fixture, not claimed prior readback.
No private producer state is copied, read or edited.
"""
import json,os,pathlib,secrets,subprocess,sys,time
source=pathlib.Path(__file__).resolve().parents[1]/'console'/'drive.py'
text=source.read_text();marker='# ---- the owner invites the household from the Console'
g={'__file__':str(source),'__name__':'prejournal_helpers'}
exec(compile(text.split(marker)[0],str(source),'exec'),g)
work=pathlib.Path(os.environ['VOPTRACK_GATE_WORK']);project=os.environ['VOPTRACK_GATE_PROJECT'];image=os.environ['VOPTRACK_CANDIDATE_IMAGE']
def run(*args,check=True,timeout=None):
 p=subprocess.run(['docker','compose','-p',project,*args],cwd=work,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=timeout)
 if check and p.returncode:raise RuntimeError('pre-journal host operation failed at '+args[0])
 return p
admin=g['app_token']('owner',g['owner_pw'],{'administer':'yes'})
for _ in range(100):
 st,body,_=g['api'](admin,'GET','/admin/api/v1/backup-operations')
 results=json.loads(body).get('results',[]) if st==200 else []
 if any(o['state']=='complete' for o in results):break
 time.sleep(.05)
else:raise RuntimeError('alpha35 fixture producer incomplete')
deployment=next(o['pair']['deployment'] for o in results if o['state']=='complete')
oldscope=deployment+'.'+secrets.token_hex(16)
export={'version':1,'deployment':deployment,'every_hours':12,'keep':2,'revision':oldscope+'.'+secrets.token_hex(16),'exported_at':__import__('datetime').datetime.now(__import__('datetime').timezone.utc).isoformat()}
path=work/'operator-export.json';path.write_text(json.dumps(export));path.chmod(0o644)
run('stop','console','tesserad','collector','kit')
os.environ['VOPTRACK_IMAGE']=image
name=project+'-bootstrap'
try:
 run('run','-d','--no-deps','--use-aliases','--name',name,'-v',str(path)+':/run/backup-settings-export.json:ro','-e','VOPTRACK_TRUSTED_BACKUP_DEPLOYMENT='+deployment,'tesserad','bootstrap-settings')
 for _ in range(300):
  try:
   st,raw,_=g['api'](admin,'GET','/admin/api/v1/backup-scheduler')
   if st==200:observed=json.loads(raw);break
  except Exception:pass
  time.sleep(.1)
 else:
  state=subprocess.run(['docker','inspect','--format','{{.State.Status}}/{{.State.ExitCode}}',name],capture_output=True,text=True).stdout.strip()
  logs=subprocess.run(['docker','logs','--tail','5',name],capture_output=True,text=True)
  raise RuntimeError('supported pre-journal bootstrap did not start: '+state+'; '+logs.stderr+logs.stdout)
 if observed['settings']['every_hours']!=12 or observed['settings']['keep']!=2 or not observed['settings_scope'].startswith(deployment+'.') or observed['settings_scope']==oldscope:raise RuntimeError('bootstrap did not preserve trusted deployment and create new settings epoch')
 oldid=oldscope+'.'+str(int(time.time()))+'-'+secrets.token_hex(16)
 if g['api'](admin,'GET','/admin/api/v1/backup-settings-operations/'+oldid)[0]!=409:raise RuntimeError('bootstrap admitted old settings scope')
 revision=observed['settings']['revision']
finally:subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
# Replaying the local bootstrap cannot overwrite an existing journal.
p=run('run','--rm','--no-deps','-T','--name',name,'-v',str(path)+':/run/backup-settings-export.json:ro','-e','VOPTRACK_TRUSTED_BACKUP_DEPLOYMENT='+deployment,'tesserad','bootstrap-settings',check=False,timeout=20)
if p.returncode==0:raise RuntimeError('bootstrap replay overwrote existing journal')
run('up','-d','--no-deps','--wait','tesserad')
st,body,_=g['api'](admin,'GET','/admin/api/v1/backup-scheduler');after=json.loads(body)
if st!=200 or after['settings']['revision']!=revision or after['settings']['every_hours']!=12 or after['settings']['keep']!=2:raise RuntimeError('normal restart undid bootstrapped policy')
print('ok — alpha35 existing producer -> supported bootstrap -> new epoch; old scope/replay refused; normal restart retains effective policy over stale defaults',flush=True)
