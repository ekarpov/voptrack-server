"""Offline actual-image preflight/refusal and fresh-target recovery drill.
Run on another Docker host for independent-host evidence. A same-host invocation
is explicitly only isolated-volume evidence. Only synthetic retained inputs enter.
"""
import contextlib,hashlib,io,json,os,pathlib,secrets,shutil,ssl,subprocess,sys,tarfile,tempfile,time,urllib.error,urllib.request
root=pathlib.Path(__file__).resolve().parents[2]
retained=pathlib.Path(sys.argv[1]).resolve();image=sys.argv[2];project=sys.argv[3];port=int(sys.argv[4])
negative_only=len(sys.argv)>5 and sys.argv[5]=='--negative-only'
prepared=pathlib.Path(sys.argv[6]).resolve() if len(sys.argv)>6 and sys.argv[5]=='--prepared-root' else None
def check(value,label):
 if not value:raise RuntimeError(label)
def docker(*args,data=None,ok=True):
 p=subprocess.run(['docker',*args],input=data,capture_output=True)
 if ok:check(p.returncode==0,'private drill host operation refused')
 return p
source_state=json.loads((retained/'source-removed.json').read_text())
check(source_state.get('source_removed') or source_state.get('source_services_unavailable'),'source-unavailability receipt missing')
base_args=['run','--rm','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--tmpfs','/tmp:uid=10001,mode=0700']
def preflight(inputs):
 return docker(*base_args[:-2],'--user',f'{os.getuid()}:{os.getgid()}','--tmpfs',f'/tmp:uid={os.getuid()},mode=0700','-v',str(inputs)+':/recovery-inputs:ro','-v',str(retained/'trust')+':/recovery-trust:ro','-v',str(retained/'key')+':/recovery-key:ro','--entrypoint','voptrack-recovery-set',image,ok=False)
selection=json.loads((retained/'inputs/selection.json').read_text())
for role in ('backup','evidence','kit','settings'):
 entry=selection[role];check(hashlib.sha256((retained/'inputs'/entry['file']).read_bytes()).hexdigest()==entry['sha256'],'retained input changed')
p=preflight(retained/'inputs');check(p.returncode==0,'valid recovery-set preflight refused')
receipt=json.loads(p.stdout);check(receipt['status']=='inputs-verified-not-restored' and receipt['off_machine_custody']=='unverified' and receipt['later_erasure_coverage']=='unknown-after-selected-evidence','preflight overstated recovery/custody')
(retained/'preflight-receipt.json').write_bytes(p.stdout);os.chmod(retained/'preflight-receipt.json',0o600)
# Recompute all untrusted outer hashes after forging the MAC. A digest-only
# implementation must fail this gate at the intended authentication assertion.
negative_context=contextlib.nullcontext(tempfile.mkdtemp(prefix='negative-',dir=retained)) if os.environ.get('VOPTRACK_RECOVERY_PRESERVE_TARGETS')=='yes' else tempfile.TemporaryDirectory(prefix='voptrack-negative-')
with negative_context as temporary:
 inputs=pathlib.Path(temporary);shutil.copytree(retained/'inputs',inputs,dirs_exist_ok=True)
 selected=json.loads((inputs/'selection.json').read_text())
 with tarfile.open(inputs/'backup.tar') as source:members={n:source.extractfile(n).read() for n in source.getnames()}
 manifest=json.loads(members['manifest.json']);metadata=next(a for a in manifest['operation']['artifacts'] if a['kind']=='metadata')
 envelope=json.loads(members[metadata['id']]);envelope['mac']=('0' if envelope['mac'][0]!='0' else '1')+envelope['mac'][1:]
 raw=json.dumps(envelope).encode();members[metadata['id']]=raw;metadata.update(size=len(raw),sha256=hashlib.sha256(raw).hexdigest());members['manifest.json']=json.dumps(manifest).encode()
 with tarfile.open(inputs/'backup.tar','w') as target:
  for name,raw in members.items():
   h=tarfile.TarInfo(name);h.size=len(raw);target.addfile(h,io.BytesIO(raw))
 selected['backup']['sha256']=hashlib.sha256((inputs/'backup.tar').read_bytes()).hexdigest();(inputs/'selection.json').write_text(json.dumps(selected))
 # UID used by the image must read these private task inputs.
 os.chmod(inputs,0o755)
 for path in inputs.iterdir():os.chmod(path,0o444)
 p=preflight(inputs);check(p.returncode==1 and not p.stdout,'preflight accepted forged metadata')
print('Actual-image forged-MAC refusal: PASS',flush=True)
if negative_only:sys.exit(0)
fixture=json.loads((retained/'drill-private.json').read_text());lineage=json.loads((retained/'trust/backup-lineage.json').read_text())
volumes={name:project+'-'+name for name in ['config','bootstrap','console','ca','public','data','collector','bundles','inputs']};server=project+'-server';console=project+'-console'
# Never remove a pre-existing target on success or failure.
for name in [server,console]:check(docker('container','inspect',name,ok=False).returncode!=0,'drill target container already exists')
for name in volumes.values():check(docker('volume','inspect',name,ok=False).returncode!=0,'drill target volume already exists')
mounts=[]
for name in ['config','bootstrap','console','ca','public']:mounts+=['-v',volumes[name]+':/var/lib/voptrack-'+name]
def restore_kit():return docker(*base_args,'-i',*mounts,'--entrypoint','voptrack-init',image,'restore-committed-kit',data=(retained/'inputs/kit.tar').read_bytes(),ok=False)
try:
 if prepared is None:
  check(restore_kit().returncode==0,'fresh kit restoration refused')
  check(restore_kit().returncode!=0,'repeated kit restore overwrote target')
  archive=io.BytesIO()
  with tarfile.open(fileobj=archive,mode='w') as target:
   for path in (retained/'inputs').iterdir():
    raw=path.read_bytes();h=tarfile.TarInfo(path.name);h.size=len(raw);h.mode=0o400;h.uid=h.gid=10001;target.addfile(h,io.BytesIO(raw))
  docker('run','--rm','-i','--network','none','--user','0:0','-v',volumes['inputs']+':/inputs','--entrypoint','sh',image,'-c','set -e; tar -xf - -C /inputs; chown 10001:10001 /inputs; chmod 0700 /inputs',data=archive.getvalue())
  # Same immutable read-only inputs that passed preflight; gate fixture extraction
  # is not an untrusted browser-upload API.
  def artifact(bundle,kind):
   with tarfile.open(retained/'inputs'/bundle) as stream:return next(a['id'] for a in json.load(stream.extractfile('manifest.json'))['operation']['artifacts'] if a['kind']==kind)
  archive=artifact('backup.tar','archive');evidence=artifact('evidence.tar','evidence')
  script='set -e; mkdir -m700 /tmp/archive /tmp/evidence /tmp/backups; tar -xf /inputs/backup.tar -C /tmp/archive; tar -xf /inputs/evidence.tar -C /tmp/evidence; sed -i "s#/var/lib/voptrack-backups#/tmp/backups#g" /var/lib/voptrack-config/server.json; cp /var/lib/voptrack-config/server.crt /var/lib/voptrack-public/server.crt; voptrack-deploy recover-producer >/tmp/producer.log 2>&1; tesserad -config /var/lib/voptrack-config/server.json -restore /tmp/archive/'+archive+' -backup-key-file /var/lib/voptrack-config/backup.key -retirement-fence /tmp/evidence/'+evidence+' >/tmp/restore.log 2>&1; exec tesserad -config /var/lib/voptrack-config/server.json'
  server_mounts=['-v',volumes['config']+':/var/lib/voptrack-config','-v',volumes['public']+':/var/lib/voptrack-public']
  docker('run','-d','--name',server,'--label','voptrack.recovery-set='+project,'--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--tmpfs','/tmp:uid=10001,mode=0700','-p',f'127.0.0.1:{port}:8443',*server_mounts,'-v',volumes['data']+':/var/lib/tessera','-v',volumes['inputs']+':/inputs:ro','-v',volumes['inputs']+':/run:ro','-e','VOPTRACK_TRUSTED_BACKUP_DEPLOYMENT='+lineage['deployment'],'--entrypoint','sh',image,'-c',script)
 else:
  check((prepared/'var/lib/tessera/dataset.db').is_file(),'guided target database missing')
  if os.environ.get('VOPTRACK_RECOVERY_ASSURANCE')=='yes' and source_state['variant']=='household':
   # Explicit operator activation regenerates derived routing after restoration.
   activation=[]
   for name in ['tessera','voptrack-config','voptrack-bootstrap','voptrack-console','voptrack-ca','voptrack-public','voptrack-backups']:
    # Empty app-only directories are not kit members; do not let Docker create
    # root-owned bind directories that the explicit activation uid cannot use.
    (prepared/'var/lib'/name).mkdir(mode=0o700,exist_ok=True)
    activation+=['-v',str(prepared/'var/lib'/name)+':/var/lib/'+name]
   p=docker('run','--rm','--network','none','--user',f'{os.getuid()}:{os.getgid()}','--tmpfs',f'/tmp:uid={os.getuid()},mode=0700',*activation,'-e','VOPTRACK_STORAGE_ENCRYPTED=yes','-e','VOPTRACK_VARIANT=household','-e','VOPTRACK_TLS=local','-e','VOPTRACK_DOMAIN=localhost','-e','VOPTRACK_PUBLIC_URL=https://localhost:'+str(port),'-e','VOPTRACK_BACKUP_EVERY_HOURS='+str(fixture['settings']['every_hours']),'-e','VOPTRACK_BACKUP_KEEP='+str(fixture['settings']['keep']),'--entrypoint','voptrack-init',image,'init',ok=False)
   (retained/'activation.log').write_bytes(p.stdout+p.stderr)
   check(p.returncode==0,'explicit recovered Home activation refused')
  server_mounts=[]
  for name in ['config','public','data','backups']:
   path=prepared/'var/lib'/('tessera' if name=='data' else 'voptrack-'+name)
   server_mounts+=['-v',str(path)+':/var/lib/'+('tessera' if name=='data' else 'voptrack-'+name)]
  docker('run','-d','--name',server,'--label','voptrack.recovery-set='+project,'--user',f'{os.getuid()}:{os.getgid()}','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--tmpfs',f'/tmp:uid={os.getuid()},mode=0700','-p',f'127.0.0.1:{port}:8443',*server_mounts,'--entrypoint','tesserad',image,'-config','/var/lib/voptrack-config/server.json')
 cert=(prepared/'var/lib/voptrack-config/server.crt').read_bytes() if prepared else docker('run',*['--rm','--network','none'], '-v',volumes['config']+':/config:ro','--entrypoint','cat',image,'/config/server.crt').stdout
 ca=retained/'restored-server.crt';ca.write_bytes(cert);ca.chmod(0o644) # Public trust material mounted into the collector UID.
 password=retained/'owner-password';password.write_text(fixture['owner_password']);os.chmod(password,0o600)
 source=root/'release/console/drive.py';marker='# ---- the owner invites the household from the Console';g={'__file__':str(source),'__name__':'recovered_helpers'}
 sys.argv=[str(source),'https://localhost:'+str(port),str(ca),str(password)]
 exec(compile(source.read_text().split(marker)[0],str(source),'exec'),g)
 for _ in range(150):
  try:
   if g['request'](g['browser'](),'GET','/.well-known/tessera')[0]==200:break
  except (OSError,urllib.error.URLError):pass
  time.sleep(.1)
 else:raise RuntimeError('recovered server readiness failed')
 admin=g['app_token']('owner',fixture['owner_password'],{'administer':'yes'})
 member=g['app_token']('drill-member',fixture['member_password'],{'read':'yes','write':'yes','schema':'yes','erase':'yes'})
 api=g['api']
 def query(token,dataset,track):
  return g['request'](g['browser'](),'POST',f'/v1/datasets/{dataset}/objects/{track}:query',js={'query':{}},headers={'Authorization':'Bearer '+token})
 st,body,_=api(admin,'GET','/admin/api/v1/backup-scheduler');check(st==200,'recovered scheduler refused');observed=json.loads(body)
 check(observed['settings_scope']!=fixture['old_scope'] and observed['settings_scope'].startswith(lineage['deployment']+'.'),'producer epoch not renewed')
 check(all(observed['settings'][k]==fixture['settings'][k] for k in ['every_hours','keep']),'settings recovery mismatch')
 check(api(fixture['source_admin'],'GET','/admin/api/v1/backup-scheduler')[0]==401,'post-snapshot source authority survived')
 st,body,_=query(member,fixture['dataset'],fixture['track'])
 check(st==200 and len(json.loads(body)['records'])==1,'unrelated records lost (HTTP '+str(st)+'; count '+str(len(json.loads(body).get('records',[])) if st==200 else -1)+')')
 check(query(admin,fixture['dataset'],fixture['track'])[0] in (403,404),'administrator gained private record access')
 check(api(member,'GET',f'/v1/stores/{fixture["content_store"]}/items/{fixture["item"]}?representation=original')[0] in (404,410),'newer evidence did not exclude erased content')
 if os.environ.get('VOPTRACK_RECOVERY_ASSURANCE')=='yes' and source_state['variant']=='household':
  check(prepared is not None,'Home assurance requires actual guided target')
  home=json.loads((prepared/'var/lib/voptrack-bootstrap/home-setup.json').read_text())
  config=json.loads((prepared/'var/lib/voptrack-config/server.json').read_text())
  product=json.loads((prepared/'var/lib/voptrack-public/voptrack.json').read_text())
  check(home['Name']=='Assurance Home' and home['TimeZone']=='Europe/Helsinki' and home['Mode']==fixture.get('home_mode','public') and home['Encrypted'] is True,'restored fixed Home choices changed')
  check(config['display_name']==home['Name'] and product['timezone']==home['TimeZone'] and product['web_interface'] is (fixture.get('home_mode','public')!='off'),'restored Home configuration mismatched choices')
  check((prepared/'var/lib/voptrack-bootstrap/home-password-retired').read_text()=='initial password retired\n','restored Home lost bootstrap password retirement')
  mode=home['Mode']
  for name in ('home.secret','console-state.secret','console-state.dataset'):
   check((prepared/'var/lib/voptrack-console'/name).exists()==(mode!='off'),'restored Home mode changed service authority')
  for name,blocked in [('console-public.caddy',mode!='public'),('console-private.caddy',mode=='off')]:
   check(('respond ' in (prepared/'var/lib/voptrack-public'/name).read_text())==blocked,'restored Home mode changed Console routing')
  (retained/'home-assurance-receipt.json').write_text(json.dumps({'fixed_choices_restored':True,'configuration_matches':True,'password_retirement_preserved':True,'mode_authority_and_routing_preserved':True,'home_mode':home['Mode'],'app_only_authority_absent':home['Mode']=='off'})+'\n')
 # Extra assurance-only checks use the genuine study wizard fixture, not a
 # fabricated study document. Credentials remain in the private fixture file.
 study=retained/'research-assurance.json'
 if study.exists():
  research=json.loads(study.read_text())
  participant=g['app_token'](research['account'],research['password'],{'read':'yes','write':'yes'})
  st,records,_=query(participant,research['dataset'],research['track'])
  check(st==200 and len(json.loads(records)['records'])==research['records'],'restored participant records lost')
  result=json.loads(records);order=[field['id'] for field in result['meta']['fields']]
  values=dict(zip(order,result['records'][0]['values']))
  check(all(values.get(key,{}).get('value')==expected for key,expected in research['fields'].items()),'restored participant field values changed')

  foreign=g['app_token']('drill-member',fixture['member_password'],{'read':'yes'})
  check(query(foreign,research['dataset'],research['track'])[0] in (403,404),'restored participant isolation failed')
  check(query(admin,research['dataset'],research['track'])[0] in (403,404),'restored owner gained participant store')
 # Explicit keyless collector re-enrollment on the recovered host.
 collector_args=['--rm','--network','container:'+server,'--read-only','--tmpfs','/tmp:uid=10001,mode=0700','-v',volumes['collector']+':/var/lib/voptrack-collector','-v',volumes['bundles']+':/var/lib/voptrack-bundles','-v',str(ca)+':/server.crt:ro']
 docker('run',*collector_args,'-i','--entrypoint','sh',image,'-c','umask 077; cat > /tmp/admin; exec voptrack-collector -mode provision -administrator-file /tmp/admin -origin https://localhost:8443 -ca /server.crt -id '+secrets.token_hex(16)+' -deployment '+lineage['deployment'],data=admin.encode())
 for _ in range(300):
  st,body,_=api(admin,'GET','/admin/api/v1/backup-scheduler');state=json.loads(body)
  if state['active_scheduled_operation'] is None and state['retention']['state']=='converged':break
  time.sleep(.05)
 identifier=str(int(time.time()))+'-'+secrets.token_hex(16)
 check(api(admin,'PUT','/admin/api/v1/backup-operations/'+identifier,{'kind':'backup'})[0]==202,'recovered backup admission refused')
 for _ in range(300):
  st,body,_=api(admin,'GET','/admin/api/v1/backup-operations/'+identifier);op=json.loads(body)
  if op['state']!='running':break
  time.sleep(.05)
 check(op['state']=='complete','recovered backup failed')
 docker('run',*collector_args,'--entrypoint','voptrack-collector',image,'-once','-origin','https://localhost:8443','-ca','/server.crt')
 docker('run',*collector_args,'--entrypoint','sh',image,'-c','test ! -f /var/lib/voptrack-config/backup.key && test -f /var/lib/voptrack-bundles/'+identifier+'/backup.tar')
 variant=source_state['variant']
 console_expected=fixture.get('console_state_expected',True)
 if not console_expected:
  check(prepared is not None,'app-only assurance needs guided target')
  for name in ('home.secret','console-state.secret','console-state.dataset'):
   check(not (prepared/'var/lib/voptrack-console'/name).exists(),'app-only recovery created Console authority')
  for name in ('console-public.caddy','console-private.caddy'):
   check('respond ' in (prepared/'var/lib/voptrack-public'/name).read_text(),'app-only recovery enabled Console routing')
 else:
  # The Console starts using restored service material and its archived state store.
  env={'TESSERA_URL':'https://localhost:8443','TESSERA_CA_FILE':'/var/lib/voptrack-public/server.crt','CONSOLE_STATE_DIR':'/var/lib/voptrack-console','STATE_SERVICE':'console-state','STATE_SECRET_FILE':'/var/lib/voptrack-console/console-state.secret','STATE_DATASET_FILE':'/var/lib/voptrack-console/console-state.dataset','HOME_SERVICE':'home','HOME_SECRET_FILE':'/var/lib/voptrack-console/home.secret','ANALYSIS_SERVICE':'analysis','ANALYSIS_SECRET_FILE':'/var/lib/voptrack-console/analysis.secret','ISSUER_ID':'home-members','ISSUER_SECRET_FILE':'/var/lib/voptrack-console/issuer-home-members.secret','VOPTRACK_VARIANT':variant,'VOPTRACK_PUBLIC_URL':'https://localhost:'+str(port),'CONSOLE_LISTEN':':9481'}
  args=[]
  for key,value in env.items():args+=['-e',key+'='+value]
  # Console uses a different listener; Docker cannot add a published port to an
  # existing container. Verify it internally through its TLS-trusting auth client
  # using an ephemeral localhost forward on the server's already published port.
  docker('run',*(['--user',f'{os.getuid()}:{os.getgid()}'] if prepared else []),'-d','--name',console,'--label','voptrack.recovery-set='+project,'--network','container:'+server,'-v',(str(prepared/'var/lib/voptrack-console') if prepared else volumes['console'])+':/var/lib/voptrack-console','-v',(str(prepared/'var/lib/voptrack-public') if prepared else volumes['public'])+':/var/lib/voptrack-public:ro',*args,'--entrypoint','voptrack-deploy',image,'console')
  for _ in range(100):
   p=docker('exec',console,'curl','-fsS','http://127.0.0.1:9481/connect-app',ok=False)
   if p.returncode==0:break
   time.sleep(.1)
  else:raise RuntimeError('restored Console failed readiness')
  docker('exec',console,'sh','-c','test ! -f /var/lib/voptrack-config/backup.key && test ! -f /var/lib/voptrack-ca/root.key')
  # Prove the archived private Console dataset is usable via its retained service
  # credential, not merely that the public connect page renders.
  check(docker('exec',console,'test','-s','/var/lib/voptrack-console/console-state.dataset').returncode==0,'Console state material missing')
  state_dataset=docker('exec',console,'cat','/var/lib/voptrack-console/console-state.dataset').stdout.decode().strip()
  state_secret=docker('exec',console,'cat','/var/lib/voptrack-console/console-state.secret').stdout.decode().strip()
  import base64
  st,body,_=g['request'](g['browser'](),'POST','/oauth/token',form={'grant_type':'client_credentials','dataset':state_dataset},headers={'Authorization':'Basic '+base64.b64encode(('console-state:'+state_secret).encode()).decode()})
  check(st==200,'restored Console service credential refused');state_token=json.loads(body)['access_token']
  st,body,_=query(state_token,state_dataset,'voptrack.console.document')
  check(st==200 and 'Recovery fixture' in body,'saved Console dashboard did not survive restore')
  if study.exists():
   st,body,_=query(state_token,state_dataset,'voptrack.console.consent')
   check(st==200,'restored study consent query refused')
   result=json.loads(body);order=[field['id'] for field in result['meta']['fields']]
   rows=[dict(zip(order,record['values'])) for record in result['records']]
   check(any(row.get('study',{}).get('value')=='assurance' and row.get('account',{}).get('value')==research['account'] and row.get('version',{}).get('value')==1 for row in rows),'restored study consent missing')

   st,body,_=query(state_token,state_dataset,'voptrack.console.document')
   check(st==200 and 'Assurance study' in body,'restored study wizard document missing')
   (retained/'research-assurance-receipt.json').write_text(json.dumps({'study_document_restored':True,'consent_restored':True,'participant_records_restored':True,'participant_isolation':True})+'\n')
 (retained/'drill-receipt.json').write_text(json.dumps({'version':1,'variant':variant,'source_unavailable':True,'preflight_selection_sha256':receipt['selection_sha256'],'restored':True,'newer_evidence_excludes_erasure':True,'private_record_isolation':True,'new_epoch_and_settings':True,'explicit_keyless_collector_and_backup':True,'console_started':console_expected,'console_state_expected':console_expected,'app_only_authority_absent':not console_expected,'physical_custody_attested':False})+'\n')
 os.chmod(retained/'drill-receipt.json',0o600)
 print('Fresh-target recovery: PASS; identity/settings, erasure, isolation, '+('Console state' if console_expected else 'absent app-only Console authority')+' and explicit keyless collector.',flush=True)
finally:
 for name in [console,server]:docker('rm','-fv',name,ok=False)
 if os.environ.get('VOPTRACK_RECOVERY_PRESERVE_TARGETS')!='yes':
  for name in volumes.values():docker('volume','rm',name,ok=False)
