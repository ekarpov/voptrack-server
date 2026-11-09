"""Actual artifact bytes held during live admission and per-device progress.
Collector retains its configured origin/TLS trust; only its DNS target is relayed.
"""
import hashlib,json,os,pathlib,secrets,subprocess,tarfile,time

def verify(g,dc,admin,phase,update):
 work=pathlib.Path(os.environ['VOPTRACK_GATE_WORK']);project=os.environ['VOPTRACK_GATE_PROJECT']
 source=pathlib.Path(__file__).resolve().parents[2];binary=work/'collector-transferproxy'
 control=work/'collector-transfer-control';override=work/'compose.override.yaml'
 relay=project+'-collector-transfer';collector=relay+'-worker';process=None
 if override.exists() or control.exists() or binary.exists():raise RuntimeError('collector overlap fixture paths occupied')
 control.mkdir(mode=0o777);control.chmod(0o777)
 def docker(*args,check=True):
  result=subprocess.run(['docker',*args],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=40)
  if check and result.returncode:raise RuntimeError('collector transfer host operation failed at '+args[0])
  return result
 def marker(name):return (control/name).exists()
 def paused():
  if not marker('started') or marker('release') or marker('released') or marker('expired') or marker('aborted'):raise RuntimeError('collector artifact not inside bounded paused transfer')
  if process.poll() is not None:raise RuntimeError('collector exited before transfer overlap')
  docker('exec',relay,'/relay','-writer-lock','/bundles/.writer')
 api=g['api']
 try:
  subprocess.run(['go','build','-trimpath','-o',str(binary),'./release/native-settings/transferproxy'],cwd=source,check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=dict(os.environ,CGO_ENABLED='0'))
  binary.chmod(0o755)  # Public test executable in a private fixture directory.
  subprocess.run([str(binary),'-selfcheck'],check=True,timeout=10,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
  # A new public operation ensures this scan must transfer an unpublished bundle.
  identifier=str(int(time.time()))+'-'+secrets.token_hex(16)
  if api(admin,'PUT','/admin/api/v1/backup-operations/'+identifier,{'kind':'backup'})[0]!=202:raise RuntimeError('collector overlap backup admission refused')
  for _ in range(2000):
   status,raw,_=api(admin,'GET','/admin/api/v1/backup-operations/'+identifier)
   operation=json.loads(raw) if status==200 else {}
   if operation.get('state')=='complete':break
   if operation.get('state') in ('failed','cancelled','expired'):raise RuntimeError('collector overlap backup failed')
   time.sleep(.02)
  else:raise RuntimeError('collector overlap backup did not complete')
  docker('run','-d','--name',relay,'--network',project+'_default','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','-v',str(binary)+':/relay:ro','-v',str(control)+':/fault-control','-v',project+'_config:/config:ro','-v',project+'_bundles:/bundles:ro','--entrypoint','/relay',os.environ['VOPTRACK_IMAGE'])
  for _ in range(200):
   if marker('ready'):break
   time.sleep(.025)
  else:raise RuntimeError('collector TLS relay did not become ready')
  address=docker('inspect','--format','{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}',relay).stdout.decode().strip()
  if not address or any(c not in '0123456789.' for c in address):raise RuntimeError('collector relay address unavailable')
  # This override affects only this disposable collector, preserving origin,
  # enrollment and TLS validation against the very same synthetic certificate.
  override.write_text(json.dumps({'services':{'collector':{'extra_hosts':{'tesserad':address}}}}))
  process=subprocess.Popen(['docker','compose','-p',project,'run','--rm','--no-deps','-T','--name',collector,'collector','-once'],cwd=work,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  for _ in range(1200):
   if marker('started'):break
   if process.poll() is not None:raise RuntimeError('collector finished without artifact transfer signal')
   time.sleep(.025)
  else:raise RuntimeError('actual collector artifact bytes not observed')
  paused()
  def overlap():
   def interval():
    g['_online_progress_until'](paused)
   try:phase('live policy during paused collector artifact transfer',interval)
   finally:(control/'release').write_text('1\n')
  update(13,9,overlap=overlap)
  if process.wait(timeout=100):raise RuntimeError('collector did not verify/commit relayed transfer')
  if not marker('released') or marker('expired') or marker('aborted'):raise RuntimeError('collector transfer release was not clean')
  # Stream the newly collected immutable bundle, checking every artifact against
  # public producer digests instead of trusting collector exit status alone.
  with g['owner'].open(g['base']+'/backups/bundle/'+identifier) as response:
   if response.status!=200:raise RuntimeError('collected overlap bundle unavailable')
   manifest=None;seen=set()
   with tarfile.open(fileobj=response,mode='r|') as bundle:
    for member in bundle:
     data=bundle.extractfile(member)
     if member.name=='manifest.json':
      manifest=json.load(data)
      if manifest['operation']['id']!=identifier or manifest['operation']['artifacts']!=operation['artifacts'] or manifest['trusted_https_origin']!='https://tesserad:8443':raise RuntimeError('collector transfer changed provenance')
      continue
     if manifest is None:raise RuntimeError('collector bundle manifest ordering changed')
     artifact=next((a for a in operation['artifacts'] if a['id']==member.name),None)
     if artifact is None or member.name in seen:raise RuntimeError('collector bundle unexpected member')
     digest=hashlib.sha256();size=0
     for chunk in iter(lambda:data.read(1<<20),b''):digest.update(chunk);size+=len(chunk)
     if size!=artifact['size'] or digest.hexdigest()!=artifact['sha256']:raise RuntimeError('paused collector artifact integrity changed')
     seen.add(member.name)
   if seen!={a['id'] for a in operation['artifacts']}:raise RuntimeError('collector bundle omitted artifacts')
  g['say']('collector transfer overlap','actual artifact byte signal and held writer lock; live request admitted and both devices progress inside pause; unchanged origin/TLS; release and all artifact digests verified')
 finally:
  (control/'release').write_text('1\n')
  docker('rm','-f',collector,check=False)
  if process is not None:
   try:process.wait(timeout=15)
   except subprocess.TimeoutExpired:process.kill();process.wait(timeout=5)
  docker('rm','-f',relay,check=False)
  if override.exists():override.unlink()
  for path in control.iterdir():path.unlink()
  control.rmdir()
  if binary.exists():binary.unlink()
