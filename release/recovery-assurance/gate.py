"""Fresh Home/Research -> native outbox -> local TLS destination -> guided restore.
No AWS endpoints, ambient AWS credentials, deployment or deletion of targets.
All sources, failed runs and restored volumes remain under private task custody.
"""
import hashlib,importlib.util,json,os,pathlib,secrets,shutil,socket,ssl,subprocess,sys,time,ipaddress

import sys as _sys
from pathlib import Path as _Path
_sys.path.insert(0, str(_Path(__file__).resolve().parents[2] / 'ci'))
from tessera_pin import expected_pin
PIN = expected_pin()
REPO=pathlib.Path(__file__).resolve().parents[2]
IMAGE=sys.argv[1];ROOT=pathlib.Path(sys.argv[2]).resolve()
HOME_MODE=os.environ.get('VOPTRACK_ASSURANCE_HOME_MODE','public')
if HOME_MODE not in ('public','private','off'):raise ValueError('unsupported Home mode')
def check(v,label):
 if not v:raise RuntimeError(label)
def write(path,value):
 with path.open('x') as f:
  os.fchmod(f.fileno(),0o600);json.dump(value,f,indent=2);f.write('\n')
def run(args,env=None,cwd=REPO,private=False):
 p=subprocess.run(args,env=env,cwd=cwd,capture_output=True)
 check(p.returncode==0,'private qualification operation refused; diagnostics retained' if private else 'qualification operation refused')
 return p.stdout
def inventory(path):run([sys.executable,str(REPO/'release/recovery-set/inventory.py'),str(path)])
check(IMAGE.startswith('sha256:') and len(IMAGE)==71,'immutable image required')
check(not ROOT.exists(),'fresh evidence root required')
ROOT.mkdir(parents=True,mode=0o700);ROOT.chmod(0o700)
# Explicitly strip inherited project/deployment/cloud settings.
ENV={k:v for k,v in os.environ.items() if not k.startswith(('VOPTRACK_','AWS_','COMPOSE_'))}
ENV['PATH']='/usr/local/go/bin:'+ENV['PATH']
ENV.update(VOPTRACK_IMAGE=IMAGE,VOPTRACK_DOMAIN='localhost',VOPTRACK_PUBLISH_PREFIX='127.0.0.1:',VOPTRACK_TLS='local',VOPTRACK_STORAGE_ENCRYPTED='yes',VOPTRACK_BACKUP_KEEP='8',VOPTRACK_BACKUP_EVERY_HOURS='1',VOPTRACK_RECOVERY_PRESERVE_TARGETS='yes',VOPTRACK_RECOVERY_ASSURANCE='yes')
inventory(ROOT/'before.json')
run(['docker','image','inspect',IMAGE],private=True)
version=run(['docker','run','--rm','--network','none','--tmpfs','/var/lib/tessera','--entrypoint','tesserad',IMAGE,'-version']).decode().strip()
check(version==PIN,'actual image pin mismatch')
run(['docker','image','inspect','caddy:2-alpine'],private=True)
for port in [31021,31022,31023,31024,30691,30943,30944]:
 with socket.socket() as probe:probe.bind(('127.0.0.1',port))
subnet=ipaddress.ip_network('10.199.230.0/24')
for network in json.loads((ROOT/'before.json').read_text())['networks']:
 for cfg in network['ipam'].get('Config') or []:
  if cfg.get('Subnet'):check(not ipaddress.ip_network(cfg['Subnet']).overlaps(subnet),'qualification subnet in use')
ENV.update(VOPTRACK_HTTP_PORT='31021',VOPTRACK_HTTPS_PORT='31022',VOPTRACK_CONSOLE_PORT='31023',VOPTRACK_SETUP_PORT='31024',VOPTRACK_SETUP_ORIGIN='https://localhost:31024',VOPTRACK_PUBLIC_URL='https://localhost:31022',VOPTRACK_PROXY_SUBNET=str(subnet),VOPTRACK_PROXY_POOL='10.199.230.128/25',VOPTRACK_PROXY_ADDRESS='10.199.230.2')
retained=ROOT/'retained';retained.mkdir(mode=0o700)
# Reuse only Browser/form helpers; importing this gate performs no Docker actions.
spec=importlib.util.spec_from_file_location('assurance_home_helpers',REPO/'release/home-setup/gate.py')
home=importlib.util.module_from_spec(spec);spec.loader.exec_module(home)
projects=[]
def source(variant):
 work=ROOT/('source-'+variant);work.mkdir(mode=0o700)
 project='voptrack-assurance-'+variant+'-'+secrets.token_hex(5)
 for args in [('ps','-aq'),('volume','ls','-q'),('network','ls','-q')]:
  check(not run(['docker',*args,'--filter','label=com.docker.compose.project='+project]).strip(),'source project already exists')
 projects.append(project)
 env=dict(ENV,VOPTRACK_VARIANT=variant)
 if variant=='household':
  env.update(VOPTRACK_ASSURANCE_HOME_MODE=HOME_MODE,VOPTRACK_CONSOLE='off' if HOME_MODE=='off' else 'on',VOPTRACK_CONSOLE_PRIVATE='yes' if HOME_MODE=='private' else 'no')
  if HOME_MODE=='private':env['VOPTRACK_ASSURANCE_CONSOLE_URL']='https://localhost:31023'
 shutil.copyfile(REPO/'release/compose.yaml',work/'compose.yaml')
 overlay={'services':{'collector':{'command':['-outbox','/var/lib/voptrack-bundles/.replication-outbox']}}}
 if variant=='household' and HOME_MODE=='off':
  overlay['services']['host-observe']={'image':IMAGE,'entrypoint':['sh','-c','umask 077; cat > /run/approved-token; exec voptrack-deploy observe-settings'],'profiles':['qualification'],'read_only':True,'cap_drop':['ALL'],'security_opt':['no-new-privileges:true'],'tmpfs':['/tmp','/run:uid=10001,mode=0700'],'volumes':['public:/var/lib/voptrack-public:ro','console:/var/lib/voptrack-console']}
 (work/'assurance.json').write_text(json.dumps(overlay))
 paths=[str(work/'compose.yaml')]
 if variant=='household':paths.append(str(REPO/'release/home-setup/compose.yaml'))
 paths.append(str(work/'assurance.json'));env['COMPOSE_FILE']=':'.join(paths)
 def dc(*args,private=False,ok=True):
  p=subprocess.run(['docker','compose','-p',project,*args],env=env,cwd=work,capture_output=True)
  if not private:
   with (work/'compose.log').open('ab') as f:f.write(p.stdout+p.stderr)
  if ok:check(p.returncode==0,'source Compose operation refused; private diagnostics retained')
  return p
 try:
  if variant=='household':
   dc('up','-d','--pull','never','setup')
   grant=json.loads(dc('run','--rm','--no-deps','-T','setup-admin','access',private=True).stdout)
   cert=work/'setup.crt';cert.write_bytes(dc('exec','-T','setup','cat','/var/lib/voptrack-setup/tls.crt',private=True).stdout)
   check(hashlib.sha256(ssl.PEM_cert_to_DER_cert(cert.read_text())).hexdigest()==grant['certificate_sha256'],'setup fingerprint mismatch')
   browser=home.Browser(env['VOPTRACK_SETUP_ORIGIN'],cert);page=home.wait(lambda:browser.call()[1],'setup did not listen')
   check(browser.post('/auth',home.field(page,'csrf'),code=grant['code'])[0]==303,'setup authority refused')
   csrf=home.field(browser.call()[1],'csrf')
   check(browser.post('/settings',csrf,name='Assurance Home',timezone='Europe/Helsinki',mode=HOME_MODE,encrypted='yes')[0]==303,'Home choices refused')
   page=browser.call()[1];form={k:home.field(page,k) for k in ('csrf','binding','confirm')}
   password=secrets.token_urlsafe(24);form.update(password=password,repeat=password,lang='en')
   check(browser.call('/apply',form)[0]==303,'Home confirmation refused')
   def ready():
    page=browser.call()[1]
    if b'action="/kit"' in page:return page
    check(b'Creating Home' in page,'Home initialization stopped')
   home.wait(ready,'Home initialization timed out',200)
   for route,name in [('kit','initial-material.tar'),('key','initial-backup.key')]:
    st,raw,_=browser.post('/'+route,csrf);check(st==200,'initial custody export refused')
    (work/name).write_bytes(raw);(work/name).chmod(0o600)
   check(browser.post('/finish',csrf,custody='yes',kitsha=home.field(browser.call()[1],'kitsha'))[0]==200,'Home completion refused')
   (work/'owner').write_text(password);(work/'owner').chmod(0o600)
  # Prepare the host-owned private outbox before any collector starts.
  dc('run','--rm','--no-deps','-T','--entrypoint','sh','collector','-c','umask 077; mkdir -m700 /var/lib/voptrack-bundles/.replication-outbox')
  dc('up','-d','--wait','--pull','never')
  if variant=='research':
   (work/'owner').write_bytes(dc('run','--rm','--no-deps','-T','init','password',private=True).stdout);(work/'owner').chmod(0o600)
  (work/'ca.crt').write_bytes(dc('exec','-T','caddy','cat','/var/lib/voptrack-public/ca.crt',private=True).stdout)
  if variant=='household':
   helper=REPO/'release/console/drive.py';saved_argv=sys.argv[:]
   try:
    sys.argv=[str(helper),env['VOPTRACK_PUBLIC_URL'],str(work/'ca.crt'),str(work/'owner')]
    g={'__file__':str(helper),'__name__':'assurance_owner_check'}
    exec(compile(helper.read_text().split('# ---- the owner invites the household from the Console')[0],str(helper),'exec'),g)
    try:g['app_token']('owner',g['owner_pw'],{'administer':'yes'})
    except (Exception,SystemExit):raise RuntimeError('chosen setup owner password did not authenticate') from None
   finally:sys.argv=saved_argv
  if variant=='research':
   with (work/'study.log').open('wb') as log:
    p=subprocess.run([sys.executable,str(REPO/'release/recovery-assurance/research.py'),env['VOPTRACK_PUBLIC_URL'],str(work/'ca.crt'),str(work/'owner'),str(work/'research-assurance.json')],env=env,stdout=log,stderr=subprocess.STDOUT)
   check(p.returncode==0,'Research wizard fixture failed; private diagnostics retained')
  with (work/'create.log').open('wb') as log:
   p=subprocess.run([sys.executable,str(REPO/'release/recovery-set/create.py'),env['VOPTRACK_PUBLIC_URL'],str(work/'ca.crt'),str(work/'owner'),str(work),project,str(retained/variant)],env=env,stdout=log,stderr=subprocess.STDOUT)
  check(p.returncode==0,'genuine recovery set creation failed; private diagnostics retained')
  if variant=='research':shutil.copyfile(work/'research-assurance.json',retained/variant/'research-assurance.json')
  check(not dc('ps','-aq',private=True).stdout.strip(),'source service containers remain')
  volumes=run(['docker','volume','ls','-q','--filter','label=com.docker.compose.project='+project]).decode().split()
  check(bool(volumes),'source volumes were not preserved')
  write(work/'result.json',{'variant':variant,'image':IMAGE,'entry':'protected-home-setup' if variant=='household' else 'operator-and-study-wizard','native_artifacts':True,'real_collector_outbox':True,'host_approved_committed_kit':True,'source_services_unavailable':True,'source_volumes_preserved':len(volumes),'home_mode':HOME_MODE if variant=='household' else None})
  print(variant+': fresh entry, native acquisition, committed kit, source stopped; targets preserved',flush=True)
 finally:
  dc('logs','--no-color',ok=False)
  dc('down','--remove-orphans',ok=False)
try:
 for variant in ('household','research'):source(variant)
 # Neither source project is running when destination transfer/fetch begins.
 for project in projects:
  check(not run(['docker','ps','-aq','--filter','label=com.docker.compose.project='+project]).strip(),'source services still available')
 env=dict(ENV,VOPTRACK_OFF_MACHINE_MODE='assurance')
 with (ROOT/'off-machine.log').open('wb') as log:
  p=subprocess.run([sys.executable,str(REPO/'release/off-machine/gate.py'),IMAGE,str(retained),str(ROOT/'off-machine')],env=env,stdout=log,stderr=subprocess.STDOUT)
 check(p.returncode==0,'integrated destination/recovery gate failed; private evidence retained')
 from report import build_report
 write(ROOT/'result.json',build_report(ROOT,IMAGE,HOME_MODE))
 print('Local recovery assurance: PASS; AWS/IAM, separate host and physical custody remain pending',flush=True)
finally:
 inventory(ROOT/'after.json')
