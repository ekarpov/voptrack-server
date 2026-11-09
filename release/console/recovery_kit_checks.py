"""Constrained online kit export in the disposable actual-image Home journey."""
import hashlib,html,io,json,os,re,subprocess,tarfile,time,urllib.parse,urllib.request,urllib.error

def verify(g,dc,phase=None):
    request,owner,csrf_of=(g[k] for k in ('request','owner','csrf_of'))
    def post(path,extra=None):
        st,page,_=request(owner,'GET','/backups')
        form=dict({'csrf':csrf_of(page)},**(extra or {}))
        if path=='/backups/kit/download':
            req=urllib.request.Request(g['base']+path,data=urllib.parse.urlencode(form).encode())
            try:
                with owner.open(req) as response:return response.status,response.read(),response.headers
            except urllib.error.HTTPError as refusal:return refusal.code,refusal.read(),refusal.headers
        return request(owner,'POST',path,form=form)
    st,page,_=post('/backups/kit/prepare')
    if st!=200 or 'data-kit-state="ready"' not in page:raise RuntimeError('online kit preparation did not become ready')
    if 'data-runtime-state="running"' not in page:raise RuntimeError('running process observation missing')
    receipt=json.loads(dc('exec','-T','tesserad','cat','/var/lib/voptrack-runtime/server.json'))
    configuration=dc('run','--rm','--no-deps','-T','--entrypoint','cat','init','/var/lib/voptrack-config/server.json')
    if receipt['config_sha256']!=hashlib.sha256(configuration).hexdigest():raise RuntimeError('launched receipt describes different bytes')
    for path in ('/backups/kit/prepare','/backups/kit/request','/backups/kit/download'):
        if request(g['sara'],'POST',path,form={'csrf':'invalid'})[0]!=403:raise RuntimeError('member accessed sensitive kit action')
        if request(owner,'POST',path,form={'csrf':'invalid'})[0]!=403:raise RuntimeError('kit CSRF accepted')
    st,page,_=post('/backups/kit/request')
    match=re.search(r'kit-admin approve ([a-f0-9]{48}) ([a-f0-9]{48})',page)
    if st!=200 or not match:raise RuntimeError('session-bound export request missing')
    identifier,revision=match.groups()
    if post('/backups/kit/download',{'request':identifier})[0]!=403:raise RuntimeError('ordinary administrator consent exported a kit')
    dc('run','--rm','--no-deps','-T','kit-admin','approve',identifier,revision)
    # Deny the real directory fsync in a disposable host-command container.
    # Successful unlink must still report failure when durability is unknown.
    profile=os.path.join(os.environ['VOPTRACK_GATE_WORK'],'deny-fsync.json')
    with open(profile,'w') as out:json.dump({'defaultAction':'SCMP_ACT_ALLOW','syscalls':[{'names':['fsync','fdatasync'],'action':'SCMP_ACT_ERRNO','errnoRet':5}]},out)
    kit_container=dc('ps','-q','kit').decode().strip()
    image=subprocess.check_output(['docker','inspect','--format','{{.Image}}',kit_container],text=True).strip()
    denied=subprocess.run(['docker','run','--rm','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges:true','--security-opt','seccomp='+profile,'--volumes-from',kit_container,'--entrypoint','/usr/local/bin/voptrack-kit',image,'revoke',identifier],capture_output=True)
    if denied.returncode!=1 or denied.stderr.strip()!=b'recovery kit operation unavailable':raise RuntimeError('revocation acknowledged without durable directory sync')
    dc('exec','-T','kit','test','!','-e','/var/lib/voptrack-kits/request-'+identifier+'.json')
    if post('/backups/kit/download',{'request':identifier})[0]!=403:raise RuntimeError('failed-sync revocation remained deliverable')
    g['say']('revocation directory-sync failure','unlink completed; host command reports failure; delivery refused')
    st,page,_=post('/backups/kit/request')
    match=re.search(r'kit-admin approve ([a-f0-9]{48}) ([a-f0-9]{48})',page)
    if st!=200 or not match:raise RuntimeError('fresh export request after failed sync missing')
    identifier,revision=match.groups()
    dc('run','--rm','--no-deps','-T','kit-admin','approve',identifier,revision)
    dc('run','--rm','--no-deps','-T','kit-admin','revoke',identifier)
    if post('/backups/kit/download',{'request':identifier})[0]!=403:raise RuntimeError('host-revoked kit approval delivered secrets')
    st,page,_=post('/backups/kit/request')
    match=re.search(r'kit-admin approve ([a-f0-9]{48}) ([a-f0-9]{48})',page)
    if st!=200 or not match:raise RuntimeError('fresh export request after revocation missing')
    identifier,revision=match.groups()
    dc('run','--rm','--no-deps','-T','kit-admin','approve',identifier,revision)
    st,page,_=request(owner,'GET','/backups')
    form=urllib.parse.urlencode({'csrf':csrf_of(page),'request':identifier}).encode()
    req=urllib.request.Request(g['base']+'/backups/kit/download',data=form)
    with owner.open(req) as response:
        kit=response.read()
        if response.headers.get('Cache-Control')!='no-store' or response.headers.get('Content-Type')!='application/x-tar':raise RuntimeError('sensitive attachment delivery headers')
    if post('/backups/kit/download',{'request':identifier})[0]!=403:raise RuntimeError('kit approval replay delivered secrets')
    with tarfile.open(fileobj=io.BytesIO(kit)) as archive:
        names=archive.getnames()
        if any('retired' in n or 'credential' in n or '/backup-intents/' in n for n in names):raise RuntimeError('kit includes changing evidence or collector authority')
        manifest=json.load(archive.extractfile('var/lib/voptrack-bootstrap/kit-manifest.json'))
        if manifest['metadata']['revision']!=revision:raise RuntimeError('approved revision substituted')
    # Private host-side output is used for the exported-kit fresh-volume gate.
    path=os.path.join(os.environ['VOPTRACK_GATE_WORK'],'approved-kit.tar')
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'wb') as out:out.write(kit)
    g['_approved_kit']=path
    # Exercise repeated online preparation/export requests while the existing
    # independent device workers continue content reads and uploads.
    if phase:
        def preparation_window():
            until=time.monotonic()+2
            while time.monotonic()<until:
                if post('/backups/kit/prepare')[0]!=200:raise RuntimeError('online kit re-preparation failed')
        phase('kit preparation',preparation_window)
        def export_window():
            until=time.monotonic()+2
            while time.monotonic()<until:
                st,page,_=post('/backups/kit/request')
                match=re.search(r'kit-admin approve ([a-f0-9]{48}) ([a-f0-9]{48})',page)
                if st!=200 or not match:raise RuntimeError('kit request failed')
                identifier,revision=match.groups();dc('run','--rm','--no-deps','-T','kit-admin','approve',identifier,revision)
                st,page,_=request(owner,'GET','/backups')
                form=urllib.parse.urlencode({'csrf':csrf_of(page),'request':identifier}).encode()
                with owner.open(urllib.request.Request(g['base']+'/backups/kit/download',data=form)) as response:
                    delivered=response.read()
                    if response.status!=200 or not delivered:raise RuntimeError('approved kit refused')
        phase('kit export',export_window)
    g['say']('recovery kit and runtime observations','current committed revision; host-only one-use approval; no-store; member/CSRF/replay refusal; exact launched configuration')
