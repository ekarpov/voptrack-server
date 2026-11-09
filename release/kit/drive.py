"""Reuse the existing Console gate's browser helpers, without running its household scenario."""
import os,pathlib,secrets,json,re,subprocess
source=pathlib.Path(__file__).resolve().parents[1]/'console'/'drive.py'
marker='# ---- the owner invites the household from the Console'
text=source.read_text()
if text.count(marker)!=1:raise RuntimeError('Console browser-helper boundary changed')
g={'__file__':str(source),'__name__':'kit_browser_helpers'}
exec(compile(text.split(marker)[0],str(source),'exec'),g)
owner=g['browser']();st,page,url=g['console_sign_in'](owner,'owner',g['owner_pw'],{'administer':'yes'})
if st!=200 or g['urllib'].parse.urlparse(url).path not in ('/', '/home') or 'href="/manage"' not in page:raise RuntimeError('operator sign-in failed')
g['owner']=owner
# A real non-operator session exercises the browser authority boundary.
st,page,_=g['request'](owner,'GET','/members');st,page,_=g['follow'](owner,'/members/invite',form={'csrf':g['csrf_of'](page)},method='POST')
link=re.findall(r'<code class="link">([^<]+)</code>',page)[0].replace('&amp;','&')
redeem=g['browser']();st,page,_=g['request'](redeem,'GET',link);password=secrets.token_urlsafe(18)
st,_,_=g['request'](redeem,'POST','/oauth/invitation',form={'code':g['field'](page,'code'),'account':'sara','password':password})
if st!=201:raise RuntimeError('synthetic member setup failed')
sara=g['browser']();g['console_sign_in'](sara,'sara',password,{'read':'yes','write':'yes','store':True});g['sara']=sara
admin=g['setup_operator_token']()
st,body,_=g['api'](admin,'GET','/admin/api/v1/backup-operations?limit=1')
if st!=200:raise RuntimeError('native catalog unavailable')
first=json.loads(body)['results'][0]
work,project=(os.environ[k] for k in ('VOPTRACK_GATE_WORK','VOPTRACK_GATE_PROJECT'))
def dc(*args,data=None):
 p=subprocess.run(['docker','compose','-p',project,*args],cwd=work,input=data,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 if p.returncode:raise RuntimeError('private kit gate host operation failed at '+args[0])
 return p.stdout
dc('run','--rm','--no-deps','-T','--entrypoint','sh','collector-admin','-c','umask 077; cat > /tmp/admin; exec voptrack-collector -mode provision -administrator-file /tmp/admin -id '+secrets.token_hex(16)+' -deployment '+first['pair']['deployment'],data=admin.encode())
import sys
sys.path.insert(0,str(source.parent))
from native_settings_checks import verify as verify_native
verify_native(g,dc,admin)
from recovery_kit_checks import verify
verify(g,dc)

from native_fault_checks import verify as verify_faults
verify_faults(g,dc)

from native_persistence_checks import verify as verify_persistence
verify_persistence(g,dc,admin)

from native_recovery_checks import verify as verify_recovery_boundary
verify_recovery_boundary(g,dc,admin)
