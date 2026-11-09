"""Compile isolated defects, verify their actual images, and prove gate assertions.
Usage: qualify-faults.py SOURCE_COMMIT INTACT_IMAGE INPUTS NEW_PRIVATE_ROOT
Private Git indexes/objects only; no branch or active source is modified.
"""
import json,os,pathlib,subprocess,sys

import sys as _sys
from pathlib import Path as _Path
_sys.path.insert(0, str(_Path(__file__).resolve().parents[2] / 'ci'))
from tessera_pin import expected_pin
PIN = expected_pin()
REPO=pathlib.Path(__file__).resolve().parents[2]
BASE,INTACT,INPUTS=sys.argv[1:4];ROOT=pathlib.Path(sys.argv[4]).resolve()
if ROOT.exists():raise RuntimeError('fresh private qualification root required')
ROOT.mkdir(parents=True,mode=0o700)
ENV=dict(os.environ,PATH='/usr/local/go/bin:'+os.environ['PATH'])
def run(args,**kw):return subprocess.run(args,check=True,env=ENV,**kw)
def git(*args,env=None,data=None):return subprocess.check_output(['git','-C',str(REPO),*args],env=env or ENV,input=data,text=True).strip()
def replace(s,a,b):
 if s.count(a)!=1:raise RuntimeError('unique defect anchor required: '+a)
 return s.replace(a,b,1)
def variant(kind):
 directory=ROOT/kind;directory.mkdir(mode=0o700);source=directory/'source';source.mkdir()
 archive=subprocess.Popen(['git','-C',str(REPO),'archive',BASE],stdout=subprocess.PIPE)
 run(['tar','-xf','-','-C',str(source)],stdin=archive.stdout);archive.stdout.close()
 if archive.wait()!=0:raise RuntimeError('source archive failed')
 path='internal/backupreplication/worker.go';p=source/path;s=p.read_text()
 if kind=='readback-defect':s=replace(s,'verified := w.verify(ctx, m)','verified := error(nil)')
 elif kind=='premature-ack':s=replace(s,'err = w.transfer(opCtx, r, &j)','_ = q.Release(r)\n err = w.transfer(opCtx, r, &j)')
 elif kind=='conflict-defect':s=replace(s,'verified := w.verify(ctx, m)','if e == ErrExists { return nil }\n verified := w.verify(ctx, m)')
 elif kind=='status-binding-defect':s=replace(s,'bc.ReadPrivateJSON(filepath.Join(w.Config.State, "summary.json"), &old)','bc.ReadPrivateJSON(w.Config.StatusFile, &old)')
 elif kind=='owner-binding-defect':s=replace(s,'c.Outbox, c.ExpectedBucketOwner','c.Outbox')
 elif kind=='missing-binding-defect':s=replace(s,'if entry.Name() != "worker.lock" {','if false && entry.Name() != "worker.lock" {')
 elif kind=='outbox-barrier-defect':
  path='internal/backupcollector/outbox.go';p=source/path;s=p.read_text()
  s=replace(s,'if syncDir(q.Dir) != nil {\n\t\treturn ErrStorage\n\t}', 'if false && syncDir(q.Dir) != nil {\n\t\treturn ErrStorage\n\t}')
 elif kind=='durable-stops':
  s=replace(s,'return recoverymaterial.Write(filepath.Join(w.Config.State, id+".json"), raw)','if err:=recoverymaterial.Write(filepath.Join(w.Config.State,id+".json"),raw);err!=nil{return err}\n if phase,_:=os.ReadFile("/stop-at-phase");string(phase)==j.Phase{os.Exit(75)}\n return nil')
 elif kind=='publication-ack-defect':
  path='internal/backupcollector/outbox.go';p=source/path;s=p.read_text()
  s=replace(s,'if _, err := os.Lstat(filepath.Join(s.Dir, id)); err == nil {','if _, err := os.Lstat(filepath.Join(s.Dir, id)); false && err == nil {')
 elif kind=='handoff-defect':
  path='internal/backupcollector/collect.go';p=source/path;s=p.read_text()
  # Disable capture and publication acknowledgement together, leaving a compiling
  # collector that publishes/prunes native artifacts without durable obligations.
  anchor='if c.Outbox != nil {'
  start=s.index('func (c *Collector) collect(') if 'func (c *Collector) collect(' in s else s.index('func (c Collector) collect(')
  s=s[:start]+s[start:].replace(anchor,'if false && c.Outbox != nil {')
 else:raise RuntimeError('unknown defect')
 p.write_text(s);run(['gofmt','-w',str(p)])
 with (directory/'compile.log').open('w') as log:run(['go','build','./cmd/backupreplicator','./cmd/backupcollector'],cwd=source,stdout=log,stderr=subprocess.STDOUT)
 env=dict(ENV,GIT_INDEX_FILE=str(directory/'private.index'));git('read-tree',BASE,env=env)
 blob=git('hash-object','-w',str(p));git('update-index','--cacheinfo','100644',blob,path,env=env)
 tree=git('write-tree',env=env);commit=git('commit-tree',tree,'-p',BASE,data='Isolated off-machine '+kind+' qualification\n')
 tag='voptrack-off-machine-proof-'+kind+'-'+commit[:10]
 with (directory/'image-build.log').open('w') as log:run(['make','image','BUILD_IMAGE='+tag],cwd=source,stdout=log,stderr=subprocess.STDOUT)
 image=subprocess.check_output(['docker','image','inspect',tag,'--format','{{.Id}}'],text=True).strip()
 version=subprocess.check_output(['docker','run','--rm','--network','none','--tmpfs','/var/lib/tessera','--entrypoint','tesserad',image,'-version'],text=True).strip()
 if version!=PIN:raise RuntimeError('actual fault image pin changed')
 result={'kind':kind,'commit':commit,'tree':tree,'image':image,'native_compiles':True,'actual_binary_version':version}
 (directory/'identity.json').write_text(json.dumps(result,indent=2)+'\n');print('Built '+kind,flush=True);return result
def gate(image,name,mode,expect=None):
 env=dict(ENV,VOPTRACK_OFF_MACHINE_MODE=mode,VOPTRACK_OFF_MACHINE_INTACT_IMAGE=INTACT)
 with (ROOT/(name+'.log')).open('w') as log:
  p=subprocess.run([sys.executable,str(REPO/'release/off-machine/gate.py'),image,INPUTS,str(ROOT/name)],env=env,stdout=log,stderr=subprocess.STDOUT)
 text=(ROOT/(name+'.log')).read_text()
 if expect:
  if p.returncode==0 or expect not in text:raise RuntimeError('intended assertion not reached: '+name)
 elif p.returncode!=0:raise RuntimeError('intact replay failed: '+name)
 print(name+': '+('intended failure observed' if expect else 'PASS'),flush=True)
 return {'name':name,'exit':p.returncode,'intended_assertion':expect}
specs=[
 ('missing-binding-defect','binding-loss','missing or indirect binding admitted existing acknowledgements'),
 ('outbox-barrier-defect','outbox-barrier','outbox observation bypassed failed parent sync'),
 ('publication-ack-defect','local-replay','published-before-ack restart refused exact existing target'),
 ('readback-defect','readback','corrupt readback was accepted'),
 ('premature-ack','readback','failed transfer released pending bytes'),
 ('conflict-defect','conflict','conflicting object was accepted'),
 ('handoff-defect','handoff','local rotation lost durable replication obligations'),
 ('status-binding-defect','binding','public success rebound to different destination'),
 ('owner-binding-defect','binding','bucket owner changed without state refusal')]
results=[];candidates=[]
for kind,mode,assertion in specs:
 v=variant(kind);candidates.append(v)
 results.append(gate(v['image'],kind+'-negative',mode,assertion))
 results.append(gate(INTACT,kind+'-intact-replay',mode))
v=variant('durable-stops');candidates.append(v)
results.append(gate(v['image'],'durable-interruptions','interruptions'))
(ROOT/'qualification.json').write_text(json.dumps({'base':BASE,'intact_image':INTACT,'candidates':candidates,'results':results,'active_checkout_modified':False,'aws_qualified':False,'separate_host_qualified':False},indent=2)+'\n')
print('Compiling intended defects and intact actual-image replays: PASS',flush=True)
