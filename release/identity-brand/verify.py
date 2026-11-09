#!/usr/bin/env python3
"""Actual-image operator identity checks; disposable synthetic deployments only."""
import base64,hashlib,json,os,secrets,shutil,ssl,subprocess,tempfile,urllib.request,urllib.parse
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
IMAGE=os.environ['VOPTRACK_IMAGE']

def verify(kind):
    with tempfile.TemporaryDirectory(prefix='voptrack-brand-') as tmp:
        work=Path(tmp);shutil.copy(ROOT/'release/compose.yaml',work/'compose.yaml')
        project='voptrack-brand-'+secrets.token_hex(5)
        port=os.environ.get('VOPTRACK_HOME_DEV_PORT','8644')
        env=dict(os.environ,VOPTRACK_WEBSITE_SIGN_IN='off',VOPTRACK_IDENTITY_BRAND='off',VOPTRACK_IMAGE=IMAGE,VOPTRACK_STORAGE_ENCRYPTED='yes',VOPTRACK_DOMAIN='localhost',VOPTRACK_VARIANT='household' if kind=='home' else 'research',VOPTRACK_CONSOLE='off' if kind=='home' else 'on',VOPTRACK_HTTPS_PORT=port,VOPTRACK_HTTP_PORT=os.environ.get('VOPTRACK_HOME_DEV_HTTP_PORT','8680'),VOPTRACK_CONSOLE_PORT=os.environ.get('VOPTRACK_HOME_DEV_CONSOLE_PORT','9446'),VOPTRACK_PUBLIC_URL='https://localhost:'+port,VOPTRACK_PROXY_ADDRESS=os.environ.get('VOPTRACK_HOME_DEV_NET','10.117.50')+'.2',VOPTRACK_PROXY_SUBNET=os.environ.get('VOPTRACK_HOME_DEV_NET','10.117.50')+'.0/24',VOPTRACK_PROXY_POOL=os.environ.get('VOPTRACK_HOME_DEV_NET','10.117.50')+'.128/25',VOPTRACK_PUBLISH_PREFIX='127.0.0.1:',VOPTRACK_CONSOLE_BIND='127.0.0.1')
        def dc(*args,input=None,ok=True,timeout=150):
            p=subprocess.run(['docker','compose','-p',project,*args],cwd=work,env=env,input=input,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=timeout)
            if ok and p.returncode:
                detail=p.stderr.decode(errors='replace')[-1600:]
                if args[0]=='up':
                    logs=dc('logs','--no-color','init','tesserad',ok=False).stdout.decode(errors='replace')
                    selected=[line for line in logs.splitlines() if any(word in line.lower() for word in ('identity_brand','identity branding','identity-logo','palette','logo','accent','contrast'))]
                    detail+='\n'+'\n'.join(selected[-8:])
                raise AssertionError('branding fixture command failed at '+args[0]+': '+detail)
            return p
        def input_brand(d):
            dc('run','--rm','-T','--no-deps','--entrypoint','sh','init','-c','umask 077; cat > /var/lib/voptrack-config/identity-brand.json',input=json.dumps(d).encode())
        try:
            dc('run','--rm','-T','init')
            cfg=json.loads(dc('run','--rm','-T','--no-deps','--entrypoint','cat','init','/var/lib/voptrack-config/server.json').stdout)
            assert 'identity_brand' not in cfg,'branding must default off'
            brand=json.loads((ROOT/'deploy/identity-brand'/f'{kind}.json').read_text());input_brand(brand)
            dc('up','-d','--wait','--wait-timeout','100')
            cfg=json.loads(dc('run','--rm','-T','--no-deps','--entrypoint','cat','init','/var/lib/voptrack-config/server.json').stdout)
            got=cfg.get('identity_brand',{})
            assert got.get('name')==brand['name'],'operator brand missing from generated configuration'
            assert got.get('light')==brand['light'] and got.get('dark')==brand['dark'],'operator palette mismatch'
            assert 'web_client_ids' not in got,'branding must not enable first-party exceptions'
            ca=work/'ca.crt';ca.write_bytes(dc('exec','-T','caddy','cat','/var/lib/voptrack-public/ca.crt').stdout)
            context=ssl.create_default_context(cafile=str(ca));base=env['VOPTRACK_PUBLIC_URL']
            def get(path,data=None):
                req=urllib.request.Request(base+path,data=data,headers={'Content-Type':'application/json'} if data else {})
                with urllib.request.urlopen(req,context=context,timeout=30) as r:return r.read(),r.headers
            body,headers=get('/oauth/brand/logo')
            assert body[:8]==b'\x89PNG\r\n\x1a\n' and len(body)<=256*1024,'logo is not a bounded PNG'
            assert headers.get_content_type()=='image/png' and headers.get('X-Content-Type-Options')=='nosniff' and headers.get('Cache-Control')=='no-store','logo response safeguards missing'
            back='com.voprak.voptrack:/oauth/callback'
            body,_=get('/oauth/register',json.dumps({'client_name':'Branding acceptance','redirect_uris':[back],'token_endpoint_auth_method':'none','grant_types':['authorization_code'],'response_types':['code']}).encode())
            client=json.loads(body)['client_id']
            challenge=base64.urlsafe_b64encode(hashlib.sha256(secrets.token_bytes(48)).digest()).rstrip(b'=').decode()
            page,headers=get('/oauth/authorize?'+urllib.parse.urlencode({'client_id':client,'redirect_uri':back,'response_type':'code','state':'brand-check','code_challenge':challenge,'code_challenge_method':'S256'}))
            text=page.decode();csp=headers.get('Content-Security-Policy','')
            assert brand['name'] in text and '/oauth/brand/logo' in text,'operator branding absent from identity page'
            assert "form-action 'self'" in csp and 'com.voprak.voptrack:' not in csp,'native CSP weakened'
            for palette in (brand['light'],brand['dark']):
                assert all(color.lower() in text.lower() for color in palette.values()),'theme palette absent from identity page'
            dc('stop','console','tesserad')
            invalid=json.loads(json.dumps(brand));invalid['light']['accent']='#F2F2F7';input_brand(invalid)
            refused=dc('run','--rm','-T','--no-deps','init',ok=False)
            # init may delegate validation to Tessera's first command or server startup.
            if refused.returncode==0:
                refused=dc('run','--rm','-T','--no-deps','tesserad',ok=False,timeout=30)
            assert refused.returncode!=0,'invalid contrast accepted by actual runtime'
            print(kind+': explicit opt-out, palettes, validated logo, identity HTML, strict CSP and invalid-contrast refusal passed')
        finally:
            dc('down','-v','--remove-orphans',ok=False)

if __name__=='__main__':
    for kind in ('home','study'):verify(kind)
