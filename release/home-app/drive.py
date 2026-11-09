#!/usr/bin/env python3
"""Home without its web interface, driven the way the app would.

Usage: drive.py PUBLIC CONSOLE_PORT_URL CA OWNER_PASSWORD_FILE INVITATION_LINK
"""
import base64, hashlib, http.cookiejar, json, re, secrets, ssl, sys, urllib.error, urllib.parse, urllib.request

import sys as _ci_sys
from pathlib import Path as _CiPath
_ci_sys.path.insert(0, str(_CiPath(__file__).resolve().parents[2] / 'ci'))
from timing import retry_auth, timed_request, RetryBudget, phase, event

public, console, ca, owner_pw, link = sys.argv[1].rstrip("/"), sys.argv[2].rstrip("/"), sys.argv[3], \
    open(sys.argv[4]).read().strip(), sys.argv[5]
ctx = ssl.create_default_context(cafile=ca)
REDIRECT = "com.voprak.voptrack:/oauth/callback"


def fail(message):
    print(f"\nFAIL: {message}", file=sys.stderr)
    sys.exit(1)


def say(what, outcome):
    print(f"  {what:<46} {outcome}")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


def browser():
    return urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx), NoRedirect,
                                       urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


@retry_auth
def request(opener, method, url, form=None, js=None, token=None, headers=None):
    h = dict(headers or {})
    data = None
    if form is not None:
        data = urllib.parse.urlencode(form).encode()
        h["Content-Type"] = "application/x-www-form-urlencoded"
    if js is not None:
        data = json.dumps(js).encode()
        h["Content-Type"] = "application/json"
    if token:
        h["Authorization"] = "Bearer " + token
    r = urllib.request.Request(url if url.startswith("http") else public + url, data=data, method=method, headers=h)
    try:
        resp = opener.open(r, timeout=30)
        return resp.status, resp.read().decode(), resp.headers
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace"), e.headers


field = lambda p, n: re.search(r'name="%s"\s+value="([^"]*)"' % n, p).group(1)


def own_store(approval, known=None):
    """The person's own store on the approval page. With household sharing the
    chooser also lists stores others share with them -- by bare id, in no fixed
    order -- so a script that knows its store picks it by id. Before that, the
    person's own store is the only one there."""
    options = re.findall(r'<option\s+value="([^"]+)"', approval)
    if known and known in options:
        return known
    return options[0] if options else None


clients = {}  # which app client each token belongs to, for revoking it


def app_sign_in(person, password, grants, known=None):
    """An app's PKCE sign-in; grants include "store": True to pick a store, and
    "shared_reads": "yes" to read what others share, asked for in the request."""
    app = browser()
    st, body, _ = request(app, "POST", "/oauth/register", js={
        "client_name": "Voptrack (gate)", "redirect_uris": [REDIRECT], "token_endpoint_auth_method": "none",
        "grant_types": ["authorization_code", "refresh_token"], "response_types": ["code"]})
    client = json.loads(body)["client_id"]
    verifier = secrets.token_urlsafe(48)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
    params = {"response_type": "code", "client_id": client, "redirect_uri": REDIRECT, "state": "s",
              "code_challenge": challenge, "code_challenge_method": "S256", "prompt": "consent"}
    if grants.get("shared_reads"):
        params["shared_reads"] = "true"
    q = urllib.parse.urlencode(params)
    st, page, _ = request(app, "GET", "/oauth/authorize?" + q)
    st, approval, _ = request(app, "POST", "/oauth/authorize", form={
        "flow": field(page, "flow"), "csrf": field(page, "csrf"), "username": person, "password": password, "decision": "continue"})
    if st != 200:
        fail(f"{person}: sign-in answered {st}")
    form = {"flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow"}
    if grants.pop("store", False):
        form["dataset"] = own_store(approval, known)
    form.update(grants)
    st, _, headers = request(app, "POST", "/oauth/authorize", form=form)
    code = urllib.parse.parse_qs(urllib.parse.urlparse(headers["Location"]).query)["code"][0]
    st, body, _ = request(app, "POST", "/oauth/token", form={"grant_type": "authorization_code", "client_id": client,
                                                               "code": code, "redirect_uri": REDIRECT, "code_verifier": verifier})
    if st != 200:
        fail(f"{person}: token exchange answered {st}")
    token = json.loads(body)["access_token"]
    clients[token] = client
    return token


b = browser()
for url in (public + "/", public + "/members", console + "/"):
    st, body, _ = request(b, "GET", url)
    if st != 404 or "no web interface" not in body:
        fail(f"{url} answered {st}; want 404, no web interface")
st, _, _ = request(b, "GET", "/.well-known/oauth-authorization-server")
if st != 200:
    fail(f"Tessera's discovery answered {st}")
say("the public address and the Console's port", "no web interface; Tessera only")

# The owner, as the app would sign them in: server rights, no store.
owner = app_sign_in("owner", owner_pw, {"administer": "yes"})
st, body, _ = request(b, "GET", "/admin/api/v1/overview", token=owner)
overview = json.loads(body) if st == 200 else {}
services = [a["account"] for a in overview.get("accounts", []) if a.get("kind") == "service"]
if st != 200 or services:
    fail(f"the server holds service accounts {services} ({st}); want none, so nothing reads across members")
say("no credential on the server reads across members", "no service account at all")

# A member joins from the owner's host-side invitation.
r = browser()
st, form_page, _ = request(r, "GET", link)
password = secrets.token_urlsafe(18)
st, done, _ = request(r, "POST", "/oauth/invitation", form={"code": field(form_page, "code"), "account": "sara", "password": password})
if st != 201 or not re.search(r'data-recovery-code="[A-Z0-9-]+"', done):
    fail(f"redeeming the owner's invitation answered {st}, or showed no recovery code")
sara = app_sign_in("sara", password, {"store": True, "read": "yes", "write": "yes", "schema": "yes", "erase": "yes"})
st, body, _ = request(b, "GET", "/v1/datasets", token=sara)
store = json.loads(body)["datasets"][0]["id"] if st == 200 else None
if not store:
    fail(f"Sara's app cannot see her store ({st})")
st, body, _ = request(b, "GET", "/v1/datasets", token=owner)
if json.loads(body).get("datasets"):
    fail("the owner's sign-in reaches a store; want none, and never a member's")
say("Sara joins by the owner's invitation", "her app reaches her store; the owner's, none")

for action, want in (("suspend", 401), ("reinstate", 200)):
    st, _, _ = request(b, "POST", f"/admin/api/v1/accounts/sara/{action}", js={}, token=owner)
    if st != 204:
        fail(f"the owner's {action} answered {st}")
    if action == "reinstate":
        sara = app_sign_in("sara", password, {"store": True, "read": "yes", "write": "yes"})
    st, _, _ = request(b, "GET", f"/v1/datasets/{store}/meta", token=sara)
    if st != want:
        fail(f"after the owner's {action}, Sara reading her store answered {st}, want {want}")
say("the owner suspends, then reinstates, from the app", "Sara's reading refused, then back")


# ---- alpha.23: the household without any server credential ------------------------

def reads(token, store, track):
    return request(b, "POST", f"/v1/datasets/{store}/objects/{track}:aggregate", js={}, token=token)[0]


# The owner's app invites with its own sign-in: no issuer secret anywhere.
st, body, _ = request(b, "POST", "/admin/api/v1/issuers/home-members/invitations", js={}, token=owner)
invitation = json.loads(body) if st in (200, 201) else {}
if not invitation.get("redeem_url"):
    fail(f"the owner's app issuing an invitation answered {st} {body[:200]}")
r = browser()
st, form_page, _ = request(r, "GET", invitation["redeem_url"])
ben_pw = secrets.token_urlsafe(18)
st, _, _ = request(r, "POST", "/oauth/invitation", form={"code": field(form_page, "code"), "account": "ben", "password": ben_pw})
if st != 201:
    fail(f"redeeming the owner's app invitation answered {st}")
ben_own = app_sign_in("ben", ben_pw, {"store": True, "read": "yes", "write": "yes", "schema": "yes"})
ben_store = json.loads(request(b, "GET", "/v1/datasets", token=ben_own)[1])["datasets"][0]["id"]
say("the owner's app invites, with no secret", "Ben joins by it")

# Sara's app shares Spending with the household itself: one grant, the group.
spending = {"type": {"id": "org.voptrack.gate.spending", "parent_id": None, "name": "Spending", "fields": [
    {"id": "amount", "meta_id": "org.voptrack.gate.spending", "name": "Amount", "type": "fixed", "scale": 2}]}}
weight = {"type": {"id": "org.voptrack.gate.weight", "parent_id": None, "name": "Weight", "fields": [
    {"id": "kg", "meta_id": "org.voptrack.gate.weight", "name": "Kilograms", "type": "float"}]}}
sara = app_sign_in("sara", password, {"store": True, "read": "yes", "write": "yes", "schema": "yes", "administer": "yes"}, store)
for t in (spending, weight):
    st, _, _ = request(b, "POST", f"/v1/datasets/{store}/meta", js=t, token=sara)
    if st != 201:
        fail(f"Sara's app defining {t['type']['id']} answered {st}")
st, _, _ = request(b, "PUT", f"/admin/api/v1/datasets/{store}/readers/issuer:home-members",
                   js={"types": ["org.voptrack.gate.spending"]}, token=sara)
if st != 204:
    fail(f"Sara's app sharing Spending with the household answered {st}")
ben = app_sign_in("ben", ben_pw, {"store": True, "read": "yes", "write": "yes", "shared_reads": "yes"}, ben_store)
if (reads(ben, store, "org.voptrack.gate.spending"), reads(ben, store, "org.voptrack.gate.weight")) != (200, 403):
    fail("Ben's one sign-in does not read Sara's Spending, or reads her Weight")
st, _, _ = request(b, "PUT", f"/admin/api/v1/datasets/{store}/readers/issuer:home-members", js={"types": []}, token=sara)
st2, _, _ = request(b, "DELETE", f"/admin/api/v1/datasets/{store}/readers/issuer:home-members", token=sara)
if reads(ben, store, "org.voptrack.gate.spending") != 404:
    fail(f"after Sara's app stopped sharing ({st}, {st2}), Ben still reads her store")
say("Sara's app shares with the household itself", "Ben's one sign-in reads Spending, not Weight; then not at all")

# A quota the operator set (VOPTRACK_STORE_LIMIT_MB=64 in this gate; Tessera
# alpha.31's max_stored_bytes) reaches the member's own usage, which their app
# reads with its administer approval. The default, no quota, is console-check's.
st, body, _ = request(b, "GET", "/admin/api/v1/overview", token=sara)
mine = [d for d in (json.loads(body).get("datasets") or []) if d.get("id") == store] if st == 200 else []
usage = (mine[0].get("usage") or {}) if mine else {}
if not mine or usage.get("max_bytes") != 64 * 1048576:
    fail(f"Sara's overview ({st}) shows usage {usage}; want her store's quota of 64 MiB")
say("a quota the operator set", "the member's own usage shows 64 MiB")

# Moderated sharing (alpha.27): the owner stops one track Sara shares, in her
# store alone, without reading it; Sara sees who stopped it and may share again.
SPENDING, WEIGHT = "org.voptrack.gate.spending", "org.voptrack.gate.weight"
st, _, _ = request(b, "PUT", f"/admin/api/v1/datasets/{store}/readers/issuer:home-members",
                   js={"types": [SPENDING, WEIGHT]}, token=sara)
if st != 204 or (reads(ben, store, SPENDING), reads(ben, store, WEIGHT)) != (200, 200):
    fail(f"Sara sharing Spending and Weight ({st}): Ben cannot read both")
st, body, _ = request(b, "POST", "/admin/api/v1/unshare", js={"types": [SPENDING], "datasets": [store]}, token=owner)
if st != 200 or store not in json.dumps(json.loads(body).get("datasets")):
    fail(f"the owner stopping Sara's Spending answered {st} {body[:200]}; want 200 naming her store")
if (reads(ben, store, SPENDING), reads(ben, store, WEIGHT)) != (403, 200):
    fail("after the owner stopped Spending, Ben still reads it, or lost Weight too")
if reads(owner, store, SPENDING) == 200 or reads(owner, store, WEIGHT) == 200:
    fail("the owner, who may stop Sara's sharing, reads her store")
st, body, _ = request(b, "GET", f"/admin/api/v1/datasets/{store}/readers", token=sara)
stopped = json.loads(body).get("stopped") or [] if st == 200 else []
if [(s.get("type"), s.get("stopped_by")) for s in stopped] != [(SPENDING, "owner")] or not stopped[0].get("stopped_at"):
    fail(f"Sara's readers listing ({st}) does not say the owner stopped Spending, and when: {stopped}")
st, _, _ = request(b, "PUT", f"/admin/api/v1/datasets/{store}/readers/issuer:home-members",
                   js={"types": [SPENDING, WEIGHT]}, token=sara)
st2, body, _ = request(b, "GET", f"/admin/api/v1/datasets/{store}/readers", token=sara)
if st != 204 or reads(ben, store, SPENDING) != 200 or json.loads(body).get("stopped"):
    fail(f"Sara sharing Spending again ({st}) did not restore Ben's reading, or left it marked stopped")
say("the owner stops one track Sara shares", "Ben loses it, the owner reads nothing, Sara sees who and when")

# ---- device ownership, isolation and sharing authority ----------------------
sys.path.insert(0, __import__("os").path.dirname(__import__("os").path.abspath(__file__)))
import device
import ownership_checks
ownership_checks.check(lambda method, path, **kw: request(b, method, path, **kw),
                       lambda invitation, account=None: device.redeem(public, ctx, invitation, account),
                       owner, sara, ben, (store, ben_store), spending, weight, fail, say)

# Signing out the owner's app ends the invitations it issued and nobody used.
st, body, _ = request(b, "POST", "/admin/api/v1/issuers/home-members/invitations", js={}, token=owner)
unused = json.loads(body)["redeem_url"]
st, _, _ = request(b, "POST", "/oauth/revoke", form={"token": owner, "client_id": clients[owner]})
if st != 200:
    fail(f"revoking the owner's app answered {st}")
r = browser()
st, form_page, _ = request(r, "GET", unused)
code = re.search(r'name="code"\s+value="([^"]*)"', form_page)
st, _, _ = request(r, "POST", "/oauth/invitation", form={"code": code.group(1) if code else "", "account": "eve",
                                                           "password": secrets.token_urlsafe(18)})
if st == 201:
    fail("an invitation from the owner's revoked installation still let somebody in")
say("the owner's app signed out", "its unused invitation no longer works")
