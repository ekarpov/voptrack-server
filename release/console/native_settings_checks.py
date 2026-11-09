"""Actual-image alpha.36 live policy and observation checks; disposable project only."""
import datetime,json,re,secrets,time,urllib.request,urllib.error

def verify(g,dc,admin,restart=True,protection=True):
 api,request,owner,sara,csrf,field,fail,say=(g[n] for n in ('api','request','owner','sara','csrf_of','field','fail','say'))
 def observe():
  st,body,_=api(admin,'GET','/admin/api/v1/backup-scheduler')
  if st!=200:fail('native scheduler observation unavailable')
  o=json.loads(body)
  if not o['configured'] or not o['available'] or o['history_scope']!='process_instance':fail('native scheduler shape')
  return o
 def settled():
  for _ in range(300):
   o=observe()
   if not o['pending_request_id'] and not o['active_scheduled_operation']:return o
   time.sleep(.03)
  fail('native scheduler did not settle')
 o=settled();old=o['settings']['revision'];instance=o['instance_id']
 for route in ('preview','confirm','reconcile'):
  if request(owner,'POST','/backups/live/'+route,form={'csrf':'invalid'})[0]!=403:fail('live settings CSRF guard failed')
  if request(sara,'POST','/backups/live/'+route,form={'csrf':'invalid'})[0]!=403:fail('member reached live settings')
 st,page,_=request(owner,'GET','/backups')
 form={'csrf':csrf(page),'effective_revision':field(page,'effective_revision'),'journal':field(page,'journal'),'every':'12','keep':'3'}
 st,preview,_=request(owner,'POST','/backups/live/preview',form=form)
 if st!=200 or 'action="/backups/live/confirm"' not in preview:fail('live review failed')
 confirm={'csrf':csrf(preview),'confirmation':field(preview,'confirmation')}
 before=dc('ps','-q','tesserad','console')
 st,_,_=request(owner,'POST','/backups/live/confirm',form=confirm)
 if st!=303:fail('live confirmation failed')
 if request(owner,'POST','/backups/live/confirm',form=confirm)[0]!=409:fail('live confirmation replay accepted')
 for _ in range(200):
  o=settled()
  if o['settings']['every_hours']==12 and o['settings']['keep']==3:break
  time.sleep(.03)
 else:fail('live values not effective')
 if o['settings']['revision']==old or dc('ps','-q','tesserad','console')!=before:fail('live apply restarted service or kept old revision')
 if request(owner,'POST','/backups/live/preview',form=form)[0]!=409:fail('stale live form accepted')
 def command(payload,id=None):
  id=id or o['settings_scope']+'.'+str(int(time.time()))+'-'+secrets.token_hex(16)
  path='/admin/api/v1/backup-settings-operations/'+id
  st,body,_=api(admin,'PUT',path,payload)
  return id,path,st,json.loads(body)
 # A retained stale failure must be available on GET and identical replay.
 payload={'expected_revision':old,'every_hours':12,'keep':3}
 id,path,st,rejected=command(payload)
 if st!=409 or rejected['error']['details']['reason']!='stale_revision' or rejected['error']['details']['operation']['state']!='failed':fail('durable stale rejection missing')
 if json.loads(api(admin,'GET',path)[1])['state']!='failed':fail('retained rejected lookup missing')
 if api(admin,'PUT',path,payload)[0]!=409:fail('retained rejected replay changed')
 if api(admin,'PUT',path,dict(payload,keep=2))[0]!=409:fail('request reuse accepted')
 # Completion-based cadence: retention-only application must not reset due time.
 due=o['next_attempt_at'];o=settled()
 payload={'expected_revision':o['settings']['revision'],'every_hours':12,'keep':2}
 id,path,st,body=command(payload)
 if st not in (200,202):fail('retention-only application refused')
 for _ in range(200):
  status,raw,_=api(admin,'GET',path);op=json.loads(raw)
  if status==200 and op['state']=='effective':break
  time.sleep(.03)
 else:fail('retention-only application not effective')
 o=settled()
 if o['next_attempt_at']!=due:fail('retention-only update reset cadence')
 if api(admin,'PUT',path,payload)[0]!=200:fail('effective same-ID replay was not 200')
 # Collector credential cannot use any new administrative route.
 credential=json.loads(dc('run','--rm','--no-deps','-T','--entrypoint','cat','collector-admin','/var/lib/voptrack-collector/credential.json'))
 for method,p,body in [('GET','/admin/api/v1/backup-scheduler',None),('GET',path,None),('PUT',path,payload)]:
  if api(credential['token'],method,p,body)[0] not in (401,403):fail('collector reached native settings authority')
 if protection:
  # Protect every retained completed native set, then lower the effective target.
  o=settled();payload={'expected_revision':o['settings']['revision'],'every_hours':12,'keep':3}
  _,path,st,_=command(payload)
  if st not in (200,202):fail('producer expansion refused')
  while json.loads(api(admin,'GET',path)[1])['state']=='pending':time.sleep(.03)
  # Ensure at least two native archives without touching a legacy file list.
  for _ in range(2):
   backup=str(int(time.time()))+'-'+secrets.token_hex(16)
   st,_,_=api(admin,'PUT','/admin/api/v1/backup-operations/'+backup,{'kind':'backup'})
   if st!=202:fail('protected retention fixture admission failed')
   for _ in range(300):
    op=json.loads(api(admin,'GET','/admin/api/v1/backup-operations/'+backup)[1])
    if op['state']!='running':break
    time.sleep(.03)
   if op['state']!='complete':fail('protected retention fixture incomplete')
  inventory=json.loads(api(admin,'GET','/admin/api/v1/backup-operations')[1])['results']
  leases=[]
  for op in inventory:
   if op['state']=='complete':
    lease=secrets.token_hex(16);p='/admin/api/v1/backup-operations/'+op['id']+'/leases/'+lease
    if api(admin,'PUT',p)[0]!=200:fail('native retention lease refused')
    leases.append(p)
  if len(leases)<2:fail('native retained inventory fixture too small')
  o=settled();payload={'expected_revision':o['settings']['revision'],'every_hours':12,'keep':1}
  _,path,st,_=command(payload)
  if st not in (200,202):fail('protected retention reduction refused')
  for _ in range(300):
   o=settled()
   if o['settings']['keep']==1 and o['retention']['state']=='blocked':break
   time.sleep(.03)
  else:fail('protected producer retention did not defer')
  if o['retention']['retained']<2 or o['retention']['target']!=1:fail('effective policy confused with convergence')
  backup=str(int(time.time()))+'-'+secrets.token_hex(16)
  if api(admin,'PUT','/admin/api/v1/backup-operations/'+backup,{'kind':'backup'})[0]!=413:fail('retention-pending capacity not refused')
  # Test-only upstream setup exercises an externally disabled observation and
  # immediate enabling refusal without waiting twelve hours. Voptrack's own
  # form must continue refusing zero; no disabling control is added.
  o=settled();payload={'expected_revision':o['settings']['revision'],'every_hours':0,'keep':1}
  _,path,st,_=command(payload)
  if st not in (200,202):fail('disabled observation fixture refused')
  for _ in range(300):
   o=settled()
   if not o['enabled'] and o['next_attempt_at'] is None and o['next_reason']=='disabled':break
   time.sleep(.03)
  else:fail('disabled next-attempt observation invalid')
  st,page,_=request(owner,'GET','/backups')
  if request(owner,'POST','/backups/live/preview',form={'csrf':csrf(page),'effective_revision':field(page,'effective_revision'),'journal':field(page,'journal'),'every':'0','keep':'1'})[0]!=422:fail('Voptrack expanded its positive interval bounds')
  payload={'expected_revision':o['settings']['revision'],'every_hours':12,'keep':1}
  _,path,st,_=command(payload)
  if st not in (200,202):fail('enabling refusal fixture not accepted')
  for _ in range(300):
   o=settled()
   if o['last_scheduled_attempt'] and o['last_scheduled_attempt']['outcome']=='refused_capacity':break
   time.sleep(.03)
  else:fail('scheduled protected-capacity refusal not observed')
  if o['next_attempt_at'] is None or o['next_reason']!='scheduled':fail('refused scheduler did not retain bounded next intent')
  for p in leases:
   if api(admin,'DELETE',p)[0]!=200:fail('native retention lease release failed')
  for _ in range(300):
   o=settled()
   if o['retention']['state']=='converged' and o['retention']['retained']==1:break
   time.sleep(.05)
  else:fail('native retention did not converge after lease release')
  retained=json.loads(api(admin,'GET','/admin/api/v1/backup-operations')[1])['results']
  if not any(op['kind']=='backup' and op['state']=='complete' for op in retained):fail('producer lost final recovery pair')
  evidence=str(int(time.time()))+'-'+secrets.token_hex(16)
  status,body,_=api(admin,'PUT','/admin/api/v1/backup-operations/'+evidence,{'kind':'evidence'})
  if status!=413 or json.loads(body)['error']['details']['reason']!='last_recovery_pair':fail('evidence displaced final archive')
 # Record authoritative export before a restart with unchanged stale startup defaults.
 o=settled();export=o['settings_export'];revision=o['settings']['revision'];expected=o['settings'].copy()
 if restart:
  dc('restart','tesserad');dc('up','-d','--no-deps','--wait','tesserad')
  for _ in range(100):
   try:o=settled();break
   except urllib.error.URLError:time.sleep(.1)
  if o['instance_id']==instance or o['settings']!=expected:fail('restart lost effective policy or reused instance')
  if json.loads(api(admin,'GET',path)[1])['revision']!=revision:fail('settings result lost across restart')
 st,page,_=request(owner,'GET','/backups')
 if 'data-effective-revision="'+revision+'"' not in page:fail('Console omitted effective readback')
 g['_native_export']=export
 say('native scheduler/live settings','CSRF/member/collector refusal, stale/reused IDs, retained failure, replay, cadence'+('; protected producer pruning and final pair' if protection else '')+(' and restart precedence' if restart else ''))
