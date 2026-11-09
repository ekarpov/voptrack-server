"""Read-only Docker preservation inventory; retain hashes, never configurations."""
import hashlib,json,pathlib,subprocess,sys

def run(*args):return subprocess.check_output(args,text=True)
def digest(value):return hashlib.sha256(json.dumps(value,sort_keys=True).encode()).hexdigest()
ids=run('docker','ps','-aq').split();containers=json.loads(run('docker','inspect',*ids)) if ids else []
ids=run('docker','network','ls','-q').split();networks=json.loads(run('docker','network','inspect',*ids)) if ids else []
image=subprocess.run(['docker','image','inspect','voptrack-server:latest'],capture_output=True,text=True)
names=run('docker','volume','ls','-q').split();volumes=json.loads(run('docker','volume','inspect',*names)) if names else []
value={'volumes':[{'id':digest(v['Name']),'driver':v['Driver'],'scope':v['Scope'],'created_at':v.get('CreatedAt'),'labels':digest(v.get('Labels')),'options':digest(v.get('Options'))} for v in volumes],'containers':[{'id':c['Id'],'image':c['Image'],'config':digest(c['Config']),'hostconfig':digest(c['HostConfig']),'mounts':digest(sorted(c['Mounts'],key=lambda m:m['Destination'])),'state':{k:c['State'][k] for k in ['Status','Running','StartedAt','FinishedAt']},'restart_count':c['RestartCount']} for c in containers],'networks':[{'id':n['Id'],'ipam':n['IPAM'],'endpoints':digest(n.get('Containers',{}))} for n in networks],'default_image':json.loads(image.stdout)[0]['Id'] if image.returncode==0 else None}
pathlib.Path(sys.argv[1]).write_text(json.dumps(value,indent=2)+'\n')
