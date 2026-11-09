#!/usr/bin/env python3
"""Opt-in, repeatable UI demos on the two existing localhost dev servers.

Creates separate demo accounts/tracks/studies. Never changes the app integration
fixtures, their records or expected totals. Credentials and progress stay in each
ignored .voptrack-*-dev/ui-review.json file. No record content is printed.
Run again to resume; --verify checks the saved demos without adding data.
"""
import argparse
import base64
import datetime as dt
import hashlib
import html
import http.cookiejar
import json
import math
import os
from pathlib import Path
import re
import secrets
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def require(ok, message):
    if not ok:
        raise RuntimeError(message)


def field(page, name):
    match = re.search(r'name="' + re.escape(name) + r'"\s+value="([^"]*)"', page)
    require(match is not None, 'Expected form field is missing: ' + name)
    return html.unescape(match.group(1))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class Demo:
    def __init__(self, kind):
        self.kind = kind
        self.state_dir = ROOT / ('.voptrack-' + kind + '-dev')
        self.base = 'https://localhost:' + ('8544' if kind == 'home' else '8444')
        require((self.state_dir / 'owner').is_file(), 'Start the development previews first')
        self.context = ssl.create_default_context(cafile=str(self.state_dir / 'ca.crt'))
        self.path = self.state_dir / 'ui-review.json'
        self.state = json.loads(self.path.read_text()) if self.path.exists() else {
            'revision': 1, 'server': self.base, 'anchor': dt.date.today().isoformat(),
            'done': [], 'people': {}, 'studies': {}}
        require(self.state['server'] == self.base and self.state['revision'] == 1, 'Unexpected demo manifest')
        self.owner = self.console('owner', (self.state_dir / 'owner').read_text().strip())

    def save(self):
        # Write a private sibling and replace it; a partial write never loses progress.
        temp = self.path.with_suffix('.tmp')
        with os.fdopen(os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), 'w') as f:
            json.dump(self.state, f, indent=2)
        os.chmod(temp, 0o600)
        temp.replace(self.path)

    def browser(self):
        return urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=self.context), NoRedirect,
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(self, browser, method, path, form=None, body=None, token=None, key=None):
        url = urllib.parse.urljoin(self.base + '/', path)
        require(urllib.parse.urlsplit(url).netloc == urllib.parse.urlsplit(self.base).netloc,
                'Refusing a request outside the local development server')
        headers, data = {}, None
        if form is not None:
            data = urllib.parse.urlencode(form, doseq=True).encode()
            headers['Content-Type'] = 'application/x-www-form-urlencoded'
        if body is not None:
            data = json.dumps(body).encode()
            headers['Content-Type'] = 'application/json'
        if token:
            headers['Authorization'] = 'Bearer ' + token
        if key:
            headers['Idempotency-Key'] = str(uuid.uuid5(uuid.NAMESPACE_URL, self.base + '/ui-review/' + key))
        request = urllib.request.Request(url, data=data, method=method, headers=headers)
        for attempt in range(3):
            try:
                response = browser.open(request, timeout=45)
                return response.status, response.read().decode(), response.headers
            except urllib.error.HTTPError as error:
                if error.code == 429 and attempt < 2:
                    print('Waiting for the development sign-in rate limit.', flush=True)
                    time.sleep(31)
                    time.sleep(31)
                    continue
                return error.code, error.read().decode(errors='replace'), error.headers
        raise RuntimeError('Development request failed')

    def follow(self, browser, path, form=None):
        method = 'POST' if form is not None else 'GET'
        for _ in range(12):
            status, page, headers = self.request(browser, method, path, form=form)
            if status in (301, 302, 303):
                path, method, form = headers['Location'], 'GET', None
            else:
                return status, page
        raise RuntimeError('Too many development redirects')

    def approval(self, browser, page, account, password, grants):
        status, approval, _ = self.request(browser, 'POST', '/oauth/authorize', form={
            'flow': field(page, 'flow'), 'csrf': field(page, 'csrf'),
            'username': account, 'password': password, 'decision': 'continue'})
        require(status == 200, 'Development sign-in failed')
        status, _, headers = self.request(browser, 'POST', '/oauth/authorize', form={
            'flow': field(page, 'flow'), 'csrf': field(approval, 'csrf'), 'decision': 'allow', **grants})
        require(status == 303, 'Development consent failed')
        return headers['Location']

    def console(self, account, password, dataset=None):
        browser = self.browser()
        status, page = self.follow(browser, '/login')
        require(status == 200, 'Console login did not open')
        grants = {'administer': 'yes'}
        if dataset:
            grants.update(dataset=dataset, read='yes')
        location = self.approval(browser, page, account, password, grants)
        status, _ = self.follow(browser, location)
        require(status == 200, 'Console sign-in did not finish')
        return browser

    def token(self, person, schema=False):
        browser = self.browser()
        redirect = 'com.voptrack.ui-review:/oauth/callback'
        status, raw, _ = self.request(browser, 'POST', '/oauth/register', body={
            'client_name': 'Voptrack synthetic UI review', 'redirect_uris': [redirect],
            'token_endpoint_auth_method': 'none', 'grant_types': ['authorization_code'],
            'response_types': ['code']})
        require(status == 201, 'Development app registration failed')
        client = json.loads(raw)['client_id']
        verifier = secrets.token_urlsafe(48)
        challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
        query = urllib.parse.urlencode(dict(response_type='code', client_id=client, redirect_uri=redirect,
                                             code_challenge=challenge, code_challenge_method='S256', prompt='consent', state='ui'))
        status, page, _ = self.request(browser, 'GET', '/oauth/authorize?' + query)
        grants = {'read': 'yes', 'write': 'yes'}
        if schema:
            grants.update(schema='yes')
        if person.get('dataset'):
            grants['dataset'] = person['dataset']
        else:
            # Read the chooser only after authentication, below.
            status, approval, _ = self.request(browser, 'POST', '/oauth/authorize', form={
                'flow': field(page, 'flow'), 'csrf': field(page, 'csrf'), 'username': person['account'],
                'password': person['password'], 'decision': 'continue'})
            require(status == 200, 'Demo app sign-in failed')
            options = re.findall(r'<option\s+value="([^"]+)"[^>]*>([^<]*)', approval)
            mine = [value for value, label in options if html.unescape(label).strip().startswith(person['account'] + ' —')]
            if not mine and len(options) == 1:
                mine = [options[0][0]]
            require(len(mine) == 1, 'Could not identify the new demo account own store')
            person['dataset'] = html.unescape(mine[0])
            self.save()
            status, _, headers = self.request(browser, 'POST', '/oauth/authorize', form={
                'flow': field(page, 'flow'), 'csrf': field(approval, 'csrf'), 'decision': 'allow',
                'dataset': person['dataset'], **grants})
            require(status == 303, 'Demo app consent failed')
            location = headers['Location']
        if 'dataset' in grants:
            location = self.approval(browser, page, person['account'], person['password'], grants)
        code = urllib.parse.parse_qs(urllib.parse.urlsplit(location).query)['code'][0]
        status, raw, _ = self.request(browser, 'POST', '/oauth/token', form={
            'grant_type': 'authorization_code', 'client_id': client, 'code': code,
            'redirect_uri': redirect, 'code_verifier': verifier})
        require(status == 200, 'Demo token exchange failed')
        return json.loads(raw)['access_token']

    def post(self, browser, path, values):
        status, page = self.follow(browser, path)
        require(status == 200, 'Demo page did not open')
        status, result = self.follow(browser, path, form={'csrf': field(page, 'csrf'), **values})
        require(status == 200 and 'data-error=' not in result, 'Demo form was refused')
        return result

    def transact(self, token, person, operations, key):
        for start in range(0, len(operations), 128):
            batch = key + '/' + str(start)
            if batch in self.state.get('batches', []):
                continue
            status, _, _ = self.request(self.browser(), 'POST', '/v1/datasets/' + person['dataset'] + ':transact',
                body={'operations': operations[start:start+128]}, token=token, key=key + '/' + str(start))
            require(status == 200, 'Demo record batch was refused')
            self.state.setdefault('batches', []).append(batch)
            self.save()

    def dashboard(self, browser, scope, type_id, name, group='period', field_id='', measure='count'):
        query = urllib.parse.urlencode(dict(scope=scope, type=type_id, field=field_id, measure=measure,
            group=group, period='week', tz='UTC'))
        status, page = self.follow(browser, '/panel?' + query)
        require(status == 200, 'Demo panel did not open')
        status, result = self.follow(browser, '/dashboards/add', form={
            'csrf': field(page, 'csrf'), 'panel': field(page, 'panel'), 'name': name})
        require(status == 200 and 'data-error=' not in result, 'Demo dashboard was refused')

    def check_panel(self, scope, type_id, count):
        query = urllib.parse.urlencode(dict(scope=scope, type=type_id, measure='count', group='none', tz='UTC'))
        status, page = self.follow(self.owner, '/panel?' + query)
        values = re.findall(r'class="num"\s+data-value[^>]*>([^<]*)', page)
        require(status == 200 and values == [str(count)], 'Demo count verification failed')
        status, _, _ = self.request(self.owner, 'GET', '/panel.csv?' + query)
        require(status == 200, 'Demo CSV export failed')

    def verify(self):
        require('complete' in self.state['done'], 'Seed the UI demos before verification')
        for index, (scope, type_id, count) in enumerate(self.state['checks'], 1):
            try:
                self.check_panel(scope, type_id, count)
            except RuntimeError:
                raise RuntimeError('Demo panel verification failed at check ' + str(index)) from None
        if self.kind == 'research':
            status, page = self.follow(self.owner, '/studies/ui-daily-rhythm/participants')
            require(status == 200 and 'state_suspended' in page and 'state_withdrawn' in page,
                    'Demo participant states are missing')
            for slug in ('ui-daily-rhythm', 'ui-weekly-reflection'):
                status, _, _ = self.request(self.owner, 'GET', '/studies/' + slug + '/export.json')
                require(status == 200, 'Demo JSON export failed')
        print(self.kind.title() + ' synthetic UI data verified.', flush=True)


def typed(kind, value):
    return {'type': kind, 'value': value}


def dates(demo, days):
    anchor = dt.date.fromisoformat(demo.state['anchor'])
    return [(anchor - dt.timedelta(days=days-i)).isoformat() + 'T08:00:00.000Z' for i in range(days)]


def seed_home(demo):
    activity, budget, focus = ['org.voptrack.ui.' + name for name in ('activity', 'budget', 'focus')]
    types = [(activity, 'Daily activity', [('minutes', 'Active minutes', 'float'), ('category', 'Activity', 'text')]),
             (budget, 'Everyday spending', [('amount', 'Amount', 'fixed'), ('category', 'Category', 'text')]),
             (focus, 'Personal focus', [('score', 'Focus score', 'integer')])]
    for index, name in enumerate(('demo-avery', 'demo-mika', 'demo-jules')):
        if name not in demo.state['people']:
            status, page = demo.follow(demo.owner, '/members')
            status, page = demo.follow(demo.owner, '/members/invite', form={'csrf': field(page, 'csrf')})
            links = re.findall(r'<code class="link">([^<]+)</code>', page)
            require(links, 'Demo invitation was not created')
            browser = demo.browser()
            status, page = demo.follow(browser, html.unescape(links[0]))
            password = secrets.token_urlsafe(24)
            status, _, _ = demo.request(browser, 'POST', '/oauth/invitation', form={
                'code': field(page, 'code'), 'account': name, 'password': password})
            require(status == 201, 'Demo member invitation was not redeemed')
            demo.state['people'][name] = {'account': name, 'password': password}
            demo.save()
        person = demo.state['people'][name]
        if name + '-ready' in demo.state['done']:
            continue
        token = demo.token(person, schema=True)
        for type_id, title, columns in types:
            fields = [{'id': 'recorded_at', 'meta_id': type_id, 'name': 'When', 'type': 'date_time'}]
            for key, label, kind in columns:
                fields.append(dict(id=key, meta_id=type_id, name=label, type=kind, **({'scale': 2} if kind == 'fixed' else {})))
            status, _, _ = demo.request(demo.browser(), 'POST', '/v1/datasets/' + person['dataset'] + '/meta',
                token=token, body={'type': dict(id=type_id, parent_id=None, name=title, fields=fields)}, key=name+'/'+type_id)
            require(status in (201, 409), 'Demo track definition failed')
        ops = []
        if index < 2:
            for i, stamp in enumerate(dates(demo, 96)):
                if i % 11 == 0:
                    continue  # Real gaps, never filled with fabricated zeroes.
                for type_id, values in [
                    (activity, {'minutes': typed('float', round(22+index*8+18*(1+math.sin(i/5)), 2)),
                                'category': typed('text', ('Walking', 'Cycling', 'Swimming', 'Strength')[i%4])}),
                    (budget, {'amount': typed('fixed', str(round((-9.5 if i%19 == 0 else 8+(i*7+index*13)%65), 2))),
                              'category': typed('text', ('Groceries', 'Transport', 'Cafés & lunch', 'Home & utilities')[i%4])}),
                    (focus, {'score': typed('integer', 1+(i+index)%5)})]:
                    ops.append({'op': 'insert', 'type': type_id,
                                'fields': {'recorded_at': typed('date_time', stamp), **values}})
            demo.transact(token, person, ops, name+'/records')
            member = demo.console(person['account'], person['password'], person['dataset'])
            demo.post(member, '/sharing', {'dataset': person['dataset'], 'track': [activity, budget]})
            demo.dashboard(member, 'mine', activity, 'My daily rhythm', field_id='minutes', measure='average')
            demo.dashboard(member, 'mine', focus, 'My daily rhythm', field_id='score', measure='average')
        else:
            status, page = demo.follow(demo.owner, '/members')
            status, _ = demo.follow(demo.owner, '/members/'+name+'/suspend', form={'csrf': field(page,'csrf')})
            require(status == 200, 'Demo member suspension failed')
        demo.state['done'].append(name+'-ready')
        demo.save()
    if 'boards' not in demo.state['done']:
        for type_id, field_id, measure, group in [(activity,'minutes','average','period'),(budget,'amount','sum','period'),(budget,'amount','sum','field:category')]:
            demo.dashboard(demo.owner,'home',type_id,'Household overview',group,field_id,measure)
        demo.state['done'].append('boards')
    count = 2 * sum(i%11 != 0 for i in range(96))
    demo.state['checks'] = [('home',activity,count),('home',budget,count)]
    demo.state['done'].append('complete')
    demo.save()
    demo.verify()


def seed_research(demo):
    definitions = [('ui-daily-rhythm','Daily rhythm — sleep, activity & wellbeing',8),
                   ('ui-weekly-reflection','Weekly reflection',2),
                   ('ui-completed-study','Movement pilot — completed',1),
                   ('ui-next-study','Everyday routines — planning',0)]
    for slug, title, size in definitions:
        if slug not in demo.state['studies']:
            demo.post(demo.owner, '/studies', {'title': title, 'slug': slug})
            demo.state['studies'][slug] = {'people': [], 'done': []}
            demo.save()
        study = demo.state['studies'][slug]
        edit = {'title': title, 'run_by': 'Synthetic Voptrack research team',
                'summary': 'Synthetic demonstration study for reviewing the Voptrack interface. No real people or health data.',
                'collected': 'Example sleep duration, mood, activity and weight readings over several weeks.',
                'who_sees': 'Only the synthetic study researcher through the existing study permissions.',
                'how_to_leave': 'Withdraw through the app, or export and remove your data from the Console.',
                'consent': 'This is fictional test data. I agree to take part in the synthetic demonstration study.',
                'material': 'yes', 'join_mode': 'open', 'status': 'open' if size else 'draft'}
        if 'designed' not in study['done']:
            form = {'catalogue': ['com.voptrack.track.weight']}
            tracks = [('Sleep', [('Hours slept','number','h','sample'),('Quality','wholeNumber','','sample'),
                       ('Mood','text','',''),('Napped','yesNo','',''),('Bedtime','dateTime','','')]),
                      ('Activity', [('Minutes','number','min','additive'),('Steps','wholeNumber','','additive'),('Activity','text','','')])]
            for ti,(name,columns) in enumerate(tracks):
                form.update({f't{ti}.present':'1',f't{ti}.name':name})
                for fi,(label,kind,unit,meaning) in enumerate(columns):
                    prefix=f't{ti}.f{fi}.'
                    form.update({prefix+'present':'1',prefix+'name':label,prefix+'kind':kind,prefix+'unit':unit,prefix+'meaning':meaning})
            demo.post(demo.owner,'/studies/'+slug+'/tracks',form)
            demo.post(demo.owner,'/studies/'+slug+'/edit',edit)
            study['done'].append('designed')
            demo.save()
        for index in range(size):
            if index == 4 and 'v2' not in study['done']:
                edit['consent'] += ' Version two also explains that participation is voluntary and can end at any time.'
                demo.post(demo.owner,'/studies/'+slug+'/edit',edit)
                study['done'].append('v2')
                demo.save()
            if len(study['people']) <= index:
                browser=demo.browser()
                status,page=demo.follow(browser,'/study/'+slug+'/join')
                status,redeem=demo.follow(browser,'/study/'+slug+'/join',form={
                    'csrf':field(page,'csrf'),'version':field(page,'version'),'code':field(page,'code'),'agree':'yes'})
                assigned=re.search(r'data-account="([^"]+)"',redeem)
                require(assigned is not None,'Demo study join failed')
                person={'account':html.unescape(assigned.group(1)),'password':secrets.token_urlsafe(24)}
                status,_,_=demo.request(browser,'POST','/oauth/invitation',form={
                    'code':field(redeem,'code'),'account':person['account'],'password':person['password']})
                require(status == 201,'Demo participant invitation failed')
                study['people'].append(person)
                demo.save()
            person=study['people'][index]
            if person.get('ready'):
                continue
            token=demo.token(person)
            ops=[]
            prefix='study.'+slug.replace('-','_')+'.'
            if not (size == 8 and index == 5):
                for i,stamp in enumerate(dates(demo,42)):
                    if (i+index)%9 == 0:
                        continue
                    common={'timestamp':typed('date_time',stamp)}
                    sleep={'hours_slept':typed('float',round(6.4+index*.15+math.sin(i/4),2)),
                           'hours_slept_unit':typed('text','h'),'quality':typed('integer',1+(i+index)%5),
                           'mood':typed('text',('Rested','Neutral','Low energy','Very good')[i%4]),
                           'napped':typed('boolean',i%4 == 0),'bedtime':typed('date_time',stamp.replace('08:00','22:30'))}
                    activity={'minutes':typed('float',float((i*7+index*11)%85)), 'minutes_unit':typed('text','min'),
                              'steps':typed('integer',3200+(i*131+index*471)%8000),'activity':typed('text',('Walk','Cycle','Swim')[i%3])}
                    weight={'weight':typed('float',round(62+index*3+math.sin(i/8),2)), 'unit':typed('text','kg')}
                    for type_id,values in [(prefix+'sleep',sleep),(prefix+'activity',activity),('com.voptrack.track.weight',weight)]:
                        ops.append({'op':'insert','type':type_id,'fields':{**common,**values}})
                demo.transact(token,person,ops,slug+'/'+str(index)+'/records')
            if size == 8 and index == 6:
                status,page=demo.follow(demo.owner,'/studies/'+slug+'/participants')
                status,_=demo.follow(demo.owner,'/studies/'+slug+'/participants/'+person['account']+'/suspend',form={'csrf':field(page,'csrf')})
                require(status == 200,'Demo participant suspension failed')
            if size == 8 and index == 7:
                status,_,_=demo.request(demo.browser(),'POST','/api/v1/studies/'+slug+'/withdraw',body={},token=token)
                require(status == 200,'Demo participant withdrawal failed')
            person['ready']=True
            person['sleep_count']=sum((i+index)%9 != 0 for i in range(42)) if ops else 0
            demo.save()
            print('Synthetic research participant prepared.',flush=True)
        if 'finished' not in study['done']:
            if slug == 'ui-weekly-reflection':
                edit['join_mode']='invite'
                demo.post(demo.owner,'/studies/'+slug+'/edit',edit)
                status,page=demo.follow(demo.owner,'/studies/'+slug+'/participants')
                status,page=demo.follow(demo.owner,'/studies/'+slug+'/codes',form={'csrf':field(page,'csrf')})
                require(status == 200,'Demo invite-only link failed')
                link=re.search(r'data-join-link[^>]*>([^<]+)',page)
                require(link is not None,'Demo invitation link is missing')
                study['invitation']=html.unescape(link.group(1))
            if slug == 'ui-completed-study':
                edit['status']='closed'
                demo.post(demo.owner,'/studies/'+slug+'/edit',edit)
            study['done'].append('finished')
            demo.save()
    demo.state['checks']=[]
    for slug,study in demo.state['studies'].items():
        if study['people']:
            # App withdrawal freezes previously contributed records; it does not erase them.
            count=sum(p['sleep_count'] for p in study['people'])
            demo.state['checks'].append(('study:'+slug,'study.'+slug.replace('-','_')+'.sleep',count))
    demo.state['done'].append('complete')
    demo.save()
    demo.verify()


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--verify',action='store_true')
    parser.add_argument('--only',choices=('home','research'))
    args=parser.parse_args()
    for kind in ([args.only] if args.only else ['home','research']):
        demo=Demo(kind)
        if args.verify or 'complete' in demo.state['done']:
            demo.verify()
        elif kind == 'home':
            seed_home(demo)
        else:
            seed_research(demo)


if __name__ == '__main__':
    try:
        main()
    except RuntimeError as error:
        print("UI demo setup: " + str(error), file=sys.stderr)
        raise SystemExit(1) from None
    except Exception as error:
        print("UI demo setup: " + type(error).__name__, file=sys.stderr)
        # HTTP bodies and exceptions may contain credentials or record values.
        print('UI demo setup failed; local progress is saved. Inspect the failing stage without printing credentials or response bodies.',file=sys.stderr)
        raise SystemExit(1) from None
