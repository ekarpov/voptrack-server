"""Protected first-use Home setup against an actual immutable image.
Synthetic local inputs only. All targets/logs survive; only owned processes and
network endpoints are stopped. Never accesses cloud credentials or old targets.
"""
import base64,hashlib,http.cookiejar,io,json,os,pathlib,re,secrets,socket,ssl,subprocess,sys,tarfile,time,urllib.error,urllib.parse,urllib.request

import sys as _sys
from pathlib import Path as _Path
_sys.path.insert(0, str(_Path(__file__).resolve().parents[2] / 'ci'))
from tessera_pin import expected_pin
PIN = expected_pin()
REPO=pathlib.Path(__file__).resolve().parents[2]
IMAGE=sys.argv[1];WORK=pathlib.Path(sys.argv[2]).resolve()
MODE=os.environ.get('VOPTRACK_HOME_SETUP_MODE','full')
INTACT=os.environ.get('VOPTRACK_HOME_SETUP_INTACT_IMAGE',IMAGE)
PORT=int(os.environ.get('VOPTRACK_HOME_SETUP_PORT','30693'))
ORIGIN='https://localhost:'+str(PORT);PUBLIC='https://localhost:'+str(PORT+1)
PROJECT='voptrack-home-setup-'+secrets.token_hex(5);OWNED=[]
ROOTS=['tessera','voptrack-config','voptrack-bootstrap','voptrack-console','voptrack-public','voptrack-ca','voptrack-backups']
def check(v,label):
 if not v:raise RuntimeError(label)
def write(p,v):
 p.write_text(json.dumps(v,indent=2)+'\n');p.chmod(0o600)
def private(p):p.mkdir(parents=True,mode=0o700);p.chmod(0o700)
def docker(*args,ok=True,data=None):
 p=subprocess.run(['docker',*args],capture_output=True,input=data)
 if ok:check(p.returncode==0,'qualification container operation refused')
 return p
def fingerprint(root):
 return {str(p.relative_to(root)):[p.stat().st_mode,p.stat().st_size,p.stat().st_mtime_ns,hashlib.sha256(p.read_bytes()).hexdigest()] for p in root.rglob('*') if p.is_file()}
def field(raw,name):
 m=re.search(r'name="'+name+r'"\s+value="([^"]*)"',raw.decode())
 check(m is not None,'expected protected form missing');return m.group(1)
def wait(fn,label,seconds=100):
 end=time.monotonic()+seconds
 while time.monotonic()<end:
  try:
   v=fn()
   if v:return v
  except (OSError,ValueError,urllib.error.URLError):pass
  time.sleep(.1)
 raise RuntimeError(label)
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kw):return None
class Browser:
 def __init__(self,base,cert):
  self.base=base;self.jar=http.cookiejar.CookieJar()
  self.browser=urllib.request.build_opener(urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(cert))),NoRedirect,urllib.request.HTTPCookieProcessor(self.jar))
 def call(self,path='/',form=None,headers=None,body=None):
  h=dict(headers or {});data=body
  if form is not None:
   data=urllib.parse.urlencode(form).encode();h['Content-Type']='application/x-www-form-urlencoded';h.setdefault('Origin',self.base)
  req=urllib.request.Request(self.base+path,data=data,headers=h,method='POST' if data is not None else 'GET')
  try:
   with self.browser.open(req,timeout=100) as r:return r.status,r.read(),r.headers
  except urllib.error.HTTPError as e:return e.code,e.read(),e.headers
 def post(self,path,csrf,**kw):return self.call(path,dict(csrf=csrf,lang='en',**kw))
class Case:
 def __init__(self,name,image=IMAGE):
  self.dir=WORK/name;private(self.dir);self.image=image;self.name=name;self.containers=[]
  self.state=self.dir/'state';private(self.state)
  self.vol=self.dir/'volumes'
  for n in ROOTS+['voptrack-runtime']:private(self.vol/n)
 def mounts(self,all=True):
  names=ROOTS if all else ['tessera','voptrack-config','voptrack-public','voptrack-backups','voptrack-runtime']
  return [x for n in names for x in ['-v',str(self.vol/n)+':/var/lib/'+n]]
 def command(self,entry,args=(),detach=False,stateonly=False,image=None,extra=(),variant='household',data=None):
  name=PROJECT+'-'+self.name+'-'+secrets.token_hex(3)
  mounts=['-v',str(self.state)+':/var/lib/voptrack-setup']+( [] if stateonly else self.mounts(entry!='voptrack-deploy' or args!=('serve',)))
  opts=['--tmpfs',f'/var/lib/tessera:uid={os.getuid()},mode=0700'] if stateonly else []
  env={'VOPTRACK_VARIANT':variant,'VOPTRACK_DOMAIN':'localhost','VOPTRACK_PUBLIC_URL':PUBLIC,'VOPTRACK_SETUP_ORIGIN':ORIGIN,'VOPTRACK_TLS':'local','VOPTRACK_PROXY_ADDRESS':'127.0.0.1','VOPTRACK_BACKUP_EVERY_HOURS':'24','VOPTRACK_BACKUP_KEEP':'7'}
  flags=[x for k,v in env.items() for x in ['-e',k+'='+v]]
  p=docker('run',*(['-d'] if detach else (['-i'] if data is not None else [])),'--name',name,'--log-driver','none','--label','voptrack.home-setup='+PROJECT,'--network',PROJECT,'--user',f'{os.getuid()}:{os.getgid()}','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--tmpfs',f'/tmp:uid={os.getuid()},mode=0700',*opts,*mounts,*flags,*extra,'--entrypoint','/usr/local/bin/'+entry,image or self.image,*args,ok=False,data=data)
  OWNED.append(name);self.containers.append(name)
  if not detach:
   (self.dir/(name+'.log')).write_bytes(b'private command output suppressed\n' if args in [('access',),('take-backup',),('recovery-kit',)] else p.stdout+p.stderr)
  check(p.returncode==0 or not detach,'setup process did not start')
  return name if detach else p
 def start(self,**kw):
  self.daemon=self.command('voptrack-setup',('serve',),True,extra=('-p',f'127.0.0.1:{PORT}:9673',*kw.pop('extra',())),**kw)
  wait(lambda:(self.state/'tls.crt').exists(),'setup certificate not created')
  self.browser=Browser(ORIGIN,self.state/'tls.crt')
  wait(lambda:self.browser.call()[0]==200,'setup listener not available')
 def journal(self):return json.loads((self.state/'journal.json').read_text())
 def phase(self):return self.journal()['Phase']
 def login(self):
  p=self.command('voptrack-setup',('access',),stateonly=True)
  check(p.returncode==0,'host access unavailable');grant=json.loads(p.stdout)
  # The command is explicitly private; do not persist its output in ordinary logs.
  (self.dir/(self.containers[-1]+'.log')).write_text('private host access succeeded; output suppressed\n')
  check(hashlib.sha256(ssl.PEM_cert_to_DER_cert((self.state/'tls.crt').read_text())).hexdigest()==grant['certificate_sha256'],'host fingerprint mismatch')
  page=self.browser.call()[1];check(self.browser.post('/auth',field(page,'csrf'),code=grant['code'])[0]==303,'host code refused')
  return grant
 def settings(self,mode='public'):
  page=self.browser.call()[1];csrf=field(page,'csrf')
  check(self.browser.post('/settings',csrf,name='Synthetic Home '+mode,timezone='Europe/Helsinki',mode=mode)[0]==400,'missing encryption attestation admitted')
  check(self.browser.post('/settings',csrf,name='Synthetic Home '+mode,timezone='Europe/Helsinki',mode=mode,encrypted='yes')[0]==303,'valid choices refused')
  page=self.browser.call()[1]
  return {'csrf':field(page,'csrf'),'lang':'en','binding':field(page,'binding'),'confirm':field(page,'confirm'),'password':'Synthetic-owner-pass-'+self.name,'repeat':'Synthetic-owner-pass-'+self.name}
 def stop(self,name=None):
  name=name or self.daemon;docker('stop','-t','5',name,ok=False)
 def native(self,mode,password=None):
  self.server=self.command('voptrack-deploy',('serve',),True,extra=('-p',f'127.0.0.1:{PORT+1}:8443','--network-alias','tesserad'))
  b=Browser(PUBLIC,self.vol/'voptrack-public/server.crt')
  wait(lambda:b.call('/.well-known/tessera')[0]==200,'native server did not start')
  product=json.loads((self.vol/'voptrack-public/voptrack.json').read_text())
  config=json.loads((self.vol/'voptrack-config/server.json').read_text())
  check(product['product']=='home' and product['timezone']=='Europe/Helsinki' and product['web_interface']==(mode!='off'),'saved product choices not applied')
  check(config['display_name']=='Synthetic Home '+mode,'native display name not applied')
  def signin(password):
   redirect='com.voptrack.home-setup-gate:/callback'
   st,raw,_=b.call('/oauth/register',headers={'Content-Type':'application/json'},body=json.dumps({'client_name':'Synthetic setup qualification','redirect_uris':[redirect],'token_endpoint_auth_method':'none','grant_types':['authorization_code'],'response_types':['code']}).encode())
   check(st==201,'native registration refused')
   client=json.loads(raw)['client_id'];verifier=secrets.token_urlsafe(48);challenge=base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
   query=urllib.parse.urlencode(dict(response_type='code',client_id=client,redirect_uri=redirect,state='synthetic',code_challenge=challenge,code_challenge_method='S256', prompt='consent'))
   st,page,_=b.call('/oauth/authorize?'+query);check(st==200,'native sign-in page missing')
   return b.call('/oauth/authorize',dict(flow=field(page,'flow'),csrf=field(page,'csrf'),username='owner',password=password,decision='continue'))
  old=(self.vol/'voptrack-bootstrap/owner-password').read_text().strip()
  status,page,headers=signin(old)
  check(status!=200 or b'name="password"' in page,'retired bootstrap password authenticated')
  status,page,headers=signin(password or 'Synthetic-owner-pass-'+self.name)
  check(status==200 and b'name="password"' not in page,'chosen owner password did not authenticate')
  check((self.vol/'voptrack-console/home.secret').exists()==(mode!='off'),'app-only Home service authority changed')
  check((self.vol/'voptrack-console/console-state.secret').exists()==(mode!='off'),'app-only Console authority changed')
  public=(self.vol/'voptrack-public/console-public.caddy').read_text()
  private=(self.vol/'voptrack-public/console-private.caddy').read_text()
  check(('respond ' in public)==(mode!='public') and ('respond ' in private)==(mode=='off'),'mode routing did not follow persisted choices')
  self.stop(self.server)
def auth_checks(c):
 grant=c.login();page=c.browser.call()[1];csrf=field(page,'csrf')
 stranger=Browser(ORIGIN,c.state/'tls.crt');stranger.call();p=stranger.call()[1]
 check(stranger.post('/auth',field(p,'csrf'),code=grant['code'])[0]==403,'used host code admitted another visitor')
 check(c.browser.call('/settings',dict(csrf=csrf,lang='en',name='Synthetic',timezone='UTC',mode='public',encrypted='yes'),headers={'Origin':'https://wrong.invalid'})[0]==403,'cross-origin mutation admitted')
 check(c.browser.post('/settings','wrong',name='Synthetic',timezone='UTC',mode='public',encrypted='yes')[0]==403,'CSRF admitted')
 check(c.phase()=='draft','unauthorized visitor changed target state')
def normal(name,mode):
 c=Case(name);c.start();auth_checks(c)
 form=c.settings(mode)
 bad=dict(form,binding='substituted')
 check(c.browser.call('/apply',bad)[0]==409,'changed review binding admitted')
 # Refusal must not consume the valid confirmation.
 check(c.browser.call('/apply',form)[0]==303,'confirmed initialization refused')
 wait(lambda:c.phase() in ('ready','stopped'),'native initialization did not finish',200)
 check(c.phase()=='ready','native initialization stopped; private targets retained')
 # A replay cannot initialize the owner twice.
 check(c.browser.call('/apply',form)[0]==409,'initialization confirmation replay admitted')
 guard=c.command('voptrack-deploy',('serve',),detach=True)
 time.sleep(2)
 running=docker('inspect','--format','{{.State.Running}}',guard).stdout.strip()==b'true'
 c.stop(guard)
 check(not running,'service started before setup completion')
 csrf=field(c.browser.call()[1],'csrf')
 check(c.browser.post('/finish',csrf,custody='yes',kitsha='wrong')[0]==409,'completion bypassed material custody')
 st,kit,_=c.browser.post('/kit',csrf);check(st==200,'initial material export failed')
 (c.dir/'initial-material.tar').write_bytes(kit);(c.dir/'initial-material.tar').chmod(0o600)
 with tarfile.open(fileobj=io.BytesIO(kit)) as archive:
  names=archive.getnames()
  check('var/lib/voptrack-config/backup.key' in names,'initial export lost backup key')
  check('var/lib/voptrack-bootstrap/home-setup.json' in names,'initial export lost saved choices')
  check(not any('voptrack-setup/' in n for n in names),'setup grant exported with material')
 keypath=c.vol/'voptrack-config/backup.key';originalkey=keypath.read_bytes()
 keypath.write_bytes(bytes([originalkey[0]^1])+originalkey[1:])
 try:st,key,_=c.browser.post('/key',csrf)
 finally:keypath.write_bytes(originalkey)
 check(st==200 and key==originalkey,'key download mixed material generations')
 check(c.browser.post('/finish',csrf,custody='yes',kitsha='wrong')[0]==409,'changed material receipt admitted')
 page=c.browser.call()[1]
 st,page,_=c.browser.post('/finish',csrf,custody='yes',kitsha=field(page,'kitsha'))
 check(st==200 and b'voptrack://connect?' in page,'completion did not offer app connection')
 (c.dir/'completion.html').write_bytes(page)
 wait(lambda:docker('inspect','--format','{{.State.Running}}',c.daemon).stdout.strip()==b'false','privileged setup listener stayed running')
 check(c.phase()=='complete' and not (c.vol/'voptrack-public/home-setup-pending').exists(),'completion did not release startup guard')
 check(c.command('voptrack-setup',('authorize',),stateonly=True).returncode!=0,'completed setup became authorizable')
 check(c.command('voptrack-setup',('serve',)).returncode==0,'completed journal could not reconcile')
 check(c.command('voptrack-setup',('serve',),extra=('-e','VOPTRACK_PUBLIC_URL=https://changed-host.example','-e','VOPTRACK_SETUP_ORIGIN=https://localhost:9674')).returncode==0,'completed setup blocked current host routing')
 publicdir=c.vol/'voptrack-public';publicdir.chmod(0o300)
 try:check(c.command('voptrack-setup',('serve',)).returncode!=0,'missing guard bypassed failed parent sync')
 finally:publicdir.chmod(0o700)
 check(c.command('voptrack-setup',('serve',)).returncode==0,'guard absence could not resume after parent sync recovered')
 p=c.command('voptrack-init',('password',));check(p.returncode!=0,'retired bootstrap password still advertised')
 # The supported init reloads stored choices despite contradictory environment.
 p=c.command('voptrack-init',('init',),extra=('-e','VOPTRACK_NAME=wrong','-e','VOPTRACK_TIMEZONE=UTC','-e','VOPTRACK_CONSOLE=off'))
 check(p.returncode==0,'stored settings failed on restart')
 c.native(mode)
 restore_attestation(c)
 secret=form['password'].encode()
 check(not any(secret in p.read_bytes() for p in c.dir.rglob('*') if p.is_file()),'chosen password persisted in logs, state or recovery material')
 print(name+': native init, chosen password, custody, closure and restart PASS',flush=True)
 return c
def restore_attestation(source):
 archive=source.command('voptrack-init',('take-backup',))
 check(archive.returncode==0,'new Home offline backup failed')
 backup=source.dir/'offline-backup.tar';backup.write_bytes(archive.stdout);backup.chmod(0o600)
 kit=(source.dir/'initial-material.tar').read_bytes()
 target=Case(source.name+'-restore')
 check(target.command('voptrack-init',('restore-kit',),data=kit).returncode==0,'new Home material could not restore to fresh targets')
 before=fingerprint(target.vol)
 p=target.command('voptrack-init',('restore',),data=archive.stdout)
 check(p.returncode!=0,'restored Home reused old host encryption attestation')
 check(fingerprint(target.vol)==before,'missing current-host attestation modified restore targets')
 p=target.command('voptrack-init',('restore',),data=archive.stdout,extra=('-e','VOPTRACK_STORAGE_ENCRYPTED=yes'))
 check(p.returncode==0,'current-host attestation could not restore saved Home choices')
 choices=json.loads((target.vol/'voptrack-public/home-setup.json').read_text())
 check(choices['Mode']==source.name and choices['TimeZone']=='Europe/Helsinki','restored Home lost fixed choices')
 target.native(source.name,password='Synthetic-owner-pass-'+source.name)
 print(source.name+': fresh restore requires current-host encryption attestation PASS',flush=True)
def existing():
 c=Case('existing-target')
 sentinel=c.vol/'tessera/keep';sentinel.write_bytes(b'synthetic existing target')
 before=fingerprint(c.vol)
 daemon=c.command('voptrack-setup',('serve',),detach=True)
 wait(lambda:(c.state/'journal.json').exists() or docker('inspect','--format','{{.State.Running}}',daemon).stdout.strip()==b'false','existing-target refusal timed out')
 check(not (c.state/'journal.json').exists(),'preexisting target admitted')
 check(fingerprint(c.vol)==before,'preexisting target changed')
 r=Case('research-refusal');before=fingerprint(r.vol)
 check(r.command('voptrack-setup',('serve',),variant='research').returncode!=0 and fingerprint(r.vol)==before,'Research entered Home initializer')
 print('existing targets and Research refusal PASS',flush=True)
def interrupted(phase):
 c=Case('interrupted-'+phase)
 trigger=c.dir/'stop-at-phase';trigger.write_text(phase)
 c.start(extra=('-v',str(trigger)+':/stop-at-phase:ro'))
 c.login();form=c.settings()
 try:c.browser.call('/apply',form)
 except (OSError,urllib.error.URLError):pass
 wait(lambda:docker('inspect','--format','{{.State.Running}}',c.daemon).stdout.strip()==b'false','durable interruption not reached')
 check(c.phase()==phase,'interruption happened at wrong boundary')
 before=fingerprint(c.vol);c.start(image=INTACT)
 check(c.phase()=='stopped','interrupted initialization became retryable')
 check(c.command('voptrack-setup',('authorize',),stateonly=True,image=INTACT).returncode!=0,'interrupted initialization became authorizable')
 check(before==fingerprint(c.vol),'restart changed partial recovery targets')
 c.stop();print('durable '+phase+' interruption preserved targets PASS',flush=True)
def completion_interrupted():
 c=Case('interrupted-complete');trigger=c.dir/'stop-at-phase';trigger.write_text('complete')
 c.start(extra=('-v',str(trigger)+':/stop-at-phase:ro'))
 c.login();form=c.settings();c.browser.call('/apply',form)
 wait(lambda:c.phase() in ('ready','stopped'),'native setup did not finish',200);check(c.phase()=='ready','native setup stopped')
 csrf=field(c.browser.call()[1],'csrf')
 check(c.browser.post('/kit',csrf)[0]==200 and c.browser.post('/key',csrf)[0]==200,'interruption material export failed')
 kitsha=field(c.browser.call()[1],'kitsha')
 before=fingerprint(c.vol)
 try:c.browser.post('/finish',csrf,custody='yes',kitsha=kitsha)
 except (OSError,urllib.error.URLError):pass
 wait(lambda:docker('inspect','--format','{{.State.Running}}',c.daemon).stdout.strip()==b'false','completion interruption not reached')
 check(c.phase()=='complete' and (c.vol/'voptrack-public/home-setup-pending').exists(),'completion interruption happened at wrong boundary')
 check(c.command('voptrack-setup',('serve',),image=INTACT).returncode==0,'completed journal reconciliation failed')
 before.pop('voptrack-public/home-setup-pending')
 check(fingerprint(c.vol)==before,'completion reconciliation changed recovery targets')
 check(c.command('voptrack-setup',('authorize',),stateonly=True,image=INTACT).returncode!=0,'completed restart reopened setup')
 print('durable completion interruption reconciled only exact guard PASS',flush=True)
def restart_policy():
 # Compile-defect proof uses a real journal from the protected service before
 # any native mutation, then models the already durable accepted boundary.
 c=Case('restart-policy');c.start();c.stop();c.start()
 check(c.command('voptrack-setup',('access',),stateonly=True).returncode!=0,'restart preserved unused host grant')
 check(c.command('voptrack-setup',('authorize',),stateonly=True).returncode==0,'explicit restart authorization failed')
 c.login();c.settings();c.stop()
 j=c.journal();j['Phase']='accepted';write(c.state/'journal.json',j)
 before=fingerprint(c.vol);c.start()
 check(c.phase()=='stopped','interrupted initialization became retryable')
 check(c.command('voptrack-setup',('authorize',),stateonly=True).returncode!=0,'interrupted initialization became retryable')
 check(before==fingerprint(c.vol),'restart changed partial targets');c.stop()
def main():
 check(IMAGE.startswith('sha256:') and INTACT.startswith('sha256:'),'immutable actual images required')
 check(not WORK.exists(),'fresh qualification root required')
 for port in [PORT,PORT+1]:
  with socket.socket() as probe:probe.bind(('127.0.0.1',port))
 private(WORK)
 subprocess.run([sys.executable,str(REPO/'release/recovery-set/inventory.py'),str(WORK/'before.json')],check=True)
 version=docker('run','--rm','--network','none','--tmpfs','/var/lib/tessera','--entrypoint','tesserad',IMAGE,'-version').stdout.decode().strip()
 check(version==PIN,'actual image pin mismatch')
 docker('network','create','--label','voptrack.home-setup='+PROJECT,PROJECT)
 try:
  if MODE=='existing':existing()
  elif MODE=='restart':restart_policy()
  elif MODE=='interruptions':
   for phase in ['accepted','provisioning','password']:interrupted(phase)
   completion_interrupted()
  elif MODE in ['csrf','binding','startup','material','restore','routing','closed-sync']:normal('public','public')
  else:
   existing();restart_policy()
   for mode in ['public','private','off']:normal(mode,mode)
  write(WORK/'result.json',dict(pass_=True,image=IMAGE,version=version,mode=MODE,aws_qualified=False,separate_host_qualified=False))
  print('Protected Home setup '+MODE+': PASS',flush=True)
 finally:
  for name in OWNED:
   p=docker('logs',name,ok=False);(WORK/(name+'.log')).write_bytes(p.stdout+p.stderr)
   docker('stop','-t','5',name,ok=False);docker('rm',name,ok=False)
  docker('network','rm',PROJECT,ok=False)
  subprocess.run([sys.executable,str(REPO/'release/recovery-set/inventory.py'),str(WORK/'after.json')],check=True)
  # All pre-existing resources are read-only in this gate. Owned target paths
  # stay in WORK, including failures and interrupted native initialization.
  before=json.loads((WORK/'before.json').read_text());after=json.loads((WORK/'after.json').read_text())
  check(before==after,'Docker inventory changed outside owned qualification scope')

if __name__=='__main__':main()
