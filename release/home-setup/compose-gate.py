"""Actual opt-in Compose setup + persisted private Console routing.
New labelled project only; volumes and partial targets survive cleanup.
"""
import json,os,pathlib,secrets,socket,subprocess,sys,time,ipaddress
import gate
IMAGE=sys.argv[1];ROOT=pathlib.Path(sys.argv[2]).resolve()
gate.check(not ROOT.exists() and IMAGE.startswith('sha256:'),'new root and immutable image required')
gate.private(ROOT)
PROJECT='voptrack-hs-compose-'+secrets.token_hex(5)
ENV={k:v for k,v in os.environ.items() if not k.startswith('VOPTRACK_')}
PORT_BASE=int(os.environ.get('VOPTRACK_HOME_SETUP_COMPOSE_PORT_BASE','30711'))
gate.check(1 <= PORT_BASE <= 65532,'four valid consecutive qualification ports required')
ENV.update(VOPTRACK_IMAGE=IMAGE,VOPTRACK_DOMAIN='localhost',VOPTRACK_PUBLIC_URL='https://localhost:'+str(PORT_BASE+1),VOPTRACK_PUBLISH_PREFIX='127.0.0.1:',VOPTRACK_HTTP_PORT=str(PORT_BASE),VOPTRACK_HTTPS_PORT=str(PORT_BASE+1),VOPTRACK_CONSOLE_PORT=str(PORT_BASE+2),VOPTRACK_SETUP_PORT=str(PORT_BASE+3),VOPTRACK_SETUP_ORIGIN='https://localhost:'+str(PORT_BASE+3),VOPTRACK_PROXY_SUBNET=os.environ.get('VOPTRACK_HOME_SETUP_COMPOSE_SUBNET','10.199.211.0/24'),VOPTRACK_PROXY_POOL=os.environ.get('VOPTRACK_HOME_SETUP_COMPOSE_POOL','10.199.211.128/25'),VOPTRACK_PROXY_ADDRESS=os.environ.get('VOPTRACK_HOME_SETUP_COMPOSE_ADDRESS','10.199.211.2'),VOPTRACK_TLS='local')
for port in range(PORT_BASE,PORT_BASE+4):
 with socket.socket() as s:s.bind(('127.0.0.1',port))
subprocess.run([sys.executable,str(gate.REPO/'release/recovery-set/inventory.py'),str(ROOT/'before.json')],check=True)
for n in json.loads((ROOT/'before.json').read_text())['networks']:
 for c in n['ipam'].get('Config') or []:
  if c.get('Subnet') and ipaddress.ip_network(c['Subnet']).overlaps(ipaddress.ip_network(ENV['VOPTRACK_PROXY_SUBNET'])):raise RuntimeError('qualification subnet in use')
def dc(*args,ok=True,private=False):
 p=subprocess.run(['docker','compose','-p',PROJECT,'-f',str(gate.REPO/'release/compose.yaml'),'-f',str(gate.REPO/'release/home-setup/compose.yaml'),*args],env=ENV,capture_output=True)
 if not private:
  with (ROOT/'compose.log').open('ab') as log:log.write(p.stdout+p.stderr)
 if ok:gate.check(p.returncode==0,'opt-in Compose operation refused; private diagnostics retained')
 return p
config=json.loads(dc('--profile','setup-admin','config','--format','json',private=True).stdout)
gate.write(ROOT/'rendered-config.json',config)
setup=config['services']['setup'];admin=config['services']['setup-admin'];console=config['services']['console']
gate.check(admin['logging']['driver']=='none' and admin['network_mode']=='none','host access command logs or exposes grant')
gate.check({v['target'] for v in admin['volumes']}=={'/var/lib/voptrack-setup'},'host access command acquired initializer targets')
gate.check(not any(v['target'] in ['/var/lib/voptrack-setup','/var/lib/voptrack-bootstrap','/var/lib/voptrack-config','/var/lib/voptrack-ca'] for v in console['volumes']),'ordinary Console gained setup authority')
gate.check(setup['ports'][0]['host_ip']=='127.0.0.1','setup listener exposed beyond loopback')
try:
 dc('up','-d','setup')
 grant=json.loads(dc('run','--rm','--no-deps','-T','setup-admin','access',private=True).stdout)
 cert=ROOT/'setup.crt';cert.write_bytes(dc('exec','-T','setup','cat','/var/lib/voptrack-setup/tls.crt',private=True).stdout)
 import hashlib,ssl
 gate.check(hashlib.sha256(ssl.PEM_cert_to_DER_cert(cert.read_text())).hexdigest()==grant['certificate_sha256'],'Compose access fingerprint mismatch')
 b=gate.Browser(ENV['VOPTRACK_SETUP_ORIGIN'],cert)
 page=gate.wait(lambda:b.call()[1],'Compose setup did not listen')
 gate.check(b.post('/auth',gate.field(page,'csrf'),code=grant['code'])[0]==303,'Compose host grant refused')
 csrf=gate.field(b.call()[1],'csrf')
 gate.check(b.post('/settings',csrf,name='Synthetic Compose Home',timezone='Europe/Helsinki',mode='private',encrypted='yes')[0]==303,'Compose choices refused')
 page=b.call()[1];form={k:gate.field(page,k) for k in ['csrf','binding','confirm']}
 form.update(lang='en',password='Synthetic-compose-owner-password',repeat='Synthetic-compose-owner-password')
 gate.check(b.call('/apply',form)[0]==303,'Compose initialization refused')
 def ready():
  st,page,_=b.call()
  if b'action="/kit"' in page:return page
  gate.check(b'Creating Home' in page,'Compose initialization stopped')
 page=gate.wait(ready,'Compose setup not ready',200);csrf=gate.field(page,'csrf')
 for route in ['key','kit']:
  st,raw,_=b.post('/'+route,csrf);gate.check(st==200,'Compose material export failed')
  p=ROOT/('initial-material.tar' if route=='kit' else 'backup.key');p.write_bytes(raw);p.chmod(0o600)
 page=b.call()[1]
 gate.check(b.post('/finish',csrf,custody='yes',kitsha=gate.field(page,'kitsha'))[0]==200,'Compose completion failed')
 # Persisted private mode overrides the ordinary host defaults on restart.
 ENV.update(VOPTRACK_CONSOLE='off',VOPTRACK_NAME='wrong',VOPTRACK_TIMEZONE='UTC')
 dc('up','-d','--wait')
 cert=ROOT/'ca.crt';cert.write_bytes(dc('run','--rm','--no-deps','-T','--entrypoint','cat','init','/var/lib/voptrack-public/ca.crt',private=True).stdout)
 public=gate.Browser(ENV['VOPTRACK_PUBLIC_URL'],cert);private=gate.Browser('https://localhost:'+str(PORT_BASE+2),cert)
 gate.check(public.call('/healthz')[0]==404,'persisted private Console exposed on public origin')
 gate.check(private.call('/healthz')[0]==200,'persisted private Console did not start')
 status,raw,_=public.call('/.well-known/voptrack');product=json.loads(raw)
 gate.check(status==200 and product['product']=='home' and product['web_interface'] and product['timezone']=='Europe/Helsinki','Compose restart lost saved product choices')
 p=dc('run','--rm','--no-deps','-T','setup-admin','authorize',ok=False,private=True)
 gate.check(p.returncode!=0,'completed Compose setup reopened')
 gate.write(ROOT/'result.json',dict(pass_=True,image=IMAGE,project=PROJECT,private_routing_verified=True,ordinary_console_authority_unchanged=True,targets_retained=True))
 print('Opt-in Compose setup, saved choices and private Console routing: PASS',flush=True)
finally:
 dc('logs','--no-color',ok=False)
 dc('down','--remove-orphans',ok=False)
 subprocess.run([sys.executable,str(gate.REPO/'release/recovery-set/inventory.py'),str(ROOT/'after.json')],check=True)
