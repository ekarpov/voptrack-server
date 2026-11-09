"""Independent retained-byte binding for modeled local capacity evidence.
No native authenticity, provider authority, freshness or custody is inferred.
"""
import hashlib,json,os,pathlib,re
from provenance import MAX_BUNDLE,bundle,read_json,regular,require

def digest(raw):return hashlib.sha256(raw).hexdigest()
def verify_capacity_binding(root):
 root=pathlib.Path(root);case=root/'capacity';config=read_json(case/'config.json')
 require(config.get('Version')==1 and config.get('LocalFixture') is True and config.get('IndependentDestination') is False and config.get('Budget')==550<<20,'capacity destination/scope mismatch')
 deployment=config['Deployment'];require(isinstance(deployment,str) and re.fullmatch('[a-f0-9]{32}',deployment),'invalid capacity deployment')
 namespace=config['Prefix']+digest(deployment.encode())+'/'
 binding=digest(json.dumps([config[k] for k in ('Region','Bucket','Prefix','Deployment','Endpoint','Outbox','ExpectedBucketOwner')],separators=(',',':'),ensure_ascii=False).encode())
 require(read_json(case/'state/binding.json')==binding,'capacity private destination binding mismatch')
 records=list((case/'queue').glob('*/obligation.json'));require(len(records)==7,'capacity obligations incomplete')
 listing=read_json(case/'capacity-read-only-list.log')
 require(isinstance(listing,list) and len(listing)==7 and len({d['ID'] for d in listing})==7,'capacity read-only discovery incomplete')
 readers={d['ID']:d for d in listing};ids=set();sizes=[];kinds=[]
 def remote(key):return root/'objects'/digest(key.encode())
 for path in records:
  value=read_json(path);manifest=value['Manifest'];op=manifest['operation'];identifier=op['id']
  require(isinstance(identifier,str) and re.fullmatch(r'[0-9]+-[a-f0-9]{32}',identifier) and identifier==path.parent.name,'invalid capacity operation identity')
  opaque=digest(identifier.encode());ids.add(opaque);kinds.append(op['kind'])
  require(value.get('Version')==1 and value.get('LocalPublished') is True and op.get('pair',{}).get('deployment')==deployment,'capacity outbox authority mismatch')
  require(not os.path.lexists(path.parent/'backup.tar'),'capacity acknowledged payload still pending')
  descriptor=read_json(remote(namespace+'commits/'+opaque+'.json'))
  require(descriptor.get('Version')==1 and descriptor.get('ID')==opaque and descriptor.get('Deployment')==digest(deployment.encode()) and descriptor.get('Kind')==op['kind'] and descriptor.get('Published')==value['Queued'],'capacity committed operation mismatch')
  payload=descriptor['Payload'];sha=value['SHA256'];size=value['Size']
  require(isinstance(sha,str) and re.fullmatch('[a-f0-9]{64}',sha) and type(size) is int and 0<size<=MAX_BUNDLE,'invalid capacity payload bounds')
  require(payload=={'Key':namespace+'payloads/'+sha,'SHA256':sha,'Size':size},'capacity committed payload mismatch')
  require(bundle(remote(payload['Key']))==(sha,size,manifest),'capacity payload/outbox manifest mismatch')
  job=read_json(case/'state'/(opaque+'.json'))
  require(job.get('Version')==1 and job.get('Phase')=='verified' and job.get('Descriptor')==descriptor and bool(job.get('Verified')),'capacity acknowledgement/journal mismatch')
  reader=readers.get(opaque,{})
  require(reader.get('Kind')==descriptor['Kind'] and reader.get('Published')==descriptor['Published'] and reader.get('SettingsAvailable') is True,'capacity reader discovery mismatch')
  settings=descriptor.get('Settings');require(isinstance(settings,dict),'capacity settings companion missing')
  require(settings.get('Key')==namespace+'settings/'+str(settings.get('SHA256')) and type(settings.get('Size')) is int and 0<settings['Size']<=4096,'capacity settings member mismatch')
  with regular(remote(settings['Key'])) as f:raw=f.read(4097)
  require(len(raw)==settings['Size'] and digest(raw)==settings['SHA256'] and json.loads(raw).get('deployment')==deployment,'capacity settings bytes mismatch')
  sizes.append(size)
 require(ids==set(readers) and kinds.count('backup')==5 and kinds.count('evidence')==2,'capacity committed/discovered operations differ')
 return {'bound_operations':len(ids),'large_bundles':sum(size>272<<20 for size in sizes),'large_bytes':sum(size for size in sizes if size>272<<20),'native_authenticity_qualified':False}
