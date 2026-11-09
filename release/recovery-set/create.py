"""Create a small genuine retained set through supported interfaces, then stop
its source deployment. The assurance opt-in preserves all source volumes. Synthetic credentials stay only in private drill files.
"""
import datetime,hashlib,io,json,os,pathlib,re,secrets,subprocess,sys,tarfile,time,urllib.request,urllib.parse
base,ca,password_file,work,project,destination=sys.argv[1:]
root=pathlib.Path(__file__).resolve().parents[2]
source=root/'release/console/drive.py';marker='# ---- the owner invites the household from the Console'
g={'__file__':str(source),'__name__':'recovery_set_helpers'}
exec(compile(source.read_text().split(marker)[0],str(source),'exec'),g)
home_mode=os.environ.get('VOPTRACK_ASSURANCE_HOME_MODE','public') if os.environ.get('VOPTRACK_VARIANT')=='household' else 'public'
app_only=home_mode=='off'
console_base=os.environ.get('VOPTRACK_ASSURANCE_CONSOLE_URL',base)
# Native OAuth/API always uses the public server; private Console uses its own listener.
if console_base!=base:
 original_request=g['request'];g['base']=console_base
 def routed_request(opener,method,url,**kw):
  if url.startswith(('/oauth/','/admin/','/v1/','/.well-known/')):url=base+url
  return original_request(opener,method,url,**kw)
 g['request']=routed_request
def check(value,label):
 if not value:raise RuntimeError(label)
def dc(*args,data=None):
 p=subprocess.run(['docker','compose','-p',project,*args],cwd=work,input=data,capture_output=True)
 check(p.returncode==0,'source host operation refused');return p.stdout
out=pathlib.Path(destination);out.mkdir(mode=0o700)
for name in ['inputs','trust','key']:(out/name).mkdir(mode=0o700)
def write(name,data):
 path=out/name
 fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
 with os.fdopen(fd,'wb') as f:f.write(data)
# Provision an ordinary synthetic person's own store with the supported CLI,
# while every task-owned writer is stopped. No private identity mutation.
dc('stop','collector','kit','console','tesserad')
member_password=secrets.token_urlsafe(24)
dc('run','--rm','--no-deps','-T','--entrypoint','sh','init','-c','umask 077; cat > /tmp/member; exec tesserad -config /var/lib/voptrack-config/server.json -add-person drill-custodian -may-create-datasets -password-file /tmp/member',data=secrets.token_urlsafe(32).encode())
dc('up','-d','--no-deps','--wait','tesserad','console','kit','caddy','collector')
api=g['api'];admin=g['app_token']('owner',g['owner_pw'],{'administer':'yes'})
# General dataset creation is intentionally disabled by this deployment.
# The supported bounded issuer template creates the private fixture store.
policy={'id':'drill-members','template':{'administrator':'drill-custodian','display_name':'Synthetic recovery store','types':[]},'grants':{'read':True,'write':True,'schema':True,'erase':True},'cap':4}
check(api(admin,'POST','/admin/api/v1/issuers',policy)[0]==201,'fixture issuer creation refused')
st,body,_=api(admin,'POST','/admin/api/v1/issuers/drill-members/invitations',{});check(st in (200,201),'fixture invitation refused')
link=json.loads(body)['redeem_url'];joining=g['browser']();st,page,_=g['request'](joining,'GET',link);check(st==200,'fixture invitation preview refused')
check(g['request'](joining,'POST','/oauth/invitation',form={'code':g['field'](page,'code'),'account':'drill-member','password':member_password})[0]==201,'fixture member joining refused')
member=g['app_token']('drill-member',member_password,{'read':'yes','write':'yes','schema':'yes','erase':'yes'})
st,body,_=api(member,'GET','/v1/datasets');check(st==200,'member listing refused');dataset=json.loads(body)['datasets'][0];dataset_id=dataset['id'];content_store=dataset['content_store_id']
track='org.voptrack.drill.reading'
check(api(member,'POST',f'/v1/datasets/{dataset_id}/meta',{'type':{'id':track,'name':'Synthetic reading','fields':[{'id':'value','meta_id':track,'name':'Value','type':'integer'}]}})[0]==201,'fixture track refused')
check(api(member,'POST',f'/v1/datasets/{dataset_id}/objects/{track}',{'fields':{'value':{'type':'integer','value':7}}})[0]==201,'fixture record refused')
sys.path.insert(0,str(root/'release/console'))
from online_progress import upload_fixture
content=b'synthetic-bytes!'
item=upload_fixture(g,member,content_store,len(content),content=content)
st,body,_=api(admin,'GET','/admin/api/v1/backup-scheduler');check(st==200,'identity observation refused');observed=json.loads(body);deployment=observed['settings_export']['deployment']
# Independent trusted identity is observed before kit export, not inferred from it.
lineage={'deployment':deployment,'origin':'https://tesserad:8443'}
write('trust/backup-lineage.json',json.dumps(lineage).encode())
write('key/backup.key',dc('run','--rm','--no-deps','-T','--entrypoint','cat','init','/var/lib/voptrack-config/backup.key'))
dc('run','--rm','--no-deps','-T','--entrypoint','sh','collector-admin','-c','umask 077; cat > /tmp/admin; exec voptrack-collector -mode provision -administrator-file /tmp/admin -id '+secrets.token_hex(16)+' -deployment '+deployment,data=admin.encode())
def acquire(kind):
 identifier=str(int(time.time()))+'-'+secrets.token_hex(16)
 check(api(admin,'PUT','/admin/api/v1/backup-operations/'+identifier,{'kind':kind})[0]==202,'fixture acquisition admission refused')
 for _ in range(600):
  st,raw,_=api(admin,'GET','/admin/api/v1/backup-operations/'+identifier)
  check(st==200,'fixture acquisition observation refused');op=json.loads(raw)
  if op['state']!='running':break
  time.sleep(.05)
 check(op['state']=='complete','fixture acquisition failed')
 dc('run','--rm','--no-deps','-T','collector','-once',*(['-outbox','/var/lib/voptrack-bundles/.replication-outbox'] if os.environ.get('VOPTRACK_RECOVERY_PRESERVE_TARGETS')=='yes' else []))
 if app_only:
  return dc('run','--rm','--no-deps','-T','--entrypoint','cat','collector','/var/lib/voptrack-bundles/'+identifier+'/backup.tar')
 owner=g['browser']();st,_,_=g['console_sign_in'](owner,'owner',g['owner_pw'],{'administer':'yes'});check(st==200,'operator Console sign-in refused')
 with owner.open(g['base']+'/backups/bundle/'+identifier) as response:raw=response.read()
 return raw
# Retain a legacy Console document through the public record API. The first-
# release UI deliberately no longer creates personal saved dashboards. This
# fixture tests archived private state, not the retired authoring interface.
if not app_only:
 import base64
 state_dataset=dc('exec','-T','console','cat','/var/lib/voptrack-console/console-state.dataset').decode().strip()
 state_secret=dc('exec','-T','console','cat','/var/lib/voptrack-console/console-state.secret').decode().strip()
 st,body,_=g['request'](g['browser'](),'POST','/oauth/token',form={'grant_type':'client_credentials','dataset':state_dataset},headers={'Authorization':'Basic '+base64.b64encode(('console-state:'+state_secret).encode()).decode()})
 check(st==200,'Console state fixture service credential refused')
 state_token=json.loads(body)['access_token']
 document=[{'id':'recovery-fixture','name':'Recovery fixture','panels':[],'updated':'2026-11-08T00:00:00Z'}]
 check(api(state_token,'POST',f'/v1/datasets/{state_dataset}/objects/voptrack.console.document',{'fields':{'name':{'type':'text','value':'dashboards/home.json'},'body':{'type':'json','value':json.dumps(document)}}})[0]==201,'archived Console document fixture refused')
backup=acquire('backup')
# Deliberately fresh: the restored snapshot must reject this later authority.
admin=g['app_token']('owner',g['owner_pw'],{'administer':'yes'})
st,body,_=g['request'](g['browser'](),'POST',f'/v1/stores/{content_store}:erase',js={'confirm':content_store},headers={'Authorization':'Bearer '+member})
check(st==200,'fixture erasure refused (HTTP '+str(st)+'; '+str(json.loads(body).get('error',{}).get('code','unknown'))+')')
evidence=acquire('evidence')
if app_only:
 # Host-only access to the existing local socket; no Console credentials are created.
 def kit_call(action,value=None):
  return dc('exec','-T','kit','curl','-fsS','--unix-socket','/var/lib/voptrack-kit-control/kit.sock','-X','POST','-H','Content-Type: application/json','--data-binary','@-','http://localhost/'+action,data=json.dumps(value or {}).encode())
 dc('run','--rm','--no-deps','-T','host-observe',data=admin.encode())
 kit_call('prepare')
 session=secrets.token_hex(24);operator=secrets.token_hex(24)
 request=json.loads(kit_call('request',{'Session':session,'Operator':operator}))
 dc('run','--rm','--no-deps','-T','kit-admin','approve',request['ID'],request['Revision'])
 kit=kit_call('deliver',{'ID':request['ID'],'Session':session,'Operator':operator})
else:
 owner=g['browser']();check(g['console_sign_in'](owner,'owner',g['owner_pw'],{'administer':'yes'})[0]==200,'operator sign-in refused')
 def post(path,extra=None):
  st,page,_=g['request'](owner,'GET','/backups');check(st==200,'backup page refused')
  return g['request'](owner,'POST',path,form=dict({'csrf':g['csrf_of'](page)},**(extra or {})))
 check(post('/backups/kit/prepare')[0]==200,'kit preparation refused')
 st,page,_=post('/backups/kit/request');match=re.search(r'kit-admin approve ([a-f0-9]{48}) ([a-f0-9]{48})',page);check(st==200 and match,'kit request refused')
 identifier,revision=match.groups();dc('run','--rm','--no-deps','-T','kit-admin','approve',identifier,revision)
 st,page,_=g['request'](owner,'GET','/backups');form=urllib.parse.urlencode({'csrf':g['csrf_of'](page),'request':identifier}).encode()
 with owner.open(urllib.request.Request(g['base']+'/backups/kit/download',data=form)) as response:kit=response.read()
st,raw,_=api(admin,'GET','/admin/api/v1/backup-scheduler');check(st==200,'settings export refused');settings=json.loads(raw)['settings_export']
write('inputs/backup.tar',backup);write('inputs/evidence.tar',evidence);write('inputs/kit.tar',kit);write('inputs/backup-settings-export.json',json.dumps(settings).encode())
selection={'version':1}
for role,name in [('backup','backup.tar'),('evidence','evidence.tar'),('kit','kit.tar'),('settings','backup-settings-export.json')]:selection[role]={'file':name,'sha256':hashlib.sha256((out/'inputs'/name).read_bytes()).hexdigest()}
write('inputs/selection.json',json.dumps(selection).encode())
write('drill-private.json',json.dumps({'owner_password':g['owner_pw'],'member_password':member_password,'dataset':dataset_id,'content_store':content_store,'item':item,'track':track,'settings':settings,'old_scope':observed['settings_scope'],'source_admin':admin,'home_mode':home_mode,'console_state_expected':not app_only}).encode())
# An opt-in assurance run retains all source volumes and captures the real
# collector outbox before stopping the source. Default legacy gate is unchanged.
preserve=os.environ.get('VOPTRACK_RECOVERY_PRESERVE_TARGETS')=='yes'
if preserve:
 dc('stop','collector')
 publication=out/'publication';publication.mkdir(mode=0o700)
 bundle=dc('run','--rm','--no-deps','-T','--entrypoint','tar','collector','-cf','-','-C','/var/lib/voptrack-bundles','.')
 # Task-owned tar from our own collector volume, never a browser upload.
 # Reject links and path escapes before extracting even this synthetic export.
 with tarfile.open(fileobj=io.BytesIO(bundle)) as archive:
  for member in archive.getmembers():
   path=pathlib.PurePosixPath(member.name)
   check(not path.is_absolute() and '..' not in path.parts and (member.isdir() or member.isfile()),'unsafe collector export')
  archive.extractall(publication)
 write('source-service.log',dc('logs','--no-color'))
 check((publication/'.replication-outbox').is_dir(),'real collector outbox missing')
 selected={role:json.loads(tarfile.open(fileobj=io.BytesIO(raw)).extractfile('manifest.json').read())['operation']['id'] for role,raw in [('backup',backup),('evidence',evidence)]}
 for role,identifier in selected.items():
  queue=publication/'.replication-outbox'/identifier
  check((queue/'backup.tar').is_file() and (queue/'obligation.json').is_file(),'selected native outbox capture missing')
  obligation=json.loads((queue/'obligation.json').read_text())
  check(obligation['LocalPublished'] is True and obligation['SHA256']==selection[role]['sha256'] and hashlib.sha256((queue/'backup.tar').read_bytes()).hexdigest()==selection[role]['sha256'],'selected native outbox capture mismatch')
 write('publication/selection.json',json.dumps(selected).encode())
# Destroy only this disposable source after exports. Remote drill cannot consult
# its processes or volumes, not even for certificates, credentials or inventory.
dc('down',*(() if preserve else ('-v',)),'--remove-orphans')
for args in [('ps','-aq'),('network','ls','-q')]+([] if preserve else [('volume','ls','-q')]):
 result=subprocess.check_output(['docker',*args,'--filter','label=com.docker.compose.project='+project])
 check(not result.strip(),'disposable source resources remain')
write('source-removed.json',json.dumps({'source_removed':not preserve,'source_services_unavailable':True,'volumes_preserved':preserve,'variant':os.environ['VOPTRACK_VARIANT']}).encode())
print('Retained recovery selection exported; source services unavailable; volumes '+('preserved.' if preserve else 'removed.'))
