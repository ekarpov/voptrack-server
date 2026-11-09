"""Synthetic HTTP/TLS device progress under backup and collection load.
These are local test targets, not latency or zero-impact product promises.
"""
import json
import os
import threading
import time
import urllib.request
import urllib.error


def upload_fixture(g,token,store,size,content=None):
    if content is not None and len(content)!=size:raise RuntimeError("fixture payload size mismatch")
    api=g['api']
    st,body,_=api(token,'GET','/v1/stores')
    info=next(s for s in json.loads(body)['stores'] if s['id']==store)
    chunk_size=info['limits']['max_chunk_bytes']
    st,body,_=api(token,'POST',f'/v1/stores/{store}/imports',{'operation_id':os.urandom(16).hex(),'content_type':'application/octet-stream','byte_count':size,'maximum_bytes':size})
    if st!=200:raise RuntimeError('large fixture reservation refused')
    descriptor=json.loads(body)
    for offset in range(0,size,chunk_size):
        data=os.urandom(min(chunk_size,size-offset)) if content is None else content[offset:offset+chunk_size]
        req=urllib.request.Request(descriptor['url'],data=data,method=descriptor['method'],headers=dict(descriptor.get('headers',{}),**{'Content-Range':f'bytes {offset}-{offset+len(data)-1}/{size}'}))
        with urllib.request.urlopen(req,context=g['ctx'],timeout=30) as response:
            if response.status not in (200,204):raise RuntimeError('large fixture upload refused')
    st,body,_=api(token,'POST',f'/v1/stores/{store}/imports/{descriptor["upload_id"]}:commit')
    if st!=200:raise RuntimeError('large fixture commit refused')
    return json.loads(body)['item_id']


def start(g):
    api=g['api']
    errors=[]
    progress=[]
    stop=threading.Event()
    fixtures=[]
    measured_items={}
    def transfer(descriptor,data,total):
        url=descriptor['url']
        if not url.startswith(g['base']+'/'):
            raise RuntimeError('transfer origin mismatch')
        request=urllib.request.Request(url,data=data,method=descriptor['method'],headers=dict(descriptor.get('headers',{}),**{'Content-Range':f'bytes 0-{len(data)-1}/{total}'}))
        with urllib.request.urlopen(request,context=g['ctx'],timeout=30) as response:
            if response.status not in (200,204):raise RuntimeError('upload refused')
    # Two independent device authorizations and stores; 64 MiB incompressible
    # content gives producer/collector phases pressure; measured reads use a
    # separate known1KiB item so tracing is not a32MiB throughput requirement.
    for person in ('sara','ben'):
        token=g['app_tokens'][person]
        st,body,_=api(token,'GET','/v1/datasets')
        dataset=next(d for d in json.loads(body)['datasets'] if d['id']==g['stores'][person])
        store=dataset['content_store_id']
        fixtures.append((person,token,store,upload_fixture(g,token,store,32<<20)))
        expected=os.urandom(1024)
        measured_items[person]=(upload_fixture(g,token,store,len(expected),content=expected),expected)
    g["_online_fixture_items"]={person:item for person,token,store,item in fixtures}
    paused=threading.Event()
    idle={person:threading.Event() for person,_,_,_ in fixtures}
    def worker(person,token,store,item):
        measured_item,expected=measured_items[person]
        try:
            while not stop.is_set():
                if paused.is_set():
                    idle[person].set()
                    while paused.is_set() and not stop.is_set():time.sleep(.005)
                    idle[person].clear()
                if stop.is_set():break
                st=g['reads'](token,g['stores'][person],g['WEIGHT'])
                if st!=200:raise RuntimeError('record read refused')
                st,body,_=g['request'](g['browser'](),'POST',f'/v1/stores/{store}/items/{measured_item}:resolve',js={'representation':'original'},headers={'Authorization':'Bearer '+token})
                if st!=200:raise RuntimeError('content resolve refused')
                descriptor=json.loads(body)
                req=urllib.request.Request(descriptor['url'],headers=dict(descriptor.get('headers',{})))
                with urllib.request.urlopen(req,context=g['ctx'],timeout=30) as response:
                    raw=response.read()
                    if response.status!=200 or raw!=expected:
                        raise RuntimeError('content read failed')
                progress.append((person,'read',time.monotonic()))
                st,body,_=api(token,'POST',f'/v1/stores/{store}/imports',{'operation_id':os.urandom(16).hex(),'content_type':'application/octet-stream','byte_count':3,'maximum_bytes':3})
                if st!=200:raise RuntimeError('upload reservation refused')
                descriptor=json.loads(body)
                transfer(descriptor,b'abc',3)
                progress.append((person,'upload',time.monotonic()))
                if api(token,'DELETE',f'/v1/stores/{store}/imports/{descriptor["upload_id"]}')[0]!=204:
                    raise RuntimeError('upload abort refused')
                time.sleep(.025)
        except RuntimeError as error:
            errors.append(str(error))
        except urllib.error.HTTPError as error:
            errors.append('device HTTP status '+str(error.code))
        except Exception as error:
            errors.append('device fixture failed: '+type(error).__name__)
    threads=[threading.Thread(target=worker,args=v,daemon=True) for v in fixtures]
    for thread in threads:thread.start()
    time.sleep(1)
    def pause_workers():
        # Drain each whole device loop before deliberate task-fixture maintenance.
        # Request failures are never suppressed; measured windows run resumed.
        paused.set()
        until=time.monotonic()+35
        while not all(event.is_set() for event in idle.values()):
            if errors:raise RuntimeError(errors[0])
            if time.monotonic()>until:raise RuntimeError('device fixture maintenance barrier did not drain')
            time.sleep(.005)
        if errors:raise RuntimeError(errors[0])
    def resume_workers():paused.clear()
    g['_online_pause']=pause_workers
    g['_online_resume']=resume_workers
    def progress_until(guard):
        # This is a bounded evidence barrier, not a product latency target.
        begin=time.monotonic();until=begin+10
        guard()
        while not all(any(p==person and k==kind and stamp>begin for p,k,stamp in progress) for person in ('sara','ben') for kind in ('read','upload')):
            if errors:raise RuntimeError(errors[0])
            if time.monotonic()>until:
                import sys,traceback
                frames=sys._current_frames()
                diagnostic=[{'read_completions':sum(p==person and k=='read' and stamp>begin for p,k,stamp in progress),'upload_completions':sum(p==person and k=='upload' and stamp>begin for p,k,stamp in progress),'idle':idle[person].is_set(),'alive':thread.is_alive(),'stack':[f.name+':'+str(f.lineno) for f in traceback.extract_stack(frames[thread.ident])[-8:]] if thread.ident in frames else []} for person,thread in zip(('sara','ben'),threads)]
                raise RuntimeError('device progress evidence barrier timed out: '+json.dumps(diagnostic))
            guard();time.sleep(.02)
        guard() # The same overlap boundary still holds after all four events.
    g['_online_progress_until']=progress_until
    def phase(label,action):
        begin=time.monotonic()
        result=action()
        end=time.monotonic()
        if errors:raise RuntimeError(errors[0])
        for person in ('sara','ben'):
            for kind in ('read','upload'):
                if not any(p==person and k==kind and begin<t<end for p,k,t in progress):
                    raise RuntimeError('no successful '+kind+' inside '+label+' for '+person)
        g['say']('device HTTP progress during '+label,'both devices read content and advance uploads')
        return result
    def finish():
        stop.set()
        paused.clear()
        for thread in threads:thread.join(35)
        if errors or any(t.is_alive() for t in threads):raise RuntimeError('device progress worker failed')
    return phase,finish
