"""Compile isolated intended defects and verify actual-image gate failures.
Uses private source archives/indexes and tree objects; no commit or branch move.
Usage: qualify-faults.py SOURCE_TREE INTACT_IMAGE NEW_PRIVATE_ROOT
"""
import json,os,pathlib,subprocess,sys

import sys as _sys
from pathlib import Path as _Path
_sys.path.insert(0, str(_Path(__file__).resolve().parents[2] / 'ci'))
from tessera_pin import expected_pin
PIN = expected_pin()
REPO=pathlib.Path(__file__).resolve().parents[2]
TREE,INTACT=sys.argv[1:3];ROOT=pathlib.Path(sys.argv[3]).resolve()
if ROOT.exists():raise RuntimeError('fresh private root required')
ROOT.mkdir(parents=True,mode=0o700)
ENV=dict(os.environ,PATH='/usr/local/go/bin:'+os.environ['PATH'])
def run(args,**kw):return subprocess.run(args,check=True,env=ENV,**kw)
def git(*args,env=None,data=None):return subprocess.check_output(['git','-C',str(REPO),*args],env=env or ENV,input=data,text=True).strip()
def replace(s,a,b):
 if s.count(a)!=1:raise RuntimeError('unique defect anchor required')
 return s.replace(a,b,1)
def variant(kind):
 directory=ROOT/kind;directory.mkdir(mode=0o700);source=directory/'source';source.mkdir()
 archive=subprocess.Popen(['git','-C',str(REPO),'archive',TREE],stdout=subprocess.PIPE)
 run(['tar','-xf','-','-C',str(source)],stdin=archive.stdout);archive.stdout.close()
 if archive.wait()!=0:raise RuntimeError('source archive failed')
 path='internal/homesetup/service.go'
 if kind=='csrf-defect':
  path='internal/homesetup/http.go';a='!same(v.CSRF, r.PostForm.Get("csrf"))';b='(false && !same(v.CSRF, r.PostForm.Get("csrf")))'
 elif kind=='binding-defect':
  path='internal/homesetup/http.go';a='r.PostForm.Get("binding") != s.journal.Binding';b='(false && r.PostForm.Get("binding") != s.journal.Binding)'
 elif kind=='material-binding-defect':
  path='internal/homesetup/http.go';a='raw, e = materialKey(raw)';b='raw, e = readRegular(at(s.Config.Root,"var/lib/voptrack-config/backup.key"),32)'
 elif kind=='restore-attestation-defect':
  path='cmd/homesetup/main.go';a='if os.Args[2] == "restore" {';b='if false && os.Args[2] == "restore" {'
 elif kind=='completed-routing-defect':
  a='(s.journal.Phase != "complete" && (s.journal.Origin != c.Origin || s.journal.PublicURL != c.PublicURL))';b='(s.journal.Origin != c.Origin || s.journal.PublicURL != c.PublicURL)'
 elif kind=='closed-sync-defect':
  a='if os.IsNotExist(e) {\n\t\treturn syncDir(filepath.Dir(path))\n\t}'
  b='if os.IsNotExist(e) {\n\t\treturn nil\n\t}'
 elif kind=='restart-defect':
  a='case "accepted", "provisioning", "password":\n\t\t\ts.journal.Phase = "stopped"';b='case "accepted", "provisioning", "password":\n\t\t\ts.journal.Phase = "review"'
 elif kind=='unused-grant-defect':
  a='return writeJSON(filepath.Join(c.State, "authorization.json"), grant{Consumed: true})\n\t\tcase "accepted"'
  b='return nil\n\t\tcase "accepted"'
 elif kind=='existing-target-defect':a='if c.fresh() != nil {';b='if false && c.fresh() != nil {'
 elif kind=='startup-guard-defect':
  path='cmd/deployctl/main.go';a='if homesetup.LaunchAllowed(homesetup.PendingFile) != nil {';b='if false && homesetup.LaunchAllowed(homesetup.PendingFile) != nil {'
 elif kind=='durable-stops':
  a='return writeJSON(filepath.Join(s.Config.State, "journal.json"), s.journal)'
  b='if e:=writeJSON(filepath.Join(s.Config.State,"journal.json"),s.journal);e!=nil{return e}\n if phase,_:=os.ReadFile("/stop-at-phase");string(phase)==s.journal.Phase{os.Exit(75)}\n return nil'
 else:raise RuntimeError('unknown defect')
 p=source/path;p.write_text(replace(p.read_text(),a,b));run(['gofmt','-w',str(p)])
 with (directory/'compile.log').open('w') as log:run(['go','build','./cmd/homesetup','./cmd/deployctl'],cwd=source,stdout=log,stderr=subprocess.STDOUT)
 env=dict(ENV,GIT_INDEX_FILE=str(directory/'private.index'));git('read-tree',TREE,env=env)
 blob=git('hash-object','-w',str(p));git('update-index','--cacheinfo','100644',blob,path,env=env)
 tree=git('write-tree',env=env);tag='voptrack-home-setup-proof-'+kind+'-'+tree[:10]
 with (directory/'image-build.log').open('w') as log:run(['make','image','BUILD_IMAGE='+tag],cwd=source,stdout=log,stderr=subprocess.STDOUT)
 image=subprocess.check_output(['docker','image','inspect',tag,'--format','{{.Id}}'],text=True).strip()
 version=subprocess.check_output(['docker','run','--rm','--network','none','--tmpfs','/var/lib/tessera','--entrypoint','tesserad',image,'-version'],text=True).strip()
 if version!=PIN:raise RuntimeError('fault image pin changed')
 result=dict(kind=kind,tree=tree,image=image,native_compiles=True,actual_binary_version=version)
 (directory/'identity.json').write_text(json.dumps(result,indent=2)+'\n');print('Built '+kind,flush=True);return result
def gate(image,name,mode,expect=None):
 env=dict(ENV,VOPTRACK_HOME_SETUP_MODE=mode,VOPTRACK_HOME_SETUP_INTACT_IMAGE=INTACT)
 with (ROOT/(name+'.log')).open('w') as log:
  p=subprocess.run([sys.executable,str(REPO/'release/home-setup/gate.py'),image,str(ROOT/name)],env=env,stdout=log,stderr=subprocess.STDOUT)
 text=(ROOT/(name+'.log')).read_text()
 if expect:
  if p.returncode==0 or expect not in text:raise RuntimeError('intended assertion not reached: '+name)
 elif p.returncode!=0:raise RuntimeError('intact replay failed: '+name)
 print(name+': '+('intended failure observed' if expect else 'PASS'),flush=True)
 return dict(name=name,exit=p.returncode,intended_assertion=expect)
def main():
 specs=[
  ('csrf-defect','csrf','CSRF admitted'),
  ('binding-defect','binding','changed review binding admitted'),
  ('material-binding-defect','material','key download mixed material generations'),
  ('restore-attestation-defect','restore','restored Home reused old host encryption attestation'),
  ('completed-routing-defect','routing','completed setup blocked current host routing'),
  ('closed-sync-defect','closed-sync','missing guard bypassed failed parent sync'),
  ('restart-defect','restart','interrupted initialization became retryable'),
  ('unused-grant-defect','restart','restart preserved unused host grant'),
  ('existing-target-defect','existing','preexisting target admitted'),
  ('startup-guard-defect','startup','service started before setup completion')]
 results=[];candidates=[]
 for kind,mode,assertion in specs:
  v=variant(kind);candidates.append(v)
  results.append(gate(v['image'],kind+'-negative',mode,assertion))
  results.append(gate(INTACT,kind+'-intact-replay',mode))
 v=variant('durable-stops');candidates.append(v);results.append(gate(v['image'],'durable-interruptions','interruptions'))
 (ROOT/'qualification.json').write_text(json.dumps(dict(base_tree=TREE,intact_image=INTACT,candidates=candidates,results=results,active_checkout_modified=False,aws_qualified=False,separate_host_qualified=False),indent=2)+'\n')
 print('Compiling defects and intact actual-image replays: PASS',flush=True)

if __name__=='__main__':main()
