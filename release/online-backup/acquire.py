#!/usr/bin/env python3
"""Disposable release-gate acquisition through supported HTTPS routes only.
No credential, response body or resolved URL is printed, including on failure.
"""
import sys as _ci_sys
from pathlib import Path as _CiPath
_ci_sys.path.insert(0, str(_CiPath(__file__).resolve().parents[2] / "ci"))
from timing import RetryBudget
import argparse,base64,hashlib,http.cookiejar,json,os,re,secrets,ssl,time,urllib.error,urllib.parse,urllib.request

def run():
 p=argparse.ArgumentParser();p.add_argument('base');p.add_argument('ca');p.add_argument('password');p.add_argument('output');p.add_argument('--account',default='owner');p.add_argument('--oldest',action='store_true');p.add_argument('--count',action='store_true');p.add_argument('--evidence',action='store_true');a=p.parse_args()
 ctx=ssl.create_default_context(cafile=a.ca)
 class NoRedirect(urllib.request.HTTPRedirectHandler):
  def redirect_request(self,*args,**kwargs):return None
 browser=urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx),NoRedirect,urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
 def call(method,path,body=None,form=None,token='',headers=None):
  h=dict(headers or {});data=None
  if body is not None:data=json.dumps(body).encode();h['Content-Type']='application/json'
  if form is not None:data=urllib.parse.urlencode(form).encode();h['Content-Type']='application/x-www-form-urlencoded'
  if token:h['Authorization']='Bearer '+token
  req=urllib.request.Request(a.base+path,data=data,method=method,headers=h)
  try:
   with browser.open(req,timeout=115) as r:return r.status,r.read(),r.headers
  except urllib.error.HTTPError as e:return e.code,e.read(),e.headers
 def expect(status,result):
  if result[0]!=status:raise RuntimeError('request refused with HTTP '+str(result[0]))
  return json.loads(result[1])
 def field(raw,name):return re.search(r'name="'+name+r'"\s+value="([^"]*)"',raw.decode()).group(1)
 redirect='com.voptrack.backup-gate:/oauth/callback'
 client=expect(201,call('POST','/oauth/register',{'client_name':'Disposable backup gate','redirect_uris':[redirect],'token_endpoint_auth_method':'none','grant_types':['authorization_code'],'response_types':['code']}))['client_id']
 verifier=secrets.token_urlsafe(48);challenge=base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
 _,page,_=call('GET','/oauth/authorize?'+urllib.parse.urlencode({'response_type':'code','client_id':client,'redirect_uri':redirect,'state':'gate','code_challenge':challenge,'code_challenge_method':'S256','prompt':'consent'}))
 flow=field(page,'flow');status,approval,retry_headers=call('POST','/oauth/authorize',form={'flow':flow,'csrf':field(page,'csrf'),'username':a.account,'password':open(a.password).read().strip(),'decision':'continue'})
 budget=RetryBudget()
 for attempt in range(2):
  if status!=429 or not budget.wait(retry_headers,attempt):break
  status,approval,retry_headers=call('POST','/oauth/authorize',form={'flow':flow,'csrf':field(page,'csrf'),'username':a.account,'password':open(a.password).read().strip(),'decision':'continue'})
 if status!=200:raise RuntimeError('sign-in refused')
 consent={'flow':flow,'csrf':field(approval,'csrf'),'decision':'allow','administer':'yes'}
 options=re.findall(r'<option\s+value="([^<"]+)"',approval.decode())
 if options:consent['dataset']=options[0]
 status,_,headers=call('POST','/oauth/authorize',form=consent)
 if status!=303:raise RuntimeError('consent refused')
 code=urllib.parse.parse_qs(urllib.parse.urlparse(headers['Location']).query)['code'][0]
 token=expect(200,call('POST','/oauth/token',form={'grant_type':'authorization_code','client_id':client,'code':code,'redirect_uri':redirect,'code_verifier':verifier}))['access_token']
 results=[];cursor=''
 for _ in range(24):
  page=expect(200,call('GET','/admin/api/v1/backup-operations?limit=100'+('&cursor='+urllib.parse.quote(cursor,safe='') if cursor else ''),token=token));results.extend(page['results']);cursor=page.get('next_cursor','')
  if not cursor:break
 else:raise RuntimeError('catalog bound')
 backups=[op for op in results if op['kind']=='backup']
 if a.count:print(len(backups));return
 if a.evidence:
  operation=str(int(time.time()))+'-'+secrets.token_hex(16)
  expect(202,call('PUT','/admin/api/v1/backup-operations/'+operation,{'kind':'evidence'},token=token))
  for _ in range(1000):
   op=expect(200,call('GET','/admin/api/v1/backup-operations/'+operation,token=token))
   if op['state']!='running':break
   time.sleep(.02)
 else:
  op=sorted(backups,key=lambda x:x['accepted_at'],reverse=not a.oldest)[0]
 if op['state']!='complete':raise RuntimeError('incomplete operation')
 os.makedirs(a.output,mode=0o700,exist_ok=False)
 lease=secrets.token_hex(16);path='/admin/api/v1/backup-operations/'+op['id']
 expect(200,call('PUT',path+'/leases/'+lease,token=token))
 try:
  for artifact in op['artifacts']:
   if not re.fullmatch('[0-9a-f]{32}',artifact['id']) or not 0<=artifact['size']<=(257<<20):raise RuntimeError('invalid descriptor')
   status,raw,headers=call('GET',path+'/artifacts/'+artifact['id'],token=token,headers={'Tessera-Backup-Lease':lease})
   if status!=200 or len(raw)!=artifact['size'] or hashlib.sha256(raw).hexdigest()!=artifact['sha256']:raise RuntimeError('artifact mismatch')
   fd=os.open(a.output+'/'+artifact['id'],os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
   with os.fdopen(fd,'wb') as f:f.write(raw)
   if artifact['kind'] in ('archive','evidence','metadata'):
    open(a.output+'/'+artifact['kind']+'-id','w').write(artifact['id'])
  open(a.output+'/deployment','w').write(op['pair']['deployment'])
 finally:call('DELETE',path+'/leases/'+lease,token=token)
if __name__=='__main__':
 try:run()
 except RuntimeError as e:raise SystemExit('supported backup acquisition failed: '+str(e))
 except Exception as e:raise SystemExit('supported backup acquisition failed: '+type(e).__name__+' (private details suppressed)')
