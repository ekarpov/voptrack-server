"""Build isolated compiling defects and prove actual-image gate assertions.
Usage: qualify-faults.py COMMIT INTACT_IMAGE SYNTHETIC_INPUTS NEW_PRIVATE_ROOT
Creates Git objects with a private index; never updates a branch or shared index.
No planted source is written to the active checkout.
"""
import json,os,pathlib,shutil,subprocess,sys

import sys as _sys
from pathlib import Path as _Path
_sys.path.insert(0, str(_Path(__file__).resolve().parents[2] / 'ci'))
from tessera_pin import expected_pin
PIN = expected_pin()
REPO=pathlib.Path(__file__).resolve().parents[2]
BASE,INTACT,INPUTS=sys.argv[1:4];ROOT=pathlib.Path(sys.argv[4]).resolve()
if ROOT.exists():raise RuntimeError('fault qualification root must be fresh')
ROOT.mkdir(parents=True,mode=0o700)
ENV=dict(os.environ,PATH='/usr/local/go/bin:'+os.environ['PATH'])
def run(args,**kw):return subprocess.run(args,check=True,env=ENV,**kw)
def git(*args,env=None,data=None):
 return subprocess.check_output(['git','-C',str(REPO),*args],env=env or ENV,input=data,text=True).strip()
def variant(kind):
 directory=ROOT/kind;directory.mkdir(mode=0o700);source=directory/'source';source.mkdir()
 archive=subprocess.Popen(['git','-C',str(REPO),'archive',BASE],stdout=subprocess.PIPE)
 subprocess.run(['tar','-xf','-','-C',str(source)],stdin=archive.stdout,check=True);archive.stdout.close()
 if archive.wait()!=0:raise RuntimeError('candidate archive failed')
 path='internal/guidedrecovery/service.go'
 if kind=='csrf-defect':
  path='internal/guidedrecovery/http.go';p=source/path;s=p.read_text()
  old='if !same(r.PostForm.Get("csrf"), sess.CSRF) {'
  if old not in s:raise RuntimeError('CSRF defect anchor missing')
  s=s.replace(old,'if false && !same(r.PostForm.Get("csrf"), sess.CSRF) {',1);p.write_text(s)
 elif kind=='restart-defect':
  p=source/path;s=p.read_text()
  start=s.index('case "accepted", "creating-target"');end=s.index('default:',start)
  block=s[start:end]
  if 's.journal.Phase = "stopped"' not in block:raise RuntimeError('restart defect anchor missing')
  s=s[:start]+block.replace('s.journal.Phase = "stopped"','s.journal.Phase = "select"',1)+s[end:];p.write_text(s)
 elif kind=='empty-evidence-defect':
  path='internal/guidedrecovery/execute.go';p=source/path;s=p.read_text()
  anchor=' && name != "tessera/dataset.db.retired"'
  if anchor not in s:raise RuntimeError('empty evidence defect anchor missing')
  p.write_text(s.replace(anchor,'',1))
 elif kind in ('short-session','expiry-defect'):
  path='internal/guidedrecovery/http.go';p=source/path;s=p.read_text().replace('"net/url"','"net/url"\n "os"',1)
  anchor='expiry := s.now().Add(15 * time.Minute)'
  if anchor not in s:raise RuntimeError('short session anchor missing')
  s=s.replace(anchor,anchor+'\n if _,err:=os.Stat("/work/short-session");err==nil{expiry=s.now().Add(5*time.Second)}',1)
  if kind=='expiry-defect':
   anchor='if !s.now().Before(sess.Expires) {'
   if anchor not in s:raise RuntimeError('expiry defect anchor missing')
   s=s.replace(anchor,'if false && !s.now().Before(sess.Expires) {',1)
  p.write_text(s)
 else:
  p=source/path;s=p.read_text()
  old='return recoverymaterial.Write(filepath.Join(s.config.State, "journal.json"), raw)'
  if old not in s:raise RuntimeError('durable stop anchor missing')
  s=s.replace(old,'''if err:=recoverymaterial.Write(filepath.Join(s.config.State,"journal.json"),raw);err!=nil{return err}
 if phase,_:=os.ReadFile("/work/stop-at-stage");string(phase)==s.journal.Phase{os.Exit(75)}
 return nil''',1);p.write_text(s)
 paths=[path]
 if kind=='durable-stops':
  command_path='internal/guidedrecovery/execute.go';command_file=source/command_path;command_source=command_file.read_text()
  anchor='cmd := exec.CommandContext(ctx, "/usr/local/bin/tesserad", args...)'
  if anchor not in command_source:raise RuntimeError('command failure anchor missing')
  injection='''mode,_:=os.ReadFile("/work/stop-at-stage")
 for _,arg:=range args{if (string(mode)=="fail-producer"&&arg=="-recover-backup-producer")||(string(mode)=="fail-records"&&arg=="-restore"){
  if err:=os.WriteFile("/work/targets/fresh/"+string(mode),[]byte("synthetic partial mutation"),0600);err!=nil{return err}
  return os.ErrInvalid
 }}
 '''
  command_file.write_text(command_source.replace(anchor,injection+anchor,1));paths.append(command_path)
  kit_path='internal/recoverykit/validate.go';kit_file=source/kit_path;kit_source=kit_file.read_text()
  anchor='if e = recoverymaterial.Write(path, b); e != nil {\n\t\t\treturn e\n\t\t}'
  if anchor not in kit_source:raise RuntimeError('partial kit failure anchor missing')
  kit_file.write_text(kit_source.replace(anchor,anchor+'\n if mode,_:=os.ReadFile("/work/stop-at-stage");string(mode)=="fail-kit"{return ErrUnavailable}',1));paths.append(kit_path)
 for changed in paths:run(['gofmt','-w',str(source/changed)])

 # Compile before building; a noncompiling defect proves nothing.
 run(['go','build','-o',str(directory/'compiled-recovery'),'./cmd/recovery'],cwd=source,stdout=subprocess.DEVNULL)
 env=dict(ENV,GIT_INDEX_FILE=str(directory/'private.index'))
 git('read-tree',BASE,env=env)
 for changed in paths:
  blob=git('hash-object','-w',str(source/changed))
  git('update-index','--cacheinfo','100644',blob,changed,env=env)
 tree=git('write-tree',env=env)
 commit=git('commit-tree',tree,'-p',BASE,data='Isolated guided recovery '+kind+' proof\n')
 tag='voptrack-guided-proof-'+kind+'-'+commit[:10]
 with (directory/'image-build.log').open('w') as log:run(['make','image','BUILD_IMAGE='+tag],cwd=source,stdout=log,stderr=subprocess.STDOUT)
 image=subprocess.check_output(['docker','image','inspect',tag,'--format','{{.Id}}'],text=True).strip()
 version=subprocess.check_output(['docker','run','--rm','--network','none','--entrypoint','tesserad',image,'-version'],text=True).strip()
 if version!=PIN:raise RuntimeError('fault image version mismatch')
 result={'kind':kind,'commit':commit,'tree':tree,'image':image,'native_compiles':True,'actual_binary_version':version}
 (directory/'identity.json').write_text(json.dumps(result,indent=2)+'\n')
 print(kind,commit,image,flush=True);return result
def gate(image,name,mode,expect=None,restart=None,script=None,inputs=None):
 directory=ROOT/name
 env=dict(ENV,VOPTRACK_GUIDED_MODE=mode,VOPTRACK_GUIDED_PORT=os.environ.get('VOPTRACK_GUIDED_FAULT_PORT','31943'),VOPTRACK_GUIDED_RESTORE_PORT=os.environ.get('VOPTRACK_GUIDED_FAULT_RESTORE_PORT','31944'))
 if restart:env['VOPTRACK_GUIDED_INTACT_IMAGE']=restart
 with (ROOT/(name+'.log')).open('w') as log:
  p=subprocess.run([sys.executable,str(script or REPO/'release/guided-recovery/gate.py'),image,str(inputs or INPUTS),str(directory)],env=env,stdout=log,stderr=subprocess.STDOUT)
 text=(ROOT/(name+'.log')).read_text()
 if expect:
  if p.returncode==0 or expect not in text:raise RuntimeError('planted defect did not fail intended assertion; private log retained')
 else:
  if p.returncode!=0:raise RuntimeError('intact replay failed; private log retained')
 print(name+': '+('intended failure observed' if expect else 'PASS'),flush=True)
 return {'name':name,'exit':p.returncode,'intended_assertion':expect,'restart_image':restart}
def fixture_preservation():
 # The defect is in the Python verifier, not in image inputs. Compile it and
 # exercise the already verified immutable image; never mutate real retained sets.
 directory=ROOT/'fixture-defect';directory.mkdir(mode=0o700)
 source=directory/'source';source.mkdir()
 archive=subprocess.Popen(['git','-C',str(REPO),'archive',BASE],stdout=subprocess.PIPE)
 subprocess.run(['tar','-xf','-','-C',str(source)],stdin=archive.stdout,check=True);archive.stdout.close()
 if archive.wait()!=0:raise RuntimeError('fixture candidate archive failed')
 path='release/guided-recovery/gate.py';script=source/path;text=script.read_text()
 anchor="str(verification),IMAGE,PROJECT+'-verify'"
 if text.count(anchor)!=1:raise RuntimeError('fixture preservation anchor missing')
 script.write_text(text.replace(anchor,"str(a.retained),IMAGE,PROJECT+'-verify'",1))
 run([sys.executable,'-m','py_compile',str(script)])
 env=dict(ENV,GIT_INDEX_FILE=str(directory/'private.index'))
 git('read-tree',BASE,env=env);blob=git('hash-object','-w',str(script))
 git('update-index','--cacheinfo','100644',blob,path,env=env);tree=git('write-tree',env=env)
 commit=git('commit-tree',tree,'-p',BASE,data='Isolated retained-fixture evidence overwrite proof\n')
 identity={'kind':'retained-source-defect','commit':commit,'tree':tree,'image':INTACT,'python_compiles':True,'runtime_image_inputs_changed':False}
 (directory/'identity.json').write_text(json.dumps(identity,indent=2)+'\n')
 inputs=ROOT/'fixture-proof-inputs';shutil.copytree(INPUTS,inputs)
 results=[
  gate(INTACT,'fixture-negative','retained-preservation',expect='retained source fixture was modified',script=script,inputs=inputs),
  gate(INTACT,'fixture-intact-replay','retained-preservation',inputs=inputs),
 ]
 (ROOT/'fixture-qualification.json').write_text(json.dumps({'base':BASE,'candidate':identity,'results':results},indent=2)+'\n')
 return results
if len(sys.argv)>5:
 if sys.argv[5]!='--fixture-preservation-only':raise RuntimeError('unknown qualification mode')
 fixture_preservation();sys.exit(0)
stops=variant('durable-stops')
csrf=variant('csrf-defect')
restart=variant('restart-defect')
short=variant('short-session')
expiry=variant('expiry-defect')
empty=variant('empty-evidence-defect')
results=[
 gate(empty['image'],'empty-evidence-negative','empty-evidence',expect='empty verified evidence was refused'),
 gate(INTACT,'empty-evidence-intact-replay','empty-evidence'),
 gate(expiry['image'],'body-expiry-negative','session-window',expect='session expired during body read was admitted'),
 gate(short['image'],'body-expiry-intact-replay','session-window'),
 gate(csrf['image'],'csrf-negative','guards',expect='CSRF admitted'),
 gate(INTACT,'csrf-intact-replay','guards'),
 gate(stops['image'],'mutation-failures','failures',restart=INTACT),
 gate(stops['image'],'interruption-intact-replay','interruptions',restart=INTACT),
 gate(stops['image'],'restart-negative','interruptions',restart=restart['image'],expect='interrupted mutation became retryable'),
 gate(stops['image'],'restart-intact-replay','interruptions',restart=INTACT),
]
results.extend(fixture_preservation())
(ROOT/'qualification.json').write_text(json.dumps({'base':BASE,'intact_image':INTACT,'candidates':[stops,csrf,restart,short,expiry,empty],'results':results,'active_checkout_modified':False},indent=2)+'\n')
print('Compiling planted defects and intact actual-image replays: PASS',flush=True)
