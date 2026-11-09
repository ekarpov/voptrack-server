#!/usr/bin/env python3
"""A bounded DAG over the unchanged full gate set. Targeted green is not release green."""
import argparse
import concurrent.futures
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
from smoke_image import verify as verify_image
from tessera_pin import expected_pin

ROOT=Path(__file__).resolve().parents[1]
MANIFEST=ROOT/'ci/gates.json'

def load_gates():
    data=json.loads(MANIFEST.read_text())
    gates=data['gates']
    names=[g['name'] for g in gates]
    if data['version']!=1 or len(names)!=len(set(names)):
        raise ValueError('invalid gate manifest')
    seen=set()
    for g in gates:
        if not re.fullmatch(r'[a-z][a-z0-9-]*',g['name']) or not set(g['depends'])<=seen:
            raise ValueError('invalid dependency graph')
        seen.add(g['name'])
    return gates

def select(gates, requested):
    if not requested:return {g['name'] for g in gates},True
    by_name={g['name']:g for g in gates};result=set()
    def add(name):
        if name not in by_name:raise ValueError('unknown selected gate')
        if name in result:return
        for dep in by_name[name]['depends']:add(dep)
        result.add(name)
    for name in requested:add(name)
    return result,False

def resources(base,slot,run_id,name):
    """Borrow the already reserved CI dev-listener bundles; never choose external ports."""
    stem='HOME' if slot==0 else 'RESEARCH'
    defaults=(8644,8680,9446,'10.117.50') if slot==0 else (8744,8780,9447,'10.117.51')
    preferred=[]
    for product,values in [('HOME',(8644,8680,9446)),('RESEARCH',(8744,8780,9447))]:
        preferred.extend(int(base.get(f'VOPTRACK_{product}_DEV_{key}',default)) for key,default in zip(('PORT','HTTP_PORT','CONSOLE_PORT'),values))
    extras={int(base.get(k,str(v))) for k,v in [('VOPTRACK_GUIDED_PORT',30943),('VOPTRACK_GUIDED_RESTORE_PORT',30944),('VOPTRACK_OFF_MACHINE_PORT',30691),('VOPTRACK_RECOVERY_SET_RESTORE_PORT',30843)]}
    reserved=set(preferred)|{int(v) for k,v in base.items() if k.startswith('VOPTRACK_') and k.endswith('_PORT') and v.isdigit() and 1024<=int(v)<=65535}
    reserved-=extras
    assigned=[]
    for preferred_port in preferred:
        candidates=[preferred_port] if preferred_port in reserved and preferred_port not in assigned else sorted(reserved-set(assigned))
        if not candidates:raise ValueError('not enough distinct reserved CI listeners')
        assigned.append(candidates[0])
    https,http,console=assigned[slot*3:slot*3+3]
    net=base.get(f'VOPTRACK_{stem}_DEV_NET',defaults[3])
    subnet=ipaddress.ip_network(net+'.0/24')
    if not subnet.is_private or not all(1024<=p<=65535 for p in (https,http,console)) or len({https,http,console})!=3:
        raise ValueError('invalid CI slot resources')
    project='voptrack-ci-'+run_id+'-'+name
    env=dict(base,VOPTRACK_PUBLISH_PREFIX="127.0.0.1:",VOPTRACK_CONSOLE_BIND="127.0.0.1")
    # Explicit names in ordinary gates plus any configured overrides.
    prefixes=('RELEASE','HOUSEHOLD','CONSOLE','KIT','RESEARCH','RECOVERY','HOMEAPP','PRIVATE','RECOVERY_SET')
    for prefix in prefixes:
        env[f'VOPTRACK_{prefix}_HTTPS_PORT']=str(https)
        env[f'VOPTRACK_{prefix}_HTTP_PORT']=str(http)
        env[f'VOPTRACK_{prefix}_CONSOLE_PORT']=str(console)
    env.update(VOPTRACK_HTTPS_PORT=str(https),VOPTRACK_HTTP_PORT=str(http),VOPTRACK_CONSOLE_PORT=str(console),
               VOPTRACK_RECOVERY_SET_PORT=str(https),
               VOPTRACK_PROXY_SUBNET=str(subnet),VOPTRACK_PROXY_ADDRESS=net+'.2',VOPTRACK_PROXY_POOL=net+'.128/25',
               VOPTRACK_RECOVERY_SET_SUBNET=str(subnet),VOPTRACK_RECOVERY_SET_PROXY_ADDRESS=net+'.2',
               VOPTRACK_RECOVERY_SET_PROXY_POOL=net+'.128/25',
               VOPTRACK_GATE_PROJECT=project,VOPTRACK_KIT_PROJECT=project,VOPTRACK_CONSOLE_PROJECT=project,
               VOPTRACK_RECOVERY_SET_PROJECT=project,VOPTRACK_HOME_DEV_PROJECT=project,VOPTRACK_RESEARCH_DEV_PROJECT=project)
    for product in ('HOME','RESEARCH'):
        for key,value in [('PORT',https),('HTTP_PORT',http),('CONSOLE_PORT',console),('NET',net)]:
            env[f'VOPTRACK_{product}_DEV_{key}']=str(value)
    return env,(https,http,console),subnet

def validate_slots(base):
    a,pa,na=resources(base,0,'probe','probe');b,pb,nb=resources(base,1,'probe','probe')
    extras={int(base.get(k,str(v))) for k,v in [('VOPTRACK_GUIDED_PORT',30943),('VOPTRACK_GUIDED_RESTORE_PORT',30944),('VOPTRACK_OFF_MACHINE_PORT',30691),('VOPTRACK_RECOVERY_SET_RESTORE_PORT',30843)]}
    if set(pa)&set(pb) or na.overlaps(nb) or (set(pa)|set(pb))&extras:
        raise ValueError('CI worker resources overlap')
    return True

def metrics(path):
    totals={'auth_retries':0,'auth_wait_seconds':0,'http_requests':0,'http_seconds':0,'setup_sessions_reused':0}
    if path.exists():
        for line in path.read_text().splitlines():
            value=json.loads(line);phase=value['phase']
            if phase=='auth-backoff':totals['auth_retries']+=1;totals['auth_wait_seconds']+=value['seconds']
            if phase=='http-request':totals['http_requests']+=1;totals['http_seconds']+=value['seconds']
            if phase=='setup-session-reused':totals['setup_sessions_reused']+=1
    return totals

def check_budget(workers):
    if workers==2 and (os.cpu_count() or 1)<2:
        raise ValueError('two CI workers need two CPUs')
    available=next((int(l.split()[1])*1024 for l in Path('/proc/meminfo').read_text().splitlines() if l.startswith('MemAvailable:')),0) if Path('/proc/meminfo').exists() else 2<<30
    if workers==2 and available<1536<<20:
        raise ValueError('two CI workers need 1.5 GiB available memory')


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--steps',default=os.environ.get('CI_STEPS',''))
    parser.add_argument('--workers',type=int,choices=(1,2),default=int(os.environ.get('CI_WORKERS','2')))
    parser.add_argument('--plan',action='store_true')
    args=parser.parse_args()
    gates=load_gates();chosen,full=select(gates,[v for v in args.steps.split(',') if v])
    if args.plan:
        print(json.dumps({'mode':'full' if full else 'partial','workers':args.workers,'gates':[g for g in gates if g['name'] in chosen]},indent=2));return 0
    status=subprocess.check_output(['git','status','--porcelain'],cwd=ROOT,text=True)
    if status:raise ValueError('commit changes before CI')
    sha=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
    base=dict(os.environ);validate_slots(base)
    check_budget(args.workers)
    run_id=time.strftime('%Y%m%dT%H%M%SZ',time.gmtime())+'-'+os.urandom(6).hex()
    commit_dir=ROOT/'.ci'/sha;run_dir=commit_dir/'runs'/run_id
    run_dir.mkdir(parents=True,mode=0o700)
    base.update(root=str(ROOT),logs=str(run_dir),VOPTRACK_STORAGE_ENCRYPTED='yes',VOPTRACK_PUBLISH_PREFIX='127.0.0.1:',VOPTRACK_CONSOLE_BIND='127.0.0.1')
    results={};image_receipt=None;started=time.monotonic();max_running=0;cancelled=False
    active={}
    def stop(signum,frame):
        nonlocal cancelled
        cancelled=True
    signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
    def execute(gate,slot,env):
        name=gate['name'];log=run_dir/(name+'.log');metric=run_dir/(name+'.metrics.jsonl')
        env=dict(env,VOPTRACK_CI_METRICS_FILE=str(metric))
        t=time.monotonic()
        with log.open('w') as output:
            process=subprocess.Popen(['bash','-o','pipefail','-c',gate['command']],cwd=ROOT,env=env,stdout=output,stderr=subprocess.STDOUT,start_new_session=True)
            stopping=False
            while process.poll() is None:
                if cancelled and not stopping:
                    try:os.killpg(process.pid,signal.SIGTERM)
                    except ProcessLookupError:pass
                    stopping=True
                time.sleep(.1)
            rc=process.returncode
        result={'name':name,'status':'PASS' if rc==0 else 'FAIL','seconds':round(time.monotonic()-t,3),'slot':slot,'metrics':metrics(metric)}
        if name=='image' and rc==0:
            try:result['image_receipt']=verify_image(env['VOPTRACK_IMAGE'])
            except (ValueError,subprocess.SubprocessError) as e:
                result['status']='FAIL'
                with log.open('a') as output:output.write('Image preflight failed: '+str(e)+'\n')
        return result
    print(f"ci: {sha[:12]}, {'full' if full else 'partial'}, {args.workers} workers",flush=True)
    last_progress=time.monotonic()
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        while len(results)<len(chosen):
            for future in list(active):
                if not future.done():continue
                gate,slot=active.pop(future)
                try:result=future.result()
                except Exception:result={'name':gate['name'],'status':'FAIL','seconds':0,'slot':slot,'metrics':{}}
                results[gate['name']]=result
                if result.get('image_receipt'):
                    image_receipt=result['image_receipt'];base['VOPTRACK_IMAGE']=image_receipt['image']
                print(f"  {gate['name']:<24} {result['status']:<4} {result['seconds']:.1f}s",flush=True)
            for gate in gates:
                name=gate['name']
                if name not in chosen or name in results or any(g['name']==name for g,s in active.values()):continue
                if cancelled or any(results.get(d,{}).get('status') in ('FAIL','SKIP') for d in gate['depends']):
                    results[name]={'name':name,'status':'SKIP','seconds':0,'reason':'cancelled' if cancelled else 'failed prerequisite','metrics':{}}
                    print(f"  {name:<24} SKIP",flush=True)
                    continue
                if not all(results.get(d,{}).get('status')=='PASS' for d in gate['depends']):continue
                # Host-wide preservation inventories need a stable Docker host.
                # Drain existing work before an exclusive gate; launch nothing
                # else until it finishes, without relaxing inventory assertions.
                if any(g.get('exclusive',False) for g,s in active.values()):break
                if gate.get('exclusive',False) and active:break
                if len(active)>=args.workers:break
                slot=next(s for s in range(args.workers) if s not in [v[1] for v in active.values()])
                env=dict(base)
                if gate['stage']=='runtime':
                    env,_,_=resources(base,slot,run_id[-12:],name)
                    env['VOPTRACK_HOME_DEV_STATE']=str(run_dir/'home-dev')
                    env['VOPTRACK_RESEARCH_DEV_STATE']=str(run_dir/'research-dev')
                elif gate['stage']=='scenario':
                    env['VOPTRACK_SCENARIO_PROJECT']='voptrack-ci-'+run_id[-12:]+'-'+name
                    env['VOPTRACK_TWIN_PROJECT']=env['VOPTRACK_SCENARIO_PROJECT']+'-verify'
                active[pool.submit(execute,gate,slot,env)]=(gate,slot)
                print(f"  {name:<24} START slot {slot+1}",flush=True)
                max_running=max(max_running,len(active))
            if len(results)==len(chosen):break
            if not active:raise RuntimeError('unsatisfied dependency graph')
            if time.monotonic()-last_progress>=30:
                print('ci: running '+', '.join(g['name'] for g,s in active.values()),flush=True);last_progress=time.monotonic()
            time.sleep(.1)
    passed=all(r['status']=='PASS' for r in results.values()) and not cancelled
    mode='full' if full else 'partial'
    receipt={'version':1,'mode':mode,'commit':sha,'tessera':expected_pin(),'image':image_receipt['image'] if image_receipt else None,
             'workers':args.workers,'maximum_concurrent_gates':max_running,'seconds':round(time.monotonic()-started,3),
             'qualified':full and passed,'manifest_sha256':hashlib.sha256(MANIFEST.read_bytes()).hexdigest(),
             'gates':[results[g['name']] for g in gates if g['name'] in chosen]}
    (run_dir/'result.json').write_text(json.dumps(receipt,indent=2)+'\n')
    lines=['ci: '+('pass' if passed else 'fail') if full else 'ci: partial '+('pass' if passed else 'fail'),
           'mode: '+mode,'commit: '+sha,'tessera: '+expected_pin(),'image: '+(receipt['image'] or 'none'),
           f"workers: {args.workers}",f"seconds: {receipt['seconds']}"]
    lines += [f"{g['name']:<24} {results[g['name']]['status']:<4} {int(results[g['name']]['seconds'])}s" for g in gates if g['name'] in chosen]
    text='\n'.join(lines)+'\n';(run_dir/'summary.txt').write_text(text)
    if full:
        (commit_dir/'summary.txt').write_text(text)
        (commit_dir/'result.json').write_text(json.dumps(receipt,indent=2)+'\n')
        subprocess.run(['git','-c','user.name=voptrack-ci','-c','user.email=ci@localhost','notes','--ref=ci','add','-f','-F',str(run_dir/'summary.txt'),sha],cwd=ROOT,check=True)
    print(f"ci: {mode} {'pass' if passed else 'fail'} in {receipt['seconds']:.1f}s; {run_dir.relative_to(ROOT)}",flush=True)
    return 0 if passed else 1

if __name__=='__main__':
    try:raise SystemExit(main())
    except (ValueError,subprocess.CalledProcessError) as e:
        sys.exit(str(e))
