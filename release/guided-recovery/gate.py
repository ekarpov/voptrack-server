"""Protected guided recovery, against the actual image and retained synthetic sets.
Every invocation needs a fresh persistent root. Never print private responses.
The fault-image mode is only for isolated compiling interruption candidates.
"""
import concurrent.futures,hashlib,http.client,http.cookiejar,io,json,os,pathlib,secrets,shutil,socket,ssl,subprocess,sys,tarfile,time,urllib.error,urllib.parse,urllib.request
from html.parser import HTMLParser

import sys as _sys
from pathlib import Path as _Path
_sys.path.insert(0, str(_Path(__file__).resolve().parents[2] / 'ci'))
from tessera_pin import expected_pin
PIN = expected_pin()

ROOT=pathlib.Path(__file__).resolve().parents[2]
IMAGE=sys.argv[1];INPUTS=pathlib.Path(sys.argv[2]).resolve();WORK=pathlib.Path(sys.argv[3]).resolve()
MODE=os.environ.get('VOPTRACK_GUIDED_MODE','full')
PORT=int(os.environ.get('VOPTRACK_GUIDED_PORT','30943'))
RESTORE_PORT=int(os.environ.get('VOPTRACK_GUIDED_RESTORE_PORT','30944'))
ORIGIN=f'https://localhost:{PORT}'
PROJECT='voptrack-guided-'+secrets.token_hex(5)
def check(value,label):
 if not value:raise RuntimeError(label)
def fixture_state(root):
 # Include helper/receipt bytes, file identity metadata and names, not just
 # selected inputs. Reads do not change mtime. Never print contents or digests.
 return {str(p.relative_to(root)):(p.stat().st_mode,p.stat().st_size,p.stat().st_mtime_ns,hashlib.sha256(p.read_bytes()).hexdigest()) for p in root.rglob('*') if p.is_file()}
def docker(*args,ok=True):
 p=subprocess.run(['docker',*args],capture_output=True)
 if ok:check(p.returncode==0,'private guided Docker operation refused')
 return p
check(IMAGE.startswith('sha256:'),'immutable image ID required')
check(not WORK.exists(),'qualification directory must be fresh')
for port in (PORT,RESTORE_PORT):
 with socket.socket() as probe:
  probe.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
  probe.bind(('127.0.0.1',port))
WORK.mkdir(parents=True,mode=0o700)
original_fixture=fixture_state(INPUTS)
subprocess.run([sys.executable,str(ROOT/'release/recovery-set/inventory.py'),str(WORK/'before.json')],check=True)
# No custom network or subnet is created. Only task-named containers use bridge.
version=docker('run','--rm','--network','none','--entrypoint','tesserad',IMAGE,'-version')
check(version.stdout.decode().strip()==PIN,'candidate binary pin mismatch')
(WORK/'version.txt').write_bytes(version.stdout)
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','2','-subj','/CN=localhost','-addext','subjectAltName=DNS:localhost,IP:127.0.0.1','-keyout',str(WORK/'tls.key'),'-out',str(WORK/'tls.crt')],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,check=True)
CONTEXT=ssl.create_default_context(cafile=str(WORK/'tls.crt'))
class Forms(HTMLParser):
 def __init__(self,html):
  super().__init__();self.forms={};self.action=None;self.feed(html)
 def handle_starttag(self,tag,attrs):
  a=dict(attrs)
  if tag=='form':self.action=a['action'];self.forms[self.action]={}
  if tag=='input' and self.action and 'name' in a:self.forms[self.action][a['name']]=a.get('value','')
 def handle_endtag(self,tag):
  if tag=='form':self.action=None
class Browser:
 def __init__(self):
  self.jar=http.cookiejar.CookieJar()
  class NoRedirect(urllib.request.HTTPRedirectHandler):
   def redirect_request(self,*args):return None
  self.opener=urllib.request.build_opener(urllib.request.HTTPSHandler(context=CONTEXT),urllib.request.HTTPCookieProcessor(self.jar),NoRedirect())
 def request(self,path='/',form=None,headers=None):
  data=urllib.parse.urlencode(form).encode() if form is not None else None
  hdr={'Origin':ORIGIN} if data is not None else {}
  if data is not None:hdr['Content-Type']='application/x-www-form-urlencoded'
  if headers:hdr.update(headers)
  req=urllib.request.Request(ORIGIN+path,data=data,headers=hdr)
  try:r=self.opener.open(req,timeout=40)
  except urllib.error.HTTPError as e:r=e
  return r.code,r.read().decode(),dict(r.headers)
 def page(self):
  st,body,headers=self.request();check(st==200,'guided page refused')
  check(headers.get('Cache-Control')=='no-store' and headers.get('Referrer-Policy')=='no-referrer','sensitive page cache/referrer guard missing')
  return Forms(body)
class App:
 def __init__(self,name,variant='household',mutate=None,expired=False,stop=None,short=False):
  self.name=PROJECT+'-'+name;self.dir=WORK/name;self.dir.mkdir(mode=0o700)
  self.retained=INPUTS/variant;source_state=json.loads((self.retained/'source-removed.json').read_text());check(source_state.get('source_removed') or source_state.get('source_services_unavailable'),'source-unavailability receipt missing')
  for part in ('state','targets','trust','key'): (self.dir/part).mkdir(mode=0o700)
  shutil.copytree(self.retained/'inputs',self.dir/'inputs')
  shutil.copyfile(self.retained/'trust/backup-lineage.json',self.dir/'trust/lineage.json')
  shutil.copyfile(self.retained/'key/backup.key',self.dir/'key/backup.key')
  self.target=self.dir/'targets/fresh'
  self.config={'Origin':ORIGIN,'State':'/work/state','Target':'/work/targets/fresh','Sets':[{'ID':'selected','Label':'Synthetic '+variant,'Directory':'/work/inputs','TrustFile':'/work/trust/lineage.json','KeyFile':'/work/key/backup.key'}]}
  (self.dir/'config.json').write_text(json.dumps(self.config))
  if mutate:mutate(self)
  self.args=['--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--user',f'{os.getuid()}:{os.getgid()}','--tmpfs',f'/tmp:uid={os.getuid()},mode=0700','--tmpfs',f'/var/lib/tessera:uid={os.getuid()},mode=0700','-v',str(self.dir)+':/work','-v',str(WORK/'tls.crt')+':/tls.crt:ro','-v',str(WORK/'tls.key')+':/tls.key:ro','--entrypoint','voptrack-recovery']
  docker('run','--rm','--network','none',*self.args,IMAGE,'-config','/work/config.json','-mode','authorize','-token-file','/work/authorization')
  self.token=(self.dir/'authorization').read_text();(self.dir/'authorization').unlink()
  if expired:
   journal=self.journal();journal['AuthExpires']='2000-01-01T00:00:00Z';(self.dir/'state/journal.json').write_text(json.dumps(journal))
  if stop:(self.dir/'stop-at-stage').write_text(stop)
  if short:(self.dir/'short-session').write_text('qualification-only')
  self.start()
 def start(self,image=IMAGE):
  check(docker('container','inspect',self.name,ok=False).returncode!=0,'task container name collision')
  docker('run','-d','--name',self.name,'--label','voptrack.guided='+PROJECT,'-p',f'127.0.0.1:{PORT}:9543',*self.args,image,'-config','/work/config.json','-listen','0.0.0.0:9543','-tls-cert','/tls.crt','-tls-key','/tls.key')
  for _ in range(200):
   try:
    if Browser().request()[0]==200:return
   except (OSError,urllib.error.URLError):pass
   time.sleep(.05)
  raise RuntimeError('recovery listener failed readiness')
 def journal(self):return json.loads((self.dir/'state/journal.json').read_text())
 def login(self):
  b=Browser();f=b.page().forms['/auth'];f['token']=self.token
  check(b.request('/auth',f)[0]==303,'host authorization refused')
  return b
 def validate(self,b,expected=303):
  page=b.page()
  if '/validate' not in page.forms:
   check(b.request('/select',page.forms['/select'])[0]==303,'return to selection refused');page=b.page()
  f=page.forms['/validate'];f.update(set='selected',target='fresh')
  check(b.request('/validate',f)[0]==expected,'preflight admission mismatch')
  return b.page()
 def confirm(self,b):
  return b.page().forms['/confirm'].copy()
 def wait(self,phase):
  for _ in range(600):
   current=self.journal()['Phase']
   if current==phase:return
   if current=='stopped' and phase!='stopped':raise RuntimeError('guided attempt stopped before '+phase)
   time.sleep(.05)
  raise RuntimeError('guided attempt did not reach '+phase)
 def close(self):
  docker('rm','-fv',self.name,ok=False)
 def clean(self):
  check(all(not (self.dir/'state'/name).exists() for name in ['snapshot','recheck','materials']),'temporary secret copies survived terminal outcome')
 def check_token_secrecy(self,b):
  check(self.token not in b.request()[1],'authorization leaked to status HTML')
  check(self.token not in docker('logs',self.name).stdout.decode(),'authorization leaked to logs')
  check(self.token not in (self.dir/'state/journal.json').read_text(),'raw authorization persisted in journal')
def mutation(kind):
 def apply(a):
  sel=json.loads((a.dir/'inputs/selection.json').read_text())
  if kind=='wrong-key':(a.dir/'key/backup.key').write_bytes(b'x'*32)
  elif kind=='lineage':
   trust=json.loads((a.dir/'trust/lineage.json').read_text());trust['deployment']='a'*32;(a.dir/'trust/lineage.json').write_text(json.dumps(trust))
  elif kind=='missing':(a.dir/'inputs'/sel['kit']['file']).unlink()
  elif kind=='wrong-kit':
   other=INPUTS/('research' if a.retained.name=='household' else 'household')/'inputs'
   foreign=json.loads((other/'selection.json').read_text())
   p=a.dir/'inputs'/sel['kit']['file'];p.write_bytes((other/foreign['kit']['file']).read_bytes());sel['kit']['sha256']=hashlib.sha256(p.read_bytes()).hexdigest()
  elif kind=='missing-settings':(a.dir/'inputs'/sel['settings']['file']).unlink()
  elif kind=='foreign-settings':
   p=a.dir/'inputs'/sel['settings']['file'];value=json.loads(p.read_text());value['deployment']='f'*32;p.write_text(json.dumps(value));sel['settings']['sha256']=hashlib.sha256(p.read_bytes()).hexdigest()
  elif kind=='oversized':
   p=a.dir/'inputs'/sel['settings']['file'];p.write_bytes(b'x'*4097);sel['settings']['sha256']=hashlib.sha256(p.read_bytes()).hexdigest()
  elif kind in ('unsafe','forged-mac'):
   p=a.dir/'inputs'/sel['backup']['file']
   with tarfile.open(p) as src:members={n:src.extractfile(n).read() for n in src.getnames()}
   if kind=='unsafe':members['../escape']=b'unsafe'
   else:
    manifest=json.loads(members['manifest.json']);meta=next(v for v in manifest['operation']['artifacts'] if v['kind']=='metadata')
    env=json.loads(members[meta['id']]);env['mac']=('0' if env['mac'][0]!='0' else '1')+env['mac'][1:]
    raw=json.dumps(env).encode();members[meta['id']]=raw;meta.update(size=len(raw),sha256=hashlib.sha256(raw).hexdigest());members['manifest.json']=json.dumps(manifest).encode()
   with tarfile.open(p,'w') as dst:
    for n,raw in members.items():
     h=tarfile.TarInfo(n);h.size=len(raw);dst.addfile(h,io.BytesIO(raw))
   sel['backup']['sha256']=hashlib.sha256(p.read_bytes()).hexdigest()
  (a.dir/'inputs/selection.json').write_text(json.dumps(sel))
 return apply

def guards():
 a=App('guards')
 try:
  b=Browser();p=b.page();f=p.forms['/auth'];f['token']='wrong'
  check(b.request('/auth',f)[0]==403,'incorrect authorization admitted')
  f['token']=a.token;check(b.request('/auth',f)[0]==303,'valid authorization refused after one failure')
  stranger=Browser();sf=stranger.page().forms['/auth'];sf['token']=a.token
  check(stranger.request('/auth',sf)[0]==403,'replayed host authorization admitted')
  check('/validate' not in stranger.page().forms,'unrelated session inspected staged sets')
  fields=b.page().forms['/validate'];fields.update(set='selected',target='fresh')
  bad=fields.copy();bad['csrf']='forged'
  check(b.request('/validate',bad)[0]==403,'CSRF admitted') # planted-defect target
  check(b.request('/validate',fields,{'Origin':'https://unrelated.example'})[0]==403,'cross-origin admitted')
  check(b.request('/validate',fields,{'Host':'unrelated.example'})[0]==403,'wrong host admitted')
  bad=fields.copy();bad['set']='../../etc'
  check(b.request('/validate',bad)[0]==400,'browser filesystem path admitted')
  a.validate(b);confirmed=a.confirm(b)
  for field in ('binding','attempt','set','target','confirm'):
   bad=confirmed.copy();bad[field]='changed'
   check(b.request('/confirm',bad)[0]==409,'changed confirmation '+field+' admitted')
  # A new validated attempt invalidates an otherwise matching old confirmation.
  a.validate(b)
  check(b.request('/confirm',confirmed)[0]==409,'stale confirmation admitted')
  a.check_token_secrecy(b)
 finally:a.close()
 a=App('expired',expired=True)
 try:
  b=Browser();f=b.page().forms['/auth'];f['token']=a.token
  check(b.request('/auth',f)[0]==403,'expired authorization admitted')
 finally:a.close()
 a=App('budget')
 try:
  b=Browser();f=b.page().forms['/auth'];f['token']='wrong'
  for _ in range(5):check(b.request('/auth',f)[0]==403,'wrong authorization admitted')
  f['token']=a.token;check(b.request('/auth',f)[0]==403,'authentication attempt budget bypassed')
 finally:a.close()
 print('Actual-image authorization, CSRF and confirmation guards: PASS',flush=True)

def negatives():
 for kind in ('wrong-key','lineage','missing','oversized','unsafe','forged-mac')+(('wrong-kit','foreign-settings','missing-settings') if os.environ.get('VOPTRACK_RECOVERY_ASSURANCE')=='yes' else ()):
  a=App('input-'+kind,mutate=mutation(kind))
  try:
   b=a.login();a.validate(b,400);check(not a.target.exists(),'invalid input mutated target');a.clean()
  finally:a.close()
 for partial in (False,True):
  a=App('partial' if partial else 'existing')
  try:
   a.target.mkdir(mode=0o700)
   if partial:(a.target/'preserve').write_bytes(b'partial')
   before=list(a.target.iterdir());b=a.login();a.validate(b,400)
   check(list(a.target.iterdir())==before,'existing target changed')
   if partial:check((a.target/'preserve').read_bytes()==b'partial','partial target overwritten')
  finally:a.close()
 a=App('changed-inputs')
 try:
  b=a.login();a.validate(b);f=a.confirm(b)
  sel=json.loads((a.dir/'inputs/selection.json').read_text());p=a.dir/'inputs'/sel['settings']['file'];p.write_bytes(p.read_bytes()+b' ')
  check(b.request('/confirm',f)[0]==303,'bound attempt not admitted')
  a.wait('stopped');check(not a.target.exists(),'changed selected bytes mutated target');a.clean()
 finally:a.close()
 a=App('late-existing')
 try:
  b=a.login();a.validate(b);f=a.confirm(b);a.target.mkdir(mode=0o700);(a.target/'preserve').write_bytes(b'late existing')
  check(b.request('/confirm',f)[0]==303,'late target attempt not admitted')
  a.wait('stopped');check((a.target/'preserve').read_bytes()==b'late existing' and len(list(a.target.iterdir()))==1,'target substituted after review was modified');a.clean()
 finally:a.close()
 print('Actual-image input authentication, confinement and target preservation: PASS',flush=True)

def successful(variant,lost=False):
 a=App('success-'+variant,variant)
 try:
  b=a.login();a.validate(b);f=a.confirm(b)
  if lost:
   cookie='; '.join(v.name+'='+v.value for v in b.jar)
   conn=http.client.HTTPSConnection('localhost',PORT,context=CONTEXT,timeout=20)
   conn.request('POST','/confirm',urllib.parse.urlencode(f),{'Origin':ORIGIN,'Content-Type':'application/x-www-form-urlencoded','Cookie':cookie})
   # Deliberately discard the response. The operation survives the browser.
   for _ in range(200):
    if a.journal()['Phase']!='review':break
    time.sleep(.02)
   check(a.journal()['Phase']!='review','lost-response request never admitted')
   conn.close();a.wait('complete')
   check(b.request('/confirm',f)[0]==409,'lost response replayed restoration')
  else:
   def submit(_):
    clone=Browser()
    for c in b.jar:clone.jar.set_cookie(c)
    return clone.request('/confirm',f)[0]
   with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:codes=list(pool.map(submit,range(6)))
   check(codes.count(303)==1 and codes.count(409)==5,'concurrent confirmation executed more than once')
   a.wait('complete')
  a.clean();a.check_token_secrecy(b)
  check(a.journal()['Checked']['Status']=='inputs-verified-not-restored' if 'Status' in a.journal()['Checked'] else a.journal()['Checked']['status']=='inputs-verified-not-restored','receipt relabelled as authority')
  check('Offline recovery completed' in b.request()[1],'completion status missing')
  # An entirely separate, explicit verification launch. Recovery never enrolls
  # collectors or cuts over. Reuse existing exact record/epoch/Console assertions.
  verification=a.dir/'verification-inputs'
  shutil.copytree(a.retained,verification)
  with (a.dir/'correctness.log').open('wb') as log:
   result=subprocess.run([sys.executable,str(ROOT/'release/recovery-set/drill.py'),str(verification),IMAGE,PROJECT+'-verify-'+variant,str(RESTORE_PORT),'--prepared-root',str(a.target)],stdout=log,stderr=subprocess.STDOUT)
  check(result.returncode==0,'guided target correctness verification failed; private diagnostics retained')
  drill=json.loads((verification/'drill-receipt.json').read_text())
  (a.dir/'correctness.json').write_text(json.dumps({'variant':variant,'actual_guided_target_verified':True,'source_services_unavailable':True,'record_isolation':True,'later_erasure_exclusion':True,'producer_epoch_settings':True,'saved_console_state':drill['console_state_expected'],'app_only_authority_absent':drill['app_only_authority_absent']})+'\n')
 finally:a.close()
 print('Guided '+variant+' recovery and retained-set correctness: PASS',flush=True)

def interruptions():
 # The isolated compiling fault image exits only after the real journal sync.
 # Restart uses the intact image, never a resumed mutation.
 intact=os.environ.get('VOPTRACK_GUIDED_INTACT_IMAGE',IMAGE)
 for phase in ('accepted','creating-target','importing-kit','recovering-producer','restoring-records','verifying'):
  a=App('interrupt-'+phase,stop=phase)
  try:
   b=a.login();a.validate(b);f=a.confirm(b)
   try:b.request('/confirm',f)
   except (OSError,http.client.HTTPException):pass
   for _ in range(300):
    state=json.loads(docker('inspect',a.name).stdout)[0]['State']
    if not state['Running']:break
    time.sleep(.05)
   check(not state['Running'] and state['ExitCode']==75,'fault candidate did not stop at durable stage')
   check(a.journal()['Phase']==phase,'fault did not hit intended stage')
   before={str(p.relative_to(a.target)):hashlib.sha256(p.read_bytes()).hexdigest() for p in a.target.rglob('*') if p.is_file()} if a.target.exists() else None
   a.close();a.start(intact)
   check(a.journal()['Phase']=='stopped','interrupted mutation became retryable') # planted-defect target
   check('/auth' in Browser().page().forms,'restart retained browser authority')
   check(b.request('/confirm',f)[0]==403,'old session survived restart')
   after={str(p.relative_to(a.target)):hashlib.sha256(p.read_bytes()).hexdigest() for p in a.target.rglob('*') if p.is_file()} if a.target.exists() else None
   check(before==after,'restart changed partial target')
   a.clean()
  finally:a.close()
 print('Actual-image durable interruption at all mutating boundaries and intact restart: PASS',flush=True)

def mutation_failures():
 intact=os.environ.get('VOPTRACK_GUIDED_INTACT_IMAGE',IMAGE)
 for failure,stage in [('fail-kit','importing-kit'),('fail-producer','recovering-producer'),('fail-records','restoring-records')]:
  a=App(failure,stop=failure)
  try:
   b=a.login();a.validate(b);f=a.confirm(b)
   check(b.request('/confirm',f)[0]==303,'fault attempt not admitted')
   a.wait('stopped');a.clean()
   check(stage in [v['Phase'] for v in a.journal()['History']],'failure missed intended mutating stage')
   before={str(p.relative_to(a.target)):hashlib.sha256(p.read_bytes()).hexdigest() for p in a.target.rglob('*') if p.is_file()}
   check(bool(before),'failure did not leave a partial target')
   check('Offline recovery completed' not in b.request()[1],'failed mutation reported success')
   a.close();a.start(intact)
   check(a.journal()['Phase']=='stopped','failed mutation became retryable')
   after={str(p.relative_to(a.target)):hashlib.sha256(p.read_bytes()).hexdigest() for p in a.target.rglob('*') if p.is_file()}
   check(before==after,'restart changed failed partial target')
   a.clean()
  finally:a.close()
 print('Actual-image partial kit/producer/record failure handling and intact restart: PASS',flush=True)

def session_window():
 a=App('body-expiry',short=True)
 try:
  b=a.login();a.validate(b);f=a.confirm(b)
  cookie='; '.join(v.name+'='+v.value for v in b.jar);payload=urllib.parse.urlencode(f).encode()
  conn=http.client.HTTPSConnection('localhost',PORT,context=CONTEXT,timeout=20)
  conn.putrequest('POST','/confirm')
  for key,value in {'Origin':ORIGIN,'Cookie':cookie,'Content-Type':'application/x-www-form-urlencoded','Content-Length':str(len(payload))}.items():conn.putheader(key,value)
  conn.endheaders();conn.send(payload[:1]);time.sleep(6);conn.send(payload[1:])
  response=conn.getresponse();code=response.status;response.read();conn.close()
  check(code==403,'session expired during body read was admitted')
  check(not a.target.exists(),'expired body mutated target');a.clean()
 finally:a.close()
 print('Actual-image session expiry during bounded body read: PASS',flush=True)

def empty_evidence():
 def initial_evidence(a):
  path=a.dir/'inputs/selection.json';selected=json.loads(path.read_text())
  backup=a.dir/'inputs'/selected['backup']['file'];evidence=a.dir/'inputs'/selected['evidence']['file']
  # Deliberate operator selection of genuine initial backup-point evidence,
  # not an application fallback or a latest/later-erasure coverage claim.
  shutil.copyfile(backup,evidence);selected['evidence']['sha256']=hashlib.sha256(evidence.read_bytes()).hexdigest();path.write_text(json.dumps(selected))
 for variant in ('household','research'):
  a=App('empty-evidence-'+variant,variant,mutate=initial_evidence)
  try:
   b=a.login();a.validate(b);f=a.confirm(b)
   check(b.request('/confirm',f)[0]==303,'zero-evidence attempt not admitted')
   for _ in range(600):
    phase=a.journal()['Phase']
    if phase in ('complete','stopped'):break
    time.sleep(.05)
   check(phase=='complete','empty verified evidence was refused')
   check((a.target/'var/lib/tessera/dataset.db.retired').stat().st_size==0,'empty evidence bytes were changed');a.clean()
  finally:a.close()
 print('Actual-image native recovery with authenticated zero-byte evidence: PASS',flush=True)

try:
 if MODE=='guards':guards()
 elif MODE=='interruptions':interruptions()
 elif MODE=='failures':mutation_failures()
 elif MODE=='session-window':session_window()
 elif MODE=='empty-evidence':empty_evidence()
 elif MODE=='retained-preservation':successful('household')
 else:
  guards();negatives();empty_evidence();successful('household',lost=True);successful('research')
finally:
 # Only containers carrying our unpredictable task label may be removed.
 ids=docker('ps','-aq','--filter','label=voptrack.guided='+PROJECT).stdout.decode().split()
 if ids:docker('rm','-fv',*ids,ok=False)
 subprocess.run([sys.executable,str(ROOT/'release/recovery-set/inventory.py'),str(WORK/'after.json')],check=True)
 check(original_fixture==fixture_state(INPUTS),'retained source fixture was modified')
(WORK/'result.json').write_text(json.dumps({'image':IMAGE,'mode':MODE,'passed':True,'pin':PIN,'independent_host':False})+'\n')
