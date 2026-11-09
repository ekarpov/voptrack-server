"""Local provenance checks bind bytes; native preflight still owns authenticity."""
import hashlib,json,os,pathlib,re,stat,tarfile

MAX_BUNDLE=275*1024*1024

def require(value,label):
 if not value:raise ValueError(label)

def regular(path):
 path=pathlib.Path(path)
 # Evidence is private host input, but indirect paths cannot establish provenance.
 for parent in (path,*path.parents):
  require(not parent.is_symlink(),'indirect provenance path')
 fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
 f=os.fdopen(fd,'rb')
 if not stat.S_ISREG(os.fstat(f.fileno()).st_mode):
  f.close();raise ValueError('nonregular provenance file')
 return f

def read_json(path):
 with regular(path) as f:
  raw=f.read((2<<20)+1)
 require(len(raw)<=2<<20,'oversized provenance document')
 return json.loads(raw)

def bundle(path):
 with regular(path) as f:
  before=os.fstat(f.fileno());require(0<before.st_size<=MAX_BUNDLE,'invalid provenance bundle size')
  h=hashlib.sha256()
  while True:
   block=f.read(1024*1024)
   if not block:break
   h.update(block)
  f.seek(0)
  with tarfile.open(fileobj=f,mode='r:') as archive:
   manifests=[];count=0
   for member in archive:
    count+=1;require(count<=8,'invalid provenance bundle members')
    if member.name=='manifest.json':
     require(member.isfile() and 0<member.size<=1<<20,'invalid provenance manifest')
     manifests.append(json.load(archive.extractfile(member)))
   require(len(manifests)==1,'missing or duplicate provenance manifest')
  after=os.fstat(f.fileno())
  require((before.st_size,before.st_mtime_ns,before.st_ctime_ns)==(after.st_size,after.st_mtime_ns,after.st_ctime_ns),'provenance bundle changed')
 return h.hexdigest(),before.st_size,manifests[0]

def selected(directory,role):
 selection=read_json(directory/'selection.json');entry=selection[role]
 require(isinstance(entry['file'],str) and pathlib.PurePosixPath(entry['file']).parts==(entry['file'],) and entry['file'] not in ('.','..') and '\\' not in entry['file'],'unsafe provenance selection')
 sha,size,manifest=bundle(directory/entry['file'])
 require(entry['sha256']==sha,'selected provenance bytes changed')
 return sha,size,manifest

def obligation(path,identifier,role,sha,size,manifest):
 value=read_json(path)
 require(value.get('Version')==1 and value.get('LocalPublished') is True and value.get('SHA256')==sha and value.get('Size')==size and value.get('Manifest')==manifest,'native outbox obligation mismatch')
 require(manifest['operation']['id']==identifier and manifest['operation']['kind']==role,'native outbox operation mismatch')

def verify_provenance(root,variant):
 root=pathlib.Path(root);source=root/'retained'/variant;case=root/'off-machine'/('success-'+variant)
 proof=read_json(case/'native-publication.json')
 require(proof.get('real_collector_outbox') is True and proof.get('selected_native_operations')==2 and proof.get('source_services_unavailable') is True,'native publication proof missing')
 chosen=read_json(source/'publication/selection.json')
 require(set(chosen)=={'backup','evidence'} and len(set(chosen.values()))==2,'native publication selection mismatch')
 require({p.parent.name for p in (case/'queue').glob('*/obligation.json')}==set(chosen.values()),'copied outbox selection mismatch')
 for role in ('backup','evidence'):
  identifier=chosen[role];require(isinstance(identifier,str) and re.fullmatch(r'[0-9]+-[a-f0-9]{32}',identifier),'unsafe native operation')
  sha,size,manifest=selected(source/'inputs',role)
  for directory in (source/'publication/.replication-outbox'/identifier,case/'queue'/identifier):
   obligation(directory/'obligation.json',identifier,role,sha,size,manifest)
  for path in (source/'publication/.replication-outbox'/identifier/'backup.tar',source/'publication'/identifier/'backup.tar',case/'published-preserved'/identifier/'backup.tar'):
   require(bundle(path)==(sha,size,manifest),'native outbox payload differs from approved selection')
  for directory in (root/'off-machine/fetched'/variant/'inputs',root/'off-machine/guided'/('success-'+variant)/'inputs'):
   require(selected(directory,role)==(sha,size,manifest),'fetched recovery bytes differ from native outbox')
 return True
