"""Retain affected pre-existing gates' synthetic targets/evidence.
Changes only disposable project names, audited local ports/subnet, persistent
work roots and cleanup retention in archived runner copies. Assertions unchanged.
Run sequentially after other Docker gates have finished.
"""
import ipaddress,json,os,pathlib,re,secrets,shutil,socket,subprocess,sys
REPO=pathlib.Path(__file__).resolve().parents[2]
IMAGE=sys.argv[1];ROOT=pathlib.Path(sys.argv[2]).resolve()
if ROOT.exists() or not IMAGE.startswith('sha256:'):raise RuntimeError('immutable image and new private root required')
ROOT.mkdir(parents=True,mode=0o700)
ENV=dict(os.environ,PATH='/usr/local/go/bin:'+os.environ['PATH'])
network=ipaddress.ip_network('10.199.210.0/24')
inventory=ROOT/'before.json'
subprocess.run([sys.executable,str(REPO/'release/recovery-set/inventory.py'),str(inventory)],check=True)
for n in json.loads(inventory.read_text())['networks']:
 for c in n['ipam'].get('Config') or []:
  if c.get('Subnet') and ipaddress.ip_network(c['Subnet']).overlaps(network):raise RuntimeError('qualification subnet in use')
for port in range(30701,30705):
 with socket.socket() as s:s.bind(('127.0.0.1',port))
results=[]
for name,script,ports in [
 ('release','release/verify.sh',{'VOPTRACK_RELEASE_HTTPS_PORT':'30701','VOPTRACK_RELEASE_HTTP_PORT':'30702'}),
 ('household','release/household/verify.sh',{'VOPTRACK_HOUSEHOLD_HTTPS_PORT':'30701','VOPTRACK_HOUSEHOLD_HTTP_PORT':'30702'}),
 ('private-console','release/private-console/verify.sh',{'VOPTRACK_PRIVATE_HTTPS_PORT':'30701','VOPTRACK_PRIVATE_HTTP_PORT':'30702','VOPTRACK_PRIVATE_CONSOLE_PORT':'30703'}),
 ('home-app','release/home-app/verify.sh',{'VOPTRACK_HOMEAPP_HTTPS_PORT':'30701','VOPTRACK_HOMEAPP_HTTP_PORT':'30702','VOPTRACK_HOMEAPP_CONSOLE_PORT':'30703'}),
 ('research','release/research/verify.sh',{'VOPTRACK_RESEARCH_HTTPS_PORT':'30701','VOPTRACK_RESEARCH_HTTP_PORT':'30702'}),
]:
 case=ROOT/name;case.mkdir(mode=0o700);work=case/'work'
 source=(REPO/script).read_text()
 # Fixed source root, no repository mutation. Keep runner bytes for review.
 source=re.sub(r'^root=".*"$','root="'+str(REPO)+'"',source,count=1,flags=re.M)
 source=source.replace('work="$(mktemp -d)"','work="'+str(work)+'"; mkdir -m 700 "$work"')
 project='voptrack-hs-compat-'+name+'-'+secrets.token_hex(4)
 source=re.sub(r'^project=".*"$','project="'+project+'"',source,count=1,flags=re.M)
 source=source.replace('down -v','down').replace('; rm -rf "$work";','; :;')
 source=source.replace('cleanup() { dc down','cleanup() { dc logs --no-color > "$work/service-logs.txt" 2>&1 || true; dc down')
 if name=='research':
  anchor='dc run --rm -T init restore-kit < "$work/restore/kit.tar"'
  source=source.replace(anchor,'project="'+project+'-restore"\n'+anchor,1)
 runner=case/'verify.sh';runner.write_text(source);runner.chmod(0o700)
 env=dict(ENV,VOPTRACK_IMAGE=IMAGE,VOPTRACK_PUBLISH_PREFIX='127.0.0.1:',VOPTRACK_CONSOLE_PORT='30703',VOPTRACK_PROXY_SUBNET=str(network),VOPTRACK_PROXY_ADDRESS='10.199.210.2',VOPTRACK_PROXY_POOL='10.199.210.128/25',**ports)
 with (case/'gate.log').open('w') as log:p=subprocess.run(['bash',str(runner)],env=env,stdout=log,stderr=subprocess.STDOUT)
 result=dict(gate=name,exit=p.returncode,project=project,targets_retained=True);results.append(result)
 (ROOT/'results.json').write_text(json.dumps(results,indent=2)+'\n')
 print(name+': '+('PASS' if p.returncode==0 else 'FAIL; retained diagnostics'),flush=True)
 if p.returncode:raise RuntimeError('compatibility gate failed: '+name)
subprocess.run([sys.executable,str(REPO/'release/recovery-set/inventory.py'),str(ROOT/'after.json')],check=True)
print('Affected release/Home/Research gates: PASS; disposable volumes and evidence retained',flush=True)
