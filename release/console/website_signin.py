#!/usr/bin/env python3
"""Actual-image compact website sign-in; disposable synthetic deployment only."""
import html,http.cookiejar,json,os,re,secrets,ssl,subprocess,tempfile,urllib.error,urllib.parse,urllib.request
from pathlib import Path
from html.parser import HTMLParser

class Page(HTMLParser):
 def __init__(self,text):
  super().__init__();self.fields={};self.refresh=None;self.buttons=[];self.feed(text)
 def handle_starttag(self,tag,attrs):
  a=dict(attrs)
  if tag=="input" and a.get("type")=="hidden" and a.get("name"):self.fields[a["name"]]=a.get("value","")
  if tag=="button" and a.get("name"):
   self.buttons.append((a["name"],a.get("value","")))
   if a["name"] not in self.fields:self.fields[a["name"]]=a.get("value","")
  if tag=="meta" and a.get("http-equiv","").lower()=="refresh":self.refresh=a.get("content","")

ROOT=Path(__file__).resolve().parents[2]

def main():
 with tempfile.TemporaryDirectory(prefix='voptrack-website-signin-') as tmp:
  project='voptrack-website-signin-'+secrets.token_hex(4)
  port='17753';origin='https://localhost:'+port
  env=dict(os.environ,VOPTRACK_IMAGE=os.environ['VOPTRACK_IMAGE'],VOPTRACK_STORAGE_ENCRYPTED='yes',VOPTRACK_DOMAIN='localhost',VOPTRACK_VARIANT='household',VOPTRACK_CONSOLE='on',VOPTRACK_HTTPS_PORT=port,VOPTRACK_HTTP_PORT='17790',VOPTRACK_CONSOLE_PORT='19753',VOPTRACK_PUBLIC_URL=origin,VOPTRACK_PROXY_ADDRESS='10.119.64.2',VOPTRACK_PROXY_SUBNET='10.119.64.0/24',VOPTRACK_PROXY_POOL='10.119.64.128/25',VOPTRACK_PUBLISH_PREFIX='127.0.0.1:',VOPTRACK_WEBSITE_CLIENT_ID='operator-website',VOPTRACK_WEBSITE_DATA_GRANTS='read write insert',VOPTRACK_WEBSITE_ADMINISTER='yes')
  def dc(*args,input=None):
   p=subprocess.run(['docker','compose','-p',project,'-f',str(ROOT/'release/compose.yaml'),*args],env=env,input=input,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=180)
   if p.returncode:raise AssertionError('disposable deployment command failed: '+args[0])
   return p.stdout
  class NoRedirect(urllib.request.HTTPRedirectHandler):
   def redirect_request(self,*a,**k):return None
  try:
   dc('run','--rm','-T','init')
   brand=(ROOT/'deploy/identity-brand/home.json').read_bytes()
   dc('run','--rm','-T','--no-deps','--entrypoint','sh','init','-c','umask 077; cat > /var/lib/voptrack-config/identity-brand.json',input=brand)
   dc('up','-d','--wait')
   password=dc('run','--rm','-T','--no-deps','init','password').decode().strip()
   ca=Path(tmp)/'ca.crt';ca.write_bytes(dc('exec','-T','caddy','cat','/var/lib/voptrack-public/ca.crt'))
   ctx=ssl.create_default_context(cafile=str(ca))
   def browser():
    jar=http.cookiejar.CookieJar()
    return urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx),NoRedirect,urllib.request.HTTPCookieProcessor(jar)),jar
   def req(b,url,form=None):
    data=urllib.parse.urlencode(form).encode() if form is not None else None
    r=urllib.request.Request(urllib.parse.urljoin(origin,url),data=data)
    try:res=b.open(r,timeout=30)
    except urllib.error.HTTPError as e:res=e
    return res.code,res.read().decode(),res.headers
   def follow(b,url):
    for _ in range(8):
     st,body,h=req(b,url)
     if st in (302,303):url=h['Location'];continue
     return st,body,h,url
    raise AssertionError('redirect loop')
   def field(page,name):
    m=re.search(r'name="'+name+r'"\s+value="([^"]*)"',page)
    assert m,'missing identity form binding';return html.unescape(m.group(1))
   def login(remember):
    b,jar=browser();st,page,h,url=follow(b,'/login?remember=yes')
    assert st==200 and '/oauth/authorize?' in url,'intro was not removed'
    assert 'name="username"' in page and 'name="password"' in page and 'name="remember"' in page,'compact form missing'
    assert 'name="dataset"' not in page and 'name="administer"' not in page,'OAuth permissions exposed'
    assert "form-action 'self'" in h.get('Content-Security-Policy',''),'CSP weakened'
    remember_tag=re.search(r'<input[^>]*name="remember"[^>]*>',page).group()
    assert 'checked' not in remember_tag,'remember did not default off'
    form=dict(Page(page).fields,username='owner',password=password)
    if remember:form['remember']='yes'
    st,page,h=req(b,'/oauth/authorize',form)
    if st==303:
     st,page,h,url=follow(b,h['Location'])
    assert st==200,'credential submission refused (status '+str(st)+')'
    # Same-origin deployments may redirect straight to the callback. Across
    # origins Tessera emits a script-free refresh document after its POST.
    if '/oauth/' in urllib.parse.urlparse(url).path:
     refresh=Page(page).refresh
     assert refresh and re.search(r'url=',refresh,re.I),'missing automatic callback completion'
     callback=re.split(r'url=',refresh,flags=re.I)[1].strip().strip(chr(34)+chr(39))
     assert urllib.parse.urlparse(callback).netloc==urllib.parse.urlparse(origin).netloc,'wrong callback'
     st,page,h,url=follow(b,callback)
    assert st==200 and '/oauth/' not in url and 'Sign out' in page,'single submission did not sign in'
    cookie=next(c for c in jar if c.name.startswith('__Host-voptrack_console_'))
    assert cookie.secure and cookie.has_nonstandard_attr('HttpOnly'),'unsafe cookie'
    assert (cookie.expires is not None)==remember,'persistence disagrees with checkbox'
    return b
   checked=login(True);ordinary=login(False)
   dc('restart','console');dc('up','-d','--no-deps','--wait','console')
   assert req(checked,'/home')[0]==200,'remembered sign-in lost after restart'
   assert req(ordinary,'/home')[0]==303,'unchecked sign-in survived restart'
   st,page,h=req(checked,'/home');csrf=field(page,'csrf')
   req(checked,'/logout',{'csrf':csrf})
   assert req(checked,'/home')[0]==303,'logout retained session'
   print('Actual image: one credential submission; no introduction/consent picker; strict CSP; unchecked default; checked-only restart persistence; logout passed')
  finally:
   dc('down','-v','--remove-orphans')

if __name__=='__main__':main()
