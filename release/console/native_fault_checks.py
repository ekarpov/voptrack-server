"""Lost native settings replies against the actual Console image via test-only TLS interposition.
The injected 503 proves consumer uncertainty handling, not Tessera fsync failure.
"""
import json,os,pathlib,subprocess,time

def verify(g,dc):
 work=pathlib.Path(os.environ['VOPTRACK_GATE_WORK']);project=os.environ['VOPTRACK_GATE_PROJECT']
 source=pathlib.Path(g['__file__']).resolve().parents[2]
 binary=work/'faultproxy'
 subprocess.run(['go','build','-trimpath','-o',str(binary),'./release/native-settings/faultproxy'],cwd=source,check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,env=dict(os.environ,CGO_ENABLED='0'))
 binary.chmod(0o755)  # Public test executable in a private fixture directory.
 image=os.environ['VOPTRACK_IMAGE'];name=project+'-settings-fault';volume=name+'-tls';override=work/'compose.override.yaml'
 def docker(*args):
  p=subprocess.run(['docker',*args],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
  if p.returncode:raise RuntimeError('settings fault host operation failed at '+args[0])
  return p.stdout
 def login():
  owner=g['browser']();status,_,location=g['console_sign_in'](owner,'owner',g['owner_pw'],{'administer':'yes'})
  if status!=200:raise RuntimeError('fault scenario operator sign-in failed: status '+str(status)+'; sanitized Console diagnostics: '+dc('logs','--tail','4','console').decode())
  g['owner']=owner
 def journal():return json.loads(dc('exec','-T','console','cat','/var/lib/voptrack-console/backup-live.json'))
 try:
  docker('run','--rm','--network','none','--user','0:0','--cap-drop','ALL','--cap-add','CHOWN','-v',volume+':/fault','--entrypoint','chown',image,'10001:10001','/fault')
  docker('run','-d','--name',name,'--network',project+'_default','--network-alias','settings-fault','--user','10001:10001','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--tmpfs','/tmp','-v',str(binary)+':/proxy:ro','-v',volume+':/fault','-v',project+'_config:/config:ro','--entrypoint','sh',image,'-c','openssl req -x509 -newkey rsa:2048 -nodes -days 1 -keyout /fault/key -out /fault/crt -subj /CN=settings-fault -addext subjectAltName=DNS:settings-fault >/dev/null 2>&1 && exec /proxy')
  for _ in range(100):
   p=subprocess.run(['docker','exec',name,'test','-f','/fault/crt'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
   if p.returncode==0:break
   time.sleep(.05)
  override.write_text('services:\n  console:\n    environment:\n      TESSERA_URL: https://settings-fault:9443\n      TESSERA_CA_FILE: /fault/crt\n    volumes:\n      - settings-fault-tls:/fault:ro\nvolumes:\n  settings-fault-tls:\n    external: true\n    name: '+volume+'\n')
  dc('up','-d','--no-deps','--wait','console');login()
  request,csrf,field=(g[n] for n in ('request','csrf_of','field'))
  for mode,keep in [('drop-put',2),('503-put',3),('drop-get',2)]:
   owner=g['owner'];st,page,_=request(owner,'GET','/backups')
   count=len(journal()['commands'])
   form={'csrf':csrf(page),'effective_revision':field(page,'effective_revision'),'journal':field(page,'journal'),'every':'12','keep':str(keep)}
   st,review,_=request(owner,'POST','/backups/live/preview',form=form)
   if st!=200:raise RuntimeError('fault scenario live review failed')
   if mode!='drop-get':docker('exec',name,'sh','-c','printf '+mode+' > /fault/mode')
   st,_,_=request(owner,'POST','/backups/live/confirm',form={'csrf':csrf(review),'confirmation':field(review,'confirmation')})
   if st!=303:raise RuntimeError('fault scenario confirmation failed')
   saved=journal();command=saved['commands'][-1];identifier=command['id'];payload=command['payload']
   if len(saved['commands'])!=count+1:raise RuntimeError('uncertain submission changed journal admission count')
   if mode=='drop-get':
    docker('exec',name,'sh','-c','printf drop-get > /fault/mode');request(owner,'GET','/backups')
   state=journal()['commands'][-1]['state']
   # Go may transparently repeat an idempotent GET after a lost reply. That
   # is safe same-ID observation; PUT uncertainty must still remain durable.
   if state!='uncertain' and not (mode=='drop-get' and state=='effective'):raise RuntimeError('reply fault '+mode+' left unexpected state '+state)
   if docker('exec',name,'cat','/fault/mode').strip():raise RuntimeError('reply fault was not exercised')
   if mode=='drop-get' and state=='effective':
    latest=journal()['commands'][-1]
    if latest['id']!=identifier or latest['payload']!=payload or len(journal()['commands'])!=count+1:raise RuntimeError('GET replay changed durable identity')
    continue
   if mode=='drop-put':dc('restart','console');dc('up','-d','--no-deps','--wait','console');login();owner=g['owner']
   st,page,_=request(owner,'POST','/backups/live/reconcile',form={'csrf':csrf(request(owner,'GET','/members')[1]),'request':identifier})
   if st!=303:raise RuntimeError('same-ID explicit reconciliation failed')
   for _ in range(100):
    request(owner,'GET','/backups');saved=journal();latest=saved['commands'][-1]
    if latest['state']=='effective':break
    time.sleep(.05)
   if latest['state']!='effective' or latest['id']!=identifier or latest['payload']!=payload or len(saved['commands'])!=count+1:raise RuntimeError('lost response reconciliation replaced command or payload')
  g['say']('actual-image settings reply faults','lost acceptance, injected 503 uncertainty, lost effective lookup; exact ID/payload survives Console restart')
 finally:
  if override.exists():override.unlink();dc('up','-d','--no-deps','--wait','console')
  subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  subprocess.run(['docker','volume','rm',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
