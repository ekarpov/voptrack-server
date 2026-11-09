#!/usr/bin/env python3
"""Seed the Home development server for the app's round trip.

Usage: seed_dev.py BASE CA OWNER_PASSWORD_FILE OUT_DIR [seed|token]

Sets a household up through the Console as a person would -- or, with
VOPTRACK_CONSOLE=off (the release shape, Home with no web interface), through
Tessera directly, as the app does: the owner's own sign-in invites and suspends
with the administration API, and each member's app sharing is a reader grant to
the household group. Either way: the owner invites
Sara, Ben and Lina; Sara and Ben record exactly the inputs of
specs/vectors/home-aggregation-v1.json and share Spending (not Weight) with
Home; Lina records, shares nothing, and is suspended. Writes OUT_DIR/home.json
(0600): every member's credentials and their own app tokens, who shares what,
who is suspended, and the household answers from the vectors. "token" signs
everyone in again and rewrites the tokens: refresh tokens rotate.
Synthetic data only.
"""
import base64, hashlib, http.cookiejar, json, os, re, secrets, ssl, sys, time
import urllib.error, urllib.parse, urllib.request
from html.parser import HTMLParser

import sys as _ci_sys
from pathlib import Path as _CiPath
_ci_sys.path.insert(0, str(_CiPath(__file__).resolve().parents[2] / 'ci'))
from timing import retry_auth, timed_request, RetryBudget, phase, event

base, ca, owner_pw, out = sys.argv[1].rstrip("/"), sys.argv[2], open(sys.argv[3]).read().strip(), sys.argv[4]
mode = sys.argv[5] if len(sys.argv) > 5 else "seed"
root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
vectors = json.load(open(os.path.join(root, "specs/vectors/home-aggregation-v1.json")))
ctx = ssl.create_default_context(cafile=ca)
SPEND, WEIGHT = "org.voptrack.gate.spending", "org.voptrack.gate.weight"
REDIRECT = "com.voprak.voptrack:/oauth/callback"


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


def browser():
    return urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx), NoRedirect,
                                       urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


@retry_auth
def request(opener, method, url, form=None, js=None, headers=None):
    h = dict(headers or {})
    data = None
    if form is not None:
        data = urllib.parse.urlencode(form, doseq=True).encode()
        h["Content-Type"] = "application/x-www-form-urlencoded"
    if js is not None:
        data = json.dumps(js).encode()
        h["Content-Type"] = "application/json"
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
        return st, body, url
    sys.exit("too many redirects")


field = lambda p, n: re.search(r'name="%s"\s+value="([^"]*)"' % n, p).group(1)
csrf_of = lambda p: re.search(r'name="csrf" value="([^"]+)"', p).group(1)


def must(ok, what):
    if not ok:
        sys.exit("home seed_dev: " + what)


def own_store(approval, known=None, others=()):
    """The person's own store on the approval page. With household sharing the
    chooser also lists stores others share with them -- by bare id, in no fixed
    order -- so a script that knows its store picks it by id. Before that, the
    person's own store is the only one there."""
    options = re.findall(r'<option\s+value="([^"]+)"', approval)
    if known and known in options:
        return known
    # A first sign-in knows no id yet: its own store is the one that is not
    # somebody else's (group sharing covers members who join later).
    mine = [o for o in options if o not in set(others)]
    return mine[0] if mine else None


def consent(b, page, person, password, grants, known=None):
    st, approval, _ = request(b, "POST", "/oauth/authorize", form={
        "flow": field(page, "flow"), "csrf": field(page, "csrf"), "username": person, "password": password, "decision": "continue"})
    must(st == 200, f"{person}: sign-in answered {st}")
    form = {"flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow"}
    option = own_store(approval, known)
    if option and grants.pop("store", False):
        form["dataset"] = option
    form.update(grants)
    st, _, headers = request(b, "POST", "/oauth/authorize", form=form)
    must(st == 303, f"{person}: approving answered {st}")
    return follow(b, headers["Location"])


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


def console(person, password, grants, known=None):
    b = browser()
    st, page, url = follow(b, "/login")
    if 'name="remember"' in page:
        st, result, headers = request(b, "POST", "/oauth/authorize", form=dict(WebsiteForm(page).fields, username=person, password=password))
        callback = headers.get("Location") if st == 303 else None
        if st == 200:
            refresh = WebsiteForm(result).refresh
            must(refresh and re.search(r"url=", refresh, re.I), "website sign-in did not complete")
            callback = re.split(r"url=", refresh, flags=re.I)[1].strip().strip(chr(34)+chr(39))
        must(callback, "website sign-in refused")
        parsed = urllib.parse.urlparse(urllib.parse.urljoin(base+"/", callback))
        must(parsed.netloc == urllib.parse.urlparse(base).netloc and parsed.path == "/auth/callback", "website callback origin refused")
        must(follow(b, callback)[0] == 200, "website callback failed")
    else:
        consent(b, page, person, password, grants, known)
    return b


APP_ONLY = os.environ.get("VOPTRACK_CONSOLE") == "off"


def app_sign_in(person, password, known=None, others=(), owner=False):
    """The member's own app sign-in: PKCE, read, write, schema and erase on
    their own store, as the Voptrack app asks -- and administer, in the release
    shape, so the app itself can share. The owner's holds no store: server
    rights only."""
    app = browser()
    st, body, _ = request(app, "POST", "/oauth/register", js={
        "client_name": "Voptrack (home-dev)", "redirect_uris": [REDIRECT], "token_endpoint_auth_method": "none",
        "grant_types": ["authorization_code", "refresh_token"], "response_types": ["code"]})
    client = json.loads(body)["client_id"]
    verifier = secrets.token_urlsafe(48)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
    q = urllib.parse.urlencode({"response_type": "code", "client_id": client, "redirect_uri": REDIRECT, "state": "s",
                                "code_challenge": challenge, "code_challenge_method": "S256", "prompt": "consent"})
    st, page, _ = request(app, "GET", "/oauth/authorize?" + q)
    st, approval, _ = request(app, "POST", "/oauth/authorize", form={
        "flow": field(page, "flow"), "csrf": field(page, "csrf"), "username": person, "password": password, "decision": "continue"})
    must(st == 200, f"{person}: app sign-in answered {st}")
    form = {"flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow"}
    if owner:
        dataset = None
        form["administer"] = "yes"
    else:
        dataset = own_store(approval, known, others)
        form.update({"dataset": dataset, "read": "yes", "write": "yes", "schema": "yes", "erase": "yes"})
        if APP_ONLY:
            form["administer"] = "yes"
    st, _, headers = request(app, "POST", "/oauth/authorize", form=form)
    code = urllib.parse.parse_qs(urllib.parse.urlparse(headers["Location"]).query)["code"][0]
    st, body, _ = request(app, "POST", "/oauth/token", form={"grant_type": "authorization_code", "client_id": client,
                                                               "code": code, "redirect_uri": REDIRECT, "code_verifier": verifier})
    must(st == 200, f"{person}: token exchange answered {st}")
    tok = json.loads(body)
    return {"dataset": dataset, "client_id": client, "redirect_uri": REDIRECT, "access_token": tok["access_token"],
            "refresh_token": tok.get("refresh_token"), "access_token_expires_at": int(time.time()) + int(tok.get("expires_in", 300))}


def api(token, method, path, body=None):
    return request(browser(), method, path, js=body, headers={"Authorization": "Bearer " + token})


def meta(type_id, name, fields):
    return {"type": {"id": type_id, "parent_id": None, "name": name, "fields": [dict(f, meta_id=type_id) for f in fields]}}


TYPES = [meta(SPEND, "Spending", [{"id": "recorded_at", "name": "When", "type": "date_time"},
                                  {"id": "amount", "name": "Amount", "type": "fixed", "scale": 2},
                                  {"id": "category", "name": "Category", "type": "text"}]),
         meta(WEIGHT, "Weight", [{"id": "recorded_at", "name": "When", "type": "date_time"},
                                 {"id": "kg", "name": "Kilograms", "type": "float"}])]

# Lina's records are hers alone: she shares nothing, so none of them may ever
# reach a household total.
LINA = {SPEND: [{"recorded_at": "2026-08-15T09:00:00.000Z", "amount": "999", "category": "food"}],
        WEIGHT: [{"recorded_at": "2026-09-01T07:00:00.000Z", "kg": "70.5"}]}


def record(token, dataset, records):
    ops = []
    for t, rows in records.items():
        for r in rows:
            fields = {"recorded_at": {"type": "date_time", "value": r["recorded_at"]}}
            if t == SPEND:
                fields["amount"] = {"type": "fixed", "value": r["amount"]}
                fields["category"] = {"type": "text", "value": r["category"]}
            else:
                fields["kg"] = {"type": "float", "value": float(r["kg"])}
            ops.append({"op": "insert", "type": t, "fields": fields})
    st, body, _ = api(token, "POST", f"/v1/datasets/{dataset}:transact", {"operations": ops})
    must(st == 200, f"recording answered {st} {body[:200]}")


def tls_pin(out):
    """The local CA's pin dev.sh copied beside ca.crt (sha256/<base64 SPKI>),
    which every connect and invitation link of this server carries; None for a
    server with a publicly trusted certificate."""
    try:
        return open(os.path.join(out, "tls-pin")).read().strip() or None
    except OSError:
        return None


os.makedirs(out, mode=0o700, exist_ok=True)
path = os.path.join(out, "home.json")

if mode == "invite":
    # One unused member invitation, issued as the owner's app would (Tessera's
    # admin API with the owner's own sign-in), carrying this server's pin.
    owner_app = app_sign_in("owner", owner_pw, owner=True)
    # A test may have closed the Home's issuer (the owner's "stop invitations");
    # a dev server reopens it, so it can always hand out a fresh link.
    st, body, _ = api(owner_app["access_token"], "PATCH", "/admin/api/v1/issuers/home-members", {"disabled": False})
    must(st == 200, f"reopening the Home's issuer answered {st} {body[:200]}")
    st, body, _ = api(owner_app["access_token"], "POST", "/admin/api/v1/issuers/home-members/invitations", {})
    must(st in (200, 201), f"issuing a member invitation answered {st} {body[:200]}")
    link, pin = json.loads(body)["redeem_url"], tls_pin(out)
    if pin:
        link += "&" + urllib.parse.urlencode({"pin": pin})
    print(link)
    sys.exit(0)

if mode == "token":
    doc = json.load(open(path))
    for name, m in doc["members"].items():
        if m["suspended"]:
            continue  # a suspended member cannot sign in; their stored tokens stay as they were
        m.update(app_sign_in(name, m["password"], m["dataset"]))
else:
    if APP_ONLY:
        owner_app = app_sign_in("owner", owner_pw, owner=True)
    else:
        owner = console("owner", owner_pw, {"administer": "yes"})
    members = {}
    for name in ("sara", "ben", "lina"):
        if APP_ONLY:
            st, body, _ = api(owner_app["access_token"], "POST", "/admin/api/v1/issuers/home-members/invitations", {})
            must(st in (200, 201), f"the owner's app inviting answered {st} {body[:200]}")
            link = json.loads(body)["redeem_url"]
        else:
            st, page, _ = request(owner, "GET", "/members")
            st, page, _ = follow(owner, "/members/invite", form={"csrf": csrf_of(page)}, method="POST")
            link = re.findall(r'<code class="link">([^<]+)</code>', page)[0].replace("&amp;", "&")
        r = browser()
        st, form_page, _ = request(r, "GET", link)
        password = secrets.token_urlsafe(18)
        st, done, _ = request(r, "POST", "/oauth/invitation", form={"code": field(form_page, "code"), "account": name,
                                                                     "password": password})
        must(st == 201, f"{name} redeeming the invitation answered {st}")
        code = re.search(r'data-recovery-code="([A-Z0-9-]+)"', done)
        members[name] = {"account": name, "password": password, "recovery_code": code.group(1) if code else None}
        members[name].update(app_sign_in(name, password, others=[m["dataset"] for m in members.values() if "dataset" in m]))
        m = members[name]
        for body in TYPES:
            st, text, _ = api(m["access_token"], "POST", f"/v1/datasets/{m['dataset']}/meta", body)
            must(st == 201, f"{name} defining {body['type']['id']} answered {st}")
        record(m["access_token"], m["dataset"], vectors["members"][name]["records"] if name in vectors["members"] else LINA)
        shares = vectors["members"].get(name, {}).get("shares", [])
        if shares and APP_ONLY:
            st, body, _ = api(m["access_token"], "PUT", f"/admin/api/v1/datasets/{m['dataset']}/readers/issuer:home-members",
                              {"types": shares})
            must(st == 204, f"{name}'s app sharing with the household answered {st} {body[:200]}")
        elif shares:
            b = console(name, password, {"store": True, "read": "yes", "administer": "yes"}, m["dataset"])
            st, page, _ = request(b, "GET", "/sharing")
            st, page, _ = follow(b, "/sharing", form={"csrf": csrf_of(page), "dataset": field(page, "dataset"),
                                                      "track": shares}, method="POST")
            must("/sharing" in (page or "") or st == 200, f"{name} sharing answered {st}")
        m["shares"] = shares
    if APP_ONLY:
        st, body, _ = api(owner_app["access_token"], "POST", "/admin/api/v1/accounts/lina/suspend", {})
        must(st == 204, f"the owner's app suspending Lina answered {st}")
    else:
        st, page, _ = request(owner, "GET", "/members")
        st, page, _ = follow(owner, "/members/lina/suspend", form={"csrf": csrf_of(page)}, method="POST")
    for name, m in members.items():
        m["suspended"] = name == "lina"
    home = {c["name"]: c["expect"] for c in vectors["cases"] if c["scope"] == "home"}
    doc = {"server": base, "ca_certificate": os.path.join(out, "ca.crt"), "variant": "household",
           "web_interface": not APP_ONLY,
           "sharing_mechanism": ("the household group reader readers/issuer:home-members, written by each member's "
                                 "own app sign-in (administer approved) -- no web interface and no Home service, the "
                                 "release shape. " if APP_ONLY else
                                 "the household group reader readers/issuer:home-members, written by the Console's "
                                 "Sharing page together with the home service reader for web dashboards (Tessera "
                                 "alpha.23). ") + "A member's app reads it with one sign-in: its own store plus "
                                "shared_reads=true.",
           "owner": {"account": "owner", "password_file": os.path.join(out, "owner")},
           "members": members,
           "vectors": "specs/vectors/home-aggregation-v1.json",
           "expected_household": home,
           "notes": ["Lina recorded 999 in Spending and 70.5 in Weight, shared nothing, and is suspended: none of it "
                     "may appear in any household answer."]}
doc["tls_pin"] = tls_pin(out)  # beside ca_certificate: the key the app pins for this server
with open(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
    json.dump(doc, f, indent=2)
for name, m in doc["members"].items():
    print(f"  {name}: store {m['dataset']}, shares {m['shares'] or 'nothing'}{', suspended' if m['suspended'] else ''}")
print(f"  household spending total: {doc['expected_household']['home spending total']['All']}")
print(f"  files: {out}")
