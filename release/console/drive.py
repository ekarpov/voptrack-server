#!/usr/bin/env python3
"""The Console gate's browser: Voptrack Home used the way a household uses it.

The owner invites Sara and Ben from the Console. They join, record from their
own app sign-in, and each ticks Spending on the Sharing page. Home adds up what
they shared -- exactly, and nothing else. Every number is checked against a
total computed here with Decimal.

Usage: drive.py BASE CA OWNER_PASSWORD_FILE
"""

import base64
import csv
import datetime
import hashlib
import html
import http.cookiejar
from html.parser import HTMLParser
import io
import json
import os
import re
import secrets
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from decimal import Decimal

import sys as _ci_sys
from pathlib import Path as _CiPath
_ci_sys.path.insert(0, str(_CiPath(__file__).resolve().parents[2] / 'ci'))
from timing import retry_auth, timed_request, RetryBudget, phase, event

base, ca, owner_pw = sys.argv[1].rstrip("/"), sys.argv[2], open(sys.argv[3]).read().strip()
ctx = ssl.create_default_context(cafile=ca)
SPEND, WEIGHT = "org.voptrack.gate.spending", "org.voptrack.gate.weight"


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


@timed_request
def request(opener, method, url, form=None, js=None, headers=None):
    data, h = None, dict(headers or {})
    if form is not None:
        data = urllib.parse.urlencode(form, doseq=True).encode()
        h["Content-Type"] = "application/x-www-form-urlencoded"
    if js is not None:
        data = json.dumps(js).encode()
        h["Content-Type"] = "application/json"
    r = urllib.request.Request(url if url.startswith("http") else base + url, data=data, method=method, headers=h)
    try:
        resp = opener.open(r)
        return resp.status, resp.read().decode(), resp.headers
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace"), e.headers


def follow(opener, url, form=None, method="GET"):
    """Follow redirects by hand, as a browser would, returning the last page."""
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


def sign_in_step(opener, form):
    budget = RetryBudget()
    for attempt in range(3):
        result = request(opener, "POST", "/oauth/authorize", form=form)
        if result[0] != 429 or not budget.wait(result[2], attempt):
            return result


def own_store(approval, known=None):
    """The person's own store on the approval page. With household sharing the
    chooser also lists stores others share with them -- by bare id, in no fixed
    order -- so a script that knows its store picks it by id. Before that, the
    person's own store is the only one there."""
    options = re.findall(r'<option\s+value="([^"]+)"', approval)
    if known and known in options:
        return known
    return options[0] if options else None


def consent(opener, page, person, password, grants):
    """Sign in on Tessera's page and approve exactly `grants`: a store's
    permissions (with "store"), or {"administer": "yes"} alone for server
    rights and no store. Returns where Tessera redirects."""
    st, approval, _ = sign_in_step(opener, {
        "flow": field(page, "flow"), "csrf": field(page, "csrf"),
        "username": person, "password": password, "decision": "continue"})
    if st != 200 or 'name="csrf"' not in approval:
        fail(f"{person}: Tessera did not accept the sign-in ({st})")
    form = {"flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow"}
    option = own_store(approval, globals().get("stores", {}).get(person))
    if option and "store" in grants:
        form["dataset"] = option
    form.update({k: v for k, v in grants.items() if k != "store"})
    st, _, headers = request(opener, "POST", "/oauth/authorize", form=form)
    if st != 303:
        fail(f"{person}: approving answered {st}")
    return headers["Location"]


class WebsiteForm(HTMLParser):
    def __init__(self, text):
        super().__init__(); self.fields = {}; self.refresh = None; self.feed(text)
    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "input" and attrs.get("type") == "hidden" and attrs.get("name"):
            self.fields[attrs["name"]] = attrs.get("value", "")
        if tag == "button" and attrs.get("name") and attrs["name"] not in self.fields:
            self.fields[attrs["name"]] = attrs.get("value", "")
        if tag == "meta" and attrs.get("http-equiv", "").lower() == "refresh":
            self.refresh = attrs.get("content", "")


def console_sign_in(opener, person, password, grants):
    st, page, url = follow(opener, "/login")
    if "/oauth/authorize" not in url:
        fail(f"{person}: the Console did not send them to Tessera to sign in ({st} {url})")
    if 'name="remember"' in page:
        st, result, headers = sign_in_step(opener, dict(WebsiteForm(page).fields, username=person, password=password))
        if st == 303:
            return follow(opener, headers["Location"])
        refresh = WebsiteForm(result).refresh
        if st != 200 or not refresh or not re.search(r"url=", refresh, re.I):
            fail("owned website sign-in did not complete")
        callback = re.split(r"url=", refresh, flags=re.I)[1].strip().strip(chr(34)+chr(39))
        parsed = urllib.parse.urlparse(callback)
        if parsed.netloc != urllib.parse.urlparse(base).netloc or parsed.path != "/auth/callback":
            fail("owned website callback used a different origin")
        return follow(opener, callback)
    return follow(opener, consent(opener, page, person, password, grants))


def app_token(person, password, grants):
    """Sign in as the Voptrack app would: its own OAuth client, PKCE, consent.
    This is how Sara and Ben record; the Console never writes their records."""
    app = browser()
    redirect = "com.voptrack.gate:/oauth/callback"
    st, body, _ = request(app, "POST", "/oauth/register", js={
        "client_name": "Voptrack (gate)", "redirect_uris": [redirect], "token_endpoint_auth_method": "none",
        "grant_types": ["authorization_code"], "response_types": ["code"]})
    client = json.loads(body)["client_id"]
    verifier = secrets.token_urlsafe(48)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
    params = {"response_type": "code", "client_id": client, "redirect_uri": redirect,
              "state": "s", "code_challenge": challenge, "code_challenge_method": "S256", "prompt": "consent"}
    if grants.get("shared_reads"):
        params["shared_reads"] = "true"  # asked for in the request; the approval page then offers it
    st, page, _ = request(app, "GET", "/oauth/authorize?" + urllib.parse.urlencode(params))
    location = consent(app, page, person, password, dict(grants, store=True))
    code = urllib.parse.parse_qs(urllib.parse.urlparse(location).query)["code"][0]
    st, body, _ = request(app, "POST", "/oauth/token", form={
        "grant_type": "authorization_code", "client_id": client, "code": code,
        "redirect_uri": redirect, "code_verifier": verifier})
    return json.loads(body)["access_token"]


def api(token, method, path, body=None):
    headers = {"Authorization": "Bearer " + token}
    if method != "GET":
        headers["Idempotency-Key"] = str(uuid.uuid4())
    return request(browser(), method, path, js=body, headers=headers)


def panel_rows(page):
    return dict(re.findall(r"<tr><td>([^<]*)</td><td class=\"num\" data-value>([^<]*)</td>", page))


def panel(opener, scope, **params):
    st, page, _ = request(opener, "GET", "/panel?" + urllib.parse.urlencode(dict(scope=scope, **params)))
    if st != 200:
        fail(f"panel {params}: {st}")
    return page


def total(rows):
    return next(iter(rows.values()), None)


def boxes(page):
    return dict((v, "checked" in tag) for tag, v in re.findall(r'(<input type="checkbox" name="track" value="([^"]+)"[^>]*>)', page))


_setup_operator = None

def setup_operator_token():
    """Reuse only an unchanged setup operator, never participant test grants."""
    global _setup_operator
    if _setup_operator:
        st, body, _ = api(_setup_operator, "GET", "/admin/api/v1/overview")
        if st == 200 and json.loads(body).get("server_administrator") is True:
            event("setup-session-reused")
            return _setup_operator
    with phase("setup-authorization"):
        _setup_operator = app_token("owner", owner_pw, {"administer": "yes"})
    st, body, _ = api(_setup_operator, "GET", "/admin/api/v1/overview")
    if st != 200 or json.loads(body).get("server_administrator") is not True:
        raise RuntimeError("setup operator authority unavailable")
    return _setup_operator


# ---- the owner invites the household from the Console ---------------------

owner = browser()
st, page, url = console_sign_in(owner, "owner", owner_pw, {"administer": "yes"})
if st != 200 or not url.endswith("/home") or 'href="/manage"' not in page:
    fail(f"the owner's sign-in did not land on Home with Manage Home navigation ({st} {url})")
say("the owner signs in to the Console", "server administrator, lands on Home; Members available")
# Check the retired surface immediately, so compiled route defects fail here.
if request(owner, "GET", "/views")[0] != 404:
    fail("retired Saved views endpoint is exposed after owner sign-in")
if 'href="/views' in page or 'Saved views' in page:
    fail("retired Saved views navigation remains")


# A restored owner sharing page must fail before unrelated fixture setup.
if request(owner, "GET", "/home/moderation")[0] != 404:
    fail("retired moderation endpoint is exposed after owner sign-in")

passwords, stores = {}, {}
for person in ("sara", "ben"):
    st, page, _ = request(owner, "GET", "/members")
    st, page, _ = follow(owner, "/members/invite", form={"csrf": csrf_of(page)}, method="POST")
    link = re.findall(r'<code class="link">([^<]+)</code>', page)[0].replace("&amp;", "&")
    # Tessera's deployment contract: whoever is invited under moderated sharing is told.
    if not re.search(r"<p data-moderated>[^<]*stop what they share", page):
        fail("the invitation does not tell the owner to say they can stop what the member shares")
    redeem = browser()
    st, form_page, _ = request(redeem, "GET", link)
    passwords[person] = secrets.token_urlsafe(18)
    st, done, _ = request(redeem, "POST", "/oauth/invitation", form={
        "code": field(form_page, "code"), "account": person, "password": passwords[person]})
    if st != 201:
        fail(f"{person} redeeming an invitation answered {st}")
    # Nobody may reset a member's password, so this code, shown once, is
    # their only way back if they forget it.
    if not re.search(r'data-recovery-code="[A-Z0-9-]+"', done):
        fail(f"{person} was not shown a recovery code on joining")
say("Sara and Ben join by invitation", "from the Console's links; each shown a recovery code")

# ---- they record from their own app sign-in ---------------------------------


def meta(type_id, name, fields):
    return {"type": {"id": type_id, "parent_id": None, "name": name,
                     "fields": [dict(f, meta_id=type_id) for f in fields]}}


spending = {
    "sara": [("2026-07-15T09:00:00.000Z", "12.5", "food"), ("2026-08-10T09:00:00.000Z", "0.1", "food"),
             ("2026-08-20T09:00:00.000Z", "0.2", "transport")],
    "ben": [("2026-08-11T09:00:00.000Z", "100", "food"), ("2026-09-05T09:00:00.000Z", "0.25", "food")],
}
weights = {"sara": ["61.2", "60.8"], "ben": ["80.1"]}
app_tokens = {}
for person in ("sara", "ben"):
    token = app_token(person, passwords[person], {"read": "yes", "write": "yes", "schema": "yes", "administer": "yes"})
    app_tokens[person] = token
    st, body, _ = api(token, "GET", "/v1/datasets")
    listed = json.loads(body)["datasets"]
    if len(listed) != 1 or listed[0]["display_name"] != person:
        fail(f"{person}'s store is not named after them: {[d.get('display_name') for d in listed]}")
    stores[person] = d = listed[0]["id"]
    for body in (meta(SPEND, "Spending", [
                     {"id": "recorded_at", "name": "When", "type": "date_time"},
                     {"id": "amount", "name": "Amount", "type": "fixed", "scale": 2},
                     {"id": "category", "name": "Category", "type": "text"}]),
                 meta(WEIGHT, "Weight", [
                     {"id": "recorded_at", "name": "When", "type": "date_time"},
                     {"id": "kg", "name": "Kilograms", "type": "fixed", "scale": 2}])):
        st, text, _ = api(token, "POST", f"/v1/datasets/{d}/meta", body)
        if st != 201:
            fail(f"defining {body['type']['id']} as {person}: {st} {text[:200]}")
    ops = [{"op": "insert", "type": SPEND, "fields": {
        "recorded_at": {"type": "date_time", "value": at}, "amount": {"type": "fixed", "value": amount},
        "category": {"type": "text", "value": cat}}} for at, amount, cat in spending[person]]
    ops += [{"op": "insert", "type": WEIGHT, "fields": {
        "recorded_at": {"type": "date_time", "value": "2026-09-01T07:00:00.000Z"},
        "kg": {"type": "fixed", "value": kg}}} for kg in weights[person]]
    st, text, _ = api(token, "POST", f"/v1/datasets/{d}:transact", {"operations": ops})
    if st != 200:
        fail(f"recording as {person}: {st} {text[:200]}")
say("each records spending and weight", "in a store named after them")

# ---- sharing: each ticks Spending, and only Spending ------------------------

consoles = {}
for person in ("sara", "ben"):
    consoles[person] = b = browser()
    console_sign_in(b, person, passwords[person], {"store": True, "read": "yes", "administer": "yes"})
    st, page, _ = request(b, "GET", "/sharing")
    if boxes(page) != {SPEND: False, WEIGHT: False}:
        fail(f"{person}'s Sharing page does not list both tracks, unshared: {boxes(page)} ({st}: "
             f"{' '.join(re.sub(r'<[^>]+>', ' ', page.split('<main>')[-1]).split())[:400]})")
    st, page, url = follow(b, "/sharing", form={"csrf": csrf_of(page), "dataset": field(page, "dataset"),
                                                "track": [SPEND]}, method="POST")
    if boxes(page) != {SPEND: True, WEIGHT: False}:
        fail(f"after {person} shared Spending the page shows {boxes(page)}")
say("each ticks Spending on the Sharing page", "Weight left unticked")
sara, ben = consoles["sara"], consoles["ben"]

# No storage quota unless the operator sets one (Tessera alpha.30): the member
# sees their store's size, and never "nearly full".
st, page, _ = request(sara, "GET", "/sharing")
usage = re.search(r"data-usage>([^<]*)<", page)
if not usage or "/" in usage.group(1) or "%" in usage.group(1) or not re.search(r"\d", usage.group(1)):
    fail(f"Sara's Sharing page shows {usage and usage.group(1)!r}; want her store's size alone: no quota by default")
say("each store's size, no quota by default", usage.group(1).strip())

# The same sharing reaches the app, through the household's group grant: Ben's
# app, with one sign-in covering his own store and what others share with him
# (Tessera alpha.23), reads Sara's Spending and is refused her Weight.
ben_app = app_token("ben", passwords["ben"], {"read": "yes", "write": "yes", "shared_reads": "yes"})


def reads(token, store, track):
    # A read sent as POST carries no Idempotency-Key: Tessera refuses one there.
    return request(browser(), "POST", f"/v1/datasets/{store}/objects/{track}:aggregate", js={},
                   headers={"Authorization": "Bearer " + token})[0]


def lists(token):
    return {d["id"]: d["access"] for d in json.loads(api(token, "GET", "/v1/datasets")[1]).get("datasets", [])}


seen = lists(ben_app)
if stores["ben"] not in seen or not seen.get(stores["sara"], {}).get("read") or seen[stores["sara"]].get("write"):
    fail(f"Ben's one sign-in lists {seen}; want his own store and Sara's, hers read only")
if (reads(ben_app, stores["sara"], SPEND), reads(ben_app, stores["sara"], WEIGHT)) != (200, 403):
    fail(f"Ben's app reading Sara's Spending and Weight answered "
         f"{reads(ben_app, stores['sara'], SPEND)}, {reads(ben_app, stores['sara'], WEIGHT)}; want 200, 403")
say("Ben's app, one sign-in, reads the household", "Sara's Spending yes, her Weight 403, read only")

csp = request(sara, "GET", "/")[2].get("Content-Security-Policy", "")
if "default-src 'none'" not in csp or "script" in csp:
    fail(f"the Console's content policy allows more than it should: {csp}")
say("no script may run on any Console page", "default-src 'none'")

# ---- the numbers ------------------------------------------------------------

sara_total = sum(Decimal(a) for _, a, _ in spending["sara"])
home_total = sum(Decimal(a) for p in spending.values() for _, a, _ in p)
rows = panel_rows(panel(sara, "mine", type=SPEND, field="amount", measure="sum", group="store"))
if rows.get("sara") != str(sara_total):
    fail(f"Sara's own spending shows {rows}, want sara = {sara_total}")
rows = panel_rows(panel(sara, "mine", type=WEIGHT, field="kg", measure="average", group="store"))
if rows.get("sara") != "61":
    fail(f"Sara's own average weight shows {rows}, want 61")
say("My dashboards: Sara's spending, weight", f"{sara_total} exact; average 61")

rows = panel_rows(panel(sara, "home", type=SPEND, field="amount", measure="sum", group="none"))
if total(rows) != str(home_total):
    fail(f"Home's spending total shows {rows}, want {home_total} exactly")
rows = panel_rows(panel(sara, "home", type=SPEND, field="amount", measure="sum", group="store"))
if rows != {"ben": "100.25", "sara": str(sara_total)}:
    fail(f"Home per member shows {rows}")
say("Home: spending, combined and per member", f"{home_total} exact; ben 100.25, sara 12.8")

rows = panel_rows(panel(sara, "home", type=SPEND, field="amount", measure="sum", group="field:category"))
want = {"food": str(Decimal("12.5") + Decimal("0.1") + Decimal("100") + Decimal("0.25")), "transport": "0.2"}
if rows != want:
    fail(f"Home by category shows {rows}, want {want}")
rows = panel_rows(panel(sara, "home", type=SPEND, field="amount", measure="sum", group="period",
                        period="month", tz="UTC", **{"from": "2026-07-01", "to": "2026-09-30"}))
if rows != {"2026-07": "12.5", "2026-08": "100.3", "2026-09": "0.25"}:
    fail(f"Home by month shows {rows}")
say("Home: by category and by month", "food 112.85, transport 0.2; Jul/Aug/Sep")

# The activity directory retains readable types even in a quiet period; Home
# intentionally omits empty activity cards. Keep the sharing-boundary assertion.
st, home_page, _ = request(sara, "GET", "/home?view=directory")
if st != 200 or "Weight" in home_page or "Spending" not in home_page:
    fail("Home shows a track nobody shared, or not the one they did")
st, page, _ = request(sara, "GET", "/panel?" + urllib.parse.urlencode(
    {"scope": "home", "type": WEIGHT, "field": "kg", "measure": "average", "group": "store"}))
if st == 200 and "61" in page:
    fail("a Home panel over an unshared track answered with Sara's data")
say("Weight in Home", "absent, and cannot be asked for")

# ---- changing one's mind ------------------------------------------------------


def set_sharing(b, tracks):
    """Save a choice on the Sharing page and check it took: the page must come
    back saved, with exactly these boxes ticked."""
    st, page, _ = request(b, "GET", "/sharing")
    st, page, url = follow(b, "/sharing", form={"csrf": csrf_of(page), "dataset": field(page, "dataset"),
                                                "track": tracks}, method="POST")
    ticked = {k for k, v in boxes(page).items() if v}
    if st != 200 or "saved=1" not in url or ticked != set(tracks):
        text = re.sub(r"\s+", " ", re.sub(r"<[^>]+>", " ", page))[:300]
        fail(f"saving sharing {tracks} answered {st} at {url}, ticked {ticked}: {text}")


set_sharing(sara, [])
rows = panel_rows(panel(ben, "home", type=SPEND, field="amount", measure="sum", group="none"))
if total(rows) != "100.25":
    fail(f"after Sara stopped sharing, Home shows {rows}, want Ben's 100.25 alone")
if stores["sara"] in lists(ben_app) or reads(ben_app, stores["sara"], SPEND) != 404:
    fail("after Sara stopped sharing, Ben's app still reaches her store")
set_sharing(sara, [SPEND])
rows = panel_rows(panel(ben, "home", type=SPEND, field="amount", measure="sum", group="none"))
if total(rows) != str(home_total):
    fail(f"after Sara shared again, Home shows {rows}, want {home_total}")
if reads(ben_app, stores["sara"], SPEND) != 200:
    fail("after Sara shared again, Ben's app does not read her Spending with the sign-in it already had")
say("Sara unticks Spending, then ticks it again", "Home and Ben's app: gone, then back, no new sign-in")

# Owner sharing controls are retired from the website. Direct stale URLs and
# mutation submissions must remain closed, including for an approved owner.
for jar in (owner, sara, browser()):
    for method, path in (("GET", "/home/moderation"),
                         ("POST", "/home/moderation/preview"),
                         ("POST", "/home/moderation/confirm")):
        st, _, _ = request(jar, method, path, form={} if method == "POST" else None)
        if st != 404:
            fail(f"retired moderation endpoint answered {st}")
for path in ("/members", "/manage"):
    st, page, _ = request(owner, "GET", path)
    if st != 200 or 'href="/home/moderation' in page:
        fail("retired sharing control remains in owner navigation")
# The supported member sharing flow still controls app and Home read access.
set_sharing(sara, [SPEND, WEIGHT])
set_sharing(sara, [SPEND])
if reads(ben_app, stores["sara"], SPEND) != 200 or reads(ben_app, stores["sara"], WEIGHT) != 403:
    fail("member sharing selection changed")
set_sharing(sara, [])
set_sharing(sara, [SPEND])
if total(panel_rows(panel(ben, "home", type=SPEND, field="amount", measure="sum", group="none"))) != str(home_total):
    fail("member re-share did not restore Home")
say("sharing controls", "retired owner endpoints closed; member sharing and re-share checked")

q = urllib.parse.urlencode({"scope": "home", "type": SPEND, "field": "amount", "measure": "sum", "group": "store"})
st, body, _ = request(sara, "GET", "/panel.csv?" + q)
table = [r for r in csv.reader(io.StringIO(body)) if r and not r[0].startswith("#")]
if st != 200 or {r[0]: r[1] for r in table[1:]} != {"ben": "100.25", "sara": "12.8"}:
    fail(f"the CSV export does not match the panel: {table}")
say("CSV export matches the panel", "exact values")

# ---- who may do what ----------------------------------------------------------

auth = "Basic " + base64.b64encode(f"owner:{owner_pw}".encode()).decode()
st, adm, _ = request(browser(), "GET", "/admin/datasets", headers={"Authorization": auth})
if st != 200 or re.search(r'data-label="Name">(sara|ben)<', adm):
    fail("the owner's administration can reach a member's store")
say("members' stores, from the owner's /admin", "invisible — sealed")

st, _, _ = request(sara, "GET", "/members")
st2, _, _ = request(owner, "POST", "/members/invite", form={"csrf": "forged"})
if st != 403 or st2 != 403:
    fail(f"a member at Members answered {st}, a forged invitation {st2}; want 403 and 403")
st, _, headers = request(browser(), "GET", "/panel?" + q)
st3, _, _ = request(sara, "POST", "/connect", form={"csrf": "forged"})
if st != 303 or headers.get("Location") != "/" or st3 != 403:
    fail("a signed-out panel or a forged connect was not refused")
say("member at Members; forged forms; signed out", "all refused")

# ---- retired Home saved-view surface ------------------------------------------

for viewer in (owner, sara, browser()):
    for path in ("/views", "/views?scope=mine", "/dashboards/home/synthetic", "/dashboards/mine/synthetic"):
        if request(viewer, "GET", path)[0] != 404:
            fail("retired Saved views read route is exposed")
    for path in ("/dashboards/add", "/dashboards/home/synthetic/remove", "/dashboards/mine/synthetic/remove"):
        if request(viewer, "POST", path, form={"csrf":"forged", "panel":"scope=home&type=synthetic&measure=count&group=store", "name":"Synthetic"})[0] != 404:
            fail("retired Saved views write route is exposed")
for viewer, scope in ((owner, "home"), (sara, "mine")):
    page = panel(viewer, scope, type=SPEND, field="amount", measure="sum", group="store")
    if '/dashboards/add' in page or 'href="/views' in page:
        fail("retired Saved views panel action remains")
say("Saved views", "retired routes and save controls absent; panel/CSV readers retained")

# ---- the owner suspends a member, for a lost phone ------------------------------

st, page, _ = request(owner, "GET", "/members")
listed = dict(re.findall(r'<td data-member>([^<]+)</td><td data-state>([^<]+)</td>', page))
if listed != {"ben": "active", "sara": "active"}:
    fail(f"the owner's member list is {listed}; want exactly Sara and Ben, not the owner, the custodian or Home")
st, page, _ = follow(owner, "/members/ben/suspend", form={"csrf": csrf_of(page)}, method="POST")
ben_read = lambda: api(app_tokens["ben"], "GET", "/v1/datasets")[0]
if ben_read() != 401:
    fail(f"Ben's app still reads after he was suspended ({ben_read()})")
# Home reads with its own credential, so Ben's Console session -- still open --
# must be checked against Tessera, not trusted: suspended, he sees no Home.
home_rows = lambda b: panel_rows(request(b, "GET", "/panel?" + urllib.parse.urlencode(
    dict(scope="home", type=SPEND, field="amount", measure="sum", group="none")))[1])
if home_rows(ben):
    fail(f"suspended Ben's open Console session still reads Home: {home_rows(ben)}")
st, page, _ = follow(owner, "/members/ben/reinstate", form={"csrf": csrf_of(page)}, method="POST")
if ben_read() != 200:
    fail(f"Ben's app did not come back after he was reinstated ({ben_read()})")
if total(home_rows(ben)) != str(home_total):
    fail(f"reinstated Ben's Console does not read Home again: {home_rows(ben)}")
st, _, _ = request(sara, "POST", "/members/ben/suspend", form={"csrf": csrf_of(request(sara, "GET", "/")[1])})
if st != 403:
    fail(f"a member suspending another answered {st}, want 403")
say("owner suspends Ben, then reinstates him", "his app and his Home: refused, then back; Sara cannot")

# One browser, two people: Sara signs in, then Ben in the same browser. Nothing
# of Sara's may carry over into Ben's session.
shared = browser()
console_sign_in(shared, "sara", passwords["sara"], {"store": True, "read": "yes"})
console_sign_in(shared, "ben", passwords["ben"], {"store": True, "read": "yes"})
st, _, _ = request(shared, "GET", f"/mine/{stores['sara']}/export.json")
st2, _, _ = request(shared, "GET", f"/mine/{stores['ben']}/export.json")
if (st, st2) != (404, 200):
    fail(f"after Ben signed in where Sara had, her store answered {st} and his {st2}; want 404 and 200")
say("Sara, then Ben, in one browser", "Ben's session holds nothing of Sara's")

# ---- backups, from the owner's Console --------------------------------------
from online_backup_checks import verify as verify_online_backups
before, after, finalize_online_recovery = verify_online_backups(globals())
st, status_page, _ = request(owner, "GET", "/backups")
expected_version=__import__('subprocess').check_output(['go','list','-m','-f','{{.Version}}','github.com/ekarpov/tessera'],cwd=os.path.abspath(os.path.join(os.path.dirname(__file__),'../..')),text=True).strip()
if st != 200 or ('data-server-version>'+expected_version+'<') not in status_page or 'data-latest' not in status_page:
    fail("backup status: verified version or authoritative last success missing")
if "Metadata authentication must be verified" not in status_page or "recovery kit separately" not in status_page:
    fail("online backup recovery/custody disclosure missing")
# Queue a constrained request. Generated values remain unchanged until the host
# deliberately applies it after this browser journey has finished.
st, settings_page, _ = request(owner, "GET", "/backups")
st, preview, _ = request(owner, "POST", "/backups/settings/preview", form={
    "csrf": csrf_of(settings_page), "revision": field(settings_page,"revision"),
    "generation": field(settings_page,"generation"), "destination":"local", "every":"12", "keep":"1"})
if st != 200 or 'action="/backups/settings/confirm"' not in preview or "saving deletes nothing" in preview:
    fail("backup settings: retention consequences missing")
st, saved, _ = request(owner,"POST","/backups/settings/confirm",form={"csrf":csrf_of(preview),"confirmation":field(preview,"confirmation")})
if st != 200 or "Request saved." not in saved:
    fail("backup settings: request was not saved")
st, settings_page, _ = request(owner,"GET","/backups")
st, preview, _ = request(owner,"POST","/backups/settings/preview",form={
    "csrf":csrf_of(settings_page),"revision":field(settings_page,"revision"),"generation":field(settings_page,"generation"),
    "destination":"reserve","every":"12","keep":"1"})
if st != 200:
    fail("backup settings: reserve preview failed")
st, saved, _ = request(owner,"POST","/backups/settings/confirm",form={"csrf":csrf_of(preview),"confirmation":field(preview,"confirmation")})
if st != 200 or "pending host application" not in saved or "24 h" not in saved:
    fail("backup settings: pending request confused with generated settings")
say("backup settings request", "reviewed retention; reserve/12h/1 pending host application")


# ---- taking my data with me ---------------------------------------------------
# Every record of Sara's store, with her own sign-in -- Weight too, which she
# never shared with Home -- and nothing of Ben's.

st, body, _ = request(sara, "GET", f"/mine/{stores['sara']}/export.json")
mine = json.loads(body) if st == 200 else {}
amounts = sorted(Decimal(r["amount"]) for r in mine.get("records", {}).get(SPEND, []))
kgs = sorted(Decimal(str(r["kg"])) for r in mine.get("records", {}).get(WEIGHT, []))
if amounts != sorted(Decimal(a) for _, a, _ in spending["sara"]) or kgs != sorted(Decimal(k) for k in weights["sara"]):
    fail(f"Sara's download ({st}) holds spending {amounts} and weight {kgs}, not what she recorded")
st, _, _ = request(sara, "GET", f"/mine/{stores['ben']}/export.json")
if st != 404:
    fail(f"Sara downloading Ben's store answered {st}, want 404")
say("Sara downloads her data", "every record, Weight included; Ben's refused")

# ---- removing my data -------------------------------------------------------


def start_leave(b, person, tick_erase):
    st, page, _ = request(b, "GET", "/leave")
    form = re.search(r'<form method="post" action="/leave/start"[^>]*>(.*?)</form>', page, re.S)
    fields = dict(re.findall(r'name="(csrf|dataset)" value="([^"]*)"', form.group(1)))
    grants = {"store": True, "read": "yes"}
    if tick_erase:
        grants["erase"] = "yes"
    st, page, url = follow(b, "/leave/start", form=fields, method="POST")
    return follow(b, consent(b, page, person, passwords[person], grants))


st, page, _ = start_leave(ben, "ben", tick_erase=False)
if "Nothing was removed" not in page:
    fail(f"declining erase did not say nothing was removed ({st})")
st, page, url = start_leave(ben, "ben", tick_erase=True)
if not url.endswith("/leave/confirm") or 'data-count="objects">3<' not in page or 'data-count="types">2<' not in page:
    fail(f"the preview does not show Ben's 3 records in 2 tracks ({url})")
st, page, _ = request(ben, "POST", "/leave/confirm", form={"csrf": csrf_of(page), "dataset": field(page, "dataset")})
if st != 200 or "has been removed" not in page:
    fail(f"confirming the removal answered {st}")
rows = panel_rows(panel(sara, "home", type=SPEND, field="amount", measure="sum", group="none"))
mine = panel_rows(panel(sara, "mine", type=SPEND, field="amount", measure="sum", group="store"))
if total(rows) != str(sara_total) or mine.get("sara") != str(sara_total):
    fail(f"after Ben left, Home shows {rows} and Sara's own {mine}; want {sara_total} for both")
say("Ben declines, then removes his data", f"Home {sara_total}; Sara untouched")

# ---- deleting an account, on Tessera's own page ------------------------------------
# The Console only links there: the password is typed on Tessera's page, the
# confirmation lists what will be erased, and the way back is the Console's
# registered return address, which ends the Console session.

back_to = base + "/account/back"
st, page, url = follow(ben, "/account/delete")
if "/oauth/account/delete" not in url or f'name="return_to" value="{back_to}"' not in page:
    fail(f"Delete my account did not reach Tessera's page with the way back ({st} {url})")
keep = {"page": "1", "client_id": field(page, "client_id"), "return_to": back_to}
st, page, _ = request(ben, "POST", "/oauth/account/delete", form=dict(keep, account="ben", password=passwords["ben"]))
if st != 200 or 'name="ticket"' not in page:
    fail(f"Tessera's deletion page did not show a confirmation ({st})")
st, page, _ = request(ben, "POST", "/oauth/account/delete", form=dict(keep, ticket=field(page, "ticket"), decision="delete"))
# By structure, not wording (the page speaks the browser's language since
# alpha.30): a link whose address is exactly the Console's way back.
way_back = next((m for m in re.finditer(r'<a [^>]*href="([^"]+)"', page) if m.group(1).replace("&amp;", "&") == back_to), None)
if st != 200 or not way_back:
    fail(f"after deleting, Tessera's page offers no way back to the Console ({st})")
st, page, headers = request(ben, "GET", way_back.group(1).replace("&amp;", "&"))
cookie_name = "__Host-voptrack_console_" + hashlib.sha256(base.rstrip("/").encode()).hexdigest()[:16]
ended = [c for c in headers.get_all("Set-Cookie") or [] if c.startswith(cookie_name + "=;") and "Max-Age=0" in c]
if st != 200 or "signed out" not in page or not ended:
    fail(f"coming back from Tessera did not end Ben's Console session ({st}, cookie cleared: {bool(ended)})")
st, page, _ = request(owner, "GET", "/members")
listed = dict(re.findall(r'<td data-member>([^<]+)</td><td data-state>([^<]+)</td>', page))
if listed != {"sara": "active"} or ben_read() != 401:
    fail(f"after Ben deleted his account, Members lists {listed} and his app answers {ben_read()}")
st, _, url = follow(sara, "/account/password")
if "/oauth/account/password" not in url:
    fail(f"Change my password did not reach Tessera's page ({url})")
say("Ben deletes his account on Tessera's page", "gone from Members; his app 401; back to the Console")

# ---- connect the app ---------------------------------------------------------

st, page, _ = request(browser(), "GET", "/connect-app")
link = re.search(r'<code class="link" data-link>([^<]+)</code>', page)
got = urllib.parse.urlparse(link.group(1).replace("&amp;", "&")) if link else None
params = dict(urllib.parse.parse_qsl(got.query)) if got else {}
st2, body, _ = request(browser(), "GET", "/.well-known/voptrack")
pin = json.loads(body).get("tls_pin") if st2 == 200 else None
if st != 200 or not got or got.scheme != "voptrack" or got.netloc != "connect" or '<path d="M' not in page \
        or not pin or params != {"server": base, "product": "home", "pin": pin}:
    fail(f"the connect page, signed out, carries {params}; want this server's origin, product=home and its pin {pin} ({st})")
say("connect-the-app page, signed out", "this server's origin, product=home and its local CA pin")

if finalize_online_recovery:
    finalize_online_recovery()
