#!/usr/bin/env python3
"""Synthetic public-export CLI recovery gate; no artifact/collector restore claim.
Positive paths never edit private state. Read-only opaque intent hashes assert
retry stability; manually created private corruption fixtures are negative only.
"""
import base64, datetime, fcntl, hashlib, http.cookiejar, json, os, pathlib, re, secrets, socket, ssl, subprocess, sys, tempfile, time, urllib.error, urllib.parse, urllib.request
normal,faulting=map(pathlib.Path,sys.argv[1:3]);processes=[]
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*a,**k):return None
def check(ok,msg):
 if not ok:raise RuntimeError(msg)
def write(p,value):p.write_bytes(value if isinstance(value,bytes) else json.dumps(value).encode());p.chmod(0o600)
def run(binary,config,*args,stage='',ok=True):
 p=subprocess.run([str(binary),'-config',str(config),*args],env=dict(os.environ,TESSERA_DEV_RECOVERY_INTERRUPT=stage),capture_output=True,timeout=25)
 check((p.returncode==0)==ok,'CLI outcome mismatch: '+(args[0] if args else 'startup'));return p
def request(opener,origin,method,path,body=None,token=None,form=False):
 headers={}
 if body is not None:
  body=urllib.parse.urlencode(body).encode() if form else json.dumps(body).encode();headers['Content-Type']='application/x-www-form-urlencoded' if form else 'application/json'
 if token:headers['Authorization']='Bearer '+token
 try:r=opener.open(urllib.request.Request(origin+path,data=body,headers=headers,method=method),timeout=5)
 except urllib.error.HTTPError as e:r=e
 with r:return r.status,r.read(),r.headers
def field(raw,name):
 m=re.search(rb'name="'+name.encode()+rb'"\s+value="([^"]*)"',raw);check(m is not None,'missing OAuth field');return m[1].decode()
def login(opener,origin,password):
 st,raw,_=request(opener,origin,'POST','/oauth/register',dict(client_name='Recovery gate',redirect_uris=['com.voptrack.gate:/callback'],token_endpoint_auth_method='none',grant_types=['authorization_code'],response_types=['code']))
 check(st==201,'registration failed');client=json.loads(raw)['client_id'];verifier=secrets.token_urlsafe(48);challenge=base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
 query=urllib.parse.urlencode(dict(client_id=client,redirect_uri='com.voptrack.gate:/callback',response_type='code',state='recovery',code_challenge=challenge,code_challenge_method='S256', prompt='consent'))
 st,page,_=request(opener,origin,'GET','/oauth/authorize?'+query)
 st,approval,_=request(opener,origin,'POST','/oauth/authorize',dict(flow=field(page,'flow'),csrf=field(page,'csrf'),username='owner',password=password,decision='continue'),form=True);check(st==200,'sign-in failed')
 st,_,headers=request(opener,origin,'POST','/oauth/authorize',dict(flow=field(page,'flow'),csrf=field(approval,'csrf'),decision='allow',administer='yes'),form=True);check(st==303,'approval failed')
 code=urllib.parse.parse_qs(urllib.parse.urlparse(headers['Location']).query)['code'][0]
 st,raw,_=request(opener,origin,'POST','/oauth/token',dict(grant_type='authorization_code',client_id=client,code=code,redirect_uri='com.voptrack.gate:/callback',code_verifier=verifier),form=True);check(st==200,'token exchange failed');return json.loads(raw)['access_token']
with tempfile.TemporaryDirectory(prefix='vop-producer-recovery-') as tmp:
 root=pathlib.Path(tmp);root.chmod(0o700);key,cert,tlskey=root/'backup.key',root/'tls.crt',root/'tls.key';write(key,secrets.token_bytes(32))
 subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=localhost','-addext','subjectAltName=IP:127.0.0.1','-keyout',str(tlskey),'-out',str(cert)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 tlskey.chmod(0o600);cert.chmod(0o600);context=ssl.create_default_context(cafile=str(cert));password=secrets.token_urlsafe(30);passwordfile=root/'password';write(passwordfile,password.encode())
 def browser():return urllib.request.build_opener(urllib.request.HTTPSHandler(context=context),NoRedirect(),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
 def destination(name,keep=1,every=24):
  base=root/name;base.mkdir(mode=0o700);backups=base/'backups';backups.mkdir(mode=0o700)
  with socket.socket() as s:s.bind(('127.0.0.1',0));port=s.getsockname()[1]
  address='127.0.0.1:'+str(port);origin='https://'+address;config=base/'config.json'
  write(config,dict(listen=address,issuer=origin,data_dir=str(base/'data'),tls_certificate=str(cert),tls_key=str(tlskey),encrypted_storage_confirmed=True,backup=dict(directory=str(backups),key_file=str(key),every_hours=every,keep=keep)));return config,backups,origin
 def provision(config,ok=True):return run(normal,config,'-add-person','owner','-password-file',str(passwordfile),'-may-administer-server',ok=ok)
 def start(config,origin):
  log=open(config.parent/'server.log','ab');proc=subprocess.Popen([str(normal),'-config',str(config)],stdout=log,stderr=log);processes.append((proc,log))
  for _ in range(150):
   check(proc.poll() is None,'server exited before readiness')
   try:
    if request(browser(),origin,'GET','/.well-known/oauth-authorization-server')[0]==200:return proc
   except (OSError,urllib.error.URLError):pass
   time.sleep(.05)
  raise RuntimeError('server readiness timeout')
 def stop(proc):
  proc.terminate()
  try:proc.wait(timeout=15)
  except subprocess.TimeoutExpired:proc.kill();proc.wait()
 def observe(origin,token):
  st,raw,_=request(browser(),origin,'GET','/admin/api/v1/backup-scheduler',token=token);check(st==200,'scheduler failed');return json.loads(raw)
 try:
  source,_,origin=destination('source',8,every=0);provision(source);proc=start(source,origin);admin=login(browser(),origin,password);initial=observe(origin,admin)
  expected=initial['settings_export']['deployment'] # independent observation retained before exported effective policy
  identifier=initial['settings_scope']+'.'+str(int(time.time()))+'-'+secrets.token_hex(16);payload=dict(expected_revision=initial['settings']['revision'],every_hours=0,keep=7)
  check(request(browser(),origin,'PUT','/admin/api/v1/backup-settings-operations/'+identifier,payload,admin)[0]==202,'settings admission failed')
  for _ in range(150):
   observed=observe(origin,admin)
   if observed['settings']['keep']==7 and observed['pending_request_id'] is None:break
   time.sleep(.05)
  else:raise RuntimeError('settings never effective')
  exported=observed['settings_export'];exportpath=root/'public-export.json';write(exportpath,exported);stop(proc)
  args=['-recover-backup-producer','-expected-backup-producer',expected,'-backup-settings-bootstrap',str(exportpath)]
  canonical=dict(reversed(list(exported.items())));canonical['exported_at']=canonical['exported_at'].replace('Z','+00:00');canonicalpath=root/'canonical.json';write(canonicalpath,canonical);canonicalargs=args[:-1]+[str(canonicalpath)]
  changed=dict(exported);stamp=datetime.datetime.strptime(changed['exported_at'][:19],'%Y-%m-%dT%H:%M:%S').replace(tzinfo=datetime.timezone.utc);changed['exported_at']=(stamp+datetime.timedelta(seconds=1)).isoformat();changedpath=root/'changed.json';write(changedpath,changed);changedargs=args[:-1]+[str(changedpath)]
  revisionpath=root/'revision.json';write(revisionpath,dict(exported,revision=observed['settings_scope']+'.'+secrets.token_hex(16)));revisionargs=args[:-1]+[str(revisionpath)]
  config,backups,target=destination('fresh');run(normal,config,*args,stage='intent');check(not (config.parent/'data').exists(),'offline opened record storage');run(normal,config,*canonicalargs);run(normal,config,*changedargs,ok=False);run(normal,config,*revisionargs,ok=False)
  provision(config);proc=start(config,target);token=login(browser(),target,password);restored=observe(target,token)
  check(restored['settings_export']['deployment']==expected and restored['settings']['every_hours']==0 and restored['settings']['keep']==7 and restored['settings_scope']!=observed['settings_scope'] and restored['settings']['revision']!=observed['settings']['revision'] and restored['pending_request_id'] is None,'recovered public policy/scope mismatch')
  for method in ('GET','PUT'):check(request(browser(),target,method,'/admin/api/v1/backup-settings-operations/'+identifier,payload if method=='PUT' else None,token)[0]==409,'old scope accepted')
  check(request(browser(),target,'GET','/admin/api/v1/backup-scheduler',token=admin)[0]==401,'old authority accepted');stop(proc);proc=start(config,target);after=observe(target,token)
  check(after['settings']==restored['settings'] and after['settings_scope']==restored['settings_scope'],'restart lost policy');stop(proc);run(normal,config,*args,ok=False)
  print('ok — genuine public export, offline fresh identity/settings, fresh scope, both differing defaults, restart and authority refusals',flush=True)
  for stage in ('marker','intent','deployment.json','settings.json','complete'):
   config,backups,_=destination('interrupt-'+stage.replace('.','-'));run(faulting,config,*args,stage=stage,ok=False);acquisition=backups/'.acquisitions';intent=acquisition/('recovery.complete' if stage=='complete' else 'recovery.pending')/'intent.json';digest=hashlib.sha256(intent.read_bytes()).digest() if intent.exists() else None
   if stage!='complete':provision(config,ok=False)
   wrong=args.copy();wrong[2]='f'*32;run(normal,config,*wrong,ok=False)
   if stage!='marker':run(normal,config,*changedargs,ok=False);run(normal,config,*revisionargs,ok=False)
   run(normal,config,*canonicalargs)
   if digest is not None:check(hashlib.sha256((acquisition/'recovery.complete'/'intent.json').read_bytes()).digest()==digest,'retry selected new intent/epoch')
   run(normal,config,*args);print('ok — interrupted '+stage+': startup fence, full canonical retry, stable intent and completed replay',flush=True)
  malformed={'unknown':dict(exported,unknown=True),'null':dict(exported,keep=None),'missing':{k:v for k,v in exported.items() if k!='every_hours'},'bounds':dict(exported,keep=0),'timestamp':dict(exported,exported_at='invalid'),'version':dict(exported,version=2),'revision':dict(exported,revision='invalid'),'identity':dict(exported,deployment='f'*32)}
  for name,value in malformed.items():
   config,backups,_=destination('invalid-'+name);path=config.parent/'bad.json';write(path,value);run(normal,config,*(args[:-1]+[str(path)]),ok=False);check(not (backups/'.acquisitions').exists(),'invalid export initialized state');run(normal,config,*args)
  config,backups,_=destination('invalid-duplicate');path=config.parent/'bad.json';write(path,exportpath.read_bytes().replace(b'"keep": 7',b'"keep": 7, "keep": 8'));run(normal,config,*(args[:-1]+[str(path)]),ok=False);check(not (backups/'.acquisitions').exists(),'duplicate initialized state')
  print('ok — malformed, duplicate, missing, null, identity, version, revision and bounds refused before initialization',flush=True)
  # Explicit negative fixtures only; never used to establish successful recovery.
  for name,setup in {
   'unknown':lambda a:write(a/'unknown',b'negative'),'journal':lambda a:write(a/'settings.json',b'{}'),'corrupt-identity':lambda a:write(a/'deployment.json',b'invalid'),'conflicting-identity':lambda a:write(a/'deployment.json',json.dumps('f'*32).encode()+b'\n'),
   'nonempty-pending':lambda a:((a/'recovery.pending').mkdir(mode=0o700),write(a/'recovery.pending'/'.journal-incomplete',b'partial')),
   'corrupt-complete':lambda a:((a/'recovery.complete').mkdir(mode=0o700),write(a/'recovery.complete'/'intent.json',b'{}')),'symlink':lambda a:(a/'deployment.json').symlink_to(exportpath),
  }.items():
   config,backups,_=destination('negative-'+name);acquisition=backups/'.acquisitions';acquisition.mkdir(mode=0o700);setup(acquisition)
   before={str(p.relative_to(acquisition)):hashlib.sha256(p.read_bytes()).digest() for p in acquisition.rglob('*') if p.is_file() and not p.is_symlink()};run(normal,config,*args,ok=False)
   check(all((acquisition/p).is_file() and hashlib.sha256((acquisition/p).read_bytes()).digest()==digest for p,digest in before.items()),'refusal changed negative fixture')
  config,_,_=destination('initialized');provision(config);run(normal,config,*args,ok=False)
  for extra in (['-version'],['-dev'],['-restore','unused'],['-add-person','other']):run(normal,config,*(args+extra),ok=False)
  config,backups,_=destination('exclusive-lock');run(faulting,config,*args,stage='intent',ok=False)
  with open(backups/'.acquisitions'/'owner.lock','rb') as lock:fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB);run(normal,config,*args,ok=False)
  print('ok — negative initialized/corrupt fixtures preserved, exclusive options and producer ownership refused',flush=True)
 finally:
  for proc,log in processes:
   if proc.poll() is None:stop(proc)
   log.close()
print('PASS — synthetic producer recovery CLI contract; collector/artifact/data restoration tested separately',flush=True)
