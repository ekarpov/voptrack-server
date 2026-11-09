#!/usr/bin/env python3
"""Fresh owned-website defaults and preserved legacy choices, actual image."""
import argparse,base64,hashlib,http.cookiejar,json,os,re,secrets,socket,ssl,subprocess,tempfile,urllib.error,urllib.parse,urllib.request
from pathlib import Path
from website_signin import Page

ROOT=Path(__file__).resolve().parents[2]
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs):return None
def require(ok,message):
 if not ok:raise AssertionError(message)
def free_ports():
 sockets=[]
 try:
  for _ in range(3):
   s=socket.socket();s.bind(('127.0.0.1',0));sockets.append(s)
  return [s.getsockname()[1] for s in sockets]
 finally:
  for s in sockets:s.close()
def network():
 import ipaddress
 ids=subprocess.check_output(['docker','network','ls','-q'],text=True).split()
 data=json.loads(subprocess.check_output(['docker','network','inspect',*ids],text=True)) if ids else []
 used=[ipaddress.ip_network(c['Subnet']) for n in data for c in n.get('IPAM',{}).get('Config',[]) or [] if c.get('Subnet')]
 return next('10.123.'+str(i) for i in range(100,240) if all(not ipaddress.ip_network('10.123.'+str(i)+'.0/24').overlaps(x) for x in used if x.version==4))
def qualify(variant,private=False,legacy=False,config_only=False):
 with tempfile.TemporaryDirectory(prefix='voptrack-web-defaults-') as tmp:
  work=Path(tmp);port,http_port,cport=free_ports();net=network()
  origin='https://localhost:'+str(port)
  console='https://localhost:'+str(cport) if private else origin
  project='voptrack-web-defaults-'+secrets.token_hex(5)
  env=dict(os.environ,VOPTRACK_IMAGE=os.environ['VOPTRACK_IMAGE'],VOPTRACK_VARIANT=variant,VOPTRACK_DOMAIN='localhost',VOPTRACK_STORAGE_ENCRYPTED='yes',VOPTRACK_CONSOLE='on',VOPTRACK_CONSOLE_PRIVATE='yes' if private else 'no',VOPTRACK_CONSOLE_URL='',VOPTRACK_PUBLIC_URL=origin,VOPTRACK_HTTPS_PORT=str(port),VOPTRACK_HTTP_PORT=str(http_port),VOPTRACK_CONSOLE_PORT=str(cport),VOPTRACK_PUBLISH_PREFIX='127.0.0.1:',VOPTRACK_PROXY_SUBNET=net+'.0/24',VOPTRACK_PROXY_ADDRESS=net+'.2',VOPTRACK_PROXY_POOL=net+'.128/25',VOPTRACK_WEBSITE_SIGN_IN='off' if legacy else 'auto',VOPTRACK_IDENTITY_BRAND='off' if legacy else 'auto',VOPTRACK_WEBSITE_CLIENT_ID='',VOPTRACK_WEBSITE_DATA_GRANTS='',VOPTRACK_WEBSITE_ADMINISTER='')
  def dc(*args):
   p=subprocess.run(['docker','compose','-p',project,'-f',str(ROOT/'release/compose.yaml'),*args],env=env,stdin=subprocess.DEVNULL,capture_output=True,timeout=180)
   require(p.returncode==0,'disposable website deployment operation failed at '+args[0])
   return p.stdout
  def config():
   return json.loads(dc('run','--rm','-T','--no-deps','--entrypoint','cat','init','/var/lib/voptrack-config/server.json'))
  try:
   dc('up','-d','--wait')
   if legacy:
    require('first_party_consoles' not in config() and 'identity_brand' not in config(),'explicit legacy opt-out ignored')
    dc('stop','console','tesserad')
    env.update(VOPTRACK_WEBSITE_SIGN_IN='auto',VOPTRACK_IDENTITY_BRAND='auto')
    dc('run','--rm','-T','--no-deps','init')
    dc('up','-d','--wait')
    require('first_party_consoles' not in config() and 'identity_brand' not in config(),'ordinary init expanded a legacy installation')
   else:
    cfg=config()
    expected=[dict(client_id='voptrack-website',name='Voptrack',origin=console,redirect_uri=console+'/auth/callback',data_grants=['read','write','insert'],administer=True)]
    require(cfg.get('first_party_consoles')==expected,'fresh website default mismatch')
    brand=cfg.get('identity_brand',{})
    name={'household':'Voptrack Home','research':'Voptrack Research','standard':'Voptrack'}[variant]
    require(brand.get('name')==name and brand.get('logo_file')=='/usr/local/share/voptrack/identity-logo.png' and brand.get('light')==brand.get('dark'),'fresh product branding mismatch')
    if config_only:
     print(variant+': fresh generated website/brand defaults passed',flush=True);return
   ca=work/'ca.crt';ca.write_bytes(dc('exec','-T','caddy','cat','/var/lib/voptrack-public/ca.crt'))
   ctx=ssl.create_default_context(cafile=str(ca))
   def browser():
    jar=http.cookiejar.CookieJar()
    return urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx),NoRedirect,urllib.request.HTTPCookieProcessor(jar)),jar
   def req(b,url,form=None,js=None):
    data=urllib.parse.urlencode(form).encode() if form is not None else None
    headers={}
    if js is not None:data=json.dumps(js).encode();headers['Content-Type']='application/json'
    request=urllib.request.Request(urllib.parse.urljoin(console,url),data=data,headers=headers)
    try:response=b.open(request,timeout=30)
    except urllib.error.HTTPError as e:response=e
    return response.code,response.read().decode(),response.headers
   def follow(b,url):
    for _ in range(10):
     st,body,h=req(b,url)
     if st in (302,303):url=urllib.parse.urljoin(url,h['Location']);continue
     return st,body,h,url
    raise AssertionError('website redirect loop')
   b,jar=browser()
   st,page,headers,url=follow(b,console+'/login?remember=yes')
   if legacy:
    require(st==200 and 'class="web-login"' in page,'legacy public introduction unavailable')
    print('legacy: opt-out and deliberate absence survive init rerun; fallback introduction retained',flush=True);return
   require(st==200 and '/oauth/authorize?' in url and all('name="'+f+'"' in page for f in ['username','password','remember']),'default compact credential form missing')
   require('<img' in page and name in page,'credential page lost product branding')
   require('name="dataset"' not in page and 'name="administer"' not in page,'consent picker appeared for owned website')
   remember=re.search(r'<input[^>]*name="remember"[^>]*>',page).group()
   require('checked' not in remember,'remember defaulted on')
   require("form-action 'self'" in headers.get('Content-Security-Policy',''),'identity CSP weakened')
   password=dc('run','--rm','-T','--no-deps','init','password').decode().strip()
   identity=urllib.parse.urlunsplit((*urllib.parse.urlsplit(url)[:2],'/oauth/authorize','',''))
   st,page,headers=req(b,identity,dict(Page(page).fields,username='owner',password=password))
   if st==303:st,page,headers,url=follow(b,urllib.parse.urljoin(identity,headers['Location']))
   if st==200 and '/oauth/' in urllib.parse.urlparse(url).path:
    refresh=Page(page).refresh
    require(refresh is not None and re.search('url=',refresh,re.I),'automatic callback missing')
    callback=re.split('url=',refresh,flags=re.I)[1].strip().strip(chr(34)+chr(39))
    require(urllib.parse.urlparse(callback).netloc==urllib.parse.urlparse(console).netloc,'callback origin mismatch')
    st,page,headers,url=follow(b,callback)
   require(st==200 and '/oauth/' not in url and 'Sign out' in page,'one credential submission did not sign in')
   cookie=next(c for c in jar if c.name.startswith('__Host-voptrack_console_'))
   require(cookie.secure and cookie.expires is None and cookie.has_nonstandard_attr('HttpOnly'),'unchecked sign-in cookie unsafe')
   # A separately registered web client cannot inherit trust by claiming our name.
   other,_=browser();redirect='https://separate-client.invalid/callback'
   st,body,_=req(other,origin+'/oauth/register',js=dict(client_name='Voptrack',redirect_uris=[redirect],token_endpoint_auth_method='none',grant_types=['authorization_code'],response_types=['code']))
   require(st==201,'separate web client registration refused')
   client=json.loads(body)['client_id'];verifier=secrets.token_urlsafe(48)
   challenge=base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
   query=urllib.parse.urlencode(dict(response_type='code',client_id=client,redirect_uri=redirect,state='synthetic',code_challenge=challenge,code_challenge_method='S256',prompt='consent'))
   st,page,_=req(other,origin+'/oauth/authorize?'+query)
   require(st==200,'separate app credential page refused')
   form=dict(Page(page).fields,username='owner',password=password,decision='continue')
   st,page,_=req(other,origin+'/oauth/authorize',form)
   require(st==200 and 'name="administer"' in page,'separate web client bypassed consent')
   # Re-rendering init retains the same website identity and ceiling.
   before=config()['first_party_consoles']
   dc('stop','console','tesserad');dc('run','--rm','-T','--no-deps','init');dc('up','-d','--wait')
   require(config()['first_party_consoles']==before,'routine init changed the policy')
   print(variant+(' private' if private else '')+': default brand, single submission, unchecked remember, exact origin, separate-app consent and rerun preservation passed',flush=True)
  finally:
   dc('down','-v','--remove-orphans')

def main():
 p=argparse.ArgumentParser();p.add_argument('--variant',choices=['household','research','standard']);p.add_argument('--config-only',action='store_true')
 args=p.parse_args()
 for variant in ([args.variant] if args.variant else ['household','research','standard']):
  qualify(variant,config_only=args.config_only)
 if not args.variant and not args.config_only:
  qualify('household',private=True);qualify('household',legacy=True)
if __name__=='__main__':main()
