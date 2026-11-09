"""Create an open synthetic study via the existing wizard and retain one participant.
Credentials are written only to a private fixture file, never stdout.
"""
import importlib.util,json,os,pathlib,re,secrets,sys,urllib.parse
REPO=pathlib.Path(__file__).resolve().parents[2]
base,ca,password_file,output=sys.argv[1:]
source=REPO/'release/console/drive.py'
g={'__file__':str(source),'__name__':'assurance_research_helpers'}
exec(compile(source.read_text().split('# ---- the owner invites the household from the Console')[0],str(source),'exec'),g)
def check(value,label):
 if not value:raise RuntimeError(label)
owner=g['browser']()
check(g['console_sign_in'](owner,'owner',g['owner_pw'],{'administer':'yes'})[0]==200,'research operator sign-in refused')
request=g['request'];follow=g['follow'];field=g['field'];csrf=g['csrf_of']
st,page,_=request(owner,'GET','/studies');check(st==200,'study list refused')
st,page,url=follow(owner,'/studies',form={'csrf':csrf(page),'title':'Assurance study','slug':'assurance'},method='POST')
check(st==200 and url.endswith('/setup/basics'),'study creation bypassed wizard')
def save(stage,action,values=None):
 st,page,_=request(owner,'GET','/studies/assurance/setup/'+stage)
 check(st==200,'study wizard page refused')
 st,body,headers=request(owner,'POST','/studies/assurance/setup/'+stage,form=dict(csrf=csrf(page),revision=field(page,'revision'),action=action,**(values or {})))
 check(st==303,'study wizard transition refused')
save('basics','next',{'title':'Assurance study','run_by':'Synthetic team'})
save('collection','next',{'catalogue':'com.voptrack.track.weight'})
save('information','next',{'summary':'Synthetic recovery purpose','collected':'Synthetic weight','who_sees':'Study team','how_to_leave':'Withdraw in Console','consent':'Synthetic assurance consent','material':'yes'})
save('recruitment','next',{'join_mode':'open'})
check(request(g['browser'](),'GET','/study/assurance')[0]==404,'draft study exposed before publication')
save('review','publish');save('review','open')
person=g['browser']();st,page,_=follow(person,'/study/assurance/join')
check(st==200,'participant consent page refused')
st,redeem,url=follow(person,'/study/assurance/join',form={'csrf':csrf(page),'version':field(page,'version'),'code':field(page,'code'),'agree':'yes'},method='POST')
check('/oauth/invitation' in url,'consent did not lead to native join')
assigned=re.search(r'data-account="([^"]+)"',redeem);check(assigned,'native pseudonym missing')
account=assigned.group(1);password=secrets.token_urlsafe(24)
form={k:v for k,v in re.findall(r'name="(code|account)" value="([^"]*)"',redeem)};form['password']=password
check(request(person,'POST','/oauth/invitation',form=form)[0]==201,'participant native join refused')
token=g['app_token'](account,password,{'read':'yes','write':'yes','store':True})
st,body,_=g['api'](token,'GET','/v1/datasets');check(st==200,'participant store listing refused')
dataset=json.loads(body)['datasets'][0]['id'];track='com.voptrack.track.weight'
check(g['api'](token,'POST',f'/v1/datasets/{dataset}/objects/{track}',{'fields':{'weight':{'type':'float','value':72.5},'unit':{'type':'text','value':'kg'}}})[0]==201,'participant record refused')
path=pathlib.Path(output)
with path.open('x') as f:
 os.fchmod(f.fileno(),0o600);json.dump({'account':account,'password':password,'dataset':dataset,'track':track,'records':1,'fields':{'weight':72.5,'unit':'kg'}},f);f.write('\n')
print('Research wizard, explicit consent, native participant join and synthetic record: PASS')
