#!/usr/bin/env python3
"""Seed the Research development server for the app's round trip.

Usage: seed_dev.py BASE CA OWNER_PASSWORD_FILE OUT_DIR [seed|join|token]

seed (the default) sets up the study and both participants. join adds any
participant file that is missing, to a server seeded earlier. token signs
every participant in again and rewrites their tokens: refresh tokens rotate,
so a spent one is replaced this way.

The researcher designs a study in the Console exactly as a person would --
the same tracks as the app's study-sleep vector, plus Weight from the
catalogue -- opens it, and two synthetic participants join. Each one's
credentials and their own OAuth tokens (with a refresh token and the client
that holds it) are written to OUT_DIR/participant.json and participant-2.json,
0600. The second is for erasure tests, so the first always keeps its study. Synthetic data
only; nothing here is anybody's.
"""
import base64, hashlib, http.cookiejar, json, os, re, secrets, ssl, sys, time
import urllib.error, urllib.parse, urllib.request
from html.parser import HTMLParser

import sys as _ci_sys
from pathlib import Path as _CiPath
_ci_sys.path.insert(0, str(_CiPath(__file__).resolve().parents[2] / 'ci'))
from timing import retry_auth, timed_request, RetryBudget, phase, event

base, ca, owner_pw, out = sys.argv[1].rstrip("/"), sys.argv[2], open(sys.argv[3]).read().strip(), sys.argv[4]
ctx = ssl.create_default_context(cafile=ca)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


def browser():
    return urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx), NoRedirect,
                                       urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


@retry_auth
def request(opener, method, url, form=None):
    data = urllib.parse.urlencode(form, doseq=True).encode() if form is not None else None
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
        return st, body, url
    sys.exit("too many redirects")


field = lambda p, n: re.search(r'name="%s"\s+value="([^"]*)"' % n, p).group(1)
csrf_of = lambda p: re.search(r'name="csrf" value="([^"]+)"', p).group(1)


def must(ok, what):
    if not ok:
        sys.exit("seed_dev: " + what)


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


def setup_study():
    """The researcher designs and opens the study; returns its slug and the
    types every participant's store receives."""
    researcher = browser()
    st, page, _ = follow(researcher, "/login")
    if 'name="remember"' in page:
        st, result, headers = request(researcher, "POST", "/oauth/authorize", form=dict(WebsiteForm(page).fields, username="owner", password=owner_pw))
        callback = headers.get("Location") if st == 303 else None
        if st == 200:
            refresh = WebsiteForm(result).refresh
            must(refresh and re.search(r"url=", refresh, re.I), "website sign-in did not complete")
            callback = re.split(r"url=", refresh, flags=re.I)[1].strip().strip(chr(34)+chr(39))
        must(callback, "website sign-in refused")
        parsed = urllib.parse.urlparse(urllib.parse.urljoin(base+"/", callback))
        must(parsed.netloc == urllib.parse.urlparse(base).netloc and parsed.path == "/auth/callback", "website callback origin refused")
        must(follow(researcher, callback)[0] == 200, "website callback failed")
    else:
        st, approval, _ = request(researcher, "POST", "/oauth/authorize", form={
            "flow": field(page, "flow"), "csrf": field(page, "csrf"), "username": "owner", "password": owner_pw, "decision": "continue"})
        st, _, headers = request(researcher, "POST", "/oauth/authorize", form={
            "flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow", "administer": "yes"})
        follow(researcher, headers["Location"])

    slug = "app-roundtrip"
    st, page, _ = request(researcher, "GET", "/studies")
    st, page, url = follow(researcher, "/studies", form={"csrf": csrf_of(page), "title": "App round trip", "slug": slug}, method="POST")
    must(st == 200 and url.endswith(f"/studies/{slug}/setup/basics"),
         f"could not create the study ({st} {url})")

    def setup_save(stage, values=None, action="next"):
        path = f"/studies/{slug}/setup/{stage}"
        status, body, _ = request(researcher, "GET", path)
        must(status == 200, f"could not load setup {stage} ({status})")
        form = {"csrf": csrf_of(body), "revision": field(body, "revision"), "action": action}
        form.update(values or {})
        status, _, headers = request(researcher, "POST", path, form=form)
        must(status == 303, f"setup {stage}/{action} was not accepted ({status})")
        status, body, url = follow(researcher, headers["Location"])
        must(status == 200, f"could not load saved setup {stage} ({status})")
        return body, url

    setup_save("basics", {"title": "App round trip", "run_by": ""})

    # The same design as the app's study-sleep vector, in the designer's form.
    values = [("Hours slept", "number", "h", "sample"), ("Awakenings", "wholeNumber", "", "additive"),
              ("Caffeine", "number", "cups", ""), ("Mood", "text", "", ""), ("Napped", "yesNo", "", ""),
              ("Bedtime", "dateTime", "", "")]
    form = {"t0.present": "1", "t0.name": "Sleep", "catalogue": ["com.voptrack.track.weight"]}
    for j, (name, kind, unit, meaning) in enumerate(values):
        p = f"t0.f{j}."
        form.update({p + "present": "1", p + "name": name, p + "kind": kind, p + "unit": unit, p + "meaning": meaning})
    setup_save("collection", form)
    setup_save("information", {
        "summary": "Synthetic study for the app's integration tests.",
        "collected": "Synthetic sleep records and weight.",
        "who_sees": "Nobody: this is a development server.",
        "how_to_leave": "Remove your data from the Console.",
        "consent": "I agree to take part in a synthetic study.", "material": "yes"})
    setup_save("recruitment", {"join_mode": "open"})
    setup_save("review", action="publish")
    _, url = setup_save("review", action="open")
    must(url.endswith(f"/studies/{slug}/setup/share"), "opening the study did not reach sharing")
    st, page, _ = request(researcher, "GET", f"/studies/{slug}/tracks")
    schema = re.search(r"<pre data-schema>(.*?)</pre>", page, re.S)
    import html
    return slug, json.loads(html.unescape(schema.group(1)))


def join(slug):
    """A participant joins: consent, a pseudonym Tessera chooses, a password."""
    person = browser()
    st, page, url = follow(person, f"/study/{slug}/join")
    form = {"csrf": csrf_of(page), "version": field(page, "version"), "code": field(page, "code"), "agree": "yes"}
    st, redeem, url = follow(person, f"/study/{slug}/join", form=form, method="POST")
    # By structure, not wording: the page speaks the browser's language.
    account = re.search(r'data-account="([^"]+)"', redeem).group(1)
    password = secrets.token_urlsafe(18)
    form = {k: v for k, v in re.findall(r'name="(code|account)" value="([^"]*)"', redeem)}
    form["password"] = password
    st, done, _ = request(person, "POST", "/oauth/invitation", form=form)
    must(st == 201, f"setting the participant's password answered {st}")
    code = re.search(r'data-recovery-code="([A-Z0-9-]+)"', done) or re.search(r"<code[^>]*>([A-Z0-9][A-Z0-9 -]{7,})</code>", done)
    return account, password, code.group(1) if code else None


def sign_in(account, password):
    """The participant's own sign-in, as an app does it: PKCE, approving read,
    write and erase on their store -- never schema, which they do not hold."""
    app = browser()
    redirect = "com.voptrack.dev:/oauth/callback"
    r = urllib.request.Request(base + "/oauth/register", method="POST", headers={"Content-Type": "application/json"},
                               data=json.dumps({"client_name": "Voptrack (research-dev)", "redirect_uris": [redirect],
                                                "token_endpoint_auth_method": "none",
                                                "grant_types": ["authorization_code", "refresh_token"],
                                                "response_types": ["code"]}).encode())
    client = json.loads(app.open(r).read())["client_id"]
    verifier = secrets.token_urlsafe(48)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
    q = urllib.parse.urlencode({"response_type": "code", "client_id": client, "redirect_uri": redirect, "state": "s",
                                "code_challenge": challenge, "code_challenge_method": "S256", "prompt": "consent"})
    st, page, _ = request(app, "GET", "/oauth/authorize?" + q)
    st, approval, _ = request(app, "POST", "/oauth/authorize", form={
        "flow": field(page, "flow"), "csrf": field(page, "csrf"), "username": account, "password": password, "decision": "continue"})
    dataset = re.search(r'<option\s+value="([^"]+)"', approval).group(1)
    st, _, headers = request(app, "POST", "/oauth/authorize", form={
        "flow": field(page, "flow"), "csrf": field(approval, "csrf"), "decision": "allow", "dataset": dataset,
        "read": "yes", "write": "yes", "erase": "yes"})
    code = urllib.parse.parse_qs(urllib.parse.urlparse(headers["Location"]).query)["code"][0]
    st, body, _ = request(app, "POST", "/oauth/token", form={"grant_type": "authorization_code", "client_id": client,
                                                               "code": code, "redirect_uri": redirect, "code_verifier": verifier})
    must(st == 200, f"the participant's token exchange answered {st}")
    return client, redirect, dataset, json.loads(body)


def tls_pin(out):
    """The local CA's pin dev.sh copied beside ca.crt (sha256/<base64 SPKI>),
    which every connect and invitation link of this server carries; None for a
    server with a publicly trusted certificate."""
    try:
        return open(os.path.join(out, "tls-pin")).read().strip() or None
    except OSError:
        return None


def write(name, slug, types, account, password, recovery, purpose):
    client, redirect, dataset, tok = sign_in(account, password)
    path = os.path.join(out, name)
    with open(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
        json.dump({"server": base, "ca_certificate": os.path.join(out, "ca.crt"), "tls_pin": tls_pin(out),
                   "study": slug, "purpose": purpose,
                   "account": account, "password": password, "recovery_code": recovery,
                   "dataset": dataset, "client_id": client, "redirect_uri": redirect,
                   "access_token": tok["access_token"], "refresh_token": tok.get("refresh_token"),
                   "access_token_expires_at": int(time.time()) + int(tok.get("expires_in", 300)),
                   "installed_types": types}, f, indent=2)
    print(f"  {name}: participant {account}, store {dataset}")


# Two participants. The first is the one tests record with; the second exists
# to be erased, so a withdrawal test never leaves the first without its study.
PARTICIPANTS = [("participant.json", "recording: keep this store"),
                ("participant-2.json", "erasing: a withdrawal test may erase this store; run it last")]

os.makedirs(out, mode=0o700, exist_ok=True)
mode = sys.argv[5] if len(sys.argv) > 5 else "seed"
if mode == "seed":
    slug, types = setup_study()
    print(f"  study {slug}: Sleep (the app's study-sleep vector) and Weight")
    for name, purpose in PARTICIPANTS:
        write(name, slug, types, *join(slug), purpose)
elif mode == "join":
    # A server seeded before a participant file existed gets the missing ones.
    first = json.load(open(os.path.join(out, "participant.json")))
    for name, purpose in PARTICIPANTS:
        if not os.path.exists(os.path.join(out, name)):
            write(name, first["study"], first["installed_types"], *join(first["study"]), purpose)
elif mode == "token":
    # Refresh tokens rotate: signing in again replaces a spent one.
    for name, purpose in PARTICIPANTS:
        path = os.path.join(out, name)
        if os.path.exists(path):
            prev = json.load(open(path))
            write(name, prev["study"], prev["installed_types"], prev["account"], prev["password"], prev.get("recovery_code"), purpose)
else:
    sys.exit(f"seed_dev: unknown mode {mode}")
print(f"  files: {out}")
