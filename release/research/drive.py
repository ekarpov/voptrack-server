#!/usr/bin/env python3
"""The Research gate's browser: a researcher sets up a study in the Console.

Usage: drive.py BASE CA OWNER_PASSWORD_FILE
"""
import json, http.cookiejar, re, ssl, sys, urllib.error, urllib.parse, urllib.request, os, subprocess

import sys as _ci_sys
from pathlib import Path as _CiPath
_ci_sys.path.insert(0, str(_CiPath(__file__).resolve().parents[2] / 'ci'))
from timing import retry_auth, timed_request, RetryBudget, phase, event

base, ca, owner_pw = sys.argv[1].rstrip("/"), sys.argv[2], open(sys.argv[3]).read().strip()
ctx = ssl.create_default_context(cafile=ca)


def say(what, outcome):
    print(f"  {what:<46} {outcome}", flush=True)


def fail(message):
    print(f"\nFAIL: {message}", file=sys.stderr)
    sys.exit(1)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


def browser():
    return urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx), NoRedirect,
                                       urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


@retry_auth
def request(opener, method, url, form=None):
    data = urllib.parse.urlencode(form).encode() if form is not None else None
    h = {"Content-Type": "application/x-www-form-urlencoded"} if form is not None else {}
    r = urllib.request.Request(url if url.startswith("http") else base + url, data=data, method=method, headers=h)
    try:
        resp = opener.open(r, timeout=30)
        return resp.status, resp.read().decode(), resp.headers
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace"), e.headers


def follow(opener, url, form=None, method="GET"):
    for _ in range(10):
        st, body, headers = request(opener, method, url, form=form)
        if st in (301, 302, 303) and headers.get("Location"):
            url, form, method = urllib.parse.urljoin(base + "/", headers["Location"]), None, "GET"
            continue
        # The default public-client flow has a CSRF-protected introductory
        # form. Submit it with persistence off; consent remains separately tested.
        if st == 200 and urllib.parse.urlparse(url).path == "/login" and 'class="web-login"' in body:
            token = re.search(r'name="csrf" value="([^"]+)"', body)
            if not token:
                fail("Console login introduction has no CSRF token")
            form, method = {"csrf": token.group(1)}, "POST"
            continue
        return st, body, url
    fail("too many redirects")


field = lambda p, n: re.search(r'name="%s"\s+value="([^"]*)"' % n, p).group(1)
csrf_of = lambda p: re.search(r'name="csrf" value="([^"]+)"', p).group(1)

# The researcher signs in approving server rights and no store.
researcher = browser()
st, page, url = follow(researcher, "/login")
st, approval, _ = request(researcher, "POST", "/oauth/authorize", form={
    "flow": field(page, "flow"), "csrf": field(page, "csrf"),
    "username": "owner", "password": owner_pw, "decision": "continue"})
st, _, headers = request(researcher, "POST", "/oauth/authorize", form={
    "flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow", "administer": "yes"})
follow(researcher, headers["Location"])
st, status_page, _ = request(researcher, "GET", "/backups")
expected_version=subprocess.check_output(["go","list","-m","-f","{{.Version}}","github.com/ekarpov/tessera"],cwd=os.path.abspath(os.path.join(os.path.dirname(__file__),"../..")),text=True).strip()
if st != 200 or ("data-server-version>"+expected_version+"<") not in status_page or "data-latest" not in status_page or "data-bundle-state" not in status_page or "Metadata authentication must be verified" not in status_page:
    fail("Research operator server status and archive-only readiness missing")
say("operator status and backup readiness", "verified version, last success, separate local/custody/kit scope")
st, page, _ = request(researcher, "GET", "/studies")
if st != 200 or "Create study" not in page:
    fail(f"the researcher cannot open Studies ({st})")
say("the researcher signs in to the Console", "Studies available")

# Guided setup: stage ownership, private unpublished text and explicit transitions.
def setup_form(slug, stage):
    status, body, _ = request(researcher, "GET", f"/studies/{slug}/setup/{stage}")
    if status != 200:
        fail(f"setup: cannot resume {stage} ({status})")
    return {"csrf": csrf_of(body), "revision": field(body, "revision")}, body


def setup_save(slug, stage, action="save", values=None):
    form, _ = setup_form(slug, stage)
    form.update(values or {})
    form["action"] = action
    status, body, headers = request(researcher, "POST", f"/studies/{slug}/setup/{stage}", form)
    if status != 303:
        fail(f"setup: {stage}/{action} failed ({status})")
    return follow(researcher, headers["Location"])


for guided in ("guided-study", "unfinished-study"):
    st, page, _ = request(researcher, "GET", "/studies")
    st, page, url = follow(researcher, "/studies", form={"csrf": csrf_of(page), "title": "Guided synthetic study", "slug": guided}, method="POST")
    if st != 200 or not url.endswith("/setup/basics"):
        fail("setup: creation did not enter Basics")
    setup_save(guided, "basics", "next", {"title": "Guided synthetic study", "run_by": ""})
    setup_save(guided, "collection", "next", {"catalogue": "com.voptrack.track.weight"})
    info = {"summary": "Guided purpose", "collected": "Guided data", "who_sees": "Guided team", "how_to_leave": "Guided leaving", "consent": "Unpublished synthetic consent", "material": "yes"}
    setup_save(guided, "information", "next", info)
    setup_save(guided, "recruitment", "back", {"join_mode": "invite"})
    old_form, _ = setup_form(guided, "basics")
    setup_save(guided, "basics", "save", {"title": "Guided synthetic study", "run_by": ""})
    _, body = setup_form(guided, "information")
    if any(value not in body for value in info.values() if value != "yes"):
        fail("setup: Basics cleared another stage's disclosures")
    setup_save(guided, "recruitment", "next", {"join_mode": "invite"})
    _, body = setup_form(guided, "review")
    if 'data-published-version>0<' not in body:
        fail("setup: navigation published consent")
    for _ in range(2):
        _, body = setup_form(guided, "review")
        if 'data-published-version>0<' not in body:
            fail("setup: review GET published consent")
    old_form.update(action="save", title="Stale retained title", run_by="")
    st, body, _ = request(researcher, "POST", f"/studies/{guided}/setup/basics", old_form)
    if st != 409 or "Stale retained title" not in body or "setup_stale" not in body:
        fail("setup: stale form overwrote or lost input")
    for suffix in (f"/study/{guided}", f"/api/v1/studies/{guided}"):
        st, body, _ = request(browser(), "GET", suffix)
        if st != 404 or "Unpublished synthetic consent" in body:
            fail("setup: draft leaked publicly")
    form, _ = setup_form(guided, "review")
    form.update(action="open", csrf="forged")
    st, _, _ = request(researcher, "POST", f"/studies/{guided}/setup/review", form)
    if st != 403:
        fail("setup: forged CSRF opened draft")
    form, _ = setup_form(guided, "review")
    form.update(action="open")
    st, _, _ = request(researcher, "POST", f"/studies/{guided}/setup/review", form)
    if st != 422:
        fail("setup: opening bypassed explicit consent publication")
    if guided == "unfinished-study":
        continue  # kept unpublished across restart and full archive restoration
    setup_save(guided, "review", "publish")
    setup_save(guided, "review", "publish")
    _, body = setup_form(guided, "review")
    if 'data-published-version>1<' not in body:
        fail("setup: explicit publication duplicated consent")
    st, body, url = setup_save(guided, "review", "open")
    if not url.endswith("/setup/share") or "data-setup-complete" not in body or "data-app-link" in body or f'/study/{guided}/join' in body:
        fail("setup: invite-only completion exposed unrestricted recruitment")
    for target in ("participants", "dashboard", "policy"):
        if f'/studies/{guided}/{target}' not in body:
            fail("setup: completion missing next actions")
say("guided study setup", "saved stages, private draft, explicit publication and opening")

st, page, url = follow(researcher, "/studies", form={"csrf": csrf_of(page), "title": "Sleep and mood", "slug": "sleep-mood"}, method="POST")
if not url.endswith("/studies/sleep-mood/setup/basics"):
    fail(f"creating a study did not open its editor ({url})")
say("a study is created", "as a draft")

public = browser()
st, _, _ = request(public, "GET", "/study/sleep-mood")
if st != 404:
    fail(f"a draft study is readable by the public ({st})")
say("the draft, from outside", "not found")

edit = {"csrf": csrf_of(page), "title": "Sleep and mood", "summary": "How sleep relates to mood.",
        "collected": "Hours slept, a daily mood score.", "who_sees": "The research team only, pseudonymously.",
        "how_to_leave": "Remove your data from the Console at any time.", "join_mode": "open",
        "status": "open", "consent": "", "material": "yes", "run_by": "Synthetic research team"}


def design(slug, tracks, catalogue=()):
    """Fill in the track designer as a researcher would: a name per track, and
    per value a name, a kind, and for numbers a unit and how it adds up."""
    st, page, _ = request(researcher, "GET", f"/studies/{slug}/tracks")
    form = {"csrf": csrf_of(page), "revision": field(page, "revision") if 'name="revision"' in page else "", "catalogue": list(catalogue)}
    for i, (name, values) in enumerate(tracks):
        form[f"t{i}.present"], form[f"t{i}.name"] = "1", name
        for j, value in enumerate(values):
            p = f"t{i}.f{j}."
            form[p + "present"], form[p + "name"], form[p + "kind"] = "1", value[0], value[1]
            form[p + "unit"] = value[2] if len(value) > 2 else ""
            form[p + "meaning"] = value[3] if len(value) > 3 else ""
    data = urllib.parse.urlencode(form, doseq=True).encode()
    r = urllib.request.Request(base + f"/studies/{slug}/tracks", data=data, method="POST",
                               headers={"Content-Type": "application/x-www-form-urlencoded"})
    try:
        resp = researcher.open(r)
        st, body = resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        st, body = e.code, e.read().decode(errors="replace")
    return st, body


# A value's unit on text is refused, with the reason, and nothing opens on it.
st, page = design("sleep-mood", [("Sleep", [("Hours slept", "text", "h")])])
if 'data-error="form_numeric_only"' not in page:
    fail(f"the designer did not report refusal of a unit on a text value ({st})")
st, page = design("sleep-mood", [("Sleep", [("Hours slept", "number", "h", "sample"), ("Quality", "wholeNumber")]),
                                 ("Mood", [("Score", "wholeNumber", "", "sample")])],
                  catalogue=["com.voptrack.track.weight"])
if st not in (302, 303):
    fail(f"a valid design was not saved ({st})")
st, page, _ = request(researcher, "GET", "/studies/sleep-mood/tracks")
schema = re.search(r"<pre data-schema>(.*?)</pre>", page, re.S)
import html as _html
installed = json.loads(_html.unescape(schema.group(1))) if schema else []
ids = [t["id"] for t in installed]
if ids != ["com.voptrack.subject", "com.voptrack.source", "com.voptrack.track", "study.sleep_mood.sleep",
           "study.sleep_mood.mood", "com.voptrack.track.weight"] or \
        installed[3]["fields"][0].get("annotations") != {"meaning": "sample", "unit": "h", "unit_field": "hours_slept_unit", "unit_source": "record"}:
    fail(f"the schema shown for the design is {ids}")
say("tracks designed, not written as a schema", "Sleep, Mood and Weight; schema on request")

st, legacy_form, _ = request(researcher, "GET", "/studies/sleep-mood/edit")
edit["revision"] = field(legacy_form, "revision")
st, page, url = follow(researcher, "/studies/sleep-mood/edit", form=edit, method="POST")
if 'data-published-version>0<' not in page or "data-readiness" not in page:
    fail("the study opened without consent text")
say("opening without consent text", "refused, with the reason")

edit.update(csrf=csrf_of(page), revision=field(page, "revision"), consent="I agree to share my sleep and mood records with the study.")
st, page, url = follow(researcher, "/studies/sleep-mood/edit", form=edit, method="POST")
if 'data-published-version>0<' not in page:
    fail("setup: legacy draft save published consent")
setup_save("sleep-mood", "review", "publish")
setup_save("sleep-mood", "review", "open")

st, page, _ = request(public, "GET", "/study/sleep-mood")
sections = dict(re.findall(r'data-section="([a-z_]+)">([^<]*)<', page))
if st != 200 or sections.get("summary") != edit["summary"] or sections.get("consent") != edit["consent"] \
        or sections.get("how_to_leave") != edit["how_to_leave"] or 'data-consent-version>1<' not in page:
    fail(f"the public page does not show the study as written ({st})")
say("opened; the public page", "every section, consent version 1")

st, page, _ = request(researcher, "GET", "/studies/sleep-mood/edit")
edit.update(csrf=csrf_of(page), revision=field(page, "revision"), consent="I agree, and I understand I can leave at any time.")
follow(researcher, "/studies/sleep-mood/edit", form=edit, method="POST")
st, page, _ = request(public, "GET", "/study/sleep-mood")
if 'data-consent-version>2<' not in page or "leave at any time" not in page:
    fail("changing the consent did not publish version 2")
say("the consent text changes", "version 2 published, version 1 kept")

st, _, _ = request(public, "GET", "/members")
st2, _, _ = request(researcher, "GET", "/members")
if st2 == 200 and "Invite a member" in _:
    fail("a research server offers household member invitations")
say("household pages on a research server", "not offered")

# ---- opening creates the study's issuer; tracks are then fixed ----------------

st, page, _ = request(researcher, "GET", "/studies/sleep-mood/tracks")
if 'class="designer"' in page or 'data-state="locked"' not in page:
    fail("after opening, the study's tracks are still editable")
st, page = design("sleep-mood", [("Sleep", [("Hours slept", "wholeNumber")])])
if 'data-error="study_tracks_locked"' not in page:
    fail("changing an open study's tracks was not refused")
say("tracks, once the study is open", "fixed")

# ---- a person joins: consent, a pseudonym, a password -------------------------

import secrets, uuid, base64, hashlib


def join(opener, path):
    st, page, url = follow(opener, path)
    if st != 200 or 'name="agree"' not in page:
        return st, None, None
    form = {"csrf": csrf_of(page), "version": field(page, "version"), "code": field(page, "code"), "agree": "yes"}
    st, redeem, url = follow(opener, path.split("?")[0], form=form, method="POST")
    return st, redeem, url


person = browser()
st, redeem, url = join(person, "/study/sleep-mood/join")
if not redeem or "/oauth/invitation" not in url:
    fail(f"consenting did not lead to Tessera's account page ({st} {url})")
# The name is Tessera's to choose: the page offers no field to type one and
# marks the pseudonym it chose (data-account, alpha.31) -- read by structure,
# since the page speaks the browser's language.
assigned = re.search(r'data-account="(p-[^"]+)"', redeem)
if not assigned or re.search(r'<input[^>]*name="account"[^>]*(type="text"|autocomplete="username")', redeem):
    fail("the account page lets the participant type a name, or states no pseudonym")
pseudonym = assigned.group(1)
password = secrets.token_urlsafe(18)
form = {k: v for k, v in re.findall(r'name="(code|account)" value="([^"]*)"', redeem)}
form["password"] = password
st, done, _ = request(person, "POST", "/oauth/invitation", form=form)
if st != 201 or pseudonym not in done:
    fail(f"setting a password answered {st}")
if not re.search(r'data-recovery-code="[A-Z0-9-]+"', done):
    fail("the participant was not shown a recovery code: nobody else can reset their password")
say("a person consents and joins", f"as {pseudonym}, choosing only a password; shown a recovery code")


def app_token(person_name, pw, grants):
    app = browser()
    redirect = "com.voptrack.gate:/oauth/callback"
    r = urllib.request.Request(base + "/oauth/register", data=json.dumps({
        "client_name": "Voptrack (gate)", "redirect_uris": [redirect], "token_endpoint_auth_method": "none",
        "grant_types": ["authorization_code"], "response_types": ["code"]}).encode(),
        headers={"Content-Type": "application/json"}, method="POST")
    client = json.loads(app.open(r).read())["client_id"]
    verifier = secrets.token_urlsafe(48)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
    q = urllib.parse.urlencode({"response_type": "code", "client_id": client, "redirect_uri": redirect,
                                "state": "s", "code_challenge": challenge, "code_challenge_method": "S256", "prompt": "consent"})
    st, page, _ = request(app, "GET", "/oauth/authorize?" + q)
    st, approval, _ = request(app, "POST", "/oauth/authorize", form={
        "flow": field(page, "flow"), "csrf": field(page, "csrf"),
        "username": person_name, "password": pw, "decision": "continue"})
    form = {"flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow",
            "dataset": re.search(r'<option\s+value="([^"]+)"', approval).group(1)}
    form.update(grants)
    st, _, headers = request(app, "POST", "/oauth/authorize", form=form)
    code = urllib.parse.parse_qs(urllib.parse.urlparse(headers["Location"]).query)["code"][0]
    st, body, _ = request(app, "POST", "/oauth/token", form={
        "grant_type": "authorization_code", "client_id": client, "code": code,
        "redirect_uri": redirect, "code_verifier": verifier})
    return json.loads(body)["access_token"]


def api(token, method, path, body=None):
    h = {"Authorization": "Bearer " + token, "Content-Type": "application/json"}
    # Erase has no receipt to replay; POST queries are reads and forbid this header.
    if method != "GET" and not path.endswith((":erase", ":query")):
        h["Idempotency-Key"] = str(uuid.uuid4())
    r = urllib.request.Request(base + path, data=json.dumps(body).encode() if body is not None else None, method=method, headers=h)
    try:
        resp = browser().open(r)
        return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except ValueError:
            return e.code, {}


token = app_token(pseudonym, password, {"read": "yes", "write": "yes"})
st, listing = api(token, "GET", "/v1/datasets")
store = listing["datasets"][0]
st, types = api(token, "GET", f"/v1/datasets/{store['id']}/meta")
names = sorted(t["name"] for t in types.get("types", []))
if store["display_name"] != pseudonym or names != ["Mood", "Sleep", "model.source", "model.subject", "model.track",
                                                   "strings.track.weight"]:
    fail(f"the participant's store is {store['display_name']!r} with tracks {names}")
sleep = "study.sleep_mood.sleep"
st, _ = api(token, "POST", f"/v1/datasets/{store['id']}/objects/{sleep}", {"fields": {
    "timestamp": {"type": "date_time", "value": "2026-09-20T07:00:00.000Z"},
    "hours_slept": {"type": "float", "value": 7.5}, "hours_slept_unit": {"type": "text", "value": "h"},
    "quality": {"type": "integer", "value": 4}}})
if st != 201:
    fail(f"the participant recording into Sleep answered {st}")
st, _ = api(token, "POST", f"/v1/datasets/{store['id']}/meta", {"type": {
    "id": "mine.diary", "parent_id": None, "name": "Diary",
    "fields": [{"id": "text", "meta_id": "mine.diary", "name": "Text", "type": "text"}]}})
if st != 403:
    fail(f"the participant defining a track of their own answered {st}, want 403")
say("the participant's store", f"named {pseudonym}: Subject, Source, the root, the tracks, Weight")
say("recording into Sleep; defining a track", "201; refused 403")

st, page, _ = request(researcher, "GET", "/studies/sleep-mood/participants")
rows = re.findall(r'<td data-account>([^<]+).*?<td data-state="[^"]+">.*?</td><td>(\d+)', page, re.S)
if rows != [(pseudonym, "2")]:
    fail(f"the participant list shows {rows}, want [({pseudonym!r}, '2')] -- the version they agreed to")
say("the researcher's participant list", f"{pseudonym}, consent version 2")

# ---- the researcher's dashboard across participants ---------------------------

from decimal import Decimal
second = browser()
st, redeem2, _ = join(second, "/study/sleep-mood/join")
pseudonym2 = re.search(r'data-account="(p-[^"]+)"', redeem2).group(1)
password2 = secrets.token_urlsafe(18)
form2 = {k: v for k, v in re.findall(r'name="(code|account)" value="([^"]*)"', redeem2)}
form2["password"] = password2
request(second, "POST", "/oauth/invitation", form=form2)
token2 = app_token(pseudonym2, password2, {"read": "yes", "write": "yes"})
store2 = api(token2, "GET", "/v1/datasets")[1]["datasets"][0]["id"]
for hours, quality in (("6.25", 3), ("8.5", 5)):
    st, _ = api(token2, "POST", f"/v1/datasets/{store2}/objects/{sleep}", {"fields": {
        "timestamp": {"type": "date_time", "value": "2026-09-21T07:00:00.000Z"},
        "hours_slept": {"type": "float", "value": float(hours)}, "hours_slept_unit": {"type": "text", "value": "h"},
        "quality": {"type": "integer", "value": quality}}})
    if st != 201:
        fail(f"the second participant recording answered {st}")

# Policy is explicitly configured, separate from study/consent edits.
policy_path = "/studies/sleep-mood/policy"
qbase = {"scope": "study:sleep-mood", "type": sleep, "field": "hours_slept", "measure": "average", "group": "none"}
q = urllib.parse.urlencode(qbase)
# Collection operations do not require an analytical suppression threshold.
st, page, _ = request(researcher, "GET", "/studies/sleep-mood/dashboard?period=all")
if st != 200 or 'data-monitoring' not in page or 'data-enrolled>2<' not in page or '3</strong><span>visible records' not in page:
    fail("collection overview: exact record count or unique enrolment is wrong")
if 'data-value>' in page or 'Saved study answers' in page or '/panel?' in page:
    fail("collection overview exposes recorded answer UI")
for actor in (researcher, second, browser()):
    for retired in ("/panel?"+q, "/dashboards/study:sleep-mood/old"):
        if request(actor,"GET",retired)[0] != 404:
            fail("retired Research value page is exposed")
    if request(actor,"POST","/dashboards/add",form={"csrf":"forged"})[0] != 404:
        fail("retired Research save action is exposed")
for route in ("dashboard", "participants", "exports"):
    for actor in (second,browser()):
        if request(actor,"GET","/studies/sleep-mood/"+route)[0] not in (303,403):
            fail("unauthorized collection operations access")
st, export_page, _ = request(researcher,"GET","/studies/sleep-mood/exports")
if st != 200 or 'data-export="csv"' not in export_page or 'data-export="json"' not in export_page or 'recorded values' not in export_page:
    fail("dedicated export screen missing scope and download controls")
say("collection-first Research UI", "exact counts; distinct participants; technical-only pages; retired analytical UI; authorized exports")

# Retain analytical CSV policy qualification. HTML analytics and saved dashboard
# lifecycle are retired; their renderer/definition regressions remain source tests.
import csv, io
def analytical_csv(query):
    status, body, _ = request(researcher,"GET","/panel.csv?"+query)
    parsed=list(csv.DictReader(io.StringIO("\n".join(line for line in body.splitlines() if not line.startswith("#"))))) if status==200 else []
    return status,parsed,body
status, rows, body=analytical_csv(q)
if status!=200 or not rows or rows[0]['status']!='unconfigured':fail("suppression: missing policy CSV bypass")
st, policy, _ = request(researcher,"GET",policy_path)
if 'placeholder="5"' not in policy or 'name="threshold" inputmode="numeric" value=""' not in policy:fail("suppression: suggestion is not explicit")
if request(second,"POST",policy_path,form={"csrf":csrf_of(policy),"threshold":"2","revision":"0"})[0] not in (303,403):fail("suppression: participant changed policy")
if request(researcher,"POST",policy_path,form={"csrf":"forged","threshold":"2","revision":"0"})[0]!=403:fail("suppression: forged CSRF changed policy")
for invalid in ("1","2.5","2147483648","99999999999999999999"):
    status,invalid_page,_=request(researcher,"POST",policy_path,form={"csrf":csrf_of(policy),"threshold":invalid,"revision":"0"})
    if status!=422 or 'data-error="policy_invalid"' not in invalid_page or f'value="{invalid}"' not in invalid_page:fail("suppression: invalid threshold or lost input")
st,policy,_=follow(researcher,policy_path,form={"csrf":csrf_of(policy),"threshold":"3","revision":"0"},method="POST")
if st!=200 or field(policy,"revision")!='1':fail("suppression: explicit configuration failed")
status,rows,body=analytical_csv(q)
if status!=200 or rows[0]['status']!='suppressed' or rows[0]['value']:fail("suppression: records counted as participants")
status,stale,_=request(researcher,"POST",policy_path,form={"csrf":csrf_of(policy),"threshold":"4","revision":"0"})
if status!=422 or 'policy_stale' not in stale:fail("suppression: stale update")
status,unconfirmed,_=request(researcher,"POST",policy_path,form={"csrf":csrf_of(policy),"threshold":"2","revision":"1"})
if status!=422 or 'policy_lower_required' not in unconfirmed:fail("suppression: unconfirmed lowering")
st,policy,_=follow(researcher,policy_path,form={"csrf":csrf_of(policy),"threshold":"2","revision":"1","confirm_lower":"yes"},method="POST")
if st!=200 or field(policy,"revision")!='2':fail("suppression: confirmed lowering failed")
q=urllib.parse.urlencode(dict(qbase,measure="sum",group="store"))
status,rows,body=analytical_csv(q)
if status!=200 or any(row['value'] or row['group'] for row in rows):fail("suppression: participant CSV value or label exposed")
q2=urllib.parse.urlencode(qbase)
status,rows,body=analytical_csv(q2)
if status!=200 or len(rows)!=1 or rows[0]['value']!='7.416666666667' or rows[0]['records'] or rows[0]['values'] or rows[0]['status']!='released':fail("suppression: weighted CSV precision/status changed")
q3=urllib.parse.urlencode(dict(qbase,measure="sum",group="period",period="day",tz="UTC",**{"from":"2026-09-20","to":"2026-09-21"}))
status,rows,body=analytical_csv(q3)
if status!=200 or any(row['value'] for row in rows):fail("suppression: small filtered day released")
qmonth=urllib.parse.urlencode(dict(qbase,measure="sum",group="period",period="month",tz="UTC"))
status,rows,body=analytical_csv(qmonth)
if status!=200 or [row['value'] for row in rows]!=['22.25']:fail("suppression: monthly CSV sum changed")
say("study suppression policy and CSV", "explicit K; records != participants; stale/lower/CSRF; exact weighted CSV")

# ---- the study's export: pseudonymous, with the consent version -----------------

import csv, io
st, body, _ = request(researcher, "GET", "/studies/sleep-mood/export.csv")
rows = list(csv.DictReader(io.StringIO(body)))
hours = sorted((r["participant"], r["value"]) for r in rows if r["field"] == "hours_slept")
want = sorted([(pseudonym, "7.5"), (pseudonym2, "6.25"), (pseudonym2, "8.5")])
if st != 200 or hours != want or {r["consent_version"] for r in rows} != {"2"} or \
        not any(r["field"] == "timestamp" and r["value"].startswith("2026-09-2") for r in rows):
    fail(f"the CSV export answered {st} with hours {hours}, want {want}, dated, consent version 2")
st, body, _ = request(researcher, "GET", "/studies/sleep-mood/export.json")
ex = json.loads(body) if st == 200 else {}
people = {p["pseudonym"]: p for p in ex.get("participants", [])}
sleeps = people.get(pseudonym2, {}).get("tracks", {}).get(sleep, {}).get("records", [])
if sorted(r["hours_slept"] for r in sleeps) != [6.25, 8.5] or people.get(pseudonym, {}).get("status") != "taking part" \
        or [v["version"] for v in ex.get("consent_versions", [])] != [1, 2]:
    fail(f"the JSON export answered {st}: participants {sorted(people)}, {pseudonym2}'s sleep {sleeps}")
st, _, _ = request(second, "GET", "/studies/sleep-mood/export.csv")
st2, _, _ = request(browser(), "GET", "/studies/sleep-mood/export.json")
if st in (200,) or st2 in (200,):
    fail(f"a participant or a stranger downloaded the study's export ({st}, {st2})")
say("the study's export, CSV and JSON", "exact values, pseudonyms, consent version; others refused")

# ---- enrolment and completeness on the participant list ------------------------

st, page, _ = request(researcher, "GET", "/studies/sleep-mood/participants?period=all")
accounts=re.findall(r'<td data-account>([^<]+)',page)
counts=re.findall(r'data-records="%s">([0-9]+)<' % re.escape(sleep),page)
if len(set(accounts))!=2 or sorted(counts)!=['1','2'] or 'data-count="active">2<' not in page or 'Latest recorded event' not in page:
    fail("participant technical collection counts or distinct enrolment are wrong")
say("participant collection activity", "unique roster; per-collection counts; latest recorded event")

st, _, _ = request(browser(), "GET", "/panel?" + q)
anon_ok = st == 303
st, _, _ = request(second, "GET", "/panel?" + q)
if request(browser(),"GET","/panel?"+q)[0]!=404 or st!=404:
    fail(f"a study panel answered a signed-out or participant browser with {st}")
say("the study dashboard, for anyone else", "refused")

# ---- an invite-only study ------------------------------------------------------

st, page, _ = request(researcher, "GET", "/studies")
follow(researcher, "/studies", form={"csrf": csrf_of(page), "title": "Diet", "slug": "diet"}, method="POST")
st, page, _ = request(researcher, "GET", "/studies/diet/edit")
design("diet", [("Meal", [("Calories", "wholeNumber", "kcal", "additive")])])
st, page, _ = request(researcher, "GET", "/studies/diet/edit")
diet = dict(edit, csrf=csrf_of(page), revision=field(page, "revision"), title="Diet", join_mode="invite", status="open",
            consent="I agree to share my meals.")
st, page, _ = follow(researcher, "/studies/diet/edit", form=diet, method="POST")
setup_save("diet", "review", "publish")
setup_save("diet", "review", "open")
st, _, _ = join(browser(), "/study/diet/join")
if st != 403:
    fail(f"joining an invite-only study with no link answered {st}, want 403")
st, page, _ = request(researcher, "GET", "/studies/diet/participants")
st, page, _ = follow(researcher, "/studies/diet/codes", form={"csrf": csrf_of(page)}, method="POST")
link = re.search(r'<code class="link" data-join-link>([^<]+)</code>', page).group(1).replace("&amp;", "&")
path = link.replace(base, "")
st, redeem, url = join(browser(), path)
if not redeem or "/oauth/invitation" not in url:
    fail(f"joining with the researcher's link did not reach Tessera's account page ({st})")
st, again, _ = follow(browser(), path)
if st != 403 or "already been used" not in again:
    fail("used invitation web message: spent link did not show its distinct refusal")
say("invite-only: no link; a link; the link again", "refused; joins; refused")

# ---- participants withdraw: through the Console, and from the app ---------------

def states():
    status,body,_=request(researcher,"GET","/studies/sleep-mood/participants?period=all")
    if status!=200:fail("participant status page unavailable")
    return {account:(kind,re.sub(r'<[^>]+>','',shown).strip()) for account,kind,shown in re.findall(r'<td data-account>([^<]+).*?<td data-state="([^"]+)">(.*?)</td>',body,re.S)}



if {a: k for a, (k, _) in states().items()} != {pseudonym: "state_active", pseudonym2: "state_active"}:
    fail(f"before anyone withdrew, the participant list says {states()}")


def console_consent(b, page, person, pw, grants):
    st, approval, _ = request(b, "POST", "/oauth/authorize", form={
        "flow": field(page, "flow"), "csrf": field(page, "csrf"),
        "username": person, "password": pw, "decision": "continue"})
    form = {"flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow",
            "dataset": re.search(r'<option\s+value="([^"]+)"', approval).group(1)}
    form.update(grants)
    st, _, headers = request(b, "POST", "/oauth/authorize", form=form)
    if st != 303:
        fail(f"{person}: approving answered {st}")
    return follow(b, headers["Location"])


# ---- the researcher blocks a participant, and lets them back ----------------------

page = request(researcher, "GET", "/studies/sleep-mood/participants")[1]
st, _, _ = follow(researcher, f"/studies/sleep-mood/participants/{pseudonym2}/suspend", form={"csrf": csrf_of(page)}, method="POST")
blocked = api(token2, "GET", "/v1/datasets")[0]
if blocked != 401 or states().get(pseudonym2, ("",))[0] != "state_suspended":
    fail(f"after suspending, {pseudonym2}'s app answered {blocked} and the list says {states().get(pseudonym2)}")
page = request(researcher, "GET", "/studies/sleep-mood/participants")[1]
follow(researcher, f"/studies/sleep-mood/participants/{pseudonym2}/reinstate", form={"csrf": csrf_of(page)}, method="POST")
back = api(token2, "GET", "/v1/datasets")[0]
if back != 200 or states().get(pseudonym2, ("",))[0] != "state_active":
    fail(f"after reinstating, {pseudonym2}'s app answered {back} and the list says {states().get(pseudonym2)}")
page = request(researcher, "GET", "/studies/sleep-mood/participants")[1]
st, _, _ = request(researcher, "POST", "/studies/sleep-mood/participants/analysis/suspend", form={"csrf": csrf_of(page)})
if st != 404:
    fail(f"suspending an account that never joined the study answered {st}, want 404")
say("researcher suspends a participant, reinstates", "their app: 401, then 200; others: 404")

# The second participant, in the Console: sign in, Remove my data, tick erase.
leaver = browser()
st, page, url = follow(leaver, "/login")
console_consent(leaver, page, pseudonym2, password2, {"read": "yes"})
st, page, _ = request(leaver, "GET", "/leave")
form = re.search(r'<form method="post" action="/leave/start"[^>]*>(.*?)</form>', page, re.S)
if not form:
    fail("a participant signed in to the Console has no Remove my data form")
st, page, _ = follow(leaver, "/leave/start", form=dict(re.findall(r'name="(csrf|dataset)" value="([^"]*)"', form.group(1))), method="POST")
st, page, url = console_consent(leaver, page, pseudonym2, password2, {"read": "yes", "erase": "yes"})
if not url.endswith("/leave/confirm"):
    fail(f"the participant's erase approval did not reach the confirm page ({url})")
st, page, _ = request(leaver, "POST", "/leave/confirm", form={"csrf": csrf_of(page), "dataset": field(page, "dataset")})
if st != 200 or "has been removed" not in page:
    fail(f"the participant confirming removal answered {st}")

# The first, from the app: erase through the protocol, never touching the Console.
erase_token = app_token(pseudonym, password, {"read": "yes", "erase": "yes"})
st, _ = api(erase_token, "POST", f"/v1/datasets/{store['id']}:erase", {"confirm": store["id"]})
if st not in (200, 204):
    fail(f"the participant erasing from the app answered {st}: {_}")
# The installation that erased keeps exactly what it held: a participant never
# holds schema, and erasing must not give it to them (Tessera finding 42).
st, listing = api(erase_token, "GET", "/v1/datasets")
replacement = next((d for d in listing.get("datasets", []) if d.get("replaced_dataset_id") == store["id"]), None)
granted = {k for k, v in (replacement or {}).get("access", {}).items() if v is True}
if st != 200 or not replacement or granted != {"read", "erase"}:
    fail(f"after erasing, the participant's installation sees {st} {replacement and replacement.get('access')}; "
         "want the replacement, named as such, with only read and erase")
say("the eraser's grants on the replacement", "exactly read and erase, as before -- never schema")

now = states()
kind, shown = now.get(pseudonym2, ("", ""))
if kind != "state_withdrawn" or not re.search(r"\d{4}-\d{2}-\d{2}$", shown):
    fail(f"the participant who withdrew in the Console is listed as {now.get(pseudonym2)}; want withdrawn, dated")
say("a participant withdraws in the Console", "listed as withdrawn, with the date")

# Tessera gives an erased store a new identity that only the eraser holds, so
# the study's reader no longer reads it: the Console lists them as withdrawn,
# undated (it did not see it happen), and the dashboard is empty.
kind, shown = now.get(pseudonym, ("", ""))
if kind != "state_withdrawn" or re.search(r"\d{4}-", shown):
    fail(f"the participant who erased from the app is listed as {now.get(pseudonym)}; want withdrawn, undated")
st, page, _ = request(researcher, "GET", "/panel?" + q)
# With nobody left there are no tracks to read, and the panel says so (404).
if st not in (200, 404) or re.findall(r'data-value>([^<]*)<', page):
    fail(f"after both withdrew, the study dashboard answered {st} or still shows values")
say("a participant withdraws from the app", "listed as withdrawn, undated; dashboard empty")

# ---- closing a study closes it to everyone at once -------------------------------

late = browser()
st, late_redeem, _ = join(late, "/study/sleep-mood/join")
if not late_redeem:
    fail("a third person could not reach the account page before the study closed")
st, page, _ = request(researcher, "GET", "/studies/sleep-mood/edit")
close = dict(edit, csrf=csrf_of(page), revision=field(page, "revision"), status="closed")
import html
for name in ("consent",):
    close[name] = html.unescape(re.search(r'<textarea name="%s"[^>]*>([^<]*)</textarea>' % name, page).group(1))
st, page, url = follow(researcher, "/studies/sleep-mood/edit", form=close, method="POST")
if "?error=" in url or 'value="closed" selected' not in page:
    fail(f"closing the study did not take ({urllib.parse.unquote(url.split('error=')[-1]) if 'error=' in url else url})")
form3 = {k: v for k, v in re.findall(r'name="(code|account)" value="([^"]*)"', late_redeem)}
form3["password"] = secrets.token_urlsafe(18)
st, _, _ = request(late, "POST", "/oauth/invitation", form=form3)
st2, _, _ = request(browser(), "GET", "/study/sleep-mood/join")
if st == 201 or st2 != 404:
    fail(f"after closing, an outstanding invitation answered {st} and the join page {st2}")
say("closing: a half-finished invitation; joining", "refused; gone")

st, page, _ = request(researcher, "GET", "/studies/sleep-mood/edit")
reopen = dict(close, csrf=csrf_of(page), revision=field(page, "revision"), status="open")
follow(researcher, "/studies/sleep-mood/edit", form=reopen, method="POST")
st, redeem, url = join(browser(), "/study/sleep-mood/join")
if not redeem or "/oauth/invitation" not in url:
    fail("after reopening, joining did not work again")
say("reopening", "people can join again")

# ---- joining from the app: the study API ------------------------------------------


def japi(method, path, body=None, token=None):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = "Bearer " + token
    r = urllib.request.Request(base + path, data=json.dumps(body).encode() if body is not None else None,
                               method=method, headers=h)
    try:
        resp = browser().open(r)
        return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except ValueError:
            return e.code, {}


st, study = japi("GET", "/api/v1/studies/sleep-mood")
sleep_track = next((t for t in study.get("tracks", []) if t["name"] == "Sleep"), {})
if st != 200 or study.get("consent", {}).get("version") != 2 or study.get("how_to_leave") != edit["how_to_leave"] \
        or {"name": "Hours slept", "kind": "number", "unit": "h", "meaning": "sample"} not in sleep_track.get("values", []):
    fail(f"the study's JSON ({st}) is not what its page shows: {study}")
st2, _ = japi("GET", "/api/v1/studies/no-such-study")
if st2 != 404:
    fail(f"a study that does not exist answered {st2}")
st, stale = japi("POST", "/api/v1/studies/sleep-mood/join", {"version": 1})
if st != 409 or stale.get("error", {}).get("code") != "stale_version" or stale["error"].get("current_version") != 2:
    fail(f"joining with consent version 1 answered {st} {stale}; want 409 stale_version, current 2")
st, joined = japi("POST", "/api/v1/studies/sleep-mood/join", {"version": 2})
if st != 200 or not joined.get("redeem_url") or not joined.get("account", "").startswith("p-"):
    fail(f"joining from the app answered {st} {joined}")
# The app opens redeem_url; the person chooses a password on Tessera's page.
app_person, app_pw = browser(), secrets.token_urlsafe(18)
st, redeem, _ = follow(app_person, joined["redeem_url"])
form = {k: v for k, v in re.findall(r'name="(code|account)" value="([^"]*)"', redeem)}
form["password"] = app_pw
st, _, _ = request(app_person, "POST", "/oauth/invitation", form=form)
if st != 201:
    fail(f"redeeming the app's invitation answered {st}")
app_tok = app_token(joined["account"], app_pw, {"read": "yes", "write": "yes"})
st, mine = japi("GET", "/api/v1/studies/sleep-mood/consent", token=app_tok)
if st != 200 or [c["version"] for c in mine.get("consents", [])] != [2]:
    fail(f"the participant reading their own consent answered {st} {mine}")
st, _ = japi("GET", "/api/v1/studies/sleep-mood/consent")
st2, _ = japi("GET", "/api/v1/studies/sleep-mood/consent", token="not-a-token")
if (st, st2) != (401, 401):
    fail(f"reading a consent without a valid token answered {st}, {st2}")
say("joining from the app, through the API", "shown, stale refused, joined, own consent read")

# Invite-only from the app: the code works once, and only with it.
st, page, _ = request(researcher, "GET", "/studies/diet/participants")
st, page, _ = follow(researcher, "/studies/diet/codes", form={"csrf": csrf_of(page)}, method="POST")
app_link = re.search(r'<code class="link" data-join-app-link>([^<]+)</code>', page)
app_q = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(html.unescape(app_link.group(1))).query)) if app_link else {}
if app_q.get("product") != "study" or app_q.get("study") != "diet" or not app_q.get("code") or app_q.get("server") != base:
    fail(f"the invitation's app link carries {app_q}")
st, _ = japi("POST", "/api/v1/studies/diet/join", {"version": 1})
st2, ok_join = japi("POST", "/api/v1/studies/diet/join", {"version": 1, "code": app_q["code"]})
st3, _ = japi("POST", "/api/v1/studies/diet/join", {"version": 1, "code": app_q["code"]})
if (st, st2, st3) != (403, 200, 403):
    fail(f"invite-only from the app: no code {st}, the code {st2}, the code again {st3}; want 403, 200, 403")
st, page, _ = request(browser(), "GET", "/study/sleep-mood")
if "product=study" not in page or "study=sleep-mood" not in page:
    fail("the open study's page does not offer joining with the app")
say("invite-only from the app; the page's app link", "code works once; product=study&study=…")

# ---- panels by consent version --------------------------------------------------
# The app's participant (version 2) records 4 hours; the researcher publishes
# version 3; a new participant joins under it and records 6. Grouped by
# consent version, the study shows v2 = 4 and v3 = 6.


def record_sleep(token, hours):
    store_id = json.loads(request_json_listing(token))["datasets"][0]["id"]
    st, _ = api(token, "POST", f"/v1/datasets/{store_id}/objects/{sleep}", {"fields": {
        "timestamp": {"type": "date_time", "value": "2026-09-25T07:00:00.000Z"},
        "hours_slept": {"type": "float", "value": hours}, "hours_slept_unit": {"type": "text", "value": "h"}}})
    if st != 201:
        fail(f"recording {hours} hours answered {st}")


def request_json_listing(token):
    return json.dumps(api(token, "GET", "/v1/datasets")[1])


record_sleep(app_tok, 4.0)
st, page, _ = request(researcher, "GET", "/studies/sleep-mood/edit")
v3 = dict(reopen, csrf=csrf_of(page), revision=field(page, "revision"), consent="Version three: the same study, clearer wording.")
follow(researcher, "/studies/sleep-mood/edit", form=v3, method="POST")
st, joined3 = japi("POST", "/api/v1/studies/sleep-mood/join", {"version": 3})
if st != 200:
    fail(f"joining under consent version 3 answered {st} {joined3}")
p3, pw3 = browser(), secrets.token_urlsafe(18)
st, redeem, _ = follow(p3, joined3["redeem_url"])
form = {k: v for k, v in re.findall(r'name="(code|account)" value="([^"]*)"', redeem)}
form["password"] = pw3
request(p3, "POST", "/oauth/invitation", form=form)
record_sleep(app_token(joined3["account"], pw3, {"read": "yes", "write": "yes"}), 6.0)
q = urllib.parse.urlencode({"scope": "study:sleep-mood", "type": sleep, "field": "hours_slept", "measure": "sum", "group": "cohort"})
status,rows,body=analytical_csv(q)
if status!=200 or any(row['value'] or row['group'] for row in rows):fail("suppression: small latest-agreement cohort released")
q=urllib.parse.urlencode({"scope":"study:sleep-mood","type":sleep,"field":"hours_slept","group":"spread"})
status,rows,body=analytical_csv(q)
if status!=200 or any(row['value'] for row in rows):fail("suppression: small exact distribution released")
say("cohort and distribution CSV", "latest agreement groups and small categories withheld")

# ---- material consent, agreement renewal, withdrawal and export history ---------
import datetime, time
consent_path = "/api/v1/studies/sleep-mood/consent"


def publish_consent(text, material):
    st, page, _ = request(researcher, "GET", "/studies/sleep-mood/edit")
    form = dict(v3, csrf=csrf_of(page), revision=field(page, "revision"), consent=text)
    if material:
        form["material"] = "yes"
    else:
        form.pop("material", None)
    st, page, url = follow(researcher, "/studies/sleep-mood/edit", form=form, method="POST")
    if st != 200 or "?error=" in url:
        fail("publishing the consent edit failed")
    return form


# First agree to the current material v3, then edit its wording only.
st, renewed = japi("POST", consent_path, {"version": 3}, token=app_tok)
if st != 200 or renewed.get("agreement_needed") is not False:
    fail("current consent POST did not record agreement")
v4 = publish_consent("Editorial version four: clearer words, same agreement.", False)
st, current = japi("GET", "/api/v1/studies/sleep-mood")
st2, mine = japi("GET", consent_path, token=app_tok)
if st != 200 or st2 != 200 or mine.get("agreement_needed") is not False or \
        current.get("consent", {}).get("material") is not False or current["consent"].get("agreement_from") != 3:
    fail("editorial consent coverage: wording-only edit requires agreement")
say("editorial consent coverage", "v3 agreement covers v4")

# HTML status is private, discoverable, and editorial wording needs no renewal.
ui_path = "/study/sleep-mood/consent"
ui3 = browser()
st, _, headers = request(ui3, "GET", ui_path)
if st != 303 or headers.get("Location") != "/login?study=sleep-mood":
    fail("participant HTML: signed-out return navigation missing")
st, login_page, _ = follow(ui3, headers["Location"])
st, ui_page, returned = console_consent(ui3, login_page, joined3["account"], pw3, {"read": "yes"})
if st != 200 or not returned.endswith(ui_path) or 'data-renewal-needed="false"' not in ui_page or "Editorial wording change" not in ui_page:
    fail("participant HTML: editorial coverage or OAuth return missing")
st, listing, _ = request(ui3, "GET", "/my-studies")
if ui_path not in listing:
    fail("participant HTML: study not discoverable")
if request(ui3, "GET", "/study/diet/consent")[0] != 404:
    fail("participant HTML: unrelated participant can see another study status")
if request(researcher, "GET", ui_path)[0] != 404:
    fail("participant HTML: unrelated researcher account can see participant status")
for action in ("renew", "withdraw", "rejoin", "approve"):
    if request(ui3, "POST", ui_path+"/"+action, form={"csrf": "forged", "confirm": "yes", "version": 4})[0] != 403:
        fail("participant HTML: forged CSRF accepted")
for lang in ("en", "de", "fr", "es", "pt", "fi", "ru"):
    st, translated, _ = request(ui3, "GET", ui_path+"?lang="+lang)
    if st != 200 or "participant_" in re.sub(r'href="[^"]*"', '', translated) or '<script' in translated:
        fail("participant HTML: missing translation or script")
request(ui3, "GET", ui_path+"?lang=en")
say("participant HTML privacy and editorial status", "own account, all languages, CSRF, discovery and OAuth return")

# A current editorial version is already covered, and retries write nothing.
before = mine["consents"]
for _ in range(2):
    st, repeated = japi("POST", consent_path, {"version": 4}, token=app_tok)
    st2, mine = japi("GET", consent_path, token=app_tok)
    if st != 200 or st2 != 200 or len(mine.get("consents", [])) != len(before) or repeated.get("consents") != before or mine.get("consents") != before:
        fail("idempotent consent POST: repeated agreement grew consent history")
say("idempotent consent POST", "covered editorial version and retry write nothing")
st, stale = japi("POST", consent_path, {"version": 3}, token=app_tok)
if st != 409 or stale.get("error", {}).get("code") != "stale_version":
    fail("stale before covered: old version must still be refused")


v5 = publish_consent("Material version five: a changed research agreement.", True)
st, mine = japi("GET", consent_path, token=app_tok)
if st != 200 or mine.get("current_version") != 5 or mine.get("agreement_needed") is not True:
    fail("material consent requires agreement: participant was still covered")
say("material consent requires agreement", "v5 stops the app until agreement")

st, ui_page, _ = request(ui3, "GET", ui_path+"/renew")
if 'data-renewal-needed="true"' not in ui_page or "Material version five" not in ui_page:
    fail("participant HTML: material renewal not shown")
# Forging the write form does not turn read approval into write approval.
form = {"csrf": csrf_of(ui_page), "confirm": "yes", "version": 5}
st, blocked, _ = follow(ui3, ui_path+"/renew", form=form, method="POST")
if st != 200 or "Sign in again and approve Read and Write" not in blocked:
    fail("participant HTML: read-only approval bypassed")
# Approval binds the participant account, and returning never agrees automatically.
st, login_page, _ = follow(ui3, ui_path+"/approve", form={"csrf": csrf_of(ui_page)}, method="POST")
st, wrong, _ = console_consent(ui3, login_page, joined["account"], app_pw, {"read": "yes", "write": "yes"})
if st != 200 or "same participant account" not in wrong:
    fail("participant HTML: another account replaced participant approval")
st, login_page, _ = follow(ui3, ui_path+"/approve", form={"csrf": csrf_of(wrong)}, method="POST")
st, approved, returned = console_consent(ui3, login_page, joined3["account"], pw3, {"read": "yes", "write": "yes"})
if st != 200 or not returned.endswith(ui_path) or 'data-renewal-needed="true"' not in approved:
    fail("participant HTML: OAuth auto-agreed or lost return navigation")
# Old text cannot be agreed, and confirmation is required even with write approval.
for version, confirm, expected in ((4, "yes", "consent text changed"), (5, "", "Tick the confirmation")):
    st, refusal, _ = follow(ui3, ui_path+"/renew", form={"csrf": csrf_of(approved), "version": version, "confirm": confirm}, method="POST")
    if st != 200 or expected not in refusal or "Material version five" not in refusal:
        fail("participant HTML: stale text or unchecked confirmation accepted")
for _ in range(2):
    st, approved, _ = follow(ui3, ui_path+"/renew", form={"csrf": csrf_of(approved), "version": 5, "confirm": "yes"}, method="POST")
    if st != 200 or 'data-renewal-needed="false"' not in approved:
        fail("participant HTML: explicit renewal failed")
st, ui_status = japi("GET", consent_path, token=app_token(joined3["account"], pw3, {"read": "yes"}))
if [c["version"] for c in ui_status.get("consents", [])] != [3,5]:
    fail("participant HTML: repeated renewal changed immutable history")
say("participant HTML material renewal", "stale/unchecked/read-only refused; approval does not agree; retry writes once")


# Record during the gap, using the time-source selected for the export.
time.sleep(0.01)
gap_at = datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")
app_store = api(app_tok, "GET", "/v1/datasets")[1]["datasets"][0]["id"]
status, _ = api(app_tok, "POST", f"/v1/datasets/{app_store}/objects/{sleep}", {"fields": {
    "timestamp": {"type": "date_time", "value": gap_at},
    "hours_slept": {"type": "float", "value": 9.0}}})
if status != 201:
    fail("writing the synthetic agreement-gap record failed")
st, stale = japi("POST", consent_path, {"version": 4}, token=app_tok)
if st != 409 or stale.get("error", {}).get("code") != "stale_version" or stale["error"].get("current_version") != 5:
    fail("stale consent POST: expected 409 stale_version, current 5")
say("stale consent POST", "409 stale_version, current 5")
time.sleep(0.01)
st, renewed = japi("POST", consent_path, {"version": 5}, token=app_tok)
st2, mine = japi("GET", consent_path, token=app_tok)
if st != 200 or st2 != 200 or renewed.get("agreement_needed") is not False or mine.get("agreement_needed") is not False \
        or [c["version"] for c in mine.get("consents", [])] != [2, 3, 5]:
    fail("fresh consent POST: agreement was not appended or still needed")
say("fresh consent POST", "immutable history appended, agreement no longer needed")
before = mine["consents"]
st, repeated = japi("POST", consent_path, {"version": 5}, token=app_tok)
st2, mine = japi("GET", consent_path, token=app_tok)
if st != 200 or st2 != 200 or len(mine.get("consents", [])) != len(before) or repeated.get("consents") != before:
    fail("idempotent consent POST: material agreement retry grew consent history")


st, body, _ = request(researcher, "GET", "/studies/sleep-mood/export.json")
ex = json.loads(body) if st == 200 else {}
p = next((p for p in ex.get("participants", []) if p["pseudonym"] == joined["account"]), {})
records = p.get("tracks", {}).get(sleep, {}).get("records", [])
if not any(r.get("timestamp") == gap_at and r.get("consent_missing") is True for r in records):
    fail("export consent gap JSON: record without material agreement was not marked")
st, body, _ = request(researcher, "GET", "/studies/sleep-mood/export.csv")
rows = list(csv.DictReader(io.StringIO(body)))
if st != 200 or not any(r["participant"] == joined["account"] and r["field"] == "hours_slept" \
                       and r["value"] == "9" and r.get("consent_missing") == "true" for r in rows):
    fail("export consent gap CSV: record without material agreement was not marked")
say("export consent gap, JSON and CSV", "historical missing agreement remains marked after renewal")

# ---- withdrawal on a frozen share, and rejoining (Tessera alpha.28) -------------
# Withdrawing keeps what the study already had and shows it nothing new;
# agreeing again shows it everything, including what was recorded meanwhile.
withdraw_path = "/api/v1/studies/sleep-mood/withdraw"


def researcher_sees(account):
    st, body, _ = request(researcher, "GET", "/studies/sleep-mood/export.json")
    people = {p["pseudonym"]: p for p in json.loads(body).get("participants", [])} if st == 200 else {}
    records = people.get(account, {}).get("tracks", {}).get(sleep, {}).get("records", [])
    return sorted(float(r["hours_slept"]) for r in records)


wd_tok = app_token(joined3["account"], pw3, {"read": "yes", "write": "yes"})
ro_tok = app_token(joined3["account"], pw3, {"read": "yes"})
st, refused = japi("POST", withdraw_path, token=ro_tok)
if st != 403 or refused.get("error", {}).get("code") != "write_approval_required":
    fail(f"withdrawing with a read-only sign-in answered {st} {refused}; want 403 write_approval_required")
before_wd = researcher_sees(joined3["account"])
if before_wd != [6.0]:
    fail(f"before withdrawing, the researcher sees {before_wd}; want [6.0]")
st, ui_page, _ = request(ui3, "GET", ui_path+"/withdraw")
if "Previously shared records are retained" not in ui_page or "later edits" not in ui_page:
    fail("participant HTML: withdrawal retention disclosure missing")
st, ui_page, _ = follow(ui3, ui_path+"/withdraw", form={"csrf": csrf_of(ui_page), "confirm": "yes", "version": 5}, method="POST")
st, mine = japi("GET", consent_path, token=wd_tok)
if st != 200 or not mine.get("withdrawn") or mine.get("agreement_needed") is not True:
    fail(f"withdraw answered {st} {mine}; want withdrawn, agreement needed")
withdrawn_at = mine["withdrawn"]
record_sleep(wd_tok, 7.5)
seen = researcher_sees(joined3["account"])
if seen != [6.0]:
    fail(f"after withdrawing, the researcher sees {seen}; want only what was sent before: [6.0]")
if states().get(joined3["account"], ("",))[0] != "state_withdrawn":
    fail(f"the participant list shows {states().get(joined3['account'])} after withdrawal; want withdrawn")
follow(ui3, ui_path+"/withdraw", form={"csrf": csrf_of(ui_page), "confirm": "yes", "version": 5}, method="POST")
st, again = japi("POST", withdraw_path, token=wd_tok)
if st != 200 or again.get("withdrawn") != withdrawn_at:
    fail("withdrawing twice recorded a second withdrawal")
say("withdrawal on a frozen share", "kept what was sent, sees nothing new; read-only sign-in refused")
# Later edits to the retained records remain visible, while new records stay hidden.
wd_store = api(wd_tok, "GET", "/v1/datasets")[1]["datasets"][0]["id"]
objects = f"/v1/datasets/{wd_store}/objects/{sleep}"
st, old = api(wd_tok, "POST", objects+":query", {"query": {"fields": ["hours_slept"]}})
retained = [r for r in old.get("records", []) if float(r["values"][0]["value"]) == 6.0] if st == 200 else []
if st != 200 or len(retained) != 1:
    fail(f"withdrawal: retained record query failed (status {st}; {len(retained)} matches)")
old_id = retained[0]["id"]["id"]
st, _ = api(wd_tok, "PATCH", objects+"/"+old_id, {"fields": {"hours_slept": {"type": "float", "value": 6.25}}})
if st != 200 or researcher_sees(joined3["account"]) != [6.25]:
    fail("withdrawal: later edit to retained record not visible, or intervening record leaked")
say("withdrawal keeps later edits visible", "retained record updated; new intervening record still hidden")


# An old renewal form cannot silently rejoin after withdrawal.
st, changed, _ = follow(ui3, ui_path+"/renew", form={"csrf": csrf_of(ui_page), "confirm": "yes", "version": 5}, method="POST")
if "participation changed" not in changed or researcher_sees(joined3["account"]) != [6.25]:
    fail("participant HTML: stale renewal silently rejoined")
st, ui_page, _ = request(ui3, "GET", ui_path+"/rejoin")
if "records written while you were withdrawn, as well as future records" not in ui_page or 'data-rejoin-confirmation' not in ui_page:
    fail("participant HTML: rejoining disclosure missing before confirmation")
st, ui_page, _ = follow(ui3, ui_path+"/rejoin", form={"csrf": csrf_of(ui_page), "confirm": "yes", "version": 5}, method="POST")
st, back = japi("GET", consent_path, token=wd_tok)
if st != 200 or back.get("withdrawn") or back.get("agreement_needed") is not False:
    fail(f"rejoining answered {st} {back}; want agreement recorded, not withdrawn")
seen = researcher_sees(joined3["account"])
if seen != [6.25, 7.5]:
    fail(f"after rejoining, the researcher sees {seen}; want everything again: [6.25, 7.5]")
if states().get(joined3["account"], ("",))[0] != "state_active":
    fail(f"the participant list shows {states().get(joined3['account'])} after rejoining; want active")
say("rejoining unfreezes", "agreement recorded; the study sees what was recorded while withdrawn")
rejoined_history = back["consents"]
follow(ui3, ui_path+"/rejoin", form={"csrf": csrf_of(ui_page), "confirm": "yes", "version": 5}, method="POST")
st, retry = japi("GET", consent_path, token=wd_tok)
if st != 200 or retry.get("consents") != rejoined_history:
    fail("participant HTML: repeated rejoining grew agreement history")


# Remove my data still erases, and an erased store cannot rejoin.
rejoiner = browser()
st, page, _ = follow(rejoiner, "/login")
console_consent(rejoiner, page, joined["account"], app_pw, {"read": "yes"})


def withdraw_current():
    st, page, _ = request(rejoiner, "GET", "/leave")
    form = re.search(r'<form method="post" action="/leave/start"[^>]*>(.*?)</form>', page, re.S)
    if not form:
        fail("Remove my data: form unavailable")
    st, page, _ = follow(rejoiner, "/leave/start", form=dict(re.findall(r'name="(csrf|dataset)" value="([^"]*)"', form.group(1))), method="POST")
    st, page, _ = console_consent(rejoiner, page, joined["account"], app_pw, {"read": "yes", "erase": "yes"})
    st, page, _ = request(rejoiner, "POST", "/leave/confirm", form={"csrf": csrf_of(page), "dataset": field(page, "dataset")})
    if st != 200:
        fail("Remove my data: erase failed")


withdraw_current()
# Write-approved, so the refusal is about the erased store, not the sign-in.
rejoin_tok = app_token(joined["account"], app_pw, {"read": "yes", "write": "yes"})
st, mine = japi("GET", consent_path, token=rejoin_tok)
if st != 200 or mine.get("agreement_needed") is not True or not mine.get("withdrawn"):
    fail("after Remove my data, GET does not report withdrawal")
before = mine["consents"]
st, refused = japi("POST", consent_path, {"version": 5}, token=rejoin_tok)
st2, mine = japi("GET", consent_path, token=rejoin_tok)
if st != 409 or refused.get("error", {}).get("code") != "store_unavailable" or st2 != 200 \
        or mine.get("consents") != before or not mine.get("withdrawn"):
    fail(f"rejoining after Remove my data answered {st} {refused}; want 409 store_unavailable, history unchanged")
st, again = japi("POST", withdraw_path, token=rejoin_tok)
if st != 200 or not again.get("withdrawn"):
    fail(f"withdrawing after Remove my data answered {st} {again}; want done -- the study already reads nothing of theirs")
say("Remove my data, then rejoin", "erased; 409 store_unavailable; withdrawing again is already done")
st, login_page, _ = follow(rejoiner, "/login?study=sleep-mood")
st, erased_page, _ = console_consent(rejoiner, login_page, joined["account"], app_pw, {"read": "yes", "write": "yes"})
if "Rejoining is unavailable after erasure" not in erased_page or 'href="'+ui_path+'/rejoin"' in erased_page:
    fail("participant HTML: write-approved erasure status still offers rejoining")
st, refused, _ = follow(rejoiner, ui_path+"/rejoin", form={"csrf": csrf_of(erased_page), "confirm": "yes", "version": 5}, method="POST")
if st != 200 or "Rejoining is unavailable after erasure" not in refused or "new participant" not in refused or 'href="'+ui_path+'/rejoin"' in refused:
    fail("participant HTML: erasure refusal or new participant option missing")
say("participant HTML after erasure", "no restored share; new participant option explained")


st, used = japi("POST", "/api/v1/studies/diet/join", {"version": 1, "code": app_q["code"]})
st2, unknown = japi("POST", "/api/v1/studies/diet/join", {"version": 1, "code": "unknown"})
if st != 403 or used.get("error", {}).get("code") != "code_used" or st2 != 403 or unknown.get("error", {}).get("code") != "invite_only":
    fail("spent versus unknown code: expected code_used and invite_only")
say("spent versus unknown code", "403 code_used; 403 invite_only")

st, page, _ = request(researcher, "GET", "/studies/sleep-mood/edit")
follow(researcher, "/studies/sleep-mood/edit", form=dict(v5, csrf=csrf_of(page), revision=field(page, "revision"), status="closed"), method="POST")
st, mine = japi("GET", consent_path, token=rejoin_tok)
if st != 200 or mine.get("status") != "closed" or not mine.get("closed_at"):
    fail("closed consent status: participant cannot see the closed study, or when it closed")
st, gone = japi("GET", "/api/v1/studies/sleep-mood")
st2, gone2 = japi("POST", "/api/v1/studies/sleep-mood/join", {"version": 5})
st3, _ = japi("GET", "/api/v1/studies/no-such-study")
if (st, st2, st3) != (410, 410, 404) or gone["error"].get("code") != "study_closed" or gone["error"].get("closed_at") != mine["closed_at"] \
        or gone2["error"].get("code") != "study_closed":
    fail(f"a closed study answered {st}/{st2} {gone}, an unknown one {st3}; want 410 study_closed with closed_at, and 404")
say("a closed study, publicly", "410 study_closed with closed_at; unknown still 404")
st, vj = japi("GET", "/.well-known/voptrack")
if st != 200 or vj.get("product") != "research" or vj.get("web_interface") is not True or vj.get("week_start") != "monday":
    fail(f"/.well-known/voptrack answered {st} {vj}; want research with a web interface")
say("/.well-known/voptrack", f"research, {vj.get('timezone')}, web interface")
say("closed consent status", "participant GET remains available")
# Use an active participant so this proves closing, independently of withdrawal.
closed_tok = app_token(joined3["account"], pw3, {"read": "yes"})
st, active = japi("GET", consent_path, token=closed_tok)
before = active.get("consents", [])
st, refused = japi("POST", consent_path, {"version": 5}, token=closed_tok)
st2, active = japi("GET", consent_path, token=closed_tok)
if st != 409 or refused.get("error", {}).get("code") != "study_closed" or st2 != 200 or active.get("consents") != before:
    fail("closed consent POST: closed study accepted agreement or changed history")
say("closed consent POST", "409 study_closed, history unchanged")
st, closed_page, _ = request(ui3, "GET", ui_path)
if st != 200 or "closed" not in closed_page or 'href="'+ui_path+'/renew"' in closed_page or 'href="'+ui_path+'/rejoin"' in closed_page:
    fail("participant HTML: closed study offers renewal or rejoining")
st, refused, _ = follow(ui3, ui_path+"/renew", form={"csrf": csrf_of(closed_page), "confirm": "yes", "version": 5}, method="POST")
if st != 200 or "closed" not in refused:
    fail("participant HTML: closed renewal accepted")
st, done, _ = follow(ui3, ui_path+"/withdraw", form={"csrf": csrf_of(closed_page), "confirm": "yes", "version": 5}, method="POST")
if st != 200 or 'data-consent-status>Withdrawn' not in done:
    fail("participant HTML: closed study withdrawal unavailable")
say("participant HTML closed study", "status remains available; renewal refused; withdrawal allowed")


# Policies can change while closed; lowering still requires confirmation.
st, policy, _ = request(researcher, "GET", policy_path)
for threshold, confirm in (("3", ""), ("2", "yes")):
    st, policy, _ = follow(researcher, policy_path, form={"csrf": csrf_of(policy), "threshold": threshold,
        "revision": field(policy, "revision"), "confirm_lower": confirm}, method="POST")
    if st != 200:
        fail("suppression: closed study policy change failed")
say("closed study suppression policy", "changes allowed; lowering explicit; consent untouched")

# The recovery part of this gate expects an open study.
st, page, _ = request(researcher, "GET", "/studies/sleep-mood/edit")
follow(researcher, "/studies/sleep-mood/edit", form=dict(v5, csrf=csrf_of(page), revision=field(page, "revision"), status="open"), method="POST")
