"""Actual-image online collection checks, invoked inside the Console journey.
Secrets stay in process memory or ephemeral mode-0600 container files.
"""
import hashlib
import html
import io
import json
import os
import re
import secrets
import subprocess
import tarfile
import time
import urllib.error


def verify(g):
    api, app_token, request, csrf_of = (g[n] for n in ('api','app_token','request','csrf_of'))
    owner, sara, base, say, fail = (g[n] for n in ('owner','sara','base','say','fail'))
    work, project = os.environ['VOPTRACK_GATE_WORK'], os.environ['VOPTRACK_GATE_PROJECT']
    def dc(*args, data=None):
        p = subprocess.run(['docker','compose','-p',project,*args], cwd=work, input=data,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if p.returncode:
            fail('collector host command failed at '+args[0]+' (private output suppressed)')
        return p.stdout
    admin = g['setup_operator_token']()
    st, body, _ = api(admin,'GET','/admin/api/v1/backup-operations?limit=1')
    if st != 200 or not json.loads(body)['results']:
        fail('native startup result missing from supported catalog')
    first = json.loads(body)['results'][0]
    credential_id = secrets.token_hex(16)
    # The host helper receives an ephemeral administrator token over stdin.
    # It is never added to argv, environment, Compose state or the collector.
    dc('run','--rm','--no-deps','-T','--entrypoint','sh','collector-admin','-c',
       'umask 077; cat > /tmp/admin; exec voptrack-collector -mode provision '
       '-administrator-file /tmp/admin -id '+credential_id+' -deployment '+first['pair']['deployment'],
       data=admin.encode())
    credential = json.loads(dc('run','--rm','--no-deps','-T','--entrypoint','cat',
                              'collector-admin','/var/lib/voptrack-collector/credential.json'))
    token = credential['token']
    checks = [('GET','/admin/api/v1/backup-scheduler',None),
              ('GET','/admin/api/v1/backup-settings-operations/'+first['pair']['deployment']+'.'+secrets.token_hex(16)+'.'+str(int(time.time()))+'-'+secrets.token_hex(16),None),
              ('PUT','/admin/api/v1/backup-operations/'+str(int(time.time()))+'-'+secrets.token_hex(16),{'kind':'backup'}),
              ('DELETE','/admin/api/v1/backup-operations/'+first['id'],None),
              ('GET','/admin/api/v1/overview',None),('GET','/admin/api/v1/backup-collectors',None),
              ('GET','/v1/datasets',None),('GET','/v1/stores',None),
              ('PUT','/admin/api/v1/backup-collectors/'+secrets.token_hex(16),{'expires_in_seconds':3600})]
    for method,path,body in checks:
        if api(token,method,path,body)[0] not in (401,403):
            fail('retrieval-only credential exceeded its authority')
    if api(admin,'PUT','/admin/api/v1/backup-collectors/'+credential_id,{'expires_in_seconds':3600})[0]!=409:
        fail('credential ID replay changed or reissued its one-time secret')
    metadata=next(a for a in first['artifacts'] if a['kind']=='metadata')
    status,raw,_=api(token,'GET','/admin/api/v1/backup-operations/'+first['id']+'/artifacts/'+metadata['id'])
    if status!=410 or json.loads(raw).get('error',{}).get('code')!='expired':
        fail('missing lease did not produce the intended expired refusal')
    def page_with_count(count):
        for _ in range(80):
            st,page,_ = request(owner,'GET','/backups')
            links = re.findall(r'href="(/backups/bundle/[^" ]+)"',page)
            if st==200 and len(links)>=count:
                return page,links
            time.sleep(1)
        fail('completed producer result did not become a downloadable bundle')
    page, before = page_with_count(1)
    phase,finish = None,None
    if os.environ.get('VOPTRACK_ONLINE_PROGRESS')=='1':
        from online_progress import start
        phase,finish=start(g)
        dc('stop','collector')
    st,_,_ = request(owner,'POST','/backups/now',form={'csrf':'invalid'})
    if st!=403: fail('manual backup CSRF guard failed')
    def create_and_wait():
        result=request(owner,'POST','/backups/now',form={'csrf':csrf_of(page)})
        # Observe the same durable ID; operation details are never printed.
        operation_id=dc('exec','-T','console','cat','/var/lib/voptrack-console/backup-intents/current').decode().strip()
        for _ in range(1000):
            status,raw,_=api(admin,'GET','/admin/api/v1/backup-operations/'+operation_id)
            if status==200 and json.loads(raw)['state']!='running':
                if json.loads(raw)['state']!='complete':fail('manual online operation failed')
                return result
            time.sleep(.02)
        fail('manual online operation did not complete')
    st,page,_ = phase('creation',create_and_wait) if phase else create_and_wait()
    if phase:
        phase('collection',lambda:dc('run','--rm','--no-deps','-T','collector','-once'))
        dc('up','-d','--no-deps','collector')
    if st!=200 or not any(s in page for s in ('Creating backup','Preparing download')):
        fail('manual operation was not reported accurately')
    page,after = page_with_count(len(before)+1)
    link=after[0]
    if phase:
        # Native retention and local retention are separate. Stop only the
        # disposable worker so explicit scans control its local keep=2 policy.
        dc('stop','collector')
    def download_status(link):
        try:
            with owner.open(base+html.unescape(link)) as response:
                return response.status,response.headers
        except urllib.error.HTTPError as refusal:
            return refusal.code,refusal.headers
    def download():
        with owner.open(base+html.unescape(link)) as response:
            chunks=[]
            while True:
                chunk=response.read(65536)
                if not chunk:break
                chunks.append(chunk)
                if phase and len(chunks)==1:
                    # keep=2 rotates the selected producer set during an active
                    # slow local download. Local immutable bytes must not change.
                    for _ in range(2):
                        # alpha.36 commits publication before asynchronous pruning
                        # converges. Admission during retention_pending is a real
                        # refusal, not failure of the already complete backup.
                        for _wait in range(600):
                            status,snapshot,_=api(admin,'GET','/admin/api/v1/backup-scheduler')
                            if status!=200:fail('producer convergence observation unavailable')
                            retention=json.loads(snapshot)['retention']
                            if retention['state']=='converged':break
                            if retention['state'] in ('blocked','degraded'):fail('rotation fixture pruning blocked or degraded')
                            time.sleep(.05)
                        else:fail('producer pruning did not converge before rotation')
                        operation=str(int(time.time()))+'-'+secrets.token_hex(16)
                        if api(admin,'PUT','/admin/api/v1/backup-operations/'+operation,{'kind':'backup'})[0]!=202:
                            fail('rotation-pressure admission failed')
                        for _ in range(1000):
                            status,raw,_=api(admin,'GET','/admin/api/v1/backup-operations/'+operation)
                            value=json.loads(raw) if status==200 else {}
                            if value.get('state')=='complete':break
                            if value.get('state') in ('failed','cancelled','expired'):fail('rotation-pressure operation failed')
                            time.sleep(.02)
                        else:fail('rotation-pressure operation stalled')
                    selected=link.rsplit('/',1)[-1]
                    status,_,_=api(admin,'PUT','/admin/api/v1/backup-operations/'+selected+'/leases/'+secrets.token_hex(16))
                    if status!=410:fail('selected producer set did not expire under keep=2 rotation')
                    # Collect both newer results while the selected tar is still
                    # pinned by this unread HTTP stream. Pruning must defer it.
                    dc('run','--rm','--no-deps','-T','collector','-once','-keep','2')
                    if link not in request(owner,'GET','/backups')[1]:
                        fail('local retention removed an active download pin')
                if phase:time.sleep(.004)
            return b''.join(chunks),response.headers
    data,download_headers=phase('slow download',download) if phase else download()
    if download_headers.get('Cache-Control')!='no-store' or 'attachment' not in download_headers.get('Content-Disposition',''):
        fail('bundle attachment/cache guard failed')
    with tarfile.open(fileobj=io.BytesIO(data)) as bundle:
        manifest=json.load(bundle.extractfile('manifest.json'))
        op=manifest['operation']
        if op['kind']!='backup' or 'metadata-mac-required-before-restore' not in manifest['authentication']:
            fail('bundle provenance or authentication disclosure missing')
        for member in op['artifacts']:
            raw=bundle.extractfile(member['id']).read()
            if len(raw)!=member['size'] or hashlib.sha256(raw).hexdigest()!=member['sha256']:
                fail('published bundle member differs from selected producer result')
    if phase:
        # The selected download has closed. A full rescan with no new publication
        # must now retire its previously pinned excess bundle. Downstream recovery
        # uses a currently retained pair, rather than relying on a retention leak.
        dc('stop','collector')
        try:
            dc('run','--rm','--no-deps','-T','collector','-once','-keep','2')
            if download_status(link)[0]!=404:
                fail('successful rescan did not retire the released excess bundle')
            page,after=page_with_count(2)
            link=after[0]
            with owner.open(base+html.unescape(link)) as response:
                data=response.read()
        finally:
            dc('up','-d','--no-deps','collector')
        say('deferred retention after download','successful rescan retires released pin without another publication')

    # Corrupt only this disposable project's local tar. The exact same image must
    # refuse delivery before attachment headers and keep independent bundles listed.
    # Restore the original bytes even on failure; never touch a development volume.
    dc('stop','collector')
    selected=link.rsplit('/',1)[-1]
    path='/var/lib/voptrack-bundles/'+selected+'/backup.tar'
    with tarfile.open(fileobj=io.BytesIO(data)) as bundle:
        archive=next(m for m in bundle.getmembers() if m.name!='manifest.json' and m.size>0)
        offset=archive.offset_data
    try:
        dc('run','--rm','--no-deps','-T','--entrypoint','sh','collector','-c',
           'dd of='+path+' bs=1 seek='+str(offset)+' conv=notrunc 2>/dev/null',data=bytes([data[offset]^1]))
        st,headers=download_status(link)
        if st!=404 or 'attachment' in headers.get('Content-Disposition',''):
            fail('corrupt published bundle reached download')
        st,page,_=request(owner,'GET','/backups')
        remaining=re.findall(r'href="(/backups/bundle/[^" ]+)"',page)
        if st!=200 or link in remaining or not remaining:
            fail('corrupt publication hid independent usable bundles')
    finally:
        dc('run','--rm','--no-deps','-T','--entrypoint','sh','collector','-c',
           'cat > '+path,data=data)
    try:
        # Reproduce the old in-place retention crash boundary without waiting for
        # a crash. Other complete publications must remain available to Console.
        dc('run','--rm','--no-deps','-T','--entrypoint','sh','collector','-c',
           'mv '+path+' '+path+'.saved')
        st,page,_=request(owner,'GET','/backups')
        remaining=re.findall(r'href="(/backups/bundle/[^" ]+)"',page)
        if st!=200 or link in remaining or not remaining:
            fail('interrupted retention hid intact published bundles')
    finally:
        dc('run','--rm','--no-deps','-T','--entrypoint','sh','collector','-c',
           'mv '+path+'.saved '+path)
        dc('up','-d','--no-deps','collector')
    say('published integrity and interrupted retention','corrupt delivery refused; independent intact backups remain listed')
    # Closing a response early is not custody or completion. No receipt changes.
    with owner.open(base+html.unescape(link)) as interrupted:
        interrupted.read(1024)
    if 'Off-machine custody is unknown' not in request(owner,'GET','/backups')[1]:
        fail('interrupted delivery changed custody status')
    if request(sara,'GET',link)[0]!=403:
        fail('member reached an operator bundle')
    from native_settings_checks import verify as verify_native
    if phase:
        phase('live interval/retention application',lambda:verify_native(g,dc,admin,restart=False,protection=False))
        from native_overlap_checks import verify as verify_overlap
        verify_overlap(g,dc,admin,phase)
    from recovery_kit_checks import verify as verify_kit
    verify_kit(g,dc,phase)
    final_recovery=None
    if finish:
        finish()
        from online_recovery import prepare
        final_recovery=prepare(g,dc,admin,data,work,first['pair']['deployment'])
    # Revocation must stop collection requests, while native production continues.
    admin=g['setup_operator_token']()
    st,raw,_=api(admin,'GET','/admin/api/v1/backup-operations')
    prior_ids={op['id'] for op in json.loads(raw)['results']} if st==200 else set()
    st,_,_=api(admin,'DELETE','/admin/api/v1/backup-collectors/'+credential_id)
    if st!=204 or api(token,'GET','/admin/api/v1/backup-operations')[0]!=401:
        fail('collector revocation failed')
    dc('restart','tesserad')
    dc('up','-d','--no-deps','--wait','tesserad','console','caddy','collector')
    for _ in range(40):
        st,body,_=api(admin,'GET','/admin/api/v1/backup-operations')
        if st==200 and any(op['id'] not in prior_ids for op in json.loads(body)['results']):break
        time.sleep(1)
    else: fail('revocation stopped native startup scheduling')
    # The previous complete immutable download survives revocation/restart.
    with owner.open(base+link) as response:
        if response.read()!=data:fail('prior usable bundle lost after revocation')
    # Synthetic gate-only admin token for shell catalog checks; private and
    # removed with the disposable project. It is never a runtime credential.
    path=os.path.join(work,'gate-administrator')
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
    with os.fdopen(fd,'w') as out:out.write(admin)
    say('online backup producer and collector','scheduled/manual bundles; exact bytes; member/CSRF refusal; revoke preserves schedule and prior bundle')
    return before,after,final_recovery
