"""Actual-image after-R erasure and fresh-volume recovery acceptance.
Invoked only by the explicit extended gate, with synthetic fixture data.
"""
import io,json,os,secrets,ssl,subprocess,tarfile,time,urllib.request


def prepare(g,dc,admin,old_bundle,work,deployment):
    with tarfile.open(fileobj=io.BytesIO(old_bundle)) as archive:
        old=json.load(archive.extractfile('manifest.json'))['operation']
    if old['pair']['deployment']!=deployment:raise RuntimeError('untrusted old deployment')
    def terminal(admin,operation):
        for _ in range(1000):
            st,raw,_=g['api'](admin,'GET','/admin/api/v1/backup-operations/'+operation)
            if st==200:
                value=json.loads(raw)
                if value['state']!='running':return value
            time.sleep(.02)
        raise RuntimeError('producer did not terminate')
    def failed_creation(label):
        st,overview,_=g['api'](admin,'GET','/admin/api/v1/overview')
        if st!=200:raise RuntimeError('authoritative success observation unavailable')
        previous_success=json.loads(overview)['server']['last_backup_at']
        last_scheduled=json.loads(g['api'](admin,'GET','/admin/api/v1/backup-scheduler')[1])['last_scheduled_attempt']
        st,page,_=g['request'](g['owner'],'GET','/backups')
        st,_,_=g['request'](g['owner'],'POST','/backups/now',form={'csrf':g['csrf_of'](page)})
        if st!=200:raise RuntimeError('limit operation was not accepted')
        operation=dc('exec','-T','console','cat','/var/lib/voptrack-console/backup-intents/current').decode().strip()
        value=terminal(admin,operation)
        if value['state']!='failed' or value.get('failure')!='query_result_limit_exceeded':
            raise RuntimeError('source bound was not reported as failure')
        st,page,_=g['request'](g['owner'],'GET','/backups')
        if st!=200 or '/backups/bundle/'+old['id'] not in page or '/backups/bundle/'+operation in page:
            raise RuntimeError('failed creation displaced previous usable bundle')
        with g['owner'].open(g['base']+'/backups/bundle/'+old['id']) as response:
            if response.read()!=old_bundle:raise RuntimeError('previous bundle changed after creation failure')
        g['say'](label,'safe failed operation; exact previous bundle remains downloadable')
        if json.loads(g['api'](admin,'GET','/admin/api/v1/backup-scheduler')[1])['last_scheduled_attempt']!=last_scheduled:raise RuntimeError('manual operation rewrote scheduled history')
        st,overview,_=g['api'](admin,'GET','/admin/api/v1/overview')
        if st!=200 or json.loads(overview)['server']['last_backup_at']!=previous_success:
            raise RuntimeError('failed creation changed the last successful backup observation')

    from native_pending_checks import verify as verify_pending_restart
    verify_pending_restart(g,dc,admin)

    # Exercise the required-entry bound with zero-byte completed items, which
    # count even though no content inode needs pinning. Failure must preserve
    # the exact previous usable bundle and never expose a completed result.
    token=g['app_token']('ben',g['passwords']['ben'],{'read':'yes','write':'yes','erase':'yes'})
    st,raw,_=g['api'](token,'GET','/v1/datasets')
    store=next(d for d in json.loads(raw)['datasets'] if d['id']==g['stores']['ben'])['content_store_id']
    reservations=[]
    items=[]
    try:
        for _ in range(257):
            st,raw,_=g['api'](token,'POST',f'/v1/stores/{store}/imports',{'operation_id':secrets.token_hex(16),'content_type':'application/octet-stream','byte_count':0,'maximum_bytes':1})
            if st!=200:raise RuntimeError('limit fixture reservation refused')
            upload=json.loads(raw)['upload_id']
            reservations.append(upload)
            st,raw,_=g['api'](token,'POST',f'/v1/stores/{store}/imports/{upload}:commit')
            if st!=200:raise RuntimeError('limit fixture commit refused')
            items.append(json.loads(raw)['item_id'])
        failed_creation('257 required content entries')
        prior=json.loads(g['api'](admin,'GET','/admin/api/v1/backup-scheduler')[1])
        dc('restart','tesserad');dc('up','-d','--no-deps','--wait','tesserad')
        for _ in range(300):
            st,body,_=g['api'](admin,'GET','/admin/api/v1/backup-scheduler');snapshot=json.loads(body)
            if st==200 and snapshot['last_scheduled_attempt'] and snapshot['last_scheduled_attempt']['outcome']!='in_progress':break
            time.sleep(.03)
        else:raise RuntimeError('scheduled failure fixture did not finish')
        if snapshot['instance_id']==prior['instance_id'] or snapshot['last_scheduled_attempt']['outcome']!='failed' or snapshot['next_attempt_at'] is None or snapshot['next_reason']!='scheduled':raise RuntimeError('scheduled failure/restart/next intent observation invalid')
        g['say']('scheduled bounded source failure','new instance; safe failed scheduled outcome; next intent separate from manual history')
    finally:
        for item in items:
            if g['api'](token,'DELETE',f'/v1/stores/{store}/items/{item}')[0]!=204:
                raise RuntimeError('limit fixture cleanup failed')
        for upload in reservations:
            g['api'](token,'DELETE',f'/v1/stores/{store}/imports/{upload}')

    # Six live items now hold 320 MiB (64 MiB original + four 64 MiB items),
    # below the entry bound but above the published 256 MiB source-byte bound.
    from online_progress import upload_fixture
    large=[]
    try:
        for _ in range(4):large.append(upload_fixture(g,token,store,64<<20))
        failed_creation('320 MiB source content')
    finally:
        for item in large:
            if g['api'](token,'DELETE',f'/v1/stores/{store}/items/{item}')[0]!=204:
                raise RuntimeError('size fixture cleanup failed')

    def finish():
        # Fresh ephemeral person-authorized tokens; no unattended producer right.
        operator=g['app_token']('owner',g['owner_pw'],{'administer':'yes'})
        sara=g['app_token']('sara',g['passwords']['sara'],{'read':'yes','write':'yes','erase':'yes'})
        st,raw,_=g['api'](sara,'GET','/v1/datasets')
        sara_store=next(d for d in json.loads(raw)['datasets'] if d['id']==g['stores']['sara'])['content_store_id']
        # The old pair already contains Sara's completed fixture. Its exact item
        # is read from the gate's fixture receipt, never from private journals.
        fixture=g['_online_fixture_items']['sara']
        operation=str(int(time.time()))+'-'+secrets.token_hex(16)
        st,raw,_=g['api'](operator,'PUT','/admin/api/v1/backup-operations/'+operation,{'kind':'backup'})
        if st!=202 or json.loads(raw)['state']!='running':raise RuntimeError('concurrent backup was not active')
        st,_,_=g['request'](g['browser'](),'POST',f'/v1/stores/{sara_store}:erase',js={'confirm':sara_store},headers={'Authorization':'Bearer '+sara})
        if st!=200:raise RuntimeError('concurrent content erasure failed')
        if terminal(operator,operation)['state']!='complete':raise RuntimeError('concurrent backup failed')
        # alpha.36 policy effectiveness and pruning convergence are separate.
        # Wait for the preceding complete backup's native pruning before
        # admitting evidence; all non-converged terminal problems remain failures.
        for _ in range(600):
            status,snapshot,_=g['api'](operator,'GET','/admin/api/v1/backup-scheduler')
            if status!=200:raise RuntimeError('evidence convergence observation unavailable')
            retention=json.loads(snapshot)['retention']
            if retention['state']=='converged':break
            if retention['state'] in ('blocked','degraded'):raise RuntimeError('evidence fixture pruning blocked or degraded')
            time.sleep(.05)
        else:raise RuntimeError('pruning did not converge before evidence')
        evidence_id=str(int(time.time()))+'-'+secrets.token_hex(16)
        if g['api'](operator,'PUT','/admin/api/v1/backup-operations/'+evidence_id,{'kind':'evidence'})[0]!=202:
            raise RuntimeError('explicit evidence admission failed')
        evidence=terminal(operator,evidence_id)
        if evidence['state']!='complete' or evidence['pair']['deployment']!=deployment:raise RuntimeError('compatible evidence unavailable')
        # Original collector credential has already been revoked. Explicitly
        # provision a fresh retrieval credential for this remaining collection.
        dc('stop','collector')
        dc('run','--rm','--no-deps','-T','--entrypoint','sh','collector-admin','-c','rm -f /var/lib/voptrack-collector/credential.json')
        credential_id=secrets.token_hex(16)
        dc('run','--rm','--no-deps','-T','--entrypoint','sh','collector-admin','-c',
           'umask 077; cat > /tmp/admin; exec voptrack-collector -mode provision -administrator-file /tmp/admin -id '+credential_id+' -deployment '+deployment,data=operator.encode())
        dc('run','--rm','--no-deps','-T','collector','-once')
        # Retain this still-valid old-host retrieval credential only in private
        # test memory; the recovered acquisition registry must not import it.
        former_collector=json.loads(dc('run','--rm','--no-deps','-T','--entrypoint','cat','collector-admin','/var/lib/voptrack-collector/credential.json'))['token']
        with g['owner'].open(g['base']+'/backups/bundle/'+evidence_id) as response:
            newer=response.read()
        with tarfile.open(fileobj=io.BytesIO(newer)) as archive:
            newer_op=json.load(archive.extractfile('manifest.json'))['operation']
        if newer_op['id']!=evidence_id or newer_op['kind']!='evidence':raise RuntimeError('wrong evidence bundle')
        status,scheduler_raw,_=g['api'](operator,'GET','/admin/api/v1/backup-scheduler')
        if status!=200:raise RuntimeError('retained effective settings export unavailable')
        retained=json.loads(scheduler_raw);settings_export=retained['settings_export']
        if settings_export['deployment']!=deployment or settings_export['revision']!=retained['settings']['revision']:
            raise RuntimeError('public export does not describe acknowledged effective settings')
        # Stop the former producer before continuing its independently retained lineage.
        dc('stop','collector','tesserad','console','kit')
        root=os.path.abspath(os.path.join(os.path.dirname(g['__file__']),'../..'))
        module=subprocess.check_output(['go','list','-m','-f','{{.Dir}}','github.com/ekarpov/tessera'],cwd=root,text=True).strip()
        project=os.environ['VOPTRACK_GATE_PROJECT'];image=os.environ['VOPTRACK_IMAGE']
        def docker(*args,data=None):
            p=subprocess.run(['docker',*args],input=data,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
            if p.returncode:raise RuntimeError('fresh recovery host command failed (private details suppressed)')
            return p.stdout
        def member(op,kind):return next(a['id'] for a in op['artifacts'] if a['kind']==kind)
        kit_path=g['_approved_kit']
        kit_project=project+'-exported-kit'
        kit_volumes={n:kit_project+'-'+n for n in ('config','bootstrap','console','ca','public')}
        mounts=[]
        for n,v in kit_volumes.items():mounts.extend(['-v',v+':/var/lib/voptrack-'+n])
        kit_raw=open(kit_path,'rb').read()
        with tarfile.open(fileobj=io.BytesIO(kit_raw)) as exported:
            trusted=json.load(exported.extractfile('var/lib/voptrack-bootstrap/backup-lineage.json'))
        if trusted['deployment']!=deployment:raise RuntimeError('exported kit belongs to another producer lineage')
        input_volume=project+'-recovery-public-inputs'
        try:
            restored=subprocess.run(['docker','run','--rm','-i','--network','none','--tmpfs','/tmp:uid=10001,mode=0700',*mounts,'--entrypoint','/usr/local/bin/voptrack-init',image,'restore-committed-kit'],input=kit_raw,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
            if restored.returncode:raise RuntimeError('exported committed kit failed fresh-volume validation/restore')
            recovered_config=kit_volumes['config']
            # Retention may retire the selected old service copy after later matrix
            # acquisitions. Restore the exact already-downloaded PUBLIC bytes, not a
            # guessed current inventory member or any native private state.
            legacy=subprocess.run(['docker','run','--rm','--network','none','-v',project+'_bundles:/bundles:ro','--entrypoint','test',image,'-f','/bundles/'+old['id']+'/backup.tar'],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
            if legacy.returncode not in (0,1):raise RuntimeError('old recovery inventory probe unavailable')
            g['say']('selected old public recovery input','collector inventory copy '+('present' if legacy.returncode==0 else 'retired')+'; exact previously downloaded bytes retained independently')
            input_volume=project+'-recovery-public-inputs'
            public_inputs=io.BytesIO()
            with tarfile.open(fileobj=public_inputs,mode='w') as fixtures:
                for name,raw in [('old.tar',old_bundle),('newer.tar',newer),('backup-settings-export.json',json.dumps(settings_export).encode())]:
                    entry=tarfile.TarInfo(name);entry.size=len(raw);entry.mode=0o400;entry.uid=entry.gid=10001
                    fixtures.addfile(entry,io.BytesIO(raw))
            docker('run','--rm','-i','--network','none','--user','0:0','-v',input_volume+':/recovery-inputs','--entrypoint','sh',image,'-c','set -e; tar -xf - -C /recovery-inputs; chown 10001:10001 /recovery-inputs /recovery-inputs/old.tar /recovery-inputs/newer.tar /recovery-inputs/backup-settings-export.json; chmod 0700 /recovery-inputs',data=public_inputs.getvalue())
            # Supported upstream verifier runs in a disposable offline container.
            # Only verifier/restore containers see the key; ordinary Console and
            # the collector remain keyless.
            for op in (old,newer_op):
                script='mkdir -m700 /tmp/pair; tar -xf /recovery-inputs/'+('old.tar' if op is old else 'newer.tar')+' -C /tmp/pair; exec python3 /tools/verify_acquisition.py --metadata /tmp/pair/'+member(op,'metadata')+' --key-file /config/backup.key --deployment '+deployment
                docker('run','--rm','--network','none','-v',input_volume+':/recovery-inputs:ro','-v',recovered_config+':/config:ro','-v',module+'/tools:/tools:ro','python:3-alpine','sh','-c',script)
            server_ca=os.path.join(work,'restore-ca.crt')
            with open(server_ca,'wb') as output:output.write(dc('exec','-T','caddy','cat','/var/lib/voptrack-public/server.crt'))
            port=int(os.environ.get('VOPTRACK_ONLINE_RESTORE_PORT','19043'))
            for label,fence,expect_present in [('old-fence',old,True),('newer-fence',newer_op,False)]:
                name=project+'-restore-'+label;volume=name+'-data';collector_volume=name+'-collector';bundle_volume=name+'-bundles'
                try:
                    script='set -e; cp /retained-config/* /var/lib/voptrack-config/; mkdir -m700 /tmp/old /tmp/fence /tmp/rbackups; tar -xf /recovery-inputs/old.tar -C /tmp/old; tar -xf /recovery-inputs/'+('old.tar' if fence is old else 'newer.tar')+' -C /tmp/fence; sed -e "s#0.0.0.0:8443#0.0.0.0:9443#" -e "s#/var/lib/voptrack-backups#/tmp/rbackups#" /retained-config/server.json > /var/lib/voptrack-config/server.json; if VOPTRACK_TRUSTED_BACKUP_DEPLOYMENT=ffffffffffffffffffffffffffffffff voptrack-deploy recover-producer >/tmp/wrong-identity-log 2>&1; then exit 99; fi; voptrack-deploy recover-producer >/tmp/producer-recovery-log 2>&1; tesserad -config /var/lib/voptrack-config/server.json -restore /tmp/old/'+member(old,'archive')+' -backup-key-file /var/lib/voptrack-config/backup.key -retirement-fence /tmp/fence/'+member(fence,'evidence')+' >/tmp/restore-log 2>&1; exec tesserad -config /var/lib/voptrack-config/server.json'
                    docker('run','-d','--name',name,'--user','10001:10001','--read-only','--tmpfs','/tmp:uid=10001,mode=0700','-p','127.0.0.1:'+str(port)+':9443','-v',volume+':/var/lib/tessera','-e','VOPTRACK_TRUSTED_BACKUP_DEPLOYMENT='+deployment,'--tmpfs','/var/lib/voptrack-config:uid=10001,mode=0700','--tmpfs','/var/lib/voptrack-public:uid=10001,mode=0700','-v',recovered_config+':/retained-config:ro','-v',input_volume+':/recovery-inputs:ro','-v',input_volume+':/run:ro','--entrypoint','sh',image,'-c',script)
                    original_base,original_ctx=g['base'],g['ctx']
                    try:
                        g['base']='https://localhost:'+str(port);g['ctx']=ssl.create_default_context(cafile=server_ca)
                        for _ in range(60):
                            try:
                                if g['request'](g['browser'](),'GET','/.well-known/tessera')[0]==200:break
                            except Exception:pass
                            time.sleep(.25)
                        else:
                            state=json.loads(docker('inspect',name))[0]['State']
                            if state['ExitCode']==99:raise RuntimeError('host recovery adapter accepted mismatched independent producer identity')
                            raise RuntimeError('fresh restored server did not start')
                        recovered_admin=g['app_token']('owner',g['owner_pw'],{'administer':'yes'})
                        status,raw,_=g['api'](recovered_admin,'GET','/admin/api/v1/backup-scheduler')
                        recovered=json.loads(raw)
                        if status!=200 or recovered['settings_scope']==retained['settings_scope'] or not recovered['settings_scope'].startswith(deployment+'.') or any(recovered['settings'][k]!=settings_export[k] for k in ('every_hours','keep')):
                            raise RuntimeError('fresh-host producer identity/settings recovery failed')
                        if g['api'](former_collector,'GET','/admin/api/v1/backup-operations')[0]!=401:
                            raise RuntimeError('former producer collector authority was copied')
                        if g['api'](operator,'GET','/admin/api/v1/backup-scheduler')[0]!=401:
                            raise RuntimeError('post-snapshot former administrator authority was copied')
                        old_id=retained['settings_scope']+'.'+str(int(time.time()))+'-'+secrets.token_hex(16)
                        if g['api'](recovered_admin,'GET','/admin/api/v1/backup-settings-operations/'+old_id)[0]!=409:
                            raise RuntimeError('old settings scope accepted after recovery')
                        # Independent lineage and artifact MAC/member checks above
                        # precede explicit operator rebinding; never auto-enroll.
                        collector_id=secrets.token_hex(16)
                        collector_args=['--rm','--network','container:'+name,'--user','10001:10001','--read-only','--tmpfs','/tmp:uid=10001,mode=0700','--cap-drop','ALL','--security-opt','no-new-privileges:true','-v',collector_volume+':/var/lib/voptrack-collector','-v',bundle_volume+':/var/lib/voptrack-bundles','-v',server_ca+':/server.crt:ro']
                        # The collector sidecar sees only its credential, output and
                        # public CA. It shares networking, never the key filesystem.
                        docker('run','-i',*collector_args,'--entrypoint','sh',image,'-c','set -e; test ! -f /var/lib/voptrack-config/backup.key; umask 077; cat > /tmp/rebind-admin; exec voptrack-collector -mode provision -administrator-file /tmp/rebind-admin -origin https://localhost:9443 -ca /server.crt -id '+collector_id+' -deployment '+deployment,data=recovered_admin.encode())
                        credential=json.loads(docker('run',*collector_args,'--entrypoint','cat',image,'/var/lib/voptrack-collector/credential.json'))
                        if g['api'](credential['token'],'GET','/admin/api/v1/backup-scheduler')[0] not in (401,403):
                            raise RuntimeError('rebound collector received administrator authority')
                        for _ in range(600):
                            status,body,_=g['api'](recovered_admin,'GET','/admin/api/v1/backup-scheduler')
                            ready=json.loads(body) if status==200 else {}
                            retention=ready.get('retention') or {}
                            if ready.get('active_scheduled_operation') is None and retention.get('state')=='converged':break
                            if retention.get('state') in ('blocked','degraded'):raise RuntimeError('recovered startup retention failed')
                            time.sleep(.05)
                        else:raise RuntimeError('recovered startup acquisition did not settle')
                        new_id=str(int(time.time()))+'-'+secrets.token_hex(16)
                        if g['api'](recovered_admin,'PUT','/admin/api/v1/backup-operations/'+new_id,{'kind':'backup'})[0]!=202:
                            raise RuntimeError('post-recovery backup rejected')
                        if terminal(recovered_admin,new_id)['state']!='complete':raise RuntimeError('post-recovery backup failed')
                        docker('run',*collector_args,'--entrypoint','voptrack-collector',image,'-once','-origin','https://localhost:9443','-ca','/server.crt')
                        docker('run',*collector_args,'--entrypoint','sh',image,'-c','test ! -f /var/lib/voptrack-config/backup.key && test -f /var/lib/voptrack-bundles/'+new_id+'/backup.tar')
                        if g['api'](recovered_admin,'DELETE','/admin/api/v1/backup-collectors/'+collector_id)[0]!=204:
                            raise RuntimeError('rebound collector revocation failed')
                        g['say']('fresh-host '+label,'independent identity and genuine export recovered; new epoch; explicit collector rebind and completed backup collection')
                        restored=g['app_token']('sara',g['passwords']['sara'],{'read':'yes','write':'yes'})
                        st,raw,_=g['request'](g['browser'](),'POST',f'/v1/datasets/{g["stores"]["sara"]}/objects/{g["WEIGHT"]}:aggregate',js={'field':'kg'},headers={'Authorization':'Bearer '+restored})
                        if st!=200 or json.loads(raw)['aggregates'][0]['sum']!=sum(float(x) for x in g['weights']['sara']):
                            raise RuntimeError('unrelated record values did not survive restore')
                        st,_,_=g['api'](restored,'GET',f'/v1/stores/{sara_store}/items/{fixture}?representation=original')
                        if (expect_present and st!=200) or (not expect_present and st not in (404,410)):
                            raise RuntimeError('old archive / newer evidence erasure contract failed')
                    finally:g['base'],g['ctx']=original_base,original_ctx
                finally:
                    subprocess.run(['docker','rm','-fv',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
                    subprocess.run(['docker','volume','rm',volume,collector_volume,bundle_volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            g['say']('online bundles, fresh-volume restoration','MACs verified; old fence exposes after-R item; newer evidence removes it; unrelated records survive')
        finally:
            for volume in [input_volume,*kit_volumes.values()]:
                subprocess.run(['docker','volume','rm',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)

    return finish
