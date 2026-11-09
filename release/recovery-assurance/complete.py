"""Local completion matrix. No external provider, deployment or target cleanup."""
import json,os,pathlib,subprocess,sys
from report import build_report
from capacity import verify_capacity_binding
from provenance import read_json,require

import sys as _sys
from pathlib import Path as _Path
_sys.path.insert(0, str(_Path(__file__).resolve().parents[2] / 'ci'))
from tessera_pin import expected_pin
PIN = expected_pin()
REPO=pathlib.Path(__file__).resolve().parents[2]
MODES={'private':'private','app-only':'off','public':'public'}
def aggregate(root,image,paths=None):
 root=pathlib.Path(root)
 if paths is None:paths={label:label for label in [*MODES,'capacity']}
 if set(paths)!={*MODES,'capacity'} or any(not isinstance(v,str) or pathlib.PurePosixPath(v).parts!=(v,) or v in ('.','..') or '\\' in v for v in paths.values()):raise ValueError('invalid evidence run mapping')
 products={label:build_report(root/paths[label],image,mode) for label,mode in MODES.items()}
 capacity=read_json(root/paths['capacity']/'capacity-result.json')
 required=('passed','synthetic_transport_only','queue_exhaustion_refused','outage_preserved_pending','restart_drained')
 if not all(capacity.get(k) is True for k in required) or capacity.get('native_authenticity_qualified') is not False or capacity.get('remote_deletion') is not False:
  raise ValueError('capacity evidence incomplete or overstated')
 receipt=read_json(root/paths['capacity']/'result.json')
 if receipt.get('image')!=image or receipt.get('mode')!='capacity' or receipt.get('passed') is not True or receipt.get('aws_qualified') is not False or receipt.get('separate_host_qualified') is not False:
  raise ValueError('capacity image/scope mismatch')
 expected={'large_bundles':3,'archive_bytes_each':256<<20,'evidence_bytes_each':16<<20,'queue_budget':550<<20,'worker_memory_limit_bytes':384<<20,'worker_cpus':2}
 require(all(type(capacity.get(k)) is int and capacity[k]==v for k,v in expected.items()),'capacity qualification limits missing or mismatched')
 require(receipt.get('local_synthetic_only') is True and receipt.get('physical_independence_attested') is False,'capacity receipt overstates scope')
 binding=verify_capacity_binding(root/paths['capacity'])
 require(binding['large_bundles']==3 and binding['large_bytes']>816<<20,'capacity near-limit byte proof missing')
 return {'version':1,'image':image,'pin':PIN,'evidence_runs':paths,'home_modes':products,'capacity':capacity,'capacity_binding':binding,'aws_iam':'deferred-pre-release-unverified','independent_host':'pending','physical_custody':'unverified','deployment_performed':False}
def main():
 image=sys.argv[1];root=pathlib.Path(sys.argv[2]).resolve()
 if not image.startswith('sha256:') or len(image)!=71:raise ValueError('immutable image required')
 root.mkdir(mode=0o700);root.chmod(0o700)
 env={k:v for k,v in os.environ.items() if not k.startswith(('VOPTRACK_','AWS_','COMPOSE_'))}
 env['PATH']='/usr/local/go/bin:'+env['PATH']
 def run(label,args,extra=None):
  with (root/(label+'.log')).open('xb') as log:
   os.fchmod(log.fileno(),0o600)
   p=subprocess.run(args,cwd=REPO,env=dict(env,**(extra or {})),stdout=log,stderr=subprocess.STDOUT)
  if p.returncode:raise RuntimeError(label+' refused; private diagnostics and partial targets retained')
  print(label+': PASS',flush=True)
 inventory=[sys.executable,str(REPO/'release/recovery-set/inventory.py')]
 subprocess.run(inventory+[str(root/'before.json')],check=True)
 try:
  for label,mode in MODES.items():run(label,[sys.executable,str(REPO/'release/recovery-assurance/gate.py'),image,str(root/label)],{'VOPTRACK_ASSURANCE_HOME_MODE':mode})
  run('capacity',[sys.executable,str(REPO/'release/off-machine/gate.py'),image,str(root/'public/retained'),str(root/'capacity')],{'VOPTRACK_OFF_MACHINE_MODE':'capacity'})
  with (root/'result.json').open('x') as f:os.fchmod(f.fileno(),0o600);json.dump(aggregate(root,image),f,indent=2);f.write('\n')
 finally:subprocess.run(inventory+[str(root/'after.json')],check=True)
if __name__=='__main__':main()
